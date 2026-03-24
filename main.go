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

	MaxRounds int // max rounds for epic verification and review loops

	// Shared resources — set when run() is called as a sub-process (e.g. sweep mode).
	StdinCh         <-chan string // shared stdin reader (nil = create own)
	Log             *Logger      // shared logger (nil = create own)
	Stats           *RunStats    // shared stats (nil = create own)
	SuppressSummary bool         // skip printSummary
	SkipRecovery    bool         // skip recoverStuckTasks (caller already ran it)
	SkipEpicClose   bool         // skip tryCloseEpics (caller handles epic lifecycle)
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

	flag.StringVar(&cfg.EpicFilter, "epic", "", "Only work on tasks under this epic (comma-separated for multiple)")
	flag.StringVar(&cfg.TagFilter, "tag", "", "Only work on tasks with this tag")
	flag.IntVar(&cfg.MaxTasks, "max-tasks", 0, "Stop after N tasks (0 = unlimited)")
	flag.IntVar(&cfg.BatchSize, "batch-size", 1, "Tasks per Claude session before fresh context")
	flag.BoolVar(&cfg.DryRun, "dry-run", false, "Show what would happen without running")
	flag.BoolVar(&cfg.Supervised, "supervised", false, "Approve each task before running")
	flag.BoolVar(&cfg.Unclaim, "unclaim", false, "Reset all in-progress tasks to queue and exit")
	flag.IntVar(&cfg.MaxRounds, "max-rounds", 3, "Max rounds for epic verification / review loops")
	rev := flag.Bool("rev", false, "Run code review after each epic completes")
	worktree := flag.Bool("worktree", false, "Run each epic in an isolated git worktree")
	noYolo := flag.Bool("no-yolo", false, "Require permission prompts (default: skip permissions)")

	flag.StringVar(&cfg.LogDir, "log-dir", "", "Log directory (default: ~/.ata/runner-logs)")
	flag.StringVar(&cfg.Workspace, "workspace", "", "Workspace path (default: auto-detect from git)")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `aor — Agent ORchestration

Each task runs in a fresh Claude Code context window. Output streams in real time.
Tasks are managed by ata (SQLite-backed, workspace-scoped).

Usage:
  aor [flags]                    Run task orchestration loop
  aor pull [flags] [TASK_ID]     Interactive task planning and execution
  aor merge [flags] [NAME|BRANCH] Merge worktrees back into main branch
  aor rev [flags] [<ref>]        Iterative code review (see: aor rev --help)
  aor spec [flags] <file.md>...  Spec-driven task planning and execution

Use -epic ID1,ID2 to process multiple epics serially. Use --rev to review after each.
Use --worktree with --epic to run each epic in an isolated git worktree.

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

	if *worktree && cfg.EpicFilter == "" {
		fmt.Fprintf(os.Stderr, "%serror: --worktree requires --epic%s\n", cRed, cReset)
		os.Exit(1)
	}

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

	epics := collectEpics(cfg.EpicFilter)
	if len(epics) > 1 || *rev || *worktree {
		if err := runMultiEpic(cfg, epics, *rev, *worktree); err != nil {
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

// makeEpicLabels converts epic IDs to display labels, replacing "" with "(all)".
func makeEpicLabels(epics []string) []string {
	labels := make([]string, len(epics))
	for i, e := range epics {
		if e == "" {
			labels[i] = "(all)"
		} else {
			labels[i] = e
		}
	}
	return labels
}

// collectEpics parses the -epic flag value as comma-separated epic IDs.
// Returns [""] if no epics specified (no filter).
func collectEpics(epicFlag string) []string {
	if epicFlag == "" {
		return []string{""}
	}
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
	return []string{""}
}

// runMultiEpic processes multiple epics serially, optionally running code review
// after each epic's work completes. When useWorktree is true, each epic runs
// in an isolated git worktree that is merged back to main on completion.
func runMultiEpic(cfg *Config, epics []string, rev, useWorktree bool) error {
	log, err := NewLogger(cfg.LogDir)
	if err != nil {
		return err
	}
	defer log.Close()

	stdinCh := startStdinReader()
	stats := &RunStats{StartedAt: time.Now()}
	rc := &RunContext{Log: log, StdinCh: stdinCh, Stats: stats}

	cfg.Log = log
	cfg.StdinCh = stdinCh
	cfg.Stats = stats
	cfg.SuppressSummary = true

	epicLabels := makeEpicLabels(epics)
	log.Log("Multi-epic run: %s (rev=%v, worktree=%v)", strings.Join(epicLabels, ", "), rev, useWorktree)
	fmt.Println()

	// When review is enabled, expand each epic into its sub-epic tree
	// (depth-first: children before parent) so each sub-epic gets its own
	// run → review → close cycle before the parent is processed.
	if rev {
		var expanded []string
		for _, epic := range epics {
			if epic == "" {
				expanded = append(expanded, "")
			} else {
				subEpics, err := expandSubEpics(epic)
				if err != nil {
					log.Log("%sFailed to expand sub-epics for %s: %v — processing as single epic%s",
						cYellow, epic, err, cReset)
					expanded = append(expanded, epic)
				} else {
					expanded = append(expanded, subEpics...)
				}
			}
		}
		epics = expanded
		epicLabels = makeEpicLabels(epics)
		log.Log("Expanded epic list (with sub-epics): %s", strings.Join(epicLabels, ", "))
	}

	for i, epic := range epics {
		label := epicLabels[i]

		if len(epics) > 1 {
			fmt.Printf("\n%s═══ Epic %s (%d/%d) ═══════════════════════════════════════%s\n\n",
				cCyan, label, i+1, len(epics), cReset)
		}

		// Shallow-copy cfg so the loop doesn't mutate the caller's original.
		iterCfg := *cfg
		iterCfg.EpicFilter = epic
		if i > 0 {
			iterCfg.SkipRecovery = true
		}
		// When review is enabled, defer epic closure until after review
		// tasks have been filed and resolved — otherwise the epic gets
		// closed before runRevDirect has a chance to file tasks under it.
		if rev {
			iterCfg.SkipEpicClose = true
		}

		// Create worktree for this epic if requested.
		var wtPath string
		if useWorktree {
			wt, err := createEpicWorktree(epic)
			if err != nil {
				log.Log("%sFailed to create worktree for epic %s: %v%s", cRed, label, err, cReset)
				continue
			}
			wtPath = wt
			iterCfg.WorkDir = wtPath
			log.Log("Created worktree for epic %s at %s", label, wtPath)
		}

		var preSHA string
		if rev {
			preSHA, _ = headSHA()
		}

		runErr := run(&iterCfg)
		if runErr != nil {
			log.Log("%sEpic %s failed: %v%s", cRed, label, runErr, cReset)
		}

		// Merge worktree back to main and clean up. Skip merge if the run
		// failed — leave the worktree intact for inspection.
		if wtPath != "" {
			if runErr != nil {
				log.Log("%sSkipping merge for epic %s — worktree left at %s (use `aor merge` to resolve)%s",
					cYellow, label, wtPath, cReset)
			} else if strategy, err := mergeWorktreeBranch(wtPath); err != nil {
				log.Log("%sMerge for epic %s failed: %v — worktree left at %s (use `aor merge` to resolve)%s",
					cYellow, label, err, wtPath, cReset)
			} else {
				log.Log("Worktree for epic %s integrated to main (%s)", label, strategy)
			}
		}

		if rev {
			postSHA, _ := headSHA()
			if postSHA != preSHA {
				log.Log("Running code review for epic %s (base: %s)", label, preSHA)
				revCfg := &ReviewConfig{
					Base:      preSHA,
					MaxRounds: cfg.MaxRounds,
					Yolo:      cfg.Yolo,
					LogDir:    cfg.LogDir,
					Workspace: cfg.Workspace,
					EpicID:    epic,
				}
				if err := runRevDirect(revCfg, rc); err != nil {
					log.Log("%sReview for epic %s failed: %v (continuing)%s", cYellow, label, err, cReset)
				}
			} else {
				log.Log("No new commits for epic %s — skipping review", label)
			}

			// Now that review tasks have been filed and resolved,
			// close the epic if all children are complete.
			closeEpicsUnder(epic, cfg, rc)
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
