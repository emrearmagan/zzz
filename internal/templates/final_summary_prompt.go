package templates

import (
	"fmt"
	"strings"
)

type FinalSummaryInput struct {
	Objective       string
	Completed       int
	MaxIterations   int
	MaxTokens       int
	Notes           []string
	NotesMax        int
	NotesCharBudget int
}

func BuildFinalSummaryPrompt(in FinalSummaryInput) string {
	notes := boundedNotes(in.Notes, in.NotesMax, in.NotesCharBudget)
	maxIter := "unbounded"
	if in.MaxIterations > 0 {
		maxIter = fmt.Sprintf("%d", in.MaxIterations)
	}
	tokenCap := "unbounded"
	if in.MaxTokens > 0 {
		tokenCap = fmt.Sprintf("%d", in.MaxTokens)
	}

	return strings.TrimSpace(fmt.Sprintf(`You are producing the FINAL RUN SUMMARY for zzz.

Objective:
%s

Run stats:
- Completed iterations: %d
- Max iterations: %s
- Token cap: %s

Notes context:
%s

Task:
- Write a project-focused explanation of what was built/changed in this run.
- Explain the implementation in plain language so a user can understand the outcome quickly.
- Include the most important concrete file/code changes.
- Include unresolved risks or follow-ups.
- Do NOT perform any new edits; this is summary-only.

Return structured result fields:
- success=true
- summary
- changes[]
- learnings[]
- activity (e.g. "done. summary written")
`, in.Objective, in.Completed, maxIter, tokenCap, notes))
}
