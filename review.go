package main

import (
	"flag"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// revContext holds the stable context shared across sweep cycles.
type revContext struct {
	cfg        *ReviewConfig
	sessionCfg *Config
	base       string
	revTag     string
	workDir    string
	log        *Logger
	stdinCh    <-chan string
}

// reviewCycleResult holds the outcome of one review cycle (inner loop).
type reviewCycleResult struct {
	tasksFiled []ReviewTask
	stopReason string
	userQuit   bool
	rounds     []ReviewRound
}

// runReviewCycle runs the inner review round loop and returns the result.
func (rc *revContext) runReviewCycle(stats *ReviewStats, priorTasks []ReviewTask) *reviewCycleResult {
	result := &reviewCycleResult{}
	allTasks := append([]ReviewTask{}, priorTasks...)

	for round := 1; round <= rc.cfg.MaxRounds; round++ {
		diff, err := diffRange(rc.base)
		if err != nil {
			rc.log.Log("%sError getting diff: %v%s", cRed, err, cReset)
			result.stopReason = "diff error"
			break
		}

		if strings.TrimSpace(diff) == "" {
			rc.log.Log("%sNo diff from %s — nothing to review.%s", cGreen, rc.base, cReset)
			result.stopReason = "no diff"
			break
		}

		prompt := buildReviewPrompt(diff, rc.base, round, allTasks, rc.revTag)

		fmt.Printf("\n%s─── Review round %d/%d ──────────────────────────────────%s\n\n",
			cBlue, round, rc.cfg.MaxRounds, cReset)

		sr := runSession(rc.sessionCfg, rc.log, prompt, rc.stdinCh)
		stats.RoundsRun++

		if sr.Error != nil {
			rc.log.Log("%sSession error: %v%s", cRed, sr.Error, cReset)
			result.stopReason = "session error"
			break
		}

		if sr.UserQuit {
			result.stopReason = "user quit"
			result.userQuit = true
			break
		}
		if sr.UserSkipped {
			result.stopReason = "user skipped"
			break
		}

		status := parseSentinelJSON[ReviewStatus](sr.RawOutput, "REVIEW_STATUS:")
		sha, _ := headSHA()

		rr := ReviewRound{
			Number:  round,
			Status:  status,
			HeadSHA: sha,
		}

		if status != nil {
			allTasks = append(allTasks, status.TasksFiled...)
			result.tasksFiled = append(result.tasksFiled, status.TasksFiled...)
			stats.TotalTasks += len(status.TasksFiled)
			stats.TotalFixes += len(status.FixesApplied)

			// Safety net: ensure all filed tasks have the rev tag.
			for _, t := range status.TasksFiled {
				if rc.revTag != "" && t.ID != "" {
					_ = addTagToTask(t.ID, rc.revTag)
				}
			}

			if status.Error != nil {
				rc.log.Log("%sAgent reported error: %s%s", cRed, *status.Error, cReset)
			}

			rc.log.Log("Round %d: %d tasks filed, %d fixes applied, severity=%s",
				round, len(status.TasksFiled), len(status.FixesApplied), status.Severity)
		} else {
			rc.log.Log("%sWARNING: No structured status from review agent. Check session log.%s", cYellow, cReset)
		}

		result.rounds = append(result.rounds, rr)

		if reason := shouldStop(result.rounds); reason != "" {
			result.stopReason = reason
			rc.log.Log("Converged: %s", reason)
			break
		}

		if round == rc.cfg.MaxRounds {
			result.stopReason = "max rounds reached"
		}
	}

	return result
}

// runRev is the entry point for the "rev" / "review" subcommand.
func runRev(args []string) error {
	cfg, err := parseRevFlags(args)
	if err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}

	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("claude not found in PATH")
	}
	if _, err := exec.LookPath("git"); err != nil {
		return fmt.Errorf("git not found in PATH")
	}

	base, err := resolveBase(cfg.Base)
	if err != nil {
		return fmt.Errorf("resolve base ref: %w", err)
	}

	if cfg.LogDir == "" {
		cfg.LogDir = resolveLogDir()
	}

	workDir := detectWorkDir()
	if cfg.Workspace == "" {
		cfg.Workspace = detectWorkspaceFromGit()
	}

	log, err := NewLogger(cfg.LogDir)
	if err != nil {
		return err
	}
	defer log.Close()

	stdinCh := startStdinReader()
	stats := &ReviewStats{StartedAt: time.Now()}

	// Generate rev tag from worktree/directory basename.
	revTag := "rev-" + filepath.Base(workDir)

	log.Log("Code review starting (base: %s, max_rounds: %d, yolo: %v, tag: %s)",
		base, cfg.MaxRounds, cfg.Yolo, revTag)
	fmt.Println()

	rc := &revContext{
		cfg:     cfg,
		base:    base,
		revTag:  revTag,
		workDir: workDir,
		log:     log,
		stdinCh: stdinCh,
		sessionCfg: &Config{
			Yolo:    cfg.Yolo,
			LogDir:  cfg.LogDir,
			WorkDir: workDir,
		},
	}

	var allTasksFiled []ReviewTask

	// Outer sweep loop: review → fix tasks → review again.
	// Convergence checks (no issues, minor severity, repeating issues, HEAD
	// cycling) provide the safety net — no hard cycle cap.
	for cycle := 1; ; cycle++ {
		stats.SweepCycles = cycle

		if cycle > 1 {
			fmt.Printf("\n%s═══ Sweep cycle %d ═══════════════════════════════════════%s\n",
				cCyan, cycle, cReset)
		}

		// 1. Run review cycle (inner loop).
		cr := rc.runReviewCycle(stats, allTasksFiled)
		allTasksFiled = append(allTasksFiled, cr.tasksFiled...)

		if cr.userQuit {
			stats.StopReason = "user quit"
			break
		}

		// 2. Commit sweep.
		if hasUncommittedChanges() {
			rc.log.Log("Uncommitted review fixes detected — running commit sweep")
			commitPrompt := "There are uncommitted changes from a code review. " +
				"Run `git diff` to see what changed, then stage and commit the changes " +
				"with a message summarizing the review fixes. Do not push."
			sweepResult := runSession(rc.sessionCfg, rc.log, commitPrompt, rc.stdinCh)
			if sweepResult.Error != nil {
				rc.log.Log("%sCommit sweep failed: %v%s", cRed, sweepResult.Error, cReset)
			} else {
				stats.CommitSweep = true
				rc.log.Log("Commit sweep completed")
			}
		} else {
			rc.log.Log("No uncommitted changes — commit sweep skipped")
		}

		// 3. Check for open tagged tasks.
		openTasks, err := getReadyTasks("", rc.revTag, rc.cfg.Workspace)
		if err != nil {
			rc.log.Log("%sError checking tagged tasks: %v%s", cRed, err, cReset)
			stats.StopReason = "task check error"
			break
		}

		if len(openTasks) == 0 {
			rc.log.Log("%sNo open tagged tasks — review clean.%s", cGreen, cReset)
			stats.StopReason = "clean pass"
			break
		}

		rc.log.Log("%d open tagged task(s) — running orchestration to fix them", len(openTasks))

		// 4. Run orchestration loop filtered to the rev tag.
		runCfg := &Config{
			TagFilter:       rc.revTag,
			Workspace:       rc.cfg.Workspace,
			WorkDir:         rc.workDir,
			Yolo:            rc.cfg.Yolo,
			LogDir:          rc.cfg.LogDir,
			BatchSize:       1,
			StdinCh:         rc.stdinCh,
			Log:             rc.log,
			SuppressSummary: true,
			SkipRecovery:    true,
		}
		if err := run(runCfg); err != nil {
			rc.log.Log("%sOrchestration error: %v%s", cRed, err, cReset)
			stats.StopReason = "orchestration error"
			break
		}

		// Loop back — next review cycle will check if issues remain.
	}

	printReviewSummary(rc.log, stats)
	return nil
}

