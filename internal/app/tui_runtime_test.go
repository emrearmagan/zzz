package app

import (
	"context"
	"testing"

	"zzz/internal/chatbot"
	"zzz/internal/provider"
)

type captureAsker struct {
	last provider.AskContext
}

func (c *captureAsker) Ask(_ context.Context, _ string, in provider.AskContext) <-chan provider.AskEvent {
	c.last = in
	out := make(chan provider.AskEvent, 1)
	out <- provider.AskEvent{Kind: provider.AskEventAnswerFinal, Response: "ok"}
	close(out)
	return out
}

func TestChatbotAskAdapterIncludesRunNotesInContext(t *testing.T) {
	cap := &captureAsker{}
	chat := chatbot.New(
		cap,
		"run-1",
		func(runID string) ([]string, error) {
			if runID != "run-1" {
				t.Fatalf("unexpected run id: %s", runID)
			}
			return []string{"note a", "note b"}, nil
		},
		nil,
		nil,
	)
	adapter := chatbotAskAdapter{
		chat:  chat,
		model: "opencode/minimax-m.2.5-free",
	}

	stream := adapter.Ask(context.Background(), "question", 2, 77, "objective")
	for range stream {
	}

	if len(cap.last.Notes) != 2 || cap.last.Notes[0] != "note a" {
		t.Fatalf("expected notes in ask context, got %+v", cap.last)
	}
}
