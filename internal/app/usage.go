package app

func Usage() string {
	return `Usage:
  zzz [flags] "your prompt"

Examples:
  zzz "build smoke test"
  zzz --path "/path/to/repo" "refactor tests" --max-iterations 3
  zzz --path "/tmp/no-git" --unsafe-no-git --mock "quick demo"
  zzz --single-shot --provider opencode --model opencode/minimax-m2.5-free "summarize this repo"

Options:
  -h, --help                      show help
  -v, --version                   show version number
      --path string               repository path to run in (default: current directory)
      --unsafe-no-git             allow run without git safety checks (unsafe)
      --mock                      use mock provider and skip git safety checks (demo/testing mode)
      --debug                     enable debug logs and show log panel in tui
      --single-shot               run provider once and print structured JSON output
      --prompt string             prompt to run (alternative to positional prompt)
      --provider string           provider name (default from config)
      --model string              model name (default from config)
		--max-iterations int        stop after this many iterations (default: 5, 0 = unbounded)
      --max-tokens int            stop at this total token cap (0 = unbounded)
      --provider-timeout-seconds  per-iteration provider timeout in seconds
      --no-branch                 disable automatic zzz/<run-id> branch isolation
      --run-id string             resume specific run id
      --resume-latest             resume latest run for current repository
`
}
