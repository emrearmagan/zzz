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
	"sync"

	"zzz/internal/logx"
)

type OpenCodeProvider struct {
	command string
	client  *http.Client
}

func NewOpenCodeProvider() *OpenCodeProvider {
	return &OpenCodeProvider{
		command: "opencode",
		client:  &http.Client{},
	}
}

func (p *OpenCodeProvider) RunIteration(ctx context.Context, req IterationRequest, onEvent func(ProviderEvent)) (IterationResult, error) {
	if onEvent == nil {
		onEvent = func(ProviderEvent) {}
	}

	cwd, err := os.Getwd()
	if err != nil {
		return IterationResult{}, &Error{Code: ErrProtocol, Message: fmt.Sprintf("resolve cwd: %v", err)}
	}

	port, err := allocatePort()
	if err != nil {
		return IterationResult{}, &Error{Code: ErrProtocol, Message: fmt.Sprintf("allocate port: %v", err)}
	}
	baseURL := fmt.Sprintf("http://127.0.0.1:%d", port)

	server, err := startOpenCodeServer(ctx, p.command, cwd, port)
	if err != nil {
		return IterationResult{}, mapOpenCodeError(err, "start server")
	}
	logx.Debugf("opencode server started on %s", baseURL)
	defer server.shutdown()

	if err := waitForHealth(ctx, p.client, baseURL); err != nil {
		return IterationResult{}, mapOpenCodeError(err, "wait health")
	}
	logx.Debugf("opencode health check passed")

	sessionID, err := createSession(ctx, p.client, baseURL, cwd)
	if err != nil {
		return IterationResult{}, mapOpenCodeError(err, "create session")
	}
	logx.Debugf("opencode session created: %s", sessionID)
	defer bestEffortDeleteSession(p.client, baseURL, sessionID)

	var streamUsage UsageTotals
	var streamErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		logx.Debugf("opencode event stream started")
		streamUsage, streamErr = consumeEvents(ctx, p.client, baseURL, sessionID, onEvent)
		logx.Debugf("opencode event stream finished")
	}()

	onEvent(ProviderEvent{Time: nowRFC3339(), Kind: "phase", Activity: "sending request to opencode"})
	body, err := postMessage(ctx, p.client, baseURL, sessionID, buildOpenCodePrompt(req.Prompt), req.Model)
	if err != nil {
		logx.Warnf("opencode message request failed: %v", err)
		bestEffortAbortSession(p.client, baseURL, sessionID)
		wg.Wait()
		if streamErr != nil {
			return IterationResult{}, mapOpenCodeError(streamErr, "stream")
		}
		return IterationResult{}, mapOpenCodeError(err, "send message")
	}

	wg.Wait()
	if streamErr != nil {
		return IterationResult{}, mapOpenCodeError(streamErr, "stream")
	}

	parsed, err := parseMessageResponse(body)
	if err != nil {
		logx.Warnf("opencode response parse failed: %v", err)
		return IterationResult{}, mapOpenCodeError(err, "parse response")
	}
	logx.Debugf("opencode iteration parsed successfully")

	reconciled := UsageTotals{
		InputTokens:      maxInt(streamUsage.InputTokens, parsed.usage.InputTokens),
		OutputTokens:     maxInt(streamUsage.OutputTokens, parsed.usage.OutputTokens),
		CacheReadTokens:  maxInt(streamUsage.CacheReadTokens, parsed.usage.CacheReadTokens),
		CacheWriteTokens: maxInt(streamUsage.CacheWriteTokens, parsed.usage.CacheWriteTokens),
	}
	logx.Debugf("opencode usage totals: in=%d out=%d cache_read=%d cache_write=%d", reconciled.InputTokens, reconciled.OutputTokens, reconciled.CacheReadTokens, reconciled.CacheWriteTokens)

	onEvent(ProviderEvent{Time: nowRFC3339(), Kind: "usage", Usage: &UsageDelta{
		InputTokensDelta:      reconciled.InputTokens,
		OutputTokensDelta:     reconciled.OutputTokens,
		CacheReadTokensDelta:  reconciled.CacheReadTokens,
		CacheWriteTokensDelta: reconciled.CacheWriteTokens,
	}})

	return IterationResult{
		SchemaVersion: 1,
		Success:       parsed.output.Success,
		Summary:       parsed.output.Summary,
		Changes:       parsed.output.Changes,
		Learnings:     parsed.output.Learnings,
		Activity:      parsed.lastActivity,
		Usage:         reconciled,
		ProviderRaw:   parsed.raw,
	}, nil
}

