package templates

import (
	"strings"
	"testing"
)

func TestBuildIterationPromptIncludesBounds(t *testing.T) {
	p := BuildIterationPrompt(PromptInput{
		Objective:       "Ship feature",
		Iteration:       2,
		MaxIterations:   5,
		MaxTokens:       1000,
		Notes:           []string{"n1", "n2", "n3"},
		NotesMax:        2,
		NotesCharBudget: 1000,
	})
	if !strings.Contains(p, "2 / 5") {
		t.Fatalf("prompt missing iteration cap: %s", p)
	}
	if strings.Contains(p, "n1") {
		t.Fatalf("old note should be trimmed: %s", p)
	}
	if !strings.Contains(p, "Make at least one concrete code/file change") {
		t.Fatalf("prompt missing execution requirement: %s", p)
	}
}

func TestBoundedNotesCompactsByBudget(t *testing.T) {
	notes := []string{strings.Repeat("a", 200), strings.Repeat("b", 200)}
	got := boundedNotes(notes, 2, 150)
	if !strings.Contains(got, "compacted") {
		t.Fatalf("expected compacted marker: %s", got)
	}
}
