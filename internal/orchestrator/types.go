package orchestrator

import (
	"time"

	"zzz/internal/provider"
	"zzz/internal/runstore"
)

type RunConfig struct {
	RunID                  string
	Prompt                 string
	Provider               string
	Model                  string
	MaxIterations          int
	MaxTokens              int
	MaxConsecutiveFailures int
	ProviderTimeout        time.Duration
	LockStaleAfter         time.Duration
	NotesMaxEntries        int
	NotesCharBudget        int
	BranchName             string
	Resume                 bool
}

type Event struct {
	Time    time.Time
	Type    string
	Message string
	State   runstore.RunState
	Usage   provider.UsageTotals
}

type EventSink interface {
	Emit(Event)
}

type NopSink struct{}

func (NopSink) Emit(Event) {}