func consumeEvents(ctx context.Context, client *http.Client, baseURL, sessionID string, onEvent func(ProviderEvent)) (UsageTotals, error) {
	logx.Debugf("opencode: opening iteration event stream for session %s", sessionID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, baseURL+"/global/event", nil)
	if err != nil {
		logx.Warnf("opencode: failed building stream request for session %s: %v", sessionID, err)
		return UsageTotals{}, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := client.Do(req)
	if err != nil {
		logx.Warnf("opencode: stream connect failed for session %s: %v", sessionID, err)
		return UsageTotals{}, err
	}
	defer resp.Body.Close()
	defer logx.Debugf("opencode: iteration event stream closed for session %s", sessionID)

	reader := bufio.NewReader(resp.Body)
	usageByMessage := map[string]UsageTotals{}
	lastEmitted := UsageTotals{}
	lastActivity := ""

	for {
		if ctx.Err() != nil {
			logx.Debugf("opencode: stream context ended for session %s: %v", sessionID, ctx.Err())
			return lastEmitted, ctx.Err()
		}
		raw, err := readSSEData(reader)
		if err != nil {
			if errors.Is(err, io.EOF) {
				logx.Debugf("opencode: stream EOF for session %s", sessionID)
				return lastEmitted, nil
			}
			logx.Warnf("opencode: stream read error for session %s: %v", sessionID, err)
			return lastEmitted, err
		}
		if strings.TrimSpace(raw) == "" {
			continue
		}

		event, err := parseOpenCodeEvent(raw)
		if err != nil {
			continue
		}
		if event.Payload == nil || event.Payload.Properties == nil {
			continue
		}
		props := event.Payload.Properties
		if props.SessionID != sessionID {
			continue
		}

		if handleOpenCodeActivity(event, onEvent, &lastActivity) {
			continue
		}

		updated, totals := extractUsageFromEvent(event, usageByMessage)
		if updated {
			delta := UsageDelta{
				InputTokensDelta:      maxInt(0, totals.InputTokens-lastEmitted.InputTokens),
				OutputTokensDelta:     maxInt(0, totals.OutputTokens-lastEmitted.OutputTokens),
				CacheReadTokensDelta:  maxInt(0, totals.CacheReadTokens-lastEmitted.CacheReadTokens),
				CacheWriteTokensDelta: maxInt(0, totals.CacheWriteTokens-lastEmitted.CacheWriteTokens),
			}
			if delta.InputTokensDelta+delta.OutputTokensDelta+delta.CacheReadTokensDelta+delta.CacheWriteTokensDelta > 0 {
				onEvent(ProviderEvent{Time: nowRFC3339(), Kind: "usage", Usage: &delta})
				lastEmitted = totals
			}
		}

		if event.Payload.Type == "session.idle" {
			logx.Debugf("opencode: session %s became idle; stopping stream", sessionID)
			return lastEmitted, nil
		}
	}
}

func readSSEData(r *bufio.Reader) (string, error) {
	var dataLines []string
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			if errors.Is(err, io.EOF) && len(strings.TrimSpace(line)) > 0 {
				line = strings.TrimRight(line, "\r\n")
				if strings.HasPrefix(line, "data:") {
					dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
				}
				break
			}
			return "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			break
		}
		if strings.HasPrefix(line, "data:") {
			dataLines = append(dataLines, strings.TrimSpace(strings.TrimPrefix(line, "data:")))
		}
	}
	return strings.Join(dataLines, "\n"), nil
}

type openCodeEvent struct {
	Payload *struct {
		Type       string `json:"type"`
		Properties *struct {
			SessionID string `json:"sessionID"`
			Field     string `json:"field"`
			Delta     string `json:"delta"`
			PartID    string `json:"partID"`
			Info      *struct {
				ID     string          `json:"id"`
				Role   string          `json:"role"`
				Tokens *openCodeTokens `json:"tokens"`
			} `json:"info"`
			Part *struct {
				MessageID string          `json:"messageID"`
				Type      string          `json:"type"`
				Text      string          `json:"text"`
				Tokens    *openCodeTokens `json:"tokens"`
				Metadata  *struct {
					OpenAI *struct {
						Phase string `json:"phase"`
					} `json:"openai"`
				} `json:"metadata"`
			} `json:"part"`
		} `json:"properties"`
	} `json:"payload"`
}

