package provider

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"zzz/internal/logx"
)

func (p *OpenCodeProvider) Ask(ctx context.Context, question string, in AskContext) <-chan AskEvent {
	out := make(chan AskEvent, 4)
	go func() {
		defer close(out)

		cwd, err := os.Getwd()
		if err != nil {
			emitAskError(out, "resolve cwd", &Error{Code: ErrProtocol, Message: fmt.Sprintf("resolve cwd: %v", err)})
			return
		}

		port, err := allocatePort()
		if err != nil {
			emitAskError(out, "allocate port", &Error{Code: ErrProtocol, Message: fmt.Sprintf("allocate port: %v", err)})
			return
		}
		baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)
		logx.Debugf("ask: starting opencode server on %s", baseURL)

		server, err := startOpenCodeServer(ctx, p.command, cwd, port)
		if err != nil {
			emitAskError(out, "start server", mapOpenCodeError(err, "start server"))
			return
		}
		defer server.shutdown()

		if err := waitForHealth(ctx, p.client, baseURL); err != nil {
			emitAskError(out, "wait health", mapOpenCodeError(err, "wait health"))
			return
		}

		sessionID, err := createSession(ctx, p.client, baseURL, cwd)
		if err != nil {
			emitAskError(out, "create session", mapOpenCodeError(err, "create session"))
			return
		}
		defer bestEffortDeleteSession(p.client, baseURL, sessionID)

		out <- AskEvent{Kind: AskEventThinking, Update: AskUpdate{Thinking: "asking provider"}}

		type askStreamResult struct {
			text string
			err  error
		}
		streamCh := make(chan askStreamResult, 1)
		go func() {
			text, serr := consumeAskEvents(ctx, p.client, baseURL, sessionID, strings.TrimSpace(question), out)
			streamCh <- askStreamResult{text: text, err: serr}
		}()

		model := strings.TrimSpace(in.Model)
		if model != "" {
			logx.Debugf("ask: using model %s", model)
		}
		responseBody, err := postAskMessage(ctx, p.client, baseURL, sessionID, strings.TrimSpace(question), model)
		if err != nil {
			bestEffortAbortSession(p.client, baseURL, sessionID)
			res := <-streamCh
			if res.err != nil {
				emitAskError(out, "ask stream", mapOpenCodeError(res.err, "ask stream"))
				return
			}
			emitAskError(out, "ask message", mapOpenCodeError(err, "ask message"))
			return
		}

		res := <-streamCh
		if res.err != nil {
			logx.Warnf("ask: stream ended with error, falling back to final response body: %v", res.err)
		}

		answer := strings.TrimSpace(res.text)
		if answer == "" {
			answer = extractAssistantTextFromMessageResponse(responseBody)
		}
		if strings.TrimSpace(answer) == "" {
			emitAskError(out, "empty answer", &Error{Code: ErrProtocol, Message: "provider returned empty answer"})
			return
		}

		out <- AskEvent{Kind: AskEventAnswerFinal, Response: answer}
	}()
	return out
}

func emitAskError(out chan<- AskEvent, stage string, err error) {
	if err == nil {
		err = &Error{Code: ErrProtocol, Message: "unknown ask error"}
	}
	logx.Errorf("ask: %s: %v", stage, err)
	out <- AskEvent{Kind: AskEventError, Err: err}
}

