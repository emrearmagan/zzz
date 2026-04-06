package gitstate

import (
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

type DiffStats struct {
	FilesChanged int
	Insertions   int
	Deletions    int
}

type Inspector struct{}

func New() *Inspector { return &Inspector{} }

func (i *Inspector) EnsureRepository() error {
	_, err := i.run("git", "rev-parse", "--is-inside-work-tree")
	if err != nil {
		return errors.New("current directory is not a git repository")
	}
	return nil
}

func (i *Inspector) CurrentBranch() (string, error) {
	out, err := i.run("git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

func (i *Inspector) IsClean() (bool, error) {
	out, err := i.run("git", "status", "--porcelain")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) == "", nil
}

func (i *Inspector) EnsureNewRunSafe() error {
	clean, err := i.IsClean()
	if err != nil {
		return err
	}
	if !clean {
		return errors.New("new run requires clean working tree")
	}
	return nil
}

func (i *Inspector) EnsureResumeSafe(expectedBranch string) error {
	branch, err := i.CurrentBranch()
	if err != nil {
		return err
	}
	if expectedBranch != "" && branch != expectedBranch {
		return fmt.Errorf("current branch %q does not match run branch %q", branch, expectedBranch)
	}
	return nil
}

func (i *Inspector) EnsureOrCreateRunBranch(branch string) error {
	if branch == "" {
		return nil
	}
	if _, err := i.run("git", "show-ref", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		_, err = i.run("git", "checkout", branch)
		return err
	}
	_, err := i.run("git", "checkout", "-b", branch)
	return err
}

func (i *Inspector) DiffNumstatFromHEAD() (DiffStats, error) {
	out, err := i.run("git", "diff", "--numstat", "HEAD")
	if err != nil {
		return DiffStats{}, err
	}
	stats := DiffStats{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}
		add, _ := strconv.Atoi(parts[0])
		del, _ := strconv.Atoi(parts[1])
		stats.FilesChanged++
		stats.Insertions += add
		stats.Deletions += del
	}
	return stats, nil
}

func (i *Inspector) run(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	b, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%s %v failed: %w (%s)", name, args, err, strings.TrimSpace(string(b)))
	}
	return string(b), nil
}
