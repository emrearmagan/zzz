package provider

import (
	"context"
	"math/rand"
	"strconv"
	"strings"
	"time"
)

type MockProvider struct {
	seed     int64
	minDelay time.Duration
	maxDelay time.Duration
}

func NewMockProvider(seed int64) *MockProvider {
	return &MockProvider{
		seed:     seed,
		minDelay: 3 * time.Second,
		maxDelay: 8 * time.Second,
	}
}

func NewFastMockProvider(seed int64) *MockProvider {
	return &MockProvider{
		seed:     seed,
		minDelay: 20 * time.Millisecond,
		maxDelay: 40 * time.Millisecond,
	}
}

func (m *MockProvider) RunIteration(ctx context.Context, req IterationRequest, onEvent func(ProviderEvent)) (IterationResult, error) {
	if onEvent == nil {
		onEvent = func(ProviderEvent) {}
	}
	if req.SchemaVersion == 0 {
		req.SchemaVersion = 1
	}

	rng := rand.New(rand.NewSource(m.seed + int64(req.Iteration) + time.Now().UnixNano()))
	phases := []string{"analyze", "plan", "implement", "validate", "summarize"}
	phaseActivity := map[string][]string{
		"analyze": {
			"Interrogating the repository like a noir detective with three coffees",
			"Profiling the codebase for suspicious patterns and tiny chaos gremlins",
			"Sniff-testing assumptions before we step on expensive rakes",
		},
		"plan": {
			"Drafting a tiny plan that even Monday-morning me cannot misread",
			"Sketching a low-risk route that dodges dramatic refactor monologues",
			"Building a checklist so scope creep gets bounced at the door",
		},
		"implement": {
			"Dropping surgical edits while the scope gremlin is busy eating TODOs",
			"Shipping precise patches and refusing to start a side quest",
			"Threading tiny changes through high-value paths like a code ninja",
		},
		"validate": {
			"Running validation checks and asking tests to behave like adults",
			"Stress-checking the patch so future-you sleeps slightly better",
			"Sweeping edge cases with a flashlight and responsible paranoia",
		},
		"summarize": {
			"Writing notes that tomorrow's brain will high-five us for",
			"Capturing tiny wins before context evaporates into cosmic dust",
			"Documenting tradeoffs so the next lap starts sharper and sassier",
		},
	}
	activity := ""
	totals := UsageTotals{}

	for i, phase := range phases {
		select {
		case <-ctx.Done():
			return IterationResult{}, &Error{Code: ErrAbortedByUser, Message: "mock provider canceled"}
		default:
		}

		options := phaseActivity[phase]
		variant := 0
		if len(options) > 0 {
			variant = (req.Iteration + i + int(m.seed)) % len(options)
		}
		activity = options[variant]
		now := time.Now().UTC().Format(time.RFC3339)
		onEvent(ProviderEvent{Time: now, Kind: "phase", Activity: phase})
		onEvent(ProviderEvent{Time: now, Kind: "activity", Activity: activity})

		delta := UsageDelta{
			InputTokensDelta:      60 + rng.Intn(80),
			OutputTokensDelta:     30 + rng.Intn(55),
			CacheReadTokensDelta:  rng.Intn(24),
			CacheWriteTokensDelta: rng.Intn(16),
		}
		totals.InputTokens += delta.InputTokensDelta
		totals.OutputTokens += delta.OutputTokensDelta
		totals.CacheReadTokens += delta.CacheReadTokensDelta
		totals.CacheWriteTokens += delta.CacheWriteTokensDelta
		onEvent(ProviderEvent{Time: now, Kind: "usage", Usage: &delta})

		delay := randomDelay(rng, m.minDelay, m.maxDelay)
		tmr := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			tmr.Stop()
			return IterationResult{}, &Error{Code: ErrAbortedByUser, Message: "mock provider canceled"}
		case <-tmr.C:
		}
	}

	return IterationResult{
		SchemaVersion: 1,
		Success:       true,
		Summary:       "Tiny win shipped: mock iteration landed cleanly and the chaos gremlin stayed unemployed.",
		Changes:       []string{"tightened iteration notes", "validated provider result contract", "kept blast radius intentionally small"},
		Learnings:     []string{"small diffs ship faster", "explicit state beats mysterious magic", "humor reduces overnight automation anxiety"},
		Activity:      activity,
		Usage:         totals,
	}, nil
}

