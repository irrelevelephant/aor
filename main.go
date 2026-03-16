package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

// ANSI escape codes for terminal output.
const (
	cReset       = "\033[0m"
	cBold        = "\033[1m"
	cDim         = "\033[2m"
	cRed         = "\033[31m"
	cGreen       = "\033[32m"
	cYellow      = "\033[33m"
	cBlue        = "\033[34m"
	cCyan        = "\033[36m"
	cGray        = "\033[90m"
	cBgDarkRed   = "\033[48;5;52m"
	cBgDarkGreen = "\033[48;5;22m"
)

// Config holds all runtime configuration from command-line flags.
type Config struct {
	EpicFilter      string
	TagFilter       string
	MaxTasks        int
	BatchSize       int
	DryRun          bool
	Supervised      bool
	Yolo            bool
	Unclaim         bool
	LogDir          string
	Workspace       string
	WorkDir         string // actual working directory (worktree path when in a linked worktree)
	ResumeSessionID string // set internally when resuming an existing session

	// Shared resources — set when run() is called as a sub-process (e.g. sweep mode).
	StdinCh         <-chan string // shared stdin reader (nil = create own)
	Log             *Logger      // shared logger (nil = create own)
	Stats           *RunStats    // shared stats (nil = create own)
	SuppressSummary bool         // skip printSummary
	SkipRecovery    bool         // skip recoverStuckTasks (caller already ran it)
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
		case "pull":
			if err := runPull(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "%serror: %v%s\n", cRed, err, cReset)
				os.Exit(1)
			}
			return
		case "merge":
			if err := runMerge(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "%serror: %v%s\n", cRed, err, cReset)
				os.Exit(1)
			}
			return
		case "spec":
			if err := runSpec(os.Args[2:]); err != nil {
				fmt.Fprintf(os.Stderr, "%serror: %v%s\n", cRed, err, cReset)
				os.Exit(1)
			}
			return
		}
	}

	cfg := &Config{}

	flag.StringVar(&cfg.EpicFilter, "epic", "", "Only work on tasks under this epic")
	flag.StringVar(&cfg.TagFilter, "tag", "", "Only work on tasks with this tag")
	flag.IntVar(&cfg.MaxTasks, "max-tasks", 0, "Stop after N tasks (0 = unlimited)")
	flag.IntVar(&cfg.BatchSize, "batch-size", 1, "Tasks per Claude session before fresh context")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "Show what would happen without running")
	flag.BoolVar(&cfg.Supervised, "supervised", false, "Approve each task before running")
	flag.BoolVar(&cfg.Unclaim, "unclaim", false, "Reset all in-progress tasks to queue and exit")
	rev := flag.Bool("rev", false, "Run code review after each epic completes")
	noYolo := flag.Bool("no-yolo", false, "Require permission prompts (default: skip permissions)")

	flag.StringVar(&cfg.LogDir, "log-dir", "", "Log directory (default: ~/.ata/runner-logs)")
	flag.StringVar(&cfg.Workspace, "workspace", "", "Workspace path (default: auto-detect from git)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `aor — Agent ORchestration

Each task runs in a fresh Claude Code context window. Output streams in real time.
Tasks are managed by ata (SQLite-backed, workspace-scoped).

Usage:
  aor [flags] [EPIC_ID...]       Run task orchestration loop
  aor pull [flags] [TASK_ID]     Interactive task planning and execution
  aor merge [flags] [NAME|BRANCH] Merge worktrees back into main branch
  aor rev [flags] [<ref>]        Iterative code review (see: aor rev --help)
  aor spec [flags] <file.md>...  Spec-driven task planning and execution

Positional EPIC_IDs are processed serially. Use --rev to review after each epic.

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

	if cfg.Workspace == "" {
		cfg.Workspace = detectWorkspaceFromGit()
	}
	cfg.WorkDir = detectWorkDir()

	if cfg.LogDir == "" {
		cfg.LogDir = resolveLogDir()
	}

	if cfg.Unclaim {
		if err := runUnclaim(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "%serror: %v%s\n", cRed, err, cReset)
			os.Exit(1)
		}
		return
	}

	epics := collectEpics(cfg.EpicFilter, flag.Args())
	if len(epics) > 1 || *rev {
		if err := runMultiEpic(cfg, epics, *rev); err != nil {
			fmt.Fprintf(os.Stderr, "%serror: %v%s\n", cRed, err, cReset)
			os.Exit(1)
		}
	} else {
		if len(epics) == 1 && epics[0] != "" {
			cfg.EpicFilter = epics[0]
		}
		if err := run(cfg); err != nil {
			fmt.Fprintf(os.Stderr, "%serror: %v%s\n", cRed, err, cReset)
			os.Exit(1)
		}
	}
}

