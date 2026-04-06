package orchestrator

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"zzz/internal/provider"
	"zzz/internal/runstore"
)

func TestOrchestratorStopsOnMaxIterations(t *testing.T) {
	repo := t.TempDir()
	prepareRepoLikeLayout(t, repo)
	store, err := runstore.New(repo)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := store.CreateRun("r1", "build", "")
	if err != nil {
		t.Fatal(err)
	}
	o := New(provider.NewFastMockProvider(1), store, NopSink{})
	o.sleep = func(time.Duration) {}
	err = o.Run(context.Background(), RunConfig{
		RunID:                  runID,
		Prompt:                 "build",
		Provider:               "opencode",
		Model:                  "m",
		MaxIterations:          2,
		MaxConsecutiveFailures: 3,
		ProviderTimeout:        2 * time.Second,
		LockStaleAfter:         15 * time.Minute,
		NotesMaxEntries:        20,
		NotesCharBudget:        12000,
	})
	if err != nil {
		t.Fatal(err)
	}
	st, err := store.LoadState(runID)
	if err != nil {
		t.Fatal(err)
	}
	if st.LastCompleted != 2 || st.Status != "stopped" {
		t.Fatalf("unexpected state: %+v", st)
	}
}

func prepareRepoLikeLayout(t *testing.T, root string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(root, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".git", "info", "exclude"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
}