func randomDelay(rng *rand.Rand, minDelay time.Duration, maxDelay time.Duration) time.Duration {
	if maxDelay <= minDelay {
		return minDelay
	}
	span := maxDelay - minDelay
	return minDelay + time.Duration(rng.Int63n(int64(span)+1))
}

func (m *MockProvider) Ask(ctx context.Context, question string, in AskContext) <-chan AskEvent {
	out := make(chan AskEvent, 16)
	go func() {
		defer close(out)

		rng := rand.New(rand.NewSource(m.seed + int64(len(question))*31 + int64(in.Iteration)*17 + time.Now().UnixNano()))
		thinking := []string{
			"Mapping your question against current run state...",
			"Checking whether the scope gremlin is sneaking in extras...",
			"Consulting the tiny committee of overcaffeinated linters...",
			"Translating chaos into something your future self can trust...",
			"Trying three bad ideas first so we can pick the good one...",
			"Cross-referencing notes before I say something reckless...",
			"Simulating edge-cases that usually show up at 3 AM...",
			"Preparing an answer with fewer vibes and more signal...",
		}

		totalDelay := randomDelay(rng, 3*time.Second, 8*time.Second)
		ticker := time.NewTicker(1200 * time.Millisecond)
		defer ticker.Stop()
		timer := time.NewTimer(totalDelay)
		defer timer.Stop()

		index := rng.Intn(len(thinking))
		select {
		case out <- AskEvent{Kind: AskEventThinking, Update: AskUpdate{Thinking: thinking[index]}}:
		case <-ctx.Done():
			out <- AskEvent{Kind: AskEventError, Err: &Error{Code: ErrAbortedByUser, Message: "ask canceled"}}
			return
		}

		for {
			select {
			case <-ctx.Done():
				out <- AskEvent{Kind: AskEventError, Err: &Error{Code: ErrAbortedByUser, Message: "ask canceled"}}
				return
			case <-ticker.C:
				index = (index + 1 + rng.Intn(3)) % len(thinking)
				select {
				case out <- AskEvent{Kind: AskEventThinking, Update: AskUpdate{Thinking: thinking[index]}}:
				case <-ctx.Done():
					out <- AskEvent{Kind: AskEventError, Err: &Error{Code: ErrAbortedByUser, Message: "ask canceled"}}
					return
				}
			case <-timer.C:
				response := buildMockAskResponse(question, in)
				for _, chunk := range chunkResponse(response, 28) {
					select {
					case out <- AskEvent{Kind: AskEventAnswerDelta, ResponseDelta: chunk}:
					case <-ctx.Done():
						out <- AskEvent{Kind: AskEventError, Err: &Error{Code: ErrAbortedByUser, Message: "ask canceled"}}
						return
					}
					streamDelay := randomDelay(rng, 90*time.Millisecond, 180*time.Millisecond)
					pause := time.NewTimer(streamDelay)
					select {
					case <-ctx.Done():
						pause.Stop()
						out <- AskEvent{Kind: AskEventError, Err: &Error{Code: ErrAbortedByUser, Message: "ask canceled"}}
						return
					case <-pause.C:
					}
				}
				select {
				case out <- AskEvent{Kind: AskEventAnswerFinal, Response: response}:
				case <-ctx.Done():
				}
				return
			}
		}
	}()
	return out
}

func chunkResponse(s string, maxRunes int) []string {
	if maxRunes < 8 {
		maxRunes = 8
	}
	runes := []rune(s)
	if len(runes) == 0 {
		return nil
	}
	chunks := make([]string, 0, len(runes)/maxRunes+1)
	for start := 0; start < len(runes); start += maxRunes {
		end := start + maxRunes
		if end > len(runes) {
			end = len(runes)
		}
		chunks = append(chunks, string(runes[start:end]))
	}
	return chunks
}

func buildMockAskResponse(question string, in AskContext) string {
	q := strings.TrimSpace(question)
	if q == "" {
		q = "(empty question)"
	}
	return "Nice question - here's the quick read:\n" +
		"- Iteration: " + itoa(in.Iteration) +
		"- Tokens burned: " + itoa(in.TotalTokens) + "\n" +
		"- Take: keep changes small, keep intent explicit, ship in slices.\n" +
		"You asked: \"" + truncateForReply(q, 120) + "\"\n" +
		"If you want, I can turn that into a concrete next-step checklist after this run."
}

func truncateForReply(s string, max int) string {
	r := []rune(s)
	if len(r) <= max {
		return s
	}
	if max < 2 {
		return string(r[:max])
	}
	return string(r[:max-1]) + "…"
}

func itoa(v int) string {
	return strconv.Itoa(v)
}
