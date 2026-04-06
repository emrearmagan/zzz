package templates

import (
	"fmt"
	"strings"
)

type PromptInput struct {
	Objective       string
	Iteration       int
	MaxIterations   int
	MaxTokens       int
	Notes           []string
	NotesMax        int
	NotesCharBudget int
}

func BuildIterationPrompt(in PromptInput) string {
	notes := boundedNotes(in.Notes, in.NotesMax, in.NotesCharBudget)
	cap := "unbounded"
	if in.MaxIterations > 0 {
		cap = fmt.Sprintf("%d", in.MaxIterations)
	}
	tokenCap := "unbounded"
	if in.MaxTokens > 0 {
		tokenCap = fmt.Sprintf("%d", in.MaxTokens)
	}

	return strings.TrimSpace(fmt.Sprintf(`Mission:
%s

Loop:
%d / %s

Token budget:
%s

Context pack:
%s

Output contract (JSON fields):
- success
- summary
- changes[]
- learnings[]
- activity

Loop policy:
- Start with the "Next-loop instructions" section from Context pack; treat it as primary guidance for this loop.
- Deliver one focused improvement that moves the mission forward.
- Keep edits small, safe, and easy to review.
- Run at least one relevant verification command and report result.
- If blocked, set success=false and state the exact blocker and next attempt.

Headless execution rules:
- You are running autonomously in headless mode; execute directly and do not ask clarifying questions.
- Do not mention internal process/framework chatter (no references to brainstorming, skills, or tool-selection logic).
- Do not narrate meta intent (avoid phrases like "the user wants" or "I should").
- Activity text must be short, concrete progress updates about the actual work being performed.
`, in.Objective, in.Iteration, cap, tokenCap, notes))
}

func boundedNotes(notes []string, maxEntries int, charBudget int) string {
	if len(notes) == 0 {
		return "(no notes yet)"
	}
	if maxEntries <= 0 {
		maxEntries = len(notes)
	}
	if len(notes) > maxEntries {
		notes = notes[len(notes)-maxEntries:]
	}
	joined := strings.Join(notes, "\n\n")
	if charBudget <= 0 || len(joined) <= charBudget {
		return joined
	}
	if charBudget < 100 {
		charBudget = 100
	}
	trimmed := joined[len(joined)-charBudget:]
	return "[older notes compacted]\n" + trimmed
}
