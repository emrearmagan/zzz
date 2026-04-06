package app

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"zzz/internal/config"
	"zzz/internal/provider"
)

func TestParseArgsPositionalPrompt(t *testing.T) {
	p, err := parseArgs([]string{"build", "the", "thing"})
	if err != nil {
		t.Fatal(err)
	}
	if p.prompt != "build the thing" {
		t.Fatalf("prompt = %q", p.prompt)
	}
}

func TestMergeConfigPrecedence(t *testing.T) {
	cfg := config.Default()
	cfg.ProviderTimeoutSeconds = 600
	merged := mergeConfig(cfg, parseFlags{
		provider:               "opencode",
		model:                  "model-1",
		maxIterations:          10,
		maxTokens:              1000,
		noBranch:               true,
		runID:                  "r1",
		providerTimeoutSeconds: 45,
	})

	if merged.ProviderTimeoutSeconds != 45 {
		t.Fatalf("timeout = %d", merged.ProviderTimeoutSeconds)
	}
	if merged.Model != "model-1" {
		t.Fatalf("model = %q", merged.Model)
	}
	if !merged.NoBranch {
		t.Fatal("no-branch not set")
	}
}

func TestParseArgsPathAndUnsafeNoGit(t *testing.T) {
	p, err := parseArgs([]string{"--path", "/tmp/repo", "--unsafe-no-git", "--mock", "--debug", "--single-shot", "test prompt"})
	if err != nil {
		t.Fatal(err)
	}
	if p.path != "/tmp/repo" {
		t.Fatalf("path = %q", p.path)
	}
	if !p.unsafeNoGit {
		t.Fatal("unsafe-no-git should be true")
	}
	if !p.mock {
		t.Fatal("mock should be true")
	}
	if !p.singleShot {
		t.Fatal("single-shot should be true")
	}
	if !p.debug {
		t.Fatal("debug should be true")
	}
	if p.prompt != "test prompt" {
		t.Fatalf("prompt = %q", p.prompt)
	}
}

func TestMergeConfigIncludesPathAndUnsafeNoGit(t *testing.T) {
	merged := mergeConfig(config.Default(), parseFlags{path: "/tmp/repo", unsafeNoGit: true, mock: true, singleShot: true})
	if merged.Path != "/tmp/repo" {
		t.Fatalf("path = %q", merged.Path)
	}
	if !merged.UnsafeNoGit {
		t.Fatal("unsafe-no-git not propagated")
	}
	if !merged.Mock {
		t.Fatal("mock not propagated")
	}
	if !merged.SingleShot {
		t.Fatal("single-shot not propagated")
	}
}

func TestRunHelpPrintsDetailedUsage(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	err := Run(context.Background(), []string{"--help"}, &out, &errOut)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	text := out.String()
	if !strings.Contains(text, "Options:") {
		t.Fatalf("usage missing options section: %s", text)
	}
	if !strings.Contains(text, "--path") || !strings.Contains(text, "--unsafe-no-git") || !strings.Contains(text, "--mock") || !strings.Contains(text, "--single-shot") || !strings.Contains(text, "--debug") {
		t.Fatalf("usage missing new flags: %s", text)
	}
	if strings.Contains(text, "--type") {
		t.Fatalf("usage should not expose removed --type flag: %s", text)
	}
}

func TestRunVersion(t *testing.T) {
	var out bytes.Buffer
	var errOut bytes.Buffer
	err := Run(context.Background(), []string{"--version"}, &out, &errOut)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.String(), "zzz version") {
		t.Fatalf("unexpected version output: %q", out.String())
	}
}

func TestParseArgsSupportsFlagsAfterPrompt(t *testing.T) {
	p, err := parseArgs([]string{"hi", "--unsafe-no-git", "--mock", "--path", "/tmp/repo"})
	if err != nil {
		t.Fatal(err)
	}
	if p.prompt != "hi" {
		t.Fatalf("prompt = %q", p.prompt)
	}
	if !p.unsafeNoGit {
		t.Fatal("unsafe-no-git should be true")
	}
	if !p.mock {
		t.Fatal("mock should be true")
	}
	if p.path != "/tmp/repo" {
		t.Fatalf("path = %q", p.path)
	}
}

