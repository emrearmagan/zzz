package runstore

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCreateRunAndStateRoundTrip(t *testing.T) {
	repo := fakeRepo(t)
	s, err := New(repo)
	if err != nil {
		t.Fatal(err)
	}
	runID, err := s.CreateRun("abc-1", "do it", "zzz/abc")
	if err != nil {
		t.Fatal(err)
	}
	st := RunState{Status: "running", CurrentIteration: 1, LastCompleted: 0, BranchName: "zzz/abc"}
	if err := s.WriteState(runID, st); err != nil {
		t.Fatal(err)
	}
	got, err := s.LoadState(runID)
	if err != nil {
		t.Fatal(err)
	}
	if got.SchemaVersion != schemaVersion {
		t.Fatalf("schema_version = %d", got.SchemaVersion)
	}
	if got.Status != "running" {
		t.Fatalf("status = %q", got.Status)
	}
}

func TestAcquireLockReclaimWritesHistory(t *testing.T) {
	repo := fakeRepo(t)
	s, _ := New(repo)
	runID, err := s.CreateRun("r1", "do", "")
	if err != nil {
		t.Fatal(err)
	}
	old := Lock{PID: 999999, Hostname: s.hostname, AcquiredAt: time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)}
	if err := writeJSONAtomic(filepath.Join(repo, ".zzz", "runs", runID, ".lock"), old); err != nil {
		t.Fatal(err)
	}
	if err := s.AcquireLock(runID, time.Minute); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(repo, ".zzz", "runs", runID, "lock-history.log"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "999999") {
		t.Fatalf("lock history missing previous lock: %s", string(b))
	}
}

func TestAcquireLockRejectsFreshLiveLock(t *testing.T) {
	repo := fakeRepo(t)
	s, _ := New(repo)
	runID, _ := s.CreateRun("r2", "do", "")
	live := Lock{PID: os.Getpid(), Hostname: s.hostname, AcquiredAt: time.Now().UTC().Format(time.RFC3339)}
	if err := writeJSONAtomic(filepath.Join(repo, ".zzz", "runs", runID, ".lock"), live); err != nil {
		t.Fatal(err)
	}
	if err := s.AcquireLock(runID, 5*time.Minute); err == nil {
		t.Fatal("expected lock conflict error")
	}
}

func fakeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, ".git", "info"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, ".git", "info", "exclude"), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}
