package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"zzz/internal/provider"
	"zzz/internal/runstore"
)

func TestReconcileUsageUsesMaxPerField(t *testing.T) {
	got := reconcileUsage(
		provider.UsageTotals{InputTokens: 10, OutputTokens: 4, CacheReadTokens: 1, CacheWriteTokens: 3},
		provider.UsageTotals{InputTokens: 8, OutputTokens: 7, CacheReadTokens: 2, CacheWriteTokens: 2},
	)
	if got.InputTokens != 10 || got.OutputTokens != 7 || got.CacheReadTokens != 2 || got.CacheWriteTokens != 3 {
		t.Fatalf("bad reconcile result: %+v", got)
	}
}

func TestBackoffCapped(t *testing.T) {
	if d := backoff(10); d > 61*time.Second {
		t.Fatalf("backoff too large: %v", d)
	}
}

func TestIsNonRetriableForMaxTokens(t *testing.T) {
	if !isNonRetriable(&provider.Error{Code: provider.ErrMaxTokens}) {
		t.Fatal("expected non-retriable")
	}
}

func TestIsNonRetriableForUserAbort(t *testing.T) {
	if !isNonRetriable(&provider.Error{Code: provider.ErrAbortedByUser}) {
		t.Fatal("expected user abort to be non-retriable")
	}
}

func TestResumeWaitingToRunningWhenExpired(t *testing.T) {
	st := runstore.RunState{Status: "waiting", WaitingUntil: time.Now().Add(-time.Second).Format(time.RFC3339)}
	ms := &memStore{state: st}
	o := New(&fakeProvider{}, ms, NopSink{})
	o.now = func() time.Time { return time.Now() }
	cfg := RunConfig{RunID: "r1", Prompt: "x", ProviderTimeout: time.Second, MaxConsecutiveFailures: 3, Resume: true}
	got, err := o.bootstrapState(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "running" {
		t.Fatalf("status = %s", got.Status)
	}
}

type memStore struct {
	state runstore.RunState
}

func (m *memStore) AcquireLock(string, time.Duration) error        { return nil }
func (m *memStore) ReleaseLock(string) error                       { return nil }
func (m *memStore) WriteState(_ string, s runstore.RunState) error { m.state = s; return nil }
func (m *memStore) LoadState(string) (runstore.RunState, error)    { return m.state, nil }
func (m *memStore) ReadNotes(string) ([]string, error)             { return nil, nil }
func (m *memStore) AppendNote(string, string) error                { return nil }
func (m *memStore) AppendIterationEvent(string, int, any) error    { return nil }

type fakeProvider struct{ err error }

func (f *fakeProvider) RunIteration(ctx context.Context, req provider.IterationRequest, onEvent func(provider.ProviderEvent)) (provider.IterationResult, error) {
	if f.err != nil {
		return provider.IterationResult{}, f.err
	}
	onEvent(provider.ProviderEvent{Kind: "usage", Usage: &provider.UsageDelta{InputTokensDelta: 5}})
	return provider.IterationResult{SchemaVersion: 1, Success: true, Summary: "ok", Changes: []string{"a"}, Learnings: []string{"b"}, Activity: "done", Usage: provider.UsageTotals{InputTokens: 5}}, nil
}

func TestRunAbortsOnNonRetriableError(t *testing.T) {
	ms := &memStore{}
	o := New(&fakeProvider{err: &provider.Error{Code: provider.ErrMaxTokens, Message: "cap"}}, ms, NopSink{})
	o.sleep = func(time.Duration) {}
	err := o.Run(context.Background(), RunConfig{RunID: "r1", Prompt: "x", ProviderTimeout: time.Second, MaxConsecutiveFailures: 3})
	if err == nil {
		t.Fatal("expected error")
	}
	var pe *provider.Error
	if !errors.As(err, &pe) {
		t.Fatalf("expected provider error, got %T", err)
	}
}

func TestRunStopsCleanlyWhenContextCanceled(t *testing.T) {
	ms := &memStore{}
	o := New(&blockingProvider{}, ms, NopSink{})
	o.sleep = func(time.Duration) {}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := o.Run(ctx, RunConfig{RunID: "r1", Prompt: "x", ProviderTimeout: time.Second, MaxConsecutiveFailures: 3})
	if err != nil {
		t.Fatalf("expected clean stop on canceled context, got %v", err)
	}
	if ms.state.Status != "stopped" {
		t.Fatalf("expected stopped status, got %q", ms.state.Status)
	}
}

type blockingProvider struct{}

func (b *blockingProvider) RunIteration(ctx context.Context, req provider.IterationRequest, onEvent func(provider.ProviderEvent)) (provider.IterationResult, error) {
	<-ctx.Done()
	return provider.IterationResult{}, &provider.Error{Code: provider.ErrAbortedByUser, Message: "canceled"}
}
