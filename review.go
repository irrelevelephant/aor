package main

import (
	"flag"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

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

	log, err := NewLogger(cfg.LogDir)
	if err != nil {
		return err
	}
	defer log.Close()

	stdinCh := startStdinReader()
	stats := &ReviewStats{StartedAt: time.Now()}

	log.Log("Code review starting (base: %s, max_rounds: %d, max_turns: %d, yolo: %v)",
		base, cfg.MaxRounds, cfg.MaxTurns, cfg.Yolo)
	fmt.Println()

	// Build a Config for runSession compatibility.
	sessionCfg := &Config{
		MaxTurns: cfg.MaxTurns,
		Yolo:     cfg.Yolo,
		LogDir:   cfg.LogDir,
	}

	var allBeads []ReviewBead
	var rounds []ReviewRound

	for round := 1; round <= cfg.MaxRounds; round++ {
		diff, err := diffRange(base)
		if err != nil {
			log.Log("%sError getting diff: %v%s", cRed, err, cReset)
			stats.StopReason = "diff error"
			break
		}

		if strings.TrimSpace(diff) == "" {
			log.Log("%sNo diff from %s — nothing to review.%s", cGreen, base, cReset)
			stats.StopReason = "no diff"
			break
		}

		prompt := buildReviewPrompt(diff, base, round, allBeads, cfg.Scope)

		fmt.Printf("\n%s─── Review round %d/%d ──────────────────────────────────%s\n\n",
			cBlue, round, cfg.MaxRounds, cReset)

		roundStart := time.Now()
		result := runSession(sessionCfg, log, prompt, stdinCh)
		stats.RoundsRun = round

		// Reconcile scope labels for beads created during this review round.
		stats.ScopeReconciled += reconcileScope(cfg.Scope, roundStart, log)

		if result.Error != nil {
			log.Log("%sSession error: %v%s", cRed, result.Error, cReset)
			stats.StopReason = "session error"
			break
		}

		if result.UserQuit {
			stats.StopReason = "user quit"
			break
		}
		if result.UserSkipped {
			stats.StopReason = "user skipped"
			break
		}

		status := parseSentinelJSON[ReviewStatus](result.RawOutput, "REVIEW_STATUS:")
		sha, _ := headSHA()

		rr := ReviewRound{
			Number:  round,
			Status:  status,
			HeadSHA: sha,
		}

		if status != nil {
			rr.BeadsFiled = status.BeadsFiled
			allBeads = append(allBeads, status.BeadsFiled...)
			stats.TotalBeads += len(status.BeadsFiled)
			stats.TotalFixes += len(status.FixesApplied)

			if status.Error != nil {
				log.Log("%sAgent reported error: %s%s", cRed, *status.Error, cReset)
			}

			log.Log("Round %d: %d beads filed, %d fixes applied, severity=%s",
				round, len(status.BeadsFiled), len(status.FixesApplied), status.Severity)
		} else {
			log.Log("%sWARNING: No structured status from review agent. Check session log.%s", cYellow, cReset)
		}

		rounds = append(rounds, rr)

		if reason := shouldStop(rounds, allBeads); reason != "" {
			stats.StopReason = reason
			log.Log("Converged: %s", reason)
			break
		}

		if round == cfg.MaxRounds {
			stats.StopReason = "max rounds reached"
		}
	}

	// Post-review commit sweep: catch any uncommitted review fixes.
	if hasUncommittedChanges() {
		log.Log("Uncommitted review fixes detected — running commit sweep")
		commitPrompt := "There are uncommitted changes from a code review. " +
			"Run `git diff` to see what changed, then stage and commit the changes " +
			"with a message summarizing the review fixes. Do NOT stage or commit " +
			".beads/ files (stealth mode). Do not push."
		sweepCfg := &Config{
			MaxTurns: 5,
			Yolo:     sessionCfg.Yolo,
			LogDir:   sessionCfg.LogDir,
		}
		sweepStart := time.Now()
		sweepResult := runSession(sweepCfg, log, commitPrompt, stdinCh)
		stats.ScopeReconciled += reconcileScope(cfg.Scope, sweepStart, log)
		if sweepResult.Error != nil {
			log.Log("%sCommit sweep failed: %v%s", cRed, sweepResult.Error, cReset)
		} else {
			stats.CommitSweep = true
			log.Log("Commit sweep completed")
		}
	} else {
		log.Log("No uncommitted changes — commit sweep skipped")
	}

	printReviewSummary(log, stats)
	return nil
}

