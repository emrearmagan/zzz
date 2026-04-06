package orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"zzz/internal/logx"
	"zzz/internal/provider"
	"zzz/internal/runstore"
	"zzz/internal/templates"
)

type Store interface {
	AcquireLock(runID string, staleAfter time.Duration) error
	ReleaseLock(runID string) error
	WriteState(runID string, state runstore.RunState) error
	LoadState(runID string) (runstore.RunState, error)
	ReadNotes(runID string) ([]string, error)
	AppendNote(runID string, note string) error
	AppendIterationEvent(runID string, iteration int, payload any) error
	AppendJournalEvent(runID string, event runstore.JournalEvent) error
	WriteArtifact(runID string, name string, content string) error
	WriteProjectArtifact(name string, content string) error
}

type Orchestrator struct {
	provider provider.Runner
	store    Store
	sink     EventSink
	now      func() time.Time
	sleep    func(time.Duration)
}

type loopHandoff struct {
	NextFocus     string   `json:"next_focus"`
	PlannedChange string   `json:"planned_change"`
	VerifyCommand string   `json:"verify_command"`
	Risk          string   `json:"risk"`
	CarryForwards []string `json:"carry_forwards"`
}

func New(p provider.Runner, s Store, sink EventSink) *Orchestrator {
	if sink == nil {
		sink = NopSink{}
	}
	return &Orchestrator{provider: p, store: s, sink: sink, now: time.Now, sleep: time.Sleep}
}