type openCodeTokens struct {
	Input  int `json:"input"`
	Output int `json:"output"`
	Cache  *struct {
		Read  int `json:"read"`
		Write int `json:"write"`
	} `json:"cache"`
}

func parseOpenCodeEvent(raw string) (openCodeEvent, error) {
	var ev openCodeEvent
	err := json.Unmarshal([]byte(raw), &ev)
	return ev, err
}

func handleOpenCodeActivity(event openCodeEvent, onEvent func(ProviderEvent), last *string) bool {
	if event.Payload == nil || event.Payload.Properties == nil {
		return false
	}
	props := event.Payload.Properties
	if event.Payload.Type == "message.part.updated" && props.Part != nil {
		phase := ""
		if props.Part.Metadata != nil && props.Part.Metadata.OpenAI != nil {
			phase = strings.TrimSpace(props.Part.Metadata.OpenAI.Phase)
		}
		if phase != "" {
			onEvent(ProviderEvent{Time: nowRFC3339(), Kind: "phase", Activity: phase})
		}
		text := strings.TrimSpace(props.Part.Text)
		text = normalizeProviderActivity(text)
		if text != "" && text != *last {
			*last = text
			onEvent(ProviderEvent{Time: nowRFC3339(), Kind: "activity", Activity: text})
		}
	}
	return false
}

func normalizeProviderActivity(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	line := raw
	if i := strings.Index(line, "\n"); i >= 0 {
		line = line[:i]
	}
	line = strings.Join(strings.Fields(strings.TrimSpace(line)), " ")
	return truncateForSummary(line)
}

func extractUsageFromEvent(event openCodeEvent, usageByMessage map[string]UsageTotals) (bool, UsageTotals) {
	if event.Payload == nil || event.Payload.Properties == nil {
		return false, sumUsage(usageByMessage)
	}
	props := event.Payload.Properties
	changed := false

	if event.Payload.Type == "message.updated" && props.Info != nil && props.Info.Role == "assistant" {
		if props.Info.ID != "" && props.Info.Tokens != nil {
			usageByMessage[props.Info.ID] = toTotals(props.Info.Tokens)
			changed = true
		}
	}
	if event.Payload.Type == "message.part.updated" && props.Part != nil && props.Part.Type == "step-finish" {
		if props.Part.MessageID != "" && props.Part.Tokens != nil {
			usageByMessage[props.Part.MessageID] = toTotals(props.Part.Tokens)
			changed = true
		}
	}

	return changed, sumUsage(usageByMessage)
}

func toTotals(tokens *openCodeTokens) UsageTotals {
	if tokens == nil {
		return UsageTotals{}
	}
	out := UsageTotals{InputTokens: tokens.Input, OutputTokens: tokens.Output}
	if tokens.Cache != nil {
		out.CacheReadTokens = tokens.Cache.Read
		out.CacheWriteTokens = tokens.Cache.Write
	}
	return out
}

func sumUsage(usages map[string]UsageTotals) UsageTotals {
	var out UsageTotals
	for _, u := range usages {
		out.InputTokens += u.InputTokens
		out.OutputTokens += u.OutputTokens
		out.CacheReadTokens += u.CacheReadTokens
		out.CacheWriteTokens += u.CacheWriteTokens
	}
	return out
}

type parsedMessage struct {
	output       IterationResult
	usage        UsageTotals
	lastActivity string
	raw          any
}

