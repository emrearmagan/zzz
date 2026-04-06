package provider

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestFactorySupportsOpencode(t *testing.T) {
	r, err := New("opencode", true)
	if err != nil {
		t.Fatal(err)
	}
	if r == nil {
		t.Fatal("runner is nil")
	}
}

func TestFactoryRequiresMockForNow(t *testing.T) {
	r, err := New("opencode", false)
	if err != nil {
		t.Fatalf("expected live provider to be available: %v", err)
	}
	if r == nil {
		t.Fatal("expected non-nil live provider")
	}
}

func TestRunIterationProducesNormalizedResult(t *testing.T) {
	p := NewFastMockProvider(1)
	res, err := p.RunIteration(context.Background(), IterationRequest{SchemaVersion: 1, Iteration: 1}, func(ProviderEvent) {})
	if err != nil {
		t.Fatal(err)
	}
	if !res.Success || res.Summary == "" || res.Activity == "" {
		t.Fatalf("bad result: %+v", res)
	}
	if len(res.Changes) == 0 || len(res.Learnings) == 0 {
		t.Fatalf("missing structured fields: %+v", res)
	}
}

func TestRunIterationContextCancelMapsErrorCode(t *testing.T) {
	p := NewFastMockProvider(1)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := p.RunIteration(ctx, IterationRequest{SchemaVersion: 1, Iteration: 1}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	pe, ok := err.(*Error)
	if !ok || pe.Code != ErrAbortedByUser {
		t.Fatalf("unexpected error: %T %v", err, err)
	}
}

func TestProviderEventUsageDeltasAreNonNegative(t *testing.T) {
	p := NewFastMockProvider(1)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()
	_, err := p.RunIteration(ctx, IterationRequest{SchemaVersion: 1, Iteration: 1}, func(ev ProviderEvent) {
		if ev.Usage != nil {
			if ev.Usage.InputTokensDelta < 0 || ev.Usage.OutputTokensDelta < 0 || ev.Usage.CacheReadTokensDelta < 0 || ev.Usage.CacheWriteTokensDelta < 0 {
				t.Fatalf("negative usage delta: %+v", ev.Usage)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestMockProviderEmitsDescriptiveActivityText(t *testing.T) {
	p := NewFastMockProvider(1)
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Second)
	defer cancel()

	seen := []string{}
	_, err := p.RunIteration(ctx, IterationRequest{SchemaVersion: 1, Iteration: 2}, func(ev ProviderEvent) {
		if ev.Kind == "activity" && ev.Activity != "" {
			seen = append(seen, ev.Activity)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 {
		t.Fatal("expected activity events")
	}
	hasDescriptive := false
	for _, s := range seen {
		if strings.Contains(strings.ToLower(s), "repository") || strings.Contains(strings.ToLower(s), "validation") {
			hasDescriptive = true
			break
		}
	}
	if !hasDescriptive {
		t.Fatalf("expected descriptive activity text, got: %v", seen)
	}
}

func TestDefaultMockProviderUsesRealisticDelayRange(t *testing.T) {
	p := NewMockProvider(1)
	if p.minDelay < 500*time.Millisecond {
		t.Fatalf("expected realistic minimum delay, got %s", p.minDelay)
	}
	if p.maxDelay <= p.minDelay {
		t.Fatalf("expected max delay > min delay, got %s <= %s", p.maxDelay, p.minDelay)
	}
}

func TestMockProviderUsesPlayfulToneInSummary(t *testing.T) {
	p := NewFastMockProvider(4)
	res, err := p.RunIteration(context.Background(), IterationRequest{SchemaVersion: 1, Iteration: 3}, func(ProviderEvent) {})
	if err != nil {
		t.Fatal(err)
	}
	lower := strings.ToLower(res.Summary + " " + strings.Join(res.Learnings, " "))
	if !strings.Contains(lower, "tiny win") && !strings.Contains(lower, "gremlin") && !strings.Contains(lower, "ship") {
		t.Fatalf("expected playful mock tone, got summary=%q learnings=%v", res.Summary, res.Learnings)
	}
}

func TestMockProviderAskStreamsThinkingAndReturnsMultiline(t *testing.T) {
	p := NewFastMockProvider(7)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	updates := []string{}
	resp := ""
	deltas := 0
	for event := range p.Ask(ctx, "how are we doing?", AskContext{Iteration: 3, TotalTokens: 144, Prompt: "ship"}) {
		if strings.TrimSpace(event.Update.Thinking) != "" {
			updates = append(updates, event.Update.Thinking)
		}
		if event.Err != nil {
			t.Fatal(event.Err)
		}
		if event.ResponseDelta != "" {
			resp += event.ResponseDelta
			deltas++
		}
		if strings.TrimSpace(event.Response) != "" {
			resp = event.Response
		}
	}
	if len(updates) == 0 {
		t.Fatal("expected streamed thinking updates")
	}
	if deltas == 0 {
		t.Fatal("expected streamed response deltas")
	}
	if !strings.Contains(resp, "\n") {
		t.Fatalf("expected multiline ask response, got %q", resp)
	}
}

func TestMockProviderActivitiesVaryAcrossIterations(t *testing.T) {
	p := NewFastMockProvider(7)
	collect := func(iter int) []string {
		seen := []string{}
		_, err := p.RunIteration(context.Background(), IterationRequest{SchemaVersion: 1, Iteration: iter}, func(ev ProviderEvent) {
			if ev.Kind == "activity" {
				seen = append(seen, ev.Activity)
			}
		})
		if err != nil {
			t.Fatalf("run iteration %d failed: %v", iter, err)
		}
		return seen
	}

	a1 := collect(1)
	a2 := collect(2)
	if len(a1) == 0 || len(a2) == 0 {
		t.Fatalf("expected activity events for both iterations; got %d and %d", len(a1), len(a2))
	}
	if strings.Join(a1, "|") == strings.Join(a2, "|") {
		t.Fatalf("expected activities to vary across iterations, got identical sequences: %v", a1)
	}
}

func TestMockProviderUsesDifferentPhrasingAcrossIterations(t *testing.T) {
	p := NewFastMockProvider(11)
	extract := func(iter int) []string {
		phrases := []string{}
		_, err := p.RunIteration(context.Background(), IterationRequest{SchemaVersion: 1, Iteration: iter}, func(ev ProviderEvent) {
			if ev.Kind != "activity" || ev.Activity == "" {
				return
			}
			base := strings.Split(ev.Activity, " (iteration ")[0]
			phrases = append(phrases, base)
		})
		if err != nil {
			t.Fatalf("iteration %d failed: %v", iter, err)
		}
		return phrases
	}

	p1 := extract(1)
	p2 := extract(2)
	if len(p1) == 0 || len(p2) == 0 {
		t.Fatalf("expected phrases for both iterations")
	}
	if strings.Join(p1, "|") == strings.Join(p2, "|") {
		t.Fatalf("expected different phrase set across iterations; got same phrasing %v", p1)
	}
}

func TestMockProviderActivityOmitsIterationSuffix(t *testing.T) {
	p := NewFastMockProvider(13)
	seen := []string{}
	_, err := p.RunIteration(context.Background(), IterationRequest{SchemaVersion: 1, Iteration: 9}, func(ev ProviderEvent) {
		if ev.Kind == "activity" {
			seen = append(seen, ev.Activity)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(seen) == 0 {
		t.Fatal("expected activity events")
	}
	for _, s := range seen {
		if strings.Contains(strings.ToLower(s), "(iteration") {
			t.Fatalf("activity should not contain iteration suffix: %q", s)
		}
	}
}