func (o *Orchestrator) Run(ctx context.Context, cfg RunConfig) error {
	if cfg.RunID == "" {
		return errors.New("run id is required")
	}
	if err := o.store.AcquireLock(cfg.RunID, cfg.LockStaleAfter); err != nil {
		return err
	}
	defer func() { _ = o.store.ReleaseLock(cfg.RunID) }()

	state, err := o.bootstrapState(cfg)
	if err != nil {
		return err
	}
	if err := o.store.WriteState(cfg.RunID, state); err != nil {
		return err
	}
	o.emit("state", "run started", state, provider.UsageTotals{})

	for {
		if ctx.Err() != nil {
			state.Status = "stopped"
			if err := o.store.WriteState(cfg.RunID, state); err != nil {
				return err
			}
			o.emit("state", "run canceled", state, provider.UsageTotals{})
			return nil
		}

		if cfg.MaxIterations > 0 && state.LastCompleted >= cfg.MaxIterations {
			if err := o.runFinalSummary(ctx, cfg, &state); err != nil {
				return err
			}
			state.Status = "stopped"
			state.CurrentActivity = "done. summaries written to notes + PROJECT_SUMMARY.md"
			if err := o.store.WriteState(cfg.RunID, state); err != nil {
				return err
			}
			o.emit("state", "max iterations reached", state, provider.UsageTotals{})
			return nil
		}
		if cfg.MaxTokens > 0 && state.TotalTokens >= cfg.MaxTokens {
			state.Status = "stopped"
			state.CurrentActivity = "token cap reached. wrapping up run artifacts"
			if err := o.store.WriteState(cfg.RunID, state); err != nil {
				return err
			}
			o.emit("state", "token cap reached", state, provider.UsageTotals{})
			return nil
		}

		if state.Status == "waiting" {
			until, _ := time.Parse(time.RFC3339, state.WaitingUntil)
			if until.After(o.now()) {
				tmr := time.NewTimer(time.Until(until))
				select {
				case <-ctx.Done():
					tmr.Stop()
					state.Status = "stopped"
					if err := o.store.WriteState(cfg.RunID, state); err != nil {
						return err
					}
					o.emit("state", "run canceled", state, provider.UsageTotals{})
					return nil
				case <-tmr.C:
				}
			}
			state.Status = "running"
		}

		iteration := state.LastCompleted + 1
		state.CurrentIteration = iteration
		o.emit("state", fmt.Sprintf("iteration %d started", iteration), state, provider.UsageTotals{})
		notes, err := o.store.ReadNotes(cfg.RunID)
		if err != nil {
			return err
		}
		prompt := templates.BuildIterationPrompt(templates.PromptInput{
			Objective:       cfg.Prompt,
			Iteration:       iteration,
			MaxIterations:   cfg.MaxIterations,
			MaxTokens:       cfg.MaxTokens,
			Notes:           notes,
			NotesMax:        cfg.NotesMaxEntries,
			NotesCharBudget: cfg.NotesCharBudget,
		})

		iterCtx, cancel := context.WithTimeout(ctx, cfg.ProviderTimeout)
		streamUsage := provider.UsageTotals{}
		abortedForTokens := false

		result, iterErr := o.provider.RunIteration(iterCtx, provider.IterationRequest{
			SchemaVersion: 1,
			RunID:         cfg.RunID,
			Iteration:     iteration,
			Provider:      cfg.Provider,
			Model:         cfg.Model,
			Prompt:        prompt,
			StartedAt:     o.now().UTC().Format(time.RFC3339),
			Limits: provider.Limits{
				MaxIterations: cfg.MaxIterations,
				MaxTokens:     cfg.MaxTokens,
			},
		}, func(ev provider.ProviderEvent) {
			previewState := func() runstore.RunState {
				preview := state
				preview.TotalInputTokens = state.TotalInputTokens + streamUsage.InputTokens
				preview.TotalOutputTokens = state.TotalOutputTokens + streamUsage.OutputTokens
				preview.TotalCacheRead = state.TotalCacheRead + streamUsage.CacheReadTokens
				preview.TotalCacheWrite = state.TotalCacheWrite + streamUsage.CacheWriteTokens
				preview.TotalTokens = preview.TotalInputTokens + preview.TotalOutputTokens
				return preview
			}

			switch ev.Kind {
			case "activity", "phase":
				if ev.Activity != "" {
					state.CurrentActivity = ev.Activity
					preview := previewState()
					preview.CurrentActivity = ev.Activity
					o.emit("activity", ev.Activity, preview, streamUsage)
				}
			case "usage":
				if ev.Usage != nil {
					streamUsage.InputTokens += ev.Usage.InputTokensDelta
					streamUsage.OutputTokens += ev.Usage.OutputTokensDelta
					streamUsage.CacheReadTokens += ev.Usage.CacheReadTokensDelta
					streamUsage.CacheWriteTokens += ev.Usage.CacheWriteTokensDelta
					preview := previewState()
					streamTotal := usageTotal(streamUsage)
					if cfg.MaxTokens > 0 && state.TotalTokens+streamTotal >= cfg.MaxTokens {
						abortedForTokens = true
						cancel()
					}
					o.emit("usage", "usage updated", preview, streamUsage)
				}
			}
			_ = o.store.AppendIterationEvent(cfg.RunID, iteration, ev)
		})
		cancel()

		if ctx.Err() != nil {
			state.Status = "stopped"
			if err := o.store.WriteState(cfg.RunID, state); err != nil {
				return err
			}
			o.emit("state", "run canceled", state, streamUsage)
			return nil
		}

		if abortedForTokens {
			iterErr = &provider.Error{Code: provider.ErrMaxTokens, Message: "max tokens reached mid-iteration"}
		}

		if iterErr != nil {
			if isTokenCapError(iterErr) {
				state.Status = "stopped"
				state.CurrentActivity = "token cap reached. run stopped cleanly"
				if err := o.store.WriteState(cfg.RunID, state); err != nil {
					return err
				}
				o.emit("state", "token cap reached", state, streamUsage)
				return nil
			}
			state.FailCount++
			state.ConsecutiveFailures++
			logx.Warnf("iteration %d failed: %v (consecutive_failures=%d/%d)", iteration, iterErr, state.ConsecutiveFailures, cfg.MaxConsecutiveFailures)
			if isNonRetriable(iterErr) || state.ConsecutiveFailures >= cfg.MaxConsecutiveFailures {
				state.Status = "aborted"
				if err := o.store.WriteState(cfg.RunID, state); err != nil {
					return err
				}
				return iterErr
			}
			wait := backoff(state.ConsecutiveFailures)
			state.Status = "waiting"
			state.WaitingUntil = o.now().Add(wait).UTC().Format(time.RFC3339)
			if err := o.store.WriteState(cfg.RunID, state); err != nil {
				return err
			}
			o.emit("state", "iteration failed, backing off", state, streamUsage)
			continue
		}

		reconciled := reconcileUsage(streamUsage, result.Usage)
		state.TotalInputTokens += reconciled.InputTokens
		state.TotalOutputTokens += reconciled.OutputTokens
		state.TotalCacheRead += reconciled.CacheReadTokens
		state.TotalCacheWrite += reconciled.CacheWriteTokens
		state.TotalTokens = state.TotalInputTokens + state.TotalOutputTokens
		state.SuccessCount++
		state.ConsecutiveFailures = 0
		state.CurrentIteration = iteration
		state.LastCompleted = iteration
		state.Status = "running"
		state.CurrentActivity = result.Activity
		handoff := buildLoopHandoff(result)
		note := buildIterationRunCard(iteration, result, handoff)
		if err := o.store.AppendNote(cfg.RunID, note); err != nil {
			return err
		}
		_ = o.store.AppendJournalEvent(cfg.RunID, runstore.JournalEvent{
			Kind:      "checkpoint",
			Iteration: iteration,
			Title:     fmt.Sprintf("Loop card %d", iteration),
			Body:      strings.TrimSpace(result.Summary),
		})
		_ = o.store.AppendJournalEvent(cfg.RunID, runstore.JournalEvent{
			Kind:      "next-brief",
			Iteration: iteration,
			Title:     fmt.Sprintf("Next loop brief after %d", iteration),
			Body:      buildNextIterationBrief(result),
		})
		handoffJSON, err := json.MarshalIndent(handoff, "", "  ")
		if err != nil {
			return err
		}
		if err := o.store.WriteArtifact(cfg.RunID, "handoff.json", string(handoffJSON)+"\n"); err != nil {
			return fmt.Errorf("write handoff artifact: %w", err)
		}
		_ = o.store.AppendJournalEvent(cfg.RunID, runstore.JournalEvent{
			Kind:      "handoff",
			Iteration: iteration,
			Title:     fmt.Sprintf("Handoff for loop %d", iteration),
			Body:      buildHandoffBrief(handoff),
		})
		for _, l := range result.Learnings {
			l = strings.TrimSpace(l)
			if l == "" {
				continue
			}
			_ = o.store.AppendJournalEvent(cfg.RunID, runstore.JournalEvent{Kind: "learning", Iteration: iteration, Body: l})
		}
		if err := o.store.WriteState(cfg.RunID, state); err != nil {
			return err
		}
		o.emit("iteration", fmt.Sprintf("iteration %d complete", iteration), state, reconciled)
	}
}