func TestValidateResumeSelectionMissingRun(t *testing.T) {
	err := ValidateResumeSelection("missing", false, false)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestValidateResumeSelectionConflict(t *testing.T) {
	err := ValidateResumeSelection("abc", true, true)
	if err == nil {
		t.Fatal("expected error")
	}
}

func TestIsUserAbortOrCanceled(t *testing.T) {
	if !isUserAbortOrCanceled(&provider.Error{Code: provider.ErrAbortedByUser, Message: "stopped"}) {
		t.Fatal("expected provider aborted_by_user to be treated as clean cancel")
	}
	if !isUserAbortOrCanceled(context.Canceled) {
		t.Fatal("expected context.Canceled to be treated as clean cancel")
	}
	if isUserAbortOrCanceled(errors.New("boom")) {
		t.Fatal("unexpected generic error classification")
	}
}

func TestMergeConfigMockImpliesUnsafeNoGit(t *testing.T) {
	merged := mergeConfig(config.Default(), parseFlags{mock: true})
	if !merged.Mock {
		t.Fatal("mock should be true")
	}
	if !merged.UnsafeNoGit {
		t.Fatal("mock mode should imply unsafe-no-git for local demos")
	}
}

func TestMergeConfigUsesDefaultMaxIterationsWhenFlagOmitted(t *testing.T) {
	cfg := config.Default()
	if cfg.MaxIterations != 5 {
		t.Fatalf("default max_iterations changed unexpectedly: %d", cfg.MaxIterations)
	}

	merged := mergeConfig(cfg, parseFlags{maxIterations: -1})
	if merged.MaxIterations != 5 {
		t.Fatalf("max_iterations = %d, want 5", merged.MaxIterations)
	}

	override := mergeConfig(cfg, parseFlags{maxIterations: 0})
	if override.MaxIterations != 0 {
		t.Fatalf("max_iterations override = %d, want 0", override.MaxIterations)
	}
}

func TestValidateRunInputRejectsSingleShotResumeFlags(t *testing.T) {
	err := validateRunInput(RunInput{ProviderTimeoutSeconds: 1, SingleShot: true, RunID: "run-1"})
	if err == nil {
		t.Fatal("expected validation error")
	}
	err = validateRunInput(RunInput{ProviderTimeoutSeconds: 1, SingleShot: true, ResumeLatest: true})
	if err == nil {
		t.Fatal("expected validation error")
	}
}

func TestParseArgsRejectsRemovedTypeFlag(t *testing.T) {
	_, err := parseArgs([]string{"--type", "2", "prompt"})
	if err == nil {
		t.Fatal("expected parse error for removed --type flag")
	}
}

func TestRunMockWorksOutsideGitRepo(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repo := t.TempDir()
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = os.Chdir(wd) }()

	var out bytes.Buffer
	var errOut bytes.Buffer
	err = Run(context.Background(), []string{"--mock", "--max-iterations", "1", "test run"}, &out, &errOut)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if !strings.Contains(out.String(), "zzz run completed") {
		t.Fatalf("unexpected output: %q", out.String())
	}
	if _, statErr := os.Stat(filepath.Join(repo, ".zzz", "runs")); statErr != nil {
		t.Fatalf("expected .zzz run data to be created: %v", statErr)
	}
}

func TestRunSingleShotPrintsStructuredJSON(t *testing.T) {
	runner := stubRunner{}
	var out bytes.Buffer
	err := runSingleShot(context.Background(), runner, RunInput{Prompt: "single prompt", Provider: "opencode", Model: "m", MaxTokens: 123}, &out)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if !strings.Contains(text, "\"schema_version\"") || !strings.Contains(text, "\"summary\"") {
		t.Fatalf("expected structured output json, got: %s", text)
	}
}

type stubRunner struct{}

func (stubRunner) RunIteration(_ context.Context, _ provider.IterationRequest, _ func(provider.ProviderEvent)) (provider.IterationResult, error) {
	return provider.IterationResult{
		SchemaVersion: 1,
		Success:       true,
		Summary:       "ok",
		Changes:       []string{"c1"},
		Learnings:     []string{"l1"},
		Activity:      "done",
		Usage:         provider.UsageTotals{},
	}, nil
}
