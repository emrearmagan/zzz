package chatbot

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"zzz/internal/logx"
	"zzz/internal/provider"
)

type Service struct {
	asker         provider.Asker
	runID         string
	notesFn       func(string) ([]string, error)
	appendChatFn  func(ChatRecord) error
	appendEventFn func(EventRecord) error
}

type ChatRecord struct {
	Time        string
	Iteration   int
	Role        string
	Text        string
	TotalTokens int
}

type EventRecord struct {
	Time      string
	Iteration int
	Kind      string
	Title     string
	Body      string
}

type askStructuredResponse struct {
	UserResponse         string `json:"user_response"`
	Instructions         string `json:"instructions"`
	NextIterationContext string `json:"next_iteration_context"`
}

func New(
	asker provider.Asker,
	runID string,
	notesFn func(string) ([]string, error),
	appendChatFn func(ChatRecord) error,
	appendEventFn func(EventRecord) error,
) *Service {
	return &Service{asker: asker, runID: runID, notesFn: notesFn, appendChatFn: appendChatFn, appendEventFn: appendEventFn}
}

func (s *Service) Ask(ctx context.Context, question string, in provider.AskContext) <-chan provider.AskEvent {
	out := make(chan provider.AskEvent, 16)

	notes := in.Notes
	if len(notes) == 0 && s.notesFn != nil && strings.TrimSpace(s.runID) != "" {
		if loaded, err := s.notesFn(s.runID); err == nil {
			notes = loaded
		} else {
			logx.Errorf("chatbot: failed to read notes for run %s: %v", s.runID, err)
		}
	}

	prompt := buildChatPrompt(strings.TrimSpace(question), in, notes, s.runID)
	in.Notes = notes

	if s.appendChatFn != nil {
		now := time.Now().UTC().Format(time.RFC3339)
		_ = s.appendChatFn(ChatRecord{
			Time:        now,
			Iteration:   in.Iteration,
			Role:        "user",
			Text:        question,
			TotalTokens: in.TotalTokens,
		})
		if s.appendEventFn != nil {
			_ = s.appendEventFn(EventRecord{Time: now, Iteration: in.Iteration, Kind: "chat-user", Title: "User request", Body: strings.TrimSpace(question)})
			_ = s.appendEventFn(EventRecord{Time: now, Iteration: in.Iteration, Kind: "request", Title: "Carry-forward request", Body: strings.TrimSpace(question)})
		}
	}
	go func() {
		defer close(out)
		stream := s.asker.Ask(ctx, prompt, in)
		finalRaw := ""
		for event := range stream {
			if event.Kind == provider.AskEventError && event.Err != nil {
				logx.Errorf("chatbot: ask stream error for run %s (iteration=%d tokens=%d): %v", s.runID, in.Iteration, in.TotalTokens, event.Err)
			}

			if event.Kind == provider.AskEventAnswerDelta {
				continue
			}
			if event.Kind == provider.AskEventAnswerFinal {
				finalRaw = strings.TrimSpace(event.Response)
				parsed, parseErr := parseAskStructuredResponse(finalRaw)
				if parseErr != nil {
					logx.Warnf("chatbot: failed to parse structured ask response; using raw text fallback: %v", parseErr)
					parsed = askStructuredResponse{UserResponse: finalRaw}
				}
				instructions := strings.TrimSpace(parsed.Instructions)
				if instructions == "" {
					instructions = strings.TrimSpace(parsed.NextIterationContext)
				}

				now := time.Now().UTC().Format(time.RFC3339)
				if s.appendChatFn != nil {
					if instructions != "" {
						_ = s.appendChatFn(ChatRecord{
							Time:        now,
							Iteration:   in.Iteration,
							Role:        "next-loop",
							Text:        instructions,
							TotalTokens: in.TotalTokens,
						})
					}
				}
				if s.appendEventFn != nil {
					if instructions != "" {
						_ = s.appendEventFn(EventRecord{Time: now, Iteration: in.Iteration, Kind: "request", Title: "Chat carry-forward", Body: instructions})
					}
				}

				event.Response = strings.TrimSpace(parsed.UserResponse)
			}

			select {
			case out <- event:
				if event.Kind == provider.AskEventAnswerFinal && strings.TrimSpace(finalRaw) == "" {
					logx.Warnf("chatbot: ask final answer was empty")
				}
			case <-ctx.Done():
				logx.Debugf("chatbot: ask canceled for run %s: %v", s.runID, ctx.Err())
				return
			}
		}
	}()

	return out
}

func buildChatPrompt(question string, in provider.AskContext, notes []string, runID string) string {
	objective := strings.TrimSpace(in.Prompt)
	if objective == "" {
		objective = "(no objective provided)"
	}
	if len(notes) == 0 {
		notes = []string{"(no notes yet)"}
	}
	if len(notes) > 6 {
		notes = notes[len(notes)-6:]
	}
	if strings.TrimSpace(runID) == "" {
		runID = "(unknown)"
	}

	return strings.Join([]string{
		"You are zzz, the run-aware chatbot for an autonomous coding agent.",
		"Return ONLY valid JSON with this exact schema:",
		`{"user_response":"...","instructions":"..."}`,
		"Important context:",
		"- The run objective is the original user goal for this coding session.",
		"- The notes are chronological run notes written during iterations (what was tried, what changed, failures, decisions).",
		"- Notes can contain partial/older attempts; prioritize the most recent notes when there is conflict.",
		"- The user is asking about this current run, not generic coding advice.",
		"Behavior requirements:",
		"- Chat mode does NOT execute edits. It only records guidance/requests for upcoming iterations.",
		"- Never claim code was already changed from chat alone (avoid phrases like 'Done' or 'I added X').",
		"- For implementation requests, say it is captured in notes and will be applied in next iteration.",
		"- Answer directly and concretely.",
		"- If the question asks for status/progress, summarize from notes + objective.",
		"- If information is missing, say exactly what is missing and what to do next.",
		"- Do not output internal IDs, raw paths, or protocol details unless user asks for them.",
		"- Keep user_response compact but complete (typically 2-6 sentences).",
		"- instructions should be operational: explicit intent, requested changes, constraints, and verification expectation for the next loop.",
		"- Do not include markdown code fences.",
		"",
		fmt.Sprintf("Run ID: %s", runID),
		fmt.Sprintf("Current Iteration: %d", in.Iteration),
		fmt.Sprintf("Total Tokens So Far: %d", in.TotalTokens),
		"",
		"Objective:",
		objective,
		"",
		"Recent notes:",
		strings.Join(notes, "\n\n"),
		"",
		fmt.Sprintf("Question: %s", question),
	}, "\n")
}

func parseAskStructuredResponse(raw string) (askStructuredResponse, error) {
	var out askStructuredResponse
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return out, fmt.Errorf("empty response")
	}

	raw = strings.TrimPrefix(raw, "```json")
	raw = strings.TrimPrefix(raw, "```")
	raw = strings.TrimSuffix(raw, "```")
	raw = strings.TrimSpace(raw)

	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		start := strings.IndexByte(raw, '{')
		end := strings.LastIndexByte(raw, '}')
		if start >= 0 && end > start {
			candidate := raw[start : end+1]
			if err2 := json.Unmarshal([]byte(candidate), &out); err2 == nil {
				return out, nil
			}
		}
		return out, err
	}
	return out, nil
}