func (o *Orchestrator) runFinalSummary(ctx context.Context, cfg RunConfig, state *runstore.RunState) error {
	state.Status = "summarizing"
	state.CurrentActivity = "building final summary"
	if err := o.store.WriteState(cfg.RunID, *state); err != nil {
		return err
	}
	o.emit("state", "starting final summary", *state, provider.UsageTotals{})

	notes, err := o.store.ReadNotes(cfg.RunID)
	if err != nil {
		return err
	}
	prompt := templates.BuildFinalSummaryPrompt(templates.FinalSummaryInput{
		Objective:       cfg.Prompt,
		Completed:       state.LastCompleted,
		MaxIterations:   cfg.MaxIterations,
		MaxTokens:       cfg.MaxTokens,
		Notes:           notes,
		NotesMax:        cfg.NotesMaxEntries,
		NotesCharBudget: cfg.NotesCharBudget,
	})

	iterCtx, cancel := context.WithTimeout(ctx, cfg.ProviderTimeout)
	defer cancel()
	streamUsage := provider.UsageTotals{}

	result, runErr := o.provider.RunIteration(iterCtx, provider.IterationRequest{
		SchemaVersion: 1,
		RunID:         cfg.RunID,
		Iteration:     state.LastCompleted + 1,
		Provider:      cfg.Provider,
		Model:         cfg.Model,
		Prompt:        prompt,
		StartedAt:     o.now().UTC().Format(time.RFC3339),
		Limits: provider.Limits{
			MaxIterations: cfg.MaxIterations,
			MaxTokens:     cfg.MaxTokens,
		},
	}, func(ev provider.ProviderEvent) {
		if ev.Kind == "usage" && ev.Usage != nil {
			streamUsage.InputTokens += ev.Usage.InputTokensDelta
			streamUsage.OutputTokens += ev.Usage.OutputTokensDelta
			streamUsage.CacheReadTokens += ev.Usage.CacheReadTokensDelta
			streamUsage.CacheWriteTokens += ev.Usage.CacheWriteTokensDelta
		}
		_ = o.store.AppendIterationEvent(cfg.RunID, state.LastCompleted+1, ev)
	})
	if runErr != nil {
		logx.Warnf("final summary generation failed: %v", runErr)
		fallback := fmt.Sprintf("## Iteration Summary\n\nFinal summary generation failed: %v\n", runErr)
		if err := o.store.AppendNote(cfg.RunID, fallback); err != nil {
			return err
		}
		_ = o.store.AppendJournalEvent(cfg.RunID, runstore.JournalEvent{Kind: "summary", Iteration: state.LastCompleted, Title: "Final summary failed", Body: runErr.Error()})
		state.CurrentActivity = "done. summary written to notes"
		return nil
	}

	reconciled := reconcileUsage(streamUsage, result.Usage)
	state.TotalInputTokens += reconciled.InputTokens
	state.TotalOutputTokens += reconciled.OutputTokens
	state.TotalCacheRead += reconciled.CacheReadTokens
	state.TotalCacheWrite += reconciled.CacheWriteTokens
	state.TotalTokens = state.TotalInputTokens + state.TotalOutputTokens

	runMD := buildRunSummaryWithStatsMarkdown(cfg, *state, result)
	if err := o.store.AppendNote(cfg.RunID, runMD); err != nil {
		return err
	}
	_ = o.store.AppendJournalEvent(cfg.RunID, runstore.JournalEvent{Kind: "summary", Iteration: state.LastCompleted, Title: "Run wrap-up", Body: strings.TrimSpace(result.Summary)})
	projectMD := buildProjectSummaryMarkdown(cfg, *state, result)
	if err := o.store.WriteProjectArtifact("PROJECT_SUMMARY.md", projectMD); err != nil {
		return err
	}
	state.CurrentActivity = "done. summaries written"
	return nil
}