func parseMessageResponse(body []byte) (parsedMessage, error) {
	var raw map[string]any
	if err := json.Unmarshal(body, &raw); err != nil {
		return parsedMessage{}, err
	}

	var payload struct {
		Info *struct {
			Structured *struct {
				Success   bool     `json:"success"`
				Summary   string   `json:"summary"`
				Changes   []string `json:"changes"`
				Learnings []string `json:"learnings"`
			} `json:"structured"`
			Tokens *openCodeTokens `json:"tokens"`
			Error  *struct {
				Name string `json:"name"`
				Data *struct {
					Message string `json:"message"`
				} `json:"data"`
			} `json:"error"`
		} `json:"info"`
		Parts []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"parts"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return parsedMessage{}, err
	}

	usage := UsageTotals{}
	if payload.Info != nil && payload.Info.Tokens != nil {
		usage = toTotals(payload.Info.Tokens)
	}

	if payload.Info != nil && payload.Info.Error != nil && strings.EqualFold(strings.TrimSpace(payload.Info.Error.Name), "StructuredOutputError") {
		msg := "model did not produce structured output"
		if payload.Info.Error.Data != nil && strings.TrimSpace(payload.Info.Error.Data.Message) != "" {
			msg = strings.TrimSpace(payload.Info.Error.Data.Message)
		}
		logx.Warnf("provider structured output error: %s", msg)
		return parsedMessage{}, fmt.Errorf("structured output error: %s", msg)
	}

	result := IterationResult{SchemaVersion: 1, Success: true}
	if payload.Info != nil && payload.Info.Structured != nil {
		result.Success = payload.Info.Structured.Success
		result.Summary = strings.TrimSpace(payload.Info.Structured.Summary)
		result.Changes = payload.Info.Structured.Changes
		result.Learnings = payload.Info.Structured.Learnings
	} else {
		parsed, ok := parseStructuredFromParts(payload.Parts)
		if ok {
			result.Success = parsed.Success
			result.Summary = strings.TrimSpace(parsed.Summary)
			result.Changes = parsed.Changes
			result.Learnings = parsed.Learnings
		} else {
			fallback := fallbackTextFromParts(payload.Parts)
			if strings.TrimSpace(fallback) == "" {
				logx.Warnf("provider response missing structured output and textual fallback")
				return parsedMessage{}, errors.New("missing structured output from opencode response")
			}
			logx.Warnf("provider response missing structured output; using textual fallback")
			result.Summary = truncateForSummary(fallback)
			result.Changes = []string{"applied provider response without structured JSON", "inspect provider_raw for full content"}
			result.Learnings = []string{"model returned non-schema output; used text fallback"}
		}
	}

	lastText := ""
	for _, part := range payload.Parts {
		if part.Type != "text" {
			continue
		}
		text := strings.TrimSpace(part.Text)
		if text != "" {
			lastText = text
		}
	}
	if result.Summary == "" {
		result.Summary = truncateForSummary(lastText)
	}
	if len(result.Changes) == 0 {
		result.Changes = []string{"completed provider iteration"}
	}
	if len(result.Learnings) == 0 {
		result.Learnings = []string{"preserve strict output schema for reliable automation"}
	}

	return parsedMessage{output: result, usage: usage, lastActivity: lastText, raw: raw}, nil
}

type structuredOutput struct {
	Success   bool     `json:"success"`
	Summary   string   `json:"summary"`
	Changes   []string `json:"changes"`
	Learnings []string `json:"learnings"`
}

func parseStructuredFromParts(parts []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) (structuredOutput, bool) {
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i].Type != "text" {
			continue
		}
		candidate := strings.TrimSpace(parts[i].Text)
		if candidate == "" {
			continue
		}
		if candidate[0] != '{' || candidate[len(candidate)-1] != '}' {
			continue
		}
		var out structuredOutput
		if err := json.Unmarshal([]byte(candidate), &out); err != nil {
			continue
		}
		if strings.TrimSpace(out.Summary) == "" {
			continue
		}
		return out, true
	}
	return structuredOutput{}, false
}

func fallbackTextFromParts(parts []struct {
	Type string `json:"type"`
	Text string `json:"text"`
}) string {
	for i := len(parts) - 1; i >= 0; i-- {
		if parts[i].Type != "text" {
			continue
		}
		text := strings.TrimSpace(parts[i].Text)
		if text == "" {
			continue
		}
		if n := strings.Index(text, "\n"); n >= 0 {
			text = text[:n]
		}
		text = strings.Join(strings.Fields(text), " ")
		if text != "" {
			return text
		}
	}
	return ""
}

func truncateForSummary(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return "opencode iteration completed"
	}
	r := []rune(s)
	if len(r) <= 180 {
		return s
	}
	return string(r[:179]) + "…"
}