func consumeAskEvents(ctx context.Context, client *http.Client, baseURL, sessionID, sentPrompt string, out chan<- AskEvent) (string, error) {
	logx.Debugf("ask: opening event stream for session %s", sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/global/event", nil)
	if err != nil {
		logx.Warnf("ask: failed building stream request for session %s: %v", sessionID, err)
		return "", err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		logx.Warnf("ask: stream connect failed for session %s: %v", sessionID, err)
		return "", err
	}
	defer resp.Body.Close()
	defer logx.Debugf("ask: event stream closed for session %s", sessionID)

	reader := bufio.NewReader(resp.Body)
	assistantIDs := map[string]bool{}
	textByMessage := map[string]string{}

	for {
		if ctx.Err() != nil {
			logx.Debugf("ask: context ended while streaming session %s: %v", sessionID, ctx.Err())
			return bestAskText(textByMessage, assistantIDs), ctx.Err()
		}

		raw, err := readSSEData(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				logx.Debugf("ask: stream reached EOF for session %s", sessionID)
				return bestAskText(textByMessage, assistantIDs), nil
			}
			logx.Warnf("ask: stream read error for session %s: %v", sessionID, err)
			return bestAskText(textByMessage, assistantIDs), err
		}
		if strings.TrimSpace(raw) == "" {
			continue
		}

		event, err := parseOpenCodeEvent(raw)
		if err != nil || event.Payload == nil || event.Payload.Properties == nil {
			continue
		}
		props := event.Payload.Properties
		if props.SessionID != sessionID {
			continue
		}

		if event.Payload.Type == "message.updated" && props.Info != nil && props.Info.Role == "assistant" && props.Info.ID != "" {
			assistantIDs[props.Info.ID] = true
		}

		if event.Payload.Type == "message.part.updated" && props.Part != nil {
			if props.Part.Metadata != nil && props.Part.Metadata.OpenAI != nil {
				phase := strings.TrimSpace(props.Part.Metadata.OpenAI.Phase)
				if phase != "" {
					out <- AskEvent{Kind: AskEventThinking, Update: AskUpdate{Thinking: phase}}
				}
			}
			if props.Part.Type == "text" {
				prev := strings.TrimSpace(textByMessage[props.Part.MessageID])
				current := strings.TrimSpace(props.Part.Text)
				if current != "" && current != sentPrompt {
					textByMessage[props.Part.MessageID] = current
					delta := ""
					if strings.HasPrefix(current, prev) && len(current) > len(prev) {
						delta = current[len(prev):]
					} else if current != prev {
						delta = current
					}
					if strings.TrimSpace(delta) != "" {
						out <- AskEvent{Kind: AskEventAnswerDelta, ResponseDelta: delta}
					}
				}
			}
		}

		if event.Payload.Type == "session.idle" {
			logx.Debugf("ask: session %s became idle; stopping stream", sessionID)
			return bestAskText(textByMessage, assistantIDs), nil
		}
	}
}

func bestAskText(byMessage map[string]string, assistantIDs map[string]bool) string {
	best := ""
	for id := range assistantIDs {
		s := strings.TrimSpace(byMessage[id])
		if s == "" {
			continue
		}
		if len([]rune(s)) > len([]rune(best)) {
			best = s
		}
	}
	if best != "" {
		return best
	}

	for _, v := range byMessage {
		s := strings.TrimSpace(v)
		if s == "" {
			continue
		}
		if len([]rune(s)) > len([]rune(best)) {
			best = s
		}
	}
	return best
}

func postAskMessage(ctx context.Context, client *http.Client, baseURL, sessionID, prompt, model string) ([]byte, error) {
	url := fmt.Sprintf("%s/session/%s/message", baseURL, sessionID)
	return doJSON(ctx, client, http.MethodPost, url, buildAskMessagePayload(prompt, model))
}

func buildAskMessagePayload(prompt, model string) map[string]any {
	payload := map[string]any{
		"role":  "user",
		"parts": []map[string]string{{"type": "text", "text": prompt}},
	}
	if strings.TrimSpace(model) != "" {
		payload["model"] = buildModelSelector(model)
	}
	return payload
}

func extractAssistantTextFromMessageResponse(body []byte) string {
	var payload struct {
		Role  string `json:"role"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
		Info *struct {
			Summary string `json:"summary"`
		} `json:"info"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return ""
	}

	if payload.Role == "" || payload.Role == "assistant" {
		for i := len(payload.Parts) - 1; i >= 0; i-- {
			if payload.Parts[i].Type != "text" {
				continue
			}
			text := strings.TrimSpace(payload.Parts[i].Text)
			if text != "" {
				return text
			}
		}
	}

	if payload.Info != nil {
		return strings.TrimSpace(payload.Info.Summary)
	}

	return ""
}