func buildIterationRunCard(iteration int, result provider.IterationResult, handoff loopHandoff) string {
	summary := strings.TrimSpace(result.Summary)
	if summary == "" {
		summary = "(no summary)"
	}
	changes := "- (none)"
	if len(result.Changes) > 0 {
		changes = "- " + strings.Join(result.Changes, "\n- ")
	}
	learnings := "- (none)"
	if len(result.Learnings) > 0 {
		learnings = "- " + strings.Join(result.Learnings, "\n- ")
	}
	handoffBrief := buildHandoffBrief(handoff)
	return strings.TrimSpace(fmt.Sprintf(`## Loop Card %d

Headline:
%s

What changed:
%s

What we learned:
%s

Next-loop handoff:
%s
`, iteration, summary, changes, learnings, handoffBrief))
}

func buildNextIterationBrief(result provider.IterationResult) string {
	h := buildLoopHandoff(result)
	return strings.TrimSpace(fmt.Sprintf(`Immediate focus:
%s

Latest outcome:
%s

Fresh changes to build on:
%s

Verification requirement:
%s
`, oneLine(h.NextFocus), oneLine(strings.TrimSpace(result.Summary)), strings.Join(h.CarryForwards, "\n"), oneLine(h.VerifyCommand)))
}

func buildLoopHandoff(result provider.IterationResult) loopHandoff {
	summary := strings.TrimSpace(result.Summary)
	if summary == "" {
		summary = "No summary was produced; continue with the smallest concrete step toward the mission."
	}
	changes := []string{"- (none recorded)"}
	if len(result.Changes) > 0 {
		limit := len(result.Changes)
		if limit > 3 {
			limit = 3
		}
		changes = make([]string, 0, limit)
		for _, c := range result.Changes[:limit] {
			changes = append(changes, "- "+oneLine(c))
		}
	}
	nextFocus := "Continue from the latest code state and deliver the next smallest safe improvement toward the mission."
	if len(result.Learnings) > 0 {
		nextFocus = strings.TrimSpace(result.Learnings[0])
	}
	planned := "Apply one focused change that directly advances the mission based on the latest loop card."
	if len(result.Changes) > 0 {
		planned = "Build directly on this change: " + oneLine(result.Changes[0])
	}
	risk := "Potential regressions in touched files; verify before concluding loop."
	if len(result.Learnings) > 1 {
		risk = oneLine(result.Learnings[1])
	}
	return loopHandoff{
		NextFocus:     oneLine(nextFocus),
		PlannedChange: oneLine(planned),
		VerifyCommand: "Run one targeted verification command for the modified area and record exact output.",
		Risk:          risk,
		CarryForwards: changes,
	}
}

func buildHandoffBrief(h loopHandoff) string {
	carry := "- (none)"
	if len(h.CarryForwards) > 0 {
		carry = strings.Join(h.CarryForwards, "\n")
	}
	return strings.TrimSpace(fmt.Sprintf(`Immediate focus:
%s

Planned change:
%s

Verification requirement:
%s

Risk:
%s

Carry-forwards:
%s
`, oneLine(h.NextFocus), oneLine(h.PlannedChange), oneLine(h.VerifyCommand), oneLine(h.Risk), carry))
}

func oneLine(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return s
	}
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	return strings.Join(strings.Fields(s), " ")
}

