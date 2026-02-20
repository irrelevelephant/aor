package main

import (
	"flag"
	"fmt"
	"os"
)

// ANSI escape codes for terminal output.
const (
	cReset      = "\033[0m"
	cBold       = "\033[1m"
	cDim        = "\033[2m"
	cRed        = "\033[31m"
	cGreen      = "\033[32m"
	cYellow     = "\033[33m"
	cBlue       = "\033[34m"
	cMagenta    = "\033[35m"
	cCyan       = "\033[36m"
	cGray       = "\033[90m"
	cBgDarkRed  = "\033[48;5;52m"
	cBgDarkGreen = "\033[48;5;22m"
)

// Config holds all runtime configuration from command-line flags.
type Config struct {
	EpicFilter string
	MaxTasks   int
	BatchSize  int
	MaxTurns   int
	DryRun     bool
	Supervised bool
	Yolo       bool
	SkipReview bool
	LogDir     string
	Scope      string
}

func main() {
	// Subcommand dispatch: check first arg before flag.Parse() consumes it.
	if len(os.Args) > 1 && len(os.Args[1]) > 0 && os.Args[1][0] != '-' {
		switch os.Args[1] {
		case "rev", "review":
			if err := runRev(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "%serror: %v%s\n", cRed, err, cReset)
				os.Exit(1)
			}
			return
		}
	}

	cfg := &Config{}

	flag.StringVar(&cfg.EpicFilter, "epic", "", "Only work on tasks under this epic")
	flag.IntVar(&cfg.MaxTasks, "max-tasks", 0, "Stop after N tasks (0 = unlimited)")
	flag.IntVar(&cfg.BatchSize, "batch-size", 1, "Tasks per Claude session before fresh context")
	flag.IntVar(&cfg.MaxTurns, "max-turns", 50, "Max agent turns per session")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "Show what would happen without running")
	flag.BoolVar(&cfg.Supervised, "supervised", false, "Approve each task before running")
	flag.BoolVar(&cfg.SkipReview, "no-review", false, "Skip post-task review agent")

	noYolo := flag.Bool("no-yolo", false, "Require permission prompts (default: skip permissions)")

	flag.StringVar(&cfg.LogDir, "log-dir", "", "Log directory (default: auto-detect from beads location)")
	flag.StringVar(&cfg.Scope, "scope", "", "Scope label for worktree isolation (default: auto-detect from git worktree)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `aor — Agent Orchestration Runner for beads tasks

Each task runs in a fresh Claude Code context window. Output streams in real time.
Beads data stays local (not committed to git). Only code changes are pushed.

Usage:
  aor [flags]              Run task orchestration loop
  aor rev [flags] [<ref>]  Iterative code review (see: aor rev --help)

Controls while running:
  Ctrl+C       Stop agent and exit runner
  Ctrl+C x2    Kill agent immediately and exit
  i + Enter    Interject: drop into interactive Claude, then resume loop
  s + Enter    Skip current task
  q + Enter    Quit after current task

Flags:
`)
		flag.PrintDefaults()
	}

	flag.Parse()

	cfg.Yolo = !*noYolo

	if cfg.Scope == "" {
		cfg.Scope = detectWorktreeScope()
	}

	if cfg.LogDir == "" {
		cfg.LogDir = resolveLogDir()
	}

	if err := run(cfg); err != nil {
		fmt.Fprintf(os.Stderr, "%serror: %v%s\n", cRed, err, cReset)
		os.Exit(1)
	}
}
