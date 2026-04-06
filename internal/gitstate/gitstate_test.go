package gitstate

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestEnsureNewRunSafeRejectsDirtyTree(t *testing.T) {
	repo := initRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "x.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	ins := New()
	withDir(t, repo, func() {
		if err := ins.EnsureNewRunSafe(); err == nil {
			t.Fatal("expected dirty tree error")
		}
	})
}

func TestEnsureResumeSafeRejectsBranchMismatch(t *testing.T) {
	repo := initRepo(t)
	ins := New()
	withDir(t, repo, func() {
		if err := ins.EnsureResumeSafe("zzz/other"); err == nil {
			t.Fatal("expected mismatch error")
		}
	})
}

func initRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run(t, dir, "git", "init")
	run(t, dir, "git", "config", "user.email", "test@example.com")
	run(t, dir, "git", "config", "user.name", "tester")
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("ok\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run(t, dir, "git", "add", ".")
	run(t, dir, "git", "commit", "-m", "init")
	return dir
}

func run(t *testing.T, wd string, name string, args ...string) {
	t.Helper()
	cmd := exec.Command(name, args...)
	cmd.Dir = wd
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("%s %v: %v (%s)", name, args, err, string(out))
	}
}

func withDir(t *testing.T, dir string, fn func()) {
	t.Helper()
	orig, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(orig) })
	fn()
}