// collectEpics merges positional args and the -epic flag into a list of epic IDs.
// Positional args take priority. Returns [""] if no epics specified (no filter).
func collectEpics(epicFlag string, positionalArgs []string) []string {
	if len(positionalArgs) > 0 {
		return positionalArgs
	}
	if epicFlag != "" {
		parts := strings.Split(epicFlag, ",")
		var epics []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				epics = append(epics, p)
			}
		}
		if len(epics) > 0 {
			return epics
		}
	}
	return []string{""}
}

// runMultiEpic processes multiple epics serially, optionally running code review
// after each epic's work completes.
func runMultiEpic(cfg *Config, epics []string, rev bool) error {
	log, err := NewLogger(cfg.LogDir)
	if err != nil {
		return err
	}
	defer log.Close()

	stdinCh := startStdinReader()
	stats := &RunStats{StartedAt: time.Now()}

	cfg.Log = log
	cfg.StdinCh = stdinCh
	cfg.Stats = stats
	cfg.SuppressSummary = true

	epicLabels := make([]string, len(epics))
	for i, e := range epics {
		if e == "" {
			epicLabels[i] = "(all)"
		} else {
			epicLabels[i] = e
		}
	}
	log.Log("Multi-epic run: %s (rev=%v)", strings.Join(epicLabels, ", "), rev)
	fmt.Println()

	for i, epic := range epics {
		label := epicLabels[i]

		if len(epics) > 1 {
			fmt.Printf("\n%s═══ Epic %s (%d/%d) ═══════════════════════════════════════%s\n\n",
				cCyan, label, i+1, len(epics), cReset)
		}

		preSHA, _ := headSHA()

		// Shallow-copy cfg so the loop doesn't mutate the caller's original.
		iterCfg := *cfg
		iterCfg.EpicFilter = epic
		if i > 0 {
			iterCfg.SkipRecovery = true
		}

		if err := run(&iterCfg); err != nil {
			log.Log("%sEpic %s failed: %v%s", cRed, label, err, cReset)
		}

		if rev {
			postSHA, _ := headSHA()
			if postSHA != preSHA {
				log.Log("Running code review for epic %s (base: %s)", label, preSHA)
				revCfg := &ReviewConfig{
					Base:      preSHA,
					MaxRounds: 3,
					Yolo:      cfg.Yolo,
					LogDir:    cfg.LogDir,
					Workspace: cfg.Workspace,
				}
				if err := runRevDirect(revCfg, log, stdinCh); err != nil {
					log.Log("%sReview for epic %s failed: %v (continuing)%s", cYellow, label, err, cReset)
				}
			} else {
				log.Log("No new commits for epic %s — skipping review", label)
			}
		}
	}

	printSummary(log, stats)
	return nil
}

// detectWorkspaceFromGit auto-detects the workspace path from git toplevel,
// resolving linked worktrees to the main worktree so tasks are found under
// the correct registered workspace.
func detectWorkspaceFromGit() string {
	path := detectWorkDir()

	// If we're in a linked worktree, resolve to the main worktree.
	if mainWt := gitMainWorktree(); mainWt != "" {
		return mainWt
	}

	return path
}
