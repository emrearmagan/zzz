package app

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"

	"zzz/internal/chatbot"
	"zzz/internal/logx"
	"zzz/internal/orchestrator"
	"zzz/internal/provider"
	"zzz/internal/runstore"
	"zzz/internal/tui"
)

type programSink struct{ p *tea.Program }

func (s *programSink) Emit(e orchestrator.Event) {
	if s.p != nil {
		s.p.Send(tui.EventMsg(e))
	}
}

func runWithTUI(ctx context.Context, cfg orchestrator.RunConfig, runner provider.Runner, store *runstore.Store, mock bool, debug bool, logStream <-chan string) error {
	logx.Debugf("tui: preparing runtime (mock=%t debug=%t)", mock, debug)
	var gitSource tui.GitStatsSource
	if mock {
		gitSource = tui.NewMockGitStatsSource()
		logx.Debugf("tui: using mock git stats source")
	} else {
		gitSource = tui.NewLiveGitStatsSource()
		logx.Debugf("tui: using live git stats source")
	}

	var asker tui.AskHandler
	if pa, ok := runner.(provider.Asker); ok {
		logx.Debugf("tui: ask support enabled")
		chat := chatbot.New(
			pa,
			cfg.RunID,
			store.ReadNotes,
			func(record chatbot.ChatRecord) error {
				return store.AppendChatEntry(cfg.RunID, runstore.ChatEntry{
					Time:        record.Time,
					Iteration:   record.Iteration,
					Role:        record.Role,
					Text:        record.Text,
					TotalTokens: record.TotalTokens,
				})
			},
			func(event chatbot.EventRecord) error {
				return store.AppendJournalEvent(cfg.RunID, runstore.JournalEvent{
					Time:      event.Time,
					Kind:      event.Kind,
					Iteration: event.Iteration,
					Title:     event.Title,
					Body:      event.Body,
				})
			},
		)
		asker = chatbotAskAdapter{chat: chat, model: cfg.Model}
	}

	m := tui.New(cfg.Prompt, cfg.MaxIterations, gitSource, asker)
	m.SetModel(cfg.Model)
	if debug {
		m.EnableDebugLogs(logStream)
		logx.Debugf("tui: debug log stream attached")
	} else {
		m.EnableErrorLogs(logStream)
	}
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	p := tea.NewProgram(m, tea.WithAltScreen())
	sink := &programSink{p: p}
	o := orchestrator.New(runner, store, sink)
	logx.Debugf("tui: orchestrator initialized")

	runErrCh := make(chan error, 1)
	go func() {
		logx.Debugf("tui: orchestrator run started")
		err := o.Run(runCtx, cfg)
		runErrCh <- err
		if err != nil {
			p.Quit()
		}
	}()

	_, uiErr := p.Run()
	cancel()
	runErr := <-runErrCh
	if isUserAbortOrCanceled(runErr) {
		runErr = nil
	}
	if runErr != nil {
		return runErr
	}
	return uiErr
}

type chatbotAskAdapter struct {
	chat  *chatbot.Service
	model string
}

func (a chatbotAskAdapter) Ask(ctx context.Context, question string, iteration int, totalTokens int, prompt string) <-chan tui.AskStreamEvent {
	out := make(chan tui.AskStreamEvent, 8)
	go func() {
		defer close(out)
		if a.chat == nil {
			out <- tui.AskStreamEvent{Err: &provider.Error{Code: provider.ErrProtocol, Message: "chatbot not configured"}}
			return
		}
		events := a.chat.Ask(ctx, question, provider.AskContext{
			Iteration:   iteration,
			TotalTokens: totalTokens,
			Prompt:      prompt,
			Model:       a.model,
		})

		for event := range events {
			streamEvent := tui.AskStreamEvent{}
			switch event.Kind {
			case provider.AskEventThinking:
				streamEvent.Thinking = event.Update.Thinking
			case provider.AskEventAnswerDelta:
				streamEvent.AnswerDelta = event.ResponseDelta
			case provider.AskEventAnswerFinal:
				streamEvent.Answer = event.Response
			case provider.AskEventError:
				streamEvent.Err = event.Err
			default:
				streamEvent.Err = &provider.Error{Code: provider.ErrProtocol, Message: "unknown ask event kind"}
			}

			select {
			case out <- streamEvent:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out
}