// parseRevFlags parses flags for the rev subcommand.
func parseRevFlags(args []string) (*ReviewConfig, error) {
	fs := flag.NewFlagSet("rev", flag.ContinueOnError)
	cfg := &ReviewConfig{}

	fs.IntVar(&cfg.MaxRounds, "max-rounds", 3, "Maximum review rounds")
	fs.IntVar(&cfg.MaxTurns, "max-turns", 50, "Max agent turns per session")
	noYolo := fs.Bool("no-yolo", false, "Require permission prompts")
	noScope := fs.Bool("no-scope", false, "Disable worktree scope auto-detection")
	fs.StringVar(&cfg.LogDir, "log-dir", "", "Log directory")
	fs.StringVar(&cfg.Scope, "scope", "", "Scope label for worktree isolation (default: auto-detect)")

	fs.Usage = func() {
		fmt.Fprintf(fs.Output(), `aor rev — Iterative code review

Reviews the diff from a base ref to HEAD + working tree. Files beads for issues,
fixes small/medium problems inline, and iterates until convergence.

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

	if !*noScope && cfg.Scope == "" {
		cfg.Scope = detectWorktreeScope()
	}

	// Remaining positional arg is the base ref.
	if fs.NArg() > 0 {
		cfg.Base = fs.Arg(0)
	}

	return cfg, nil
}

// shouldStop checks convergence conditions and returns a stop reason,
// or empty string to continue.
func shouldStop(rounds []ReviewRound, allBeads []ReviewBead) string {
	if len(rounds) == 0 {
		return ""
	}

	current := rounds[len(rounds)-1]

	// No issues found.
	if current.Status != nil && len(current.Status.BeadsFiled) == 0 && len(current.Status.FixesApplied) == 0 {
		return "no issues found"
	}

	// All issues trivial/minor.
	if current.Status != nil && len(current.Status.BeadsFiled) > 0 {
		allMinor := true
		for _, b := range current.Status.BeadsFiled {
			if b.Priority < 4 { // P1-P3 are non-trivial
				allMinor = false
				break
			}
		}
		if allMinor {
			sev := strings.ToLower(current.Status.Severity)
			if sev == "trivial" || sev == "minor" {
				return "issues too minor"
			}
		}
	}

	// Repeating issues: >50% of new bead titles match prior rounds.
	if len(rounds) > 1 && current.Status != nil && len(current.Status.BeadsFiled) > 0 {
		priorTitles := make(map[string]bool)
		for i := 0; i < len(rounds)-1; i++ {
			if rounds[i].Status != nil {
				for _, b := range rounds[i].Status.BeadsFiled {
					priorTitles[normTitle(b.Title)] = true
				}
			}
		}

		matches := 0
		for _, b := range current.Status.BeadsFiled {
			if priorTitles[normTitle(b.Title)] || fuzzyMatchAny(b.Title, priorTitles) {
				matches++
			}
		}
		if matches > len(current.Status.BeadsFiled)/2 {
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

// normTitle normalizes a bead title for comparison.
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
	log.Log("  Rounds run:        %d", stats.RoundsRun)
	log.Log("  Beads filed:       %s%d%s", colorForBeadCount(stats.TotalBeads), stats.TotalBeads, cReset)
	log.Log("  Fixes applied:     %s%d%s", cGreen, stats.TotalFixes, cReset)
	log.Log("  Stop reason:       %s", stats.StopReason)
	if stats.ScopeReconciled > 0 {
		log.Log("  %sScope reconciled: %d%s", cYellow, stats.ScopeReconciled, cReset)
	}
	if stats.CommitSweep {
		log.Log("  Commit sweep:      %syes%s", cGreen, cReset)
	}
	log.Log("  Elapsed:           %s", elapsed)
	log.Log("  Run log:           %s", log.RunLogPath())
	log.Log("%s════════════════════════════════════════%s", cCyan, cReset)
}

// colorForBeadCount returns a color code based on bead count.
func colorForBeadCount(n int) string {
	if n == 0 {
		return cGreen
	}
	if n <= 3 {
		return cYellow
	}
	return cRed
}
