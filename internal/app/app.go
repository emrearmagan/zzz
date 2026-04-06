package app

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"zzz/internal/config"
	"zzz/internal/gitstate"
	"zzz/internal/logx"
	"zzz/internal/orchestrator"
	"zzz/internal/provider"
	"zzz/internal/runstore"
)

type RunInput struct {
	Prompt                 string
	Path                   string
	UnsafeNoGit            bool
	Mock                   bool
	Debug                  bool
	SingleShot             bool
	ShowVersion            bool
	Provider               string
	Model                  string
	MaxIterations          int
	MaxTokens              int
	NoBranch               bool
	RunID                  string
	ResumeLatest           bool
	ProviderTimeoutSeconds int
}

type parseFlags struct {
	prompt                 string
	path                   string
	unsafeNoGit            bool
	mock                   bool
	debug                  bool
	singleShot             bool
	showVersion            bool
	provider               string
	model                  string
	maxIterations          int
	maxTokens              int
	noBranch               bool
	runID                  string
	resumeLatest           bool
	providerTimeoutSeconds int
}

func Run(ctx context.Context, args []string, stdout io.Writer, stderr io.Writer) error {
	parsed, err := parseArgs(args)
	if err != nil {
		if errors.Is(err, flag.ErrHelp) {
			_, _ = io.WriteString(stdout, Usage())
			return nil
		}
		return err
	}
	if parsed.showVersion {
		_, _ = io.WriteString(stdout, "zzz version 0.1.0\n")
		return nil
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	input := mergeConfig(cfg, parsed)
	if strings.TrimSpace(input.Prompt) == "" && !input.ResumeLatest && input.RunID == "" {
		return errors.New("prompt is required unless resuming an existing run")
	}
	if input.RunID != "" && input.ResumeLatest {
		return errors.New("--run-id and --resume-latest cannot be used together")
	}
	if err := validateRunInput(input); err != nil {
		return err
	}

	repoRoot, err := resolveRepoRoot(input.Path)
	if err != nil {
		return err
	}
	if err := withWorkingDir(repoRoot, func() error {
		return runInRepo(ctx, repoRoot, input, cfg, stdout, stderr)
	}); err != nil {
		return err
	}
	return nil
}

func runInRepo(ctx context.Context, repoRoot string, input RunInput, cfg config.RunConfig, stdout io.Writer, stderr io.Writer) error {
	_ = stderr

	var logStream <-chan string
	logLevel := logx.LevelInfo
	if input.Debug {
		logLevel = logx.LevelDebug
	}
	logger, err := logx.New(logx.Config{
		Level:   logLevel,
		Pretty:  input.Debug,
		Console: false,
		File: &logx.File{
			Path:       filepath.Join(repoRoot, ".zzz", "zzz.log"),
			MaxSize:    10,
			MaxBackups: 3,
			MaxAge:     14,
			Compress:   false,
		},
		StreamBuffer: 256,
	})
	if err != nil {
		return fmt.Errorf("init logger: %w", err)
	}
	logx.SetDefault(logger)
	logStream = logger.Stream()
	if input.Debug {
		logx.Infof("debug logging enabled (file only)")
		logx.Debugf("startup: provider=%s model=%s single_shot=%t mock=%t", input.Provider, input.Model, input.SingleShot, input.Mock)
		logx.Debugf("startup: max_iterations=%d max_tokens=%d timeout=%ds", input.MaxIterations, input.MaxTokens, input.ProviderTimeoutSeconds)
		logx.Debugf("startup: repo_root=%s", repoRoot)
	}

	ins := gitstate.New()
	if !input.UnsafeNoGit {
		if err := ins.EnsureRepository(); err != nil {
			return err
		}
		logx.Debugf("git repository validated")
	}

	runner, err := provider.New(input.Provider, input.Mock)
	if err != nil {
		return err
	}
	logx.Debugf("provider initialized: %s", input.Provider)

	if input.SingleShot {
		logx.Infof("running in single-shot mode")
		return runSingleShot(ctx, runner, input, stdout)
	}

	store, err := runstore.New(repoRoot)
	if err != nil {
		return err
	}
	logx.Debugf("run store ready")
	if err := store.EnsureIgnore(); err != nil {
		if !input.UnsafeNoGit {
			return err
		}
	}

	runID, branchName, resume, err := resolveRunSelection(input, ins, store)
	if err != nil {
		return err
	}
	logx.Debugf("run selection resolved: run_id=%s branch=%s resume=%t", runID, branchName, resume)

	orchCfg := orchestrator.RunConfig{
		RunID:                  runID,
		Prompt:                 input.Prompt,
		Provider:               input.Provider,
		Model:                  input.Model,
		MaxIterations:          input.MaxIterations,
		MaxTokens:              input.MaxTokens,
		MaxConsecutiveFailures: cfg.MaxConsecutiveFailures,
		ProviderTimeout:        time.Duration(input.ProviderTimeoutSeconds) * time.Second,
		LockStaleAfter:         time.Duration(cfg.LockStaleSeconds) * time.Second,
		NotesMaxEntries:        cfg.NotesMaxEntries,
		NotesCharBudget:        cfg.NotesCharBudget,
		BranchName:             branchName,
		Resume:                 resume,
	}

	if _, ok := stdout.(*os.File); ok && os.Getenv("TERM") != "" {
		logx.Debugf("launching TUI runtime")
		return runWithTUI(ctx, orchCfg, runner, store, input.Mock, input.Debug, logStream)
	}

	o := orchestrator.New(runner, store, orchestrator.NopSink{})
	if err := o.Run(ctx, orchCfg); err != nil {
		return err
	}
	_, _ = io.WriteString(stdout, "zzz run completed\n")
	return nil
}

func validateRunInput(in RunInput) error {
	if in.MaxIterations < 0 {
		return errors.New("max-iterations must be >= 0")
	}
	if in.MaxTokens < 0 {
		return errors.New("max-tokens must be >= 0")
	}
	if in.ProviderTimeoutSeconds < 1 {
		return errors.New("provider-timeout-seconds must be >= 1")
	}
	if in.SingleShot && (in.RunID != "" || in.ResumeLatest) {
		return errors.New("--single-shot cannot be combined with --run-id or --resume-latest")
	}
	return nil
}

func runSingleShot(ctx context.Context, runner provider.Runner, input RunInput, stdout io.Writer) error {
	req := provider.IterationRequest{
		SchemaVersion: 1,
		RunID:         fmt.Sprintf("single-%d", time.Now().Unix()),
		Iteration:     1,
		Provider:      input.Provider,
		Model:         input.Model,
		Prompt:        strings.TrimSpace(input.Prompt),
		StartedAt:     time.Now().UTC().Format(time.RFC3339),
		Limits: provider.Limits{
			MaxIterations: 1,
			MaxTokens:     input.MaxTokens,
		},
	}

	result, err := runner.RunIteration(ctx, req, nil)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}

func resolveRunSelection(input RunInput, ins *gitstate.Inspector, store *runstore.Store) (runID string, branch string, resume bool, err error) {
	if input.UnsafeNoGit {
		if input.RunID != "" {
			if !store.RunExists(input.RunID) {
				return "", "", false, ValidateResumeSelection(input.RunID, false, false)
			}
			return input.RunID, "", true, nil
		}
		if input.ResumeLatest {
			runID, err := store.ResolveLatestRunForRepo()
			if err != nil {
				return "", "", false, err
			}
			return runID, "", true, nil
		}
		runID = fmt.Sprintf("run-%d", time.Now().Unix())
		if _, err := store.CreateRun(runID, input.Prompt, ""); err != nil {
			return "", "", false, err
		}
		return runID, "", false, nil
	}

	if input.RunID != "" {
		if !store.RunExists(input.RunID) {
			return "", "", false, ValidateResumeSelection(input.RunID, false, false)
		}
		meta, err := store.LoadMeta(input.RunID)
		if err != nil {
			return "", "", false, err
		}
		if err := ins.EnsureResumeSafe(meta.BranchName); err != nil {
			return "", "", false, err
		}
		return input.RunID, meta.BranchName, true, nil
	}

	if input.ResumeLatest {
		runID, err := store.ResolveLatestRunForRepo()
		if err != nil {
			return "", "", false, err
		}
		meta, err := store.LoadMeta(runID)
		if err != nil {
			return "", "", false, err
		}
		if err := ins.EnsureResumeSafe(meta.BranchName); err != nil {
			return "", "", false, err
		}
		return runID, meta.BranchName, true, nil
	}

	if err := ins.EnsureNewRunSafe(); err != nil {
		return "", "", false, err
	}
	runID = fmt.Sprintf("run-%d", time.Now().Unix())
	if !input.NoBranch {
		branch = "zzz/" + runID
	}
	if _, err := store.CreateRun(runID, input.Prompt, branch); err != nil {
		return "", "", false, err
	}
	if !input.NoBranch {
		if err := ins.EnsureOrCreateRunBranch(branch); err != nil {
			return "", "", false, err
		}
	} else {
		current, err := ins.CurrentBranch()
		if err != nil {
			return "", "", false, err
		}
		branch = current
	}
	return runID, branch, false, nil
}

func isUserAbortOrCanceled(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}
	pe := &provider.Error{}
	if errors.As(err, &pe) {
		return pe.Code == provider.ErrAbortedByUser
	}
	return false
}

