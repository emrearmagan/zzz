package tui

import (
	"context"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (m *Model) submitQuestion() tea.Cmd {
	question := strings.TrimSpace(string(m.input))
	if question == "" {
		return nil
	}
	if m.askPending {
		return nil
	}
	m.messages = append(m.messages, chatMessage{role: "user", text: question})
	m.askPending = true
	m.pendingAskID++
	m.thinkingText = "thinking..."
	m.pendingAnswer = ""
	m.input = nil

	if m.askCancel != nil {
		m.askCancel()
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.askCancel = cancel

	if m.asker == nil {
		m.askPending = false
		m.thinkingText = ""
		m.pendingAnswer = ""
		m.askCancel = nil
		m.messages = append(m.messages, chatMessage{role: "assistant", text: "Ask is unavailable: chatbot/provider not configured."})
		return nil
	}

	id := m.pendingAskID
	iteration := m.iteration
	tokens := m.totalTokens
	prompt := m.prompt
	asker := m.asker

	updates := make(chan tea.Msg, 8)
	m.askUpdates = updates

	go func() {
		defer close(updates)
		stream := asker.Ask(ctx, question, iteration, tokens, prompt)
		collectedAnswer := ""
		for event := range stream {
			if strings.TrimSpace(event.Thinking) != "" {
				select {
				case updates <- AskThinkingMsg{ID: id, Text: event.Thinking}:
				case <-ctx.Done():
					return
				}
			}
			if event.Err != nil {
				message := strings.TrimSpace(event.Err.Error())
				if message == "" {
					message = "ask failed"
				}
				select {
				case updates <- AskReplyMsg{ID: id, Answer: "Ask failed: " + message}:
				case <-ctx.Done():
				}
				return
			}
			if event.AnswerDelta != "" {
				collectedAnswer += event.AnswerDelta
				select {
				case updates <- AskAnswerDeltaMsg{ID: id, Delta: event.AnswerDelta}:
				case <-ctx.Done():
				}
			}
			if strings.TrimSpace(event.Answer) != "" {
				select {
				case updates <- AskReplyMsg{ID: id, Answer: event.Answer}:
				case <-ctx.Done():
				}
				return
			}
		}

		if strings.TrimSpace(collectedAnswer) != "" {
			select {
			case updates <- AskReplyMsg{ID: id, Answer: normalizeAnswerLine(collectedAnswer)}:
			case <-ctx.Done():
			}
			return
		}

		select {
		case updates <- AskReplyMsg{ID: id, Answer: "Provider did not return an answer. Please ask again."}:
		case <-ctx.Done():
		}
	}()

	return waitForAskMessage(updates)
}

func waitForAskMessage(ch <-chan tea.Msg) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		msg, ok := <-ch
		if !ok {
			return nil
		}
		return msg
	}
}

func waitForLogMessage(ch <-chan string) tea.Cmd {
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return nil
		}
		return LogMsg{Line: line}
	}
}

func (m Model) latestAssistantResponse() (string, bool) {
	for i := len(m.messages) - 1; i >= 0; i-- {
		if m.messages[i].role == "assistant" {
			return m.messages[i].text, true
		}
	}
	return "", false
}
