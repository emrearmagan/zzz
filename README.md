[![Go](https://img.shields.io/badge/Go-1.26-00ADD8.svg)](https://go.dev/)
[![UI](https://img.shields.io/badge/TUI-Bubble%20Tea-ffcc66.svg)](https://github.com/charmbracelet/bubbletea)
[![Provider](https://img.shields.io/badge/Provider-OpenCode-6aa6ff.svg)](#provider-flow)

# zzz

Like an intern that never asks for standup.

> [!CAUTION]
> Early project and 100% AI generated. Expect rough edges and fast iteration.

## Quick Start

```bash
# from this repo
go run . "add a tiny safe improvement"

# run against another repo
go run . --path "/path/to/target/repo" "improve test readability"
```

## Installation

```bash
# install from project root
go install .
```

## Examples

```bash
# normal loop
zzz "build smoke test"

# bounded loop
zzz --path "/path/to/repo" --max-iterations 3 "refactor tests"

# no git safety (demo only)
zzz --path "/tmp/no-git" --unsafe-no-git --mock "quick demo"

# one-shot provider output
zzz --single-shot --provider opencode --model opencode/minimax-m2.5-free "summarize this repo"
```

## Architecture

### Runtime flow

```text
CLI (internal/app)
  -> Provider + Store setup
  -> Orchestrator loop (internal/orchestrator)
       -> Prompt assembly (internal/templates)
       -> Provider iteration (internal/provider)
       -> State + artifacts (internal/runstore)
       -> TUI events (internal/tui)
  -> Final summary + stop
```

## Configuration

Optional config file:

- `~/.zzz/config.yml` (or `~/.zzz/config.json`)

Main defaults:

- provider: `opencode`
- default model: `opencode/minimax-m.2.5-free`
- max iterations: `5`
- max consecutive failures: `3`
- provider timeout: `600s`

## Design Notes

This project intentionally keeps:

- **human-readable artifacts** (`notes.md`, `chat.md`)
- **machine-readable memory** (`journal.jsonl`, `handoff.json`)
- **auditable traces** (`loop-*.trace.jsonl`)

That split keeps the run explainable for humans and deterministic for the next iteration.