func buildRunSummaryWithStatsMarkdown(cfg RunConfig, st runstore.RunState, result provider.IterationResult) string {
	changes := "- (none)"
	if len(result.Changes) > 0 {
		changes = "- " + strings.Join(result.Changes, "\n- ")
	}
	learnings := "- (none)"
	if len(result.Learnings) > 0 {
		learnings = "- " + strings.Join(result.Learnings, "\n- ")
	}
	return strings.TrimSpace(fmt.Sprintf(`## Iteration Summary

## Objective
%s

## Outcome
%s

## Iteration Stats
- Completed iterations: %d
- Success count: %d
- Failure count: %d

## Token Stats
- Input tokens: %d
- Output tokens: %d
- Total tokens: %d
- Cache read tokens: %d
- Cache write tokens: %d

## Key Changes
%s

## Key Learnings
%s
`, strings.TrimSpace(cfg.Prompt), strings.TrimSpace(result.Summary), st.LastCompleted, st.SuccessCount, st.FailCount, st.TotalInputTokens, st.TotalOutputTokens, st.TotalTokens, st.TotalCacheRead, st.TotalCacheWrite, changes, learnings)) + "\n"
}

func buildProjectSummaryMarkdown(cfg RunConfig, st runstore.RunState, result provider.IterationResult) string {
	changes := "- (none)"
	if len(result.Changes) > 0 {
		changes = "- " + strings.Join(result.Changes, "\n- ")
	}
	next := "- Review the key changes and run project-specific validation before merge."
	if len(result.Learnings) > 0 {
		next = "- " + strings.Join(result.Learnings, "\n- ")
	}
	approach := fmt.Sprintf("- Completed %d focused iterations, applying small safe edits and validating behavior incrementally.", st.LastCompleted)
	overview := strings.TrimSpace(result.Summary)
	if overview == "" {
		overview = "The objective was implemented in this repository with concrete code updates and tracked iteration notes."
	}
	arch := "- Architecture details were not provided by the model output."
	if len(result.Changes) > 0 {
		limit := len(result.Changes)
		if limit > 4 {
			limit = 4
		}
		arch = "- " + strings.Join(result.Changes[:limit], "\n- ")
	}
	return strings.TrimSpace(fmt.Sprintf(`# Project Summary

## Objective
%s

## What Changed
%s

## Project Overview
%s

Architecture highlights:
%s

## Implementation Approach
%s

## Key Changes
%s

## Next Steps
%s
`, strings.TrimSpace(cfg.Prompt), strings.TrimSpace(result.Summary), overview, arch, approach, changes, next)) + "\n"
}

func (o *Orchestrator) bootstrapState(cfg RunConfig) (runstore.RunState, error) {
	now := o.now().UTC().Format(time.RFC3339)
	if !cfg.Resume {
		return runstore.RunState{
			Status:           "running",
			CurrentIteration: 1,
			LastCompleted:    0,
			StartedAt:        now,
			UpdatedAt:        now,
			BranchName:       cfg.BranchName,
		}, nil
	}
	state, err := o.store.LoadState(cfg.RunID)
	if err != nil {
		return runstore.RunState{}, err
	}
	if cfg.BranchName != "" && state.BranchName != "" && state.BranchName != cfg.BranchName {
		return runstore.RunState{}, fmt.Errorf("current branch does not match checkpoint branch (%s != %s)", cfg.BranchName, state.BranchName)
	}
	if state.Status == "waiting" {
		until, _ := time.Parse(time.RFC3339, state.WaitingUntil)
		if !until.After(o.now()) {
			state.Status = "running"
		}
	}
	return state, nil
}

func (o *Orchestrator) emit(kind string, msg string, state runstore.RunState, usage provider.UsageTotals) {
	o.sink.Emit(Event{Time: o.now(), Type: kind, Message: msg, State: state, Usage: usage})
}

func usageTotal(u provider.UsageTotals) int {
	return u.InputTokens + u.OutputTokens
}

func reconcileUsage(stream provider.UsageTotals, result provider.UsageTotals) provider.UsageTotals {
	return provider.UsageTotals{
		InputTokens:      max(stream.InputTokens, result.InputTokens),
		OutputTokens:     max(stream.OutputTokens, result.OutputTokens),
		CacheReadTokens:  max(stream.CacheReadTokens, result.CacheReadTokens),
		CacheWriteTokens: max(stream.CacheWriteTokens, result.CacheWriteTokens),
	}
}

func backoff(n int) time.Duration {
	d := min(time.Second*time.Duration(1<<(n-1)), 60*time.Second)
	return d + 100*time.Millisecond
}

func isNonRetriable(err error) bool {
	pe := &provider.Error{}
	if errors.As(err, &pe) {
		switch pe.Code {
		case provider.ErrMaxTokens:
			return true
		case provider.ErrAbortedByUser:
			return true
		}
	}
	return false
}

func isTokenCapError(err error) bool {
	pe := &provider.Error{}
	if errors.As(err, &pe) {
		return pe.Code == provider.ErrMaxTokens
	}
	return false
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