// parseRevFlags parses flags for the rev subcommand.
func parseRevFlags(args []string) (*ReviewConfig, error) {
	fs := flag.NewFlagSet("rev", flag.ContinueOnError)
	cfg := &ReviewConfig{}

	fs.IntVar(&cfg.MaxRounds, "max-rounds", 3, "Maximum review rounds per cycle")
	noYolo := fs.Bool("no-yolo", false, "Require permission prompts")
	fs.StringVar(&cfg.LogDir, "log-dir", "", "Log directory")
	fs.StringVar(&cfg.Workspace, "workspace", "", "Workspace path (default: auto-detect from git)")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `aor rev — Iterative code review with sweep mode

Reviews the diff from a base ref to HEAD + working tree. Files tasks for issues,
fixes small/medium problems inline, and iterates until convergence. When tasks
remain after review, runs the orchestration loop to fix them, then reviews again.

Usage:
  aor rev [flags] [<commit|branch>]

If no base ref is given, auto-detects the main branch.

Flags:
`)
		fs.PrintDefaults()
	}

	if err := fs.Parse(args); err != nil {
		return nil, err
	}

	cfg.Yolo = !*noYolo

	// Remaining positional arg is the base ref.
	if fs.NArg() > 0 {
		cfg.Base = fs.Arg(0)
	}

	return cfg, nil
}