func parseArgs(args []string) (parseFlags, error) {
	var p parseFlags
	fs := flag.NewFlagSet("zzz", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	fs.StringVar(&p.prompt, "prompt", "", "prompt")
	fs.BoolVar(&p.showVersion, "v", false, "show version number")
	fs.BoolVar(&p.showVersion, "version", false, "show version number")
	fs.StringVar(&p.path, "path", "", "target repository path")
	fs.BoolVar(&p.unsafeNoGit, "unsafe-no-git", false, "allow running without git repository checks")
	fs.BoolVar(&p.mock, "mock", false, "use mock provider mode")
	fs.BoolVar(&p.debug, "debug", false, "enable debug logs and show log panel in tui")
	fs.BoolVar(&p.singleShot, "single-shot", false, "run provider once and print structured output")
	fs.StringVar(&p.provider, "provider", "", "provider")
	fs.StringVar(&p.model, "model", "", "model")
	fs.IntVar(&p.maxIterations, "max-iterations", -1, "max iterations (default 5)")
	fs.IntVar(&p.maxTokens, "max-tokens", 0, "max tokens")
	fs.BoolVar(&p.noBranch, "no-branch", false, "disable branch isolation")
	fs.StringVar(&p.runID, "run-id", "", "resume run id")
	fs.BoolVar(&p.resumeLatest, "resume-latest", false, "resume latest run")
	fs.IntVar(&p.providerTimeoutSeconds, "provider-timeout-seconds", 0, "provider timeout in seconds")

	flagArgs, positionalArgs := splitInterspersedArgs(args)
	if err := fs.Parse(flagArgs); err != nil {
		return parseFlags{}, err
	}

	if strings.TrimSpace(p.prompt) == "" {
		leftover := append(positionalArgs, fs.Args()...)
		p.prompt = strings.TrimSpace(strings.Join(leftover, " "))
	}
	return p, nil
}

func splitInterspersedArgs(args []string) (flagArgs []string, positionalArgs []string) {
	valueFlags := map[string]bool{
		"--prompt":                   true,
		"--path":                     true,
		"--provider":                 true,
		"--model":                    true,
		"--max-iterations":           true,
		"--max-tokens":               true,
		"--run-id":                   true,
		"--provider-timeout-seconds": true,
	}

	for i := 0; i < len(args); i++ {
		arg := args[i]

		if arg == "--" {
			positionalArgs = append(positionalArgs, args[i+1:]...)
			break
		}

		if strings.HasPrefix(arg, "--") {
			if strings.Contains(arg, "=") {
				flagArgs = append(flagArgs, arg)
				continue
			}
			flagArgs = append(flagArgs, arg)
			if valueFlags[arg] && i+1 < len(args) {
				i++
				flagArgs = append(flagArgs, args[i])
			}
			continue
		}

		if strings.HasPrefix(arg, "-") {
			flagArgs = append(flagArgs, arg)
			continue
		}

		positionalArgs = append(positionalArgs, arg)
	}

	return flagArgs, positionalArgs
}

func mergeConfig(cfg config.RunConfig, p parseFlags) RunInput {
	out := RunInput{
		Prompt:                 p.prompt,
		Path:                   p.path,
		UnsafeNoGit:            p.unsafeNoGit,
		Mock:                   p.mock,
		Debug:                  p.debug,
		SingleShot:             p.singleShot,
		ShowVersion:            p.showVersion,
		Provider:               cfg.Provider,
		Model:                  cfg.DefaultModel,
		MaxIterations:          cfg.MaxIterations,
		MaxTokens:              p.maxTokens,
		NoBranch:               p.noBranch,
		RunID:                  p.runID,
		ResumeLatest:           p.resumeLatest,
		ProviderTimeoutSeconds: cfg.ProviderTimeoutSeconds,
	}

	if p.provider != "" {
		out.Provider = p.provider
	}
	if p.model != "" {
		out.Model = p.model
	}
	if p.providerTimeoutSeconds > 0 {
		out.ProviderTimeoutSeconds = p.providerTimeoutSeconds
	}
	if p.maxIterations >= 0 {
		out.MaxIterations = p.maxIterations
	}
	if p.mock {
		out.UnsafeNoGit = true
	}
	return out
}

func resolveRepoRoot(pathFlag string) (string, error) {
	if strings.TrimSpace(pathFlag) == "" {
		return os.Getwd()
	}
	abs, err := filepath.Abs(pathFlag)
	if err != nil {
		return "", err
	}
	st, err := os.Stat(abs)
	if err != nil {
		return "", err
	}
	if !st.IsDir() {
		return "", fmt.Errorf("path is not a directory: %s", abs)
	}
	return abs, nil
}

func withWorkingDir(dir string, fn func() error) error {
	orig, err := os.Getwd()
	if err != nil {
		return err
	}
	if err := os.Chdir(dir); err != nil {
		return err
	}
	defer func() { _ = os.Chdir(orig) }()
	return fn()
}

func ValidateResumeSelection(runID string, resumeLatest bool, exists bool) error {
	if runID != "" && !exists {
		return fmt.Errorf("run-id %q does not exist", runID)
	}
	if runID != "" && resumeLatest {
		return errors.New("--run-id and --resume-latest cannot be used together")
	}
	return nil
}
