package tui

import (
	"encoding/json"
	"hash/fnv"
	"regexp"
	"strings"
)

var quoteTextPattern = regexp.MustCompile(`"([^"]{6,})"`)

type activityContext struct {
	raw        string
	lower      string
	focus      string
	focusLower string
}

func playfulActivity(raw string) string {
	ctx := parseActivityContext(raw)
	if ctx.raw == "" {
		return ""
	}

	switch {
	case containsAny(ctx.focusLower, "sending request", "calling opencode", "contacting", "request", "provider", "stream"):
		return withNerdIcon("󰇚", pickVariant(ctx.raw,
			"pinging the brain cloud for fresh ideas",
			"knocking on opencode's door politely",
			"sending a tiny request to the model",
			"opening a hotline to code wizard central",
			"warming up the answer stream without drama",
			"request launched; waiting for smart words to land",
		))
	case containsAny(ctx.focusLower, "objective", "question", "notes", "context", "prompt"):
		return withNerdIcon("󰋼", pickVariant(ctx.raw,
			"packing objective + notes into a snack-sized brief",
			"translating your mission into model-friendly language",
			"turning context into a clean launch checklist",
			"loading mission context with maximum chill",
			"aligning goals before touching code",
			"wrangling context into one clear north star",
		))
	case containsAny(ctx.focusLower, "analy", "inspect", "read", "scan", "explor", "trace"):
		return withNerdIcon("󰊄", pickVariant(ctx.raw,
			"sniffing through files for the safest tiny win",
			"reading code like bedtime stories for engineers",
			"mapping the repo before touching anything risky",
			"hunting for the smallest high-impact change",
			"tracking root cause footprints through the codebase",
			"zooming in on the part that actually matters",
		))
	case containsAny(ctx.focusLower, "plan", "thinking", "reason", "decid", "strategy"):
		return withNerdIcon("󰔛", pickVariant(ctx.raw,
			"plotting a tiny, low-drama move",
			"doing a calm risk check before edits",
			"lining up the next step with minimal chaos",
			"thinking in small safe slices",
			"choosing the boring fix that will not wake bugs",
			"turning big thoughts into one practical step",
		))
	case containsAny(ctx.focusLower, "edit", "change", "patch", "fix", "refactor", "update", "implement", "write"):
		return withNerdIcon("󰏫", pickVariant(ctx.raw,
			"performing gentle code surgery",
			"sneaking in a neat little improvement",
			"patching the bug without waking new ones",
			"rewiring this part with tiny-footstep energy",
			"dropping in a safe tweak and locking it down",
			"teaching old code a new polite trick",
		))
	case containsAny(ctx.focusLower, "git", "branch", "commit", "diff", "merge"):
		return withNerdIcon("󰊢", pickVariant(ctx.raw,
			"keeping git history clean and drama-free",
			"organizing changes so future-you says thanks",
			"checking the diff like a code archaeologist",
			"packing edits into a tidy commit story",
			"nudging the branch toward a peaceful landing",
		))
	case containsAny(ctx.focusLower, "build", "compile", "go test", "test", "verify", "check"):
		return withNerdIcon("󰙨", pickVariant(ctx.raw,
			"running the confidence lap",
			"asking tests for their blessing",
			"compiling reality to confirm no explosions",
			"double-checking everything with responsible paranoia",
			"doing the boring checks that save entire afternoons",
			"letting CI vibes pass through locally first",
		))
	case containsAny(ctx.focusLower, "summary", "report", "final", "done", "complete", "result"):
		return withNerdIcon("󰄬", pickVariant(ctx.raw,
			"wrapping up with a crisp changelog bow",
			"writing the tiny victory note",
			"packing results for morning-you",
			"landing the task with minimal drama",
			"shipping a neat answer with clean edges",
			"closing the loop and tucking in the details",
		))
	default:
		return withNerdIcon("󰄛", pickVariant(ctx.raw,
			"doing focused tiny-work magic",
			"moving one safe step forward",
			"keeping momentum smooth and boring (the good kind)",
			"progressing quietly like a polite build daemon",
			"making calm progress while chaos stays outside",
			"trimming risk, adding clarity, keeping speed",
		))
	}
}

func withNerdIcon(icon, text string) string {
	text = strings.TrimSpace(text)
	if text == "" {
		return ""
	}
	return text + "  " + icon
}

func parseActivityContext(raw string) activityContext {
	clean := strings.TrimSpace(raw)
	if clean == "" {
		return activityContext{}
	}

	focus := extractStructuredFocus(clean)
	if focus == "" {
		focus = extractLineFocus(clean)
	}
	if focus == "" {
		focus = clean
	}

	focus = strings.Join(strings.Fields(focus), " ")
	return activityContext{
		raw:        clean,
		lower:      strings.ToLower(clean),
		focus:      focus,
		focusLower: strings.ToLower(focus),
	}
}

func extractStructuredFocus(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if !strings.HasPrefix(trimmed, "{") && !strings.HasPrefix(trimmed, "[") {
		return ""
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return ""
	}

	if v, ok := payload["activity"].(string); ok && strings.TrimSpace(v) != "" {
		return v
	}
	if v, ok := payload["summary"].(string); ok && strings.TrimSpace(v) != "" {
		return v
	}
	if arr, ok := payload["changes"].([]any); ok {
		for _, item := range arr {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
	}
	return ""
}

func extractLineFocus(raw string) string {
	lines := strings.Split(raw, "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		line = strings.TrimLeft(line, "-•*✓☑☒▶▷→ ")
		if strings.HasSuffix(strings.ToLower(line), ":") {
			continue
		}
		if isPromptEchoLike(line) {
			continue
		}
		if q := firstQuotedText(line); q != "" {
			return q
		}
		return line
	}
	return ""
}

func firstQuotedText(s string) string {
	m := quoteTextPattern.FindStringSubmatch(s)
	if len(m) > 1 {
		return strings.TrimSpace(m[1])
	}
	return ""
}

func isPromptEchoLike(s string) bool {
	lower := strings.ToLower(strings.TrimSpace(s))
	if lower == "" {
		return true
	}
	if strings.HasPrefix(lower, "objective:") || strings.HasPrefix(lower, "question:") || strings.HasPrefix(lower, "current objective:") || strings.HasPrefix(lower, "current run notes:") {
		return true
	}
	if strings.Contains(lower, "answer directly and concretely") || strings.Contains(lower, "return only the final answer") {
		return true
	}
	return false
}

func containsAny(s string, patterns ...string) bool {
	for _, p := range patterns {
		if strings.Contains(s, p) {
			return true
		}
	}
	return false
}

func pickVariant(seed string, options ...string) string {
	if len(options) == 0 {
		return ""
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(seed))
	idx := int(h.Sum32()) % len(options)
	return options[idx]
}