// shouldStop checks convergence conditions and returns a stop reason,
// or empty string to continue.
func shouldStop(rounds []ReviewRound) string {
	if len(rounds) == 0 {
		return ""
	}

	current := rounds[len(rounds)-1]

	// No issues found.
	if current.Status != nil && len(current.Status.TasksFiled) == 0 && len(current.Status.FixesApplied) == 0 {
		return "no issues found"
	}

	// All issues trivial/minor.
	if current.Status != nil && len(current.Status.TasksFiled) > 0 {
		sev := strings.ToLower(current.Status.Severity)
		if sev == "trivial" || sev == "minor" {
			return "issues too minor"
		}
	}

	// Repeating issues: >50% of new task titles match prior rounds.
	if len(rounds) > 1 && current.Status != nil && len(current.Status.TasksFiled) > 0 {
		priorTitles := make(map[string]bool)
		for i := 0; i < len(rounds)-1; i++ {
			if rounds[i].Status != nil {
				for _, t := range rounds[i].Status.TasksFiled {
					priorTitles[normTitle(t.Title)] = true
				}
			}
		}

		matches := 0
		for _, t := range current.Status.TasksFiled {
			if priorTitles[normTitle(t.Title)] || fuzzyMatchAny(t.Title, priorTitles) {
				matches++
			}
		}
		if matches > len(current.Status.TasksFiled)/2 {
			return "repeating issues"
		}
	}

	// Cycling: current HEAD SHA matches a round from 2+ rounds ago.
	if current.HeadSHA != "" && len(rounds) > 2 {
		for i := 0; i < len(rounds)-2; i++ {
			if rounds[i].HeadSHA == current.HeadSHA {
				return "HEAD cycling detected"
			}
		}
	}

	return ""
}

// normTitle normalizes a task title for comparison.
func normTitle(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}

// fuzzyMatchAny checks if title has >50% word overlap with any prior title.
func fuzzyMatchAny(title string, priorTitles map[string]bool) bool {
	words := strings.Fields(strings.ToLower(title))
	if len(words) == 0 {
		return false
	}

	for prior := range priorTitles {
		priorWords := make(map[string]bool)
		for _, w := range strings.Fields(prior) {
			priorWords[w] = true
		}
		overlap := 0
		for _, w := range words {
			if priorWords[w] {
				overlap++
			}
		}
		if overlap > len(words)/2 {
			return true
		}
	}
	return false
}

// printReviewSummary prints the review run summary.
func printReviewSummary(log *Logger, stats *ReviewStats) {
	elapsed := time.Since(stats.StartedAt).Round(time.Second)

	fmt.Println()
	log.Log("%s════════════════════════════════════════%s", cCyan, cReset)
	log.Log("%s  Code Review Summary%s", cBold, cReset)
	log.Log("%s════════════════════════════════════════%s", cCyan, cReset)
	if stats.SweepCycles > 1 {
		log.Log("  Sweep cycles:      %d", stats.SweepCycles)
	}
	log.Log("  Rounds run:        %d", stats.RoundsRun)
	log.Log("  Tasks filed:       %s%d%s", colorForTaskCount(stats.TotalTasks), stats.TotalTasks, cReset)
	log.Log("  Fixes applied:     %s%d%s", cGreen, stats.TotalFixes, cReset)
	log.Log("  Stop reason:       %s", stats.StopReason)
	if stats.CommitSweep {
		log.Log("  Commit sweep:      %syes%s", cGreen, cReset)
	}
	log.Log("  Elapsed:           %s", elapsed)
	log.Log("  Run log:           %s", log.RunLogPath())
	log.Log("%s════════════════════════════════════════%s", cCyan, cReset)
}

// colorForTaskCount returns a color code based on task count.
func colorForTaskCount(n int) string {
	if n == 0 {
		return cGreen
	}
	if n <= 3 {
		return cYellow
	}
	return cRed
}
