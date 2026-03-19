package main

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

// buildPrompt constructs the system prompt for a Claude Code session.
// batchSize may differ from cfg.BatchSize due to adaptive sizing.
// claimedTask is the task already claimed by the runner before launching the session.
func buildPrompt(cfg *Config, batchSize int, claimedTask *AtaTask) string {
	filterInstruction := ""
	if cfg.EpicFilter != "" {
		filterInstruction += fmt.Sprintf("Only work on tasks under epic %s. Ignore unrelated ready items.\n\n", cfg.EpicFilter)
	}
	if cfg.TagFilter != "" {
		filterInstruction += fmt.Sprintf("Only work on tasks tagged \"%s\". Ignore unrelated ready items.\n\n", cfg.TagFilter)
	}

	// Inject epic spec(s) if the task belongs to an epic — walk the full ancestor chain.
	specInstruction := ""
	if claimedTask.EpicID != "" {
		ancestors := getEpicAncestorSpecs(claimedTask.EpicID)
		if len(ancestors) > 0 {
			fullSpec := formatAncestorSpecs(ancestors)
			specInstruction = fmt.Sprintf("## Epic Spec\n\n%s\n---\n\n", fullSpec)
			// Check for locked decisions in all ancestor specs.
			for _, a := range ancestors {
				specInstruction += lockedDecisionsWarning(a.Spec, fmt.Sprintf("epic %s spec", a.ID))
			}
		}
	}

	// Inject task's own spec if it has one.
	if claimedTask.Spec != "" {
		specInstruction += fmt.Sprintf("## Task Spec (%s)\n\n%s\n\n---\n\n", claimedTask.ID, claimedTask.Spec)
		specInstruction += lockedDecisionsWarning(claimedTask.Spec, "task spec")
	}

	// Inject attachments section if the task has any.
	attachmentsSection := formatAttachments(claimedTask.Attachments, claimedTask.ID)

	workspaceInstruction := ""
	if cfg.Workspace != "" {
		workspaceInstruction = fmt.Sprintf("Workspace: %s\n- When creating tasks, use: %s\n- When creating tasks under an epic, add: --epic EPIC_ID\n\n",
			cfg.Workspace, buildAtaCreateCmd("title", ataCmdOpts{Workspace: cfg.Workspace, JSON: true}))
	}

	worktreeInstruction := ""
	if cfg.WorkDir != "" && cfg.WorkDir != cfg.Workspace {
		worktreeInstruction = fmt.Sprintf("IMPORTANT — You are working in a git worktree at: %s\n"+
			"All file edits, git commits, and git operations MUST happen in this worktree.\n"+
			"Do NOT cd to or operate on the main repository at %s.\n"+
			"The --workspace flag in ata commands refers to the task database, not your working directory.\n\n",
			cfg.WorkDir, cfg.Workspace)
	}

	claimedInstruction := fmt.Sprintf(`Your first task is already claimed: %s — %s
Work on it immediately. Do not run ata ready or ata claim for this task.`, claimedTask.ID, claimedTask.Title)

	if claimedTask.Body != "" {
		claimedInstruction += fmt.Sprintf("\n\nTask description:\n%s", claimedTask.Body)
	}

	additionalTasks := ""
	if batchSize > 1 {
		readyCmd := buildAtaReadyCmd(ataCmdOpts{
			Workspace: cfg.Workspace,
			EpicID:    cfg.EpicFilter,
			Tag:       cfg.TagFilter,
			JSON:      true,
		})
		additionalTasks = fmt.Sprintf(`
After completing the claimed task, run %s for up to %d additional task(s).
For each additional task, claim it with ata claim <id> --json before working on it.

You have %d tasks to complete in this session.`, readyCmd, batchSize-1, batchSize)
	}

	// Build discovered task instruction.
	createCmd := buildAtaCreateCmd("<issue>", ataCmdOpts{
		Workspace: cfg.Workspace,
		EpicID:    claimedTask.EpicID,
		Tag:       cfg.TagFilter,
		JSON:      true,
	})
	discoveredInstruction := fmt.Sprintf(`5. File discovered issues for any new problems found outside current scope.
   Use: %s`, createCmd)

	decomposeCmd := buildAtaCreateCmd("Subtask: ...", ataCmdOpts{
		Workspace: cfg.Workspace,
		EpicID:    claimedTask.ID,
		Tag:       cfg.TagFilter,
		JSON:      true,
	})

	var b strings.Builder

	b.WriteString("You are working through tasks. Follow the @task-agent protocol in CLAUDE.md exactly.\n\n")
	b.WriteString(specInstruction)
	b.WriteString(filterInstruction)
	b.WriteString(workspaceInstruction)
	b.WriteString(worktreeInstruction)
	b.WriteString(claimedInstruction)
	b.WriteString(additionalTasks)
	b.WriteString(attachmentsSection)

	b.WriteString(`

For each task:
1. Implement the work.
2. Self-review: run git diff to inspect your changes. Look for correctness, bugs, security, error handling, performance, and code quality issues. Fix anything you find.
3. Run /simplify to check for reuse, quality, and efficiency issues. Fix in-scope issues. For any issues outside the current task's scope, file them as new tasks (step 5) instead of fixing them.
4. Make atomic commits with descriptive messages.
`)
	b.WriteString(discoveredInstruction)
	b.WriteString(`
6. Close the task: ata close <id> "reason" --json
7. Output ATA_RUNNER_STATUS (see below). You MUST complete this step — the orchestrator cannot parse your session without it.

Context management:
- Conserve context — delegate exploration to Task subagents, avoid verbose tool output.
- Prefer targeted file reads over reading entire large files.
- Do NOT run ata show or ata ready for the claimed task — all context is above.
- Make atomic commits as you go — do not accumulate a large uncommitted diff.
- Do NOT read files speculatively. Search first (grep/glob), then read only what you need.
- If context feels constrained, output ATA_RUNNER_STATUS with what you've completed so far and stop. The orchestrator will continue with a fresh session.

Task decomposition:
- If a task is too complex for this session, break it into subtasks:
`)
	fmt.Fprintf(&b, "  1. Create child tasks: %s --json\n", decomposeCmd)
	b.WriteString(`  2. Commit any progress you've made so far.
  3. Output ATA_RUNNER_STATUS with "decomposed_into": ["<child-ids>"] and "completed": [].
- The orchestrator will work the subtasks in subsequent sessions, then return to the parent.
- Only decompose when genuinely necessary — most tasks should complete in one session.

`)
	b.WriteString(sentinelBlock(
		"ATA_RUNNER_STATUS",
		`{"completed": ["<task-ids>"], "discovered": ["<task-ids>"], "decomposed_into": [], "remaining_ready": <number>, "error": null}`,
		`{"completed": [], "discovered": [], "decomposed_into": [], "remaining_ready": -1, "error": "<description>"}`,
		fmt.Sprintf("After completing %d task(s), if ata ready is empty, or if you are stopping for any reason:", batchSize),
	))
	b.WriteString(" Start now.")

	return b.String()
}

// claimTracker keeps track of the currently claimed task so we can
// unclaim it on unexpected process exit (SIGTERM, SIGHUP, etc.).
type claimTracker struct {
	mu sync.Mutex
	id string
}

func (ct *claimTracker) set(id string) {
	ct.mu.Lock()
	ct.id = id
	ct.mu.Unlock()
}

func (ct *claimTracker) clear() {
	ct.mu.Lock()
	ct.id = ""
	ct.mu.Unlock()
}

func (ct *claimTracker) get() string {
	ct.mu.Lock()
	defer ct.mu.Unlock()
	return ct.id
}

// run is the main orchestration loop. It fetches ready tasks from ata
// and launches Claude Code sessions to work through them.
func run(cfg *Config) error {
	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("claude not found in PATH")
	}
	if err := findAta(); err != nil {
		return err
	}

	// Use shared logger if provided (sweep mode), otherwise create our own.
	log := cfg.Log
	if log == nil {
		var err error
		log, err = NewLogger(cfg.LogDir)
		if err != nil {
			return err
		}
		defer log.Close()
	}

	// Track current claim for cleanup on unexpected exit.
	tracker := &claimTracker{}

	// Catch SIGTERM and SIGHUP (terminal close) at the outer level so we
	// can unclaim the in-flight task before exiting.
	exitSigCh := make(chan os.Signal, 1)
	signal.Notify(exitSigCh, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		sig, ok := <-exitSigCh
		if !ok {
			return // channel closed, clean shutdown
		}
		if id := tracker.get(); id != "" {
			fmt.Fprintf(os.Stderr, "\n%s[aor] Caught %s — unclaiming %s before exit%s\n", cYellow, sig, id, cReset)
			_ = unclaimTask(id)
		}
		os.Exit(1)
	}()
	defer func() {
		signal.Stop(exitSigCh)
		close(exitSigCh)
	}()

	// Use shared stdin reader if provided (sweep mode), otherwise create our own.
	stdinCh := cfg.StdinCh
	if stdinCh == nil {
		stdinCh = startStdinReader()
	}
	stats := cfg.Stats
	if stats == nil {
		stats = &RunStats{StartedAt: time.Now()}
	}
	type taskHistory struct {
		NoProgressCount int
	}
	failHistory := map[string]*taskHistory{}
	alreadySkipped := map[string]bool{}
	effectiveBatchSize := cfg.BatchSize

	log.Log("Agent orchestration runner started")
	log.Log("Config: batch_size=%d max_tasks=%d yolo=%v",
		cfg.BatchSize, cfg.MaxTasks, cfg.Yolo)
	if cfg.EpicFilter != "" {
		log.Log("Config: epic_filter=%s", cfg.EpicFilter)
	}
	if cfg.TagFilter != "" {
		log.Log("Config: tag_filter=%s", cfg.TagFilter)
	}
	if cfg.Workspace != "" {
		log.Log("Config: workspace=%s", cfg.Workspace)
	}
	if cfg.WorkDir != "" && cfg.WorkDir != cfg.Workspace {
		log.Log("Config: workdir=%s (worktree mode)", cfg.WorkDir)
	}
	log.Log("Controls: i=interject, s=skip, q=quit, Ctrl+C=stop & exit")
	fmt.Println()

	// Recover any tasks orphaned by a previous crashed runner (once at startup).
	if !cfg.SkipRecovery {
		if n := recoverStuckTasks(cfg.Workspace, log); n > 0 {
			stats.RecoveredTasks += n
		}
	}

	tryCloseEpics := func() {
		// Loop until no more epics become eligible. Closing a sub-epic may
		// make its parent eligible, so we must re-check after each pass.
		// Cap iterations to avoid infinite loops from unexpected edge cases.
		for range 20 {
			epics, err := getCloseEligibleEpics(cfg.Workspace)
			if err != nil {
				log.Log("%sEpic eligibility check failed: %v%s", cYellow, err, cReset)
				return
			}
			if len(epics) == 0 {
				return
			}
			closedAny := false
			for _, epic := range epics {
				if epic.Spec != "" {
					// Has spec — run verification.
					log.Log("Epic %s children all closed — verifying...", epic.ID)
					passed, err := verifyEpic(epic.ID, cfg, log, stdinCh, stats)
					if err != nil {
						log.Log("Epic %s verification error: %v", epic.ID, err)
					} else if passed {
						log.Log("Epic %s verified and closed", epic.ID)
						closedAny = true
					} else {
						log.Log("Epic %s did not pass verification", epic.ID)
					}
				} else {
					// No spec — auto-close like before.
					if err := closeTask(epic.ID, "all children closed"); err == nil {
						stats.EpicsClosed++
						log.Log("Auto-closed epic %s (no spec)", epic.ID)
						closedAny = true
					}
				}
			}
			// If nothing was closed this pass, no point re-checking —
			// no parent epic could have become newly eligible.
			if !closedAny {
				return
			}
		}
	}

	for {
		tasks, err := getReadyTasks(cfg.EpicFilter, cfg.TagFilter, cfg.Workspace)
		if err != nil {
			log.Log("%sError checking ready tasks: %v%s", cRed, err, cReset)
			stats.Errors++
			break
		}

		if len(tasks) == 0 {
			// Before declaring "all done", check if the filtered epic itself needs verification.
			if cfg.EpicFilter != "" {
				if tryVerifyFilteredEpic(cfg.EpicFilter, cfg, log, stdinCh, stats) {
					// Verification may have filed new tasks — re-check.
					continue
				}
			}
			log.Log("%sNo ready tasks. All done!%s", cGreen, cReset)
			break
		}

		log.Log("Ready queue: %d task(s) available", len(tasks))

		// Filter out tasks that have been triaged as stuck (repeated no-progress).
		var eligible []AtaTask
		for _, t := range tasks {
			if h := failHistory[t.ID]; h != nil && h.NoProgressCount >= 2 {
				if !alreadySkipped[t.ID] {
					log.Log("%sSkipping %s — %d zero-progress attempts, likely stuck%s",
						cYellow, t.ID, h.NoProgressCount, cReset)
					stats.TriageSkipped++
					alreadySkipped[t.ID] = true
				}
				continue
			}
			eligible = append(eligible, t)
		}
		if len(eligible) == 0 {
			log.Log("%sAll %d ready task(s) are stuck (repeated no-progress). Stopping.%s",
				cYellow, len(tasks), cReset)
			break
		}

		next := topTask(eligible)
		if next == nil {
			break
		}

		log.Log("Next: %s%s%s — %s",
			cBold, next.ID, cReset, next.Title)

		if cfg.MaxTasks > 0 && stats.SessionsRun >= cfg.MaxTasks {
			log.Log("Reached max tasks limit (%d). Stopping.", cfg.MaxTasks)
			break
		}

		if cfg.DryRun {
			log.Log("%s[dry run] Would invoke Claude Code for: %s — %s%s",
				cGray, next.ID, next.Title, cReset)
			stats.SessionsRun++
			time.Sleep(200 * time.Millisecond)
			continue
		}

		if cfg.Supervised {
			fmt.Printf("\n%sProceed with %s — %s? [Y/n/s(kip)/q(uit)] %s",
				cYellow, next.ID, next.Title, cReset)
			answer := <-stdinCh
			switch answer {
			case "n", "s", "skip":
				log.Log("Skipped by user.")
				continue
			case "q", "quit":
				log.Log("Quit by user.")
				printSummary(log, stats)
				return nil
			}
		}

		// Pre-claim the task before launching the session.
		log.Log("Claiming %s ...", next.ID)
		if err := claimTask(next.ID); err != nil {
			log.Log("%sFailed to claim %s: %v — skipping%s", cRed, next.ID, err, cReset)
			stats.Errors++
			continue
		}
		tracker.set(next.ID)

		stats.SessionsRun++

		// Inject comments so the agent has human notes and previous-attempt context.
		if human, system, err := getTaskComments(next.ID); err == nil {
			if human != "" {
				next.Body += "\n\n## Human Comments\n" + human
			}
			if system != "" {
				next.Body += "\n\n## Previous Attempt Notes\n" + system
			}
		}

		prompt := buildPrompt(cfg, effectiveBatchSize, next)

		// Capture pre-task HEAD for post-task review diffing.
		preSHA, _ := headSHA()

		fmt.Printf("\n%s─── Session %d ──────────────────────────────────────────%s\n\n",
			cBlue, stats.SessionsRun, cReset)

		sessionStart := time.Now()
		result := runSession(cfg, log, prompt, stdinCh)

		// Log session usage if available.
		if result.InputTokens > 0 || result.OutputTokens > 0 {
			log.Log("Session usage: %s input + %s output tokens, $%.4f",
				formatTokens(result.InputTokens), formatTokens(result.OutputTokens),
				result.TotalCostUSD)
			stats.TotalCostUSD += result.TotalCostUSD
			stats.TotalInput += result.InputTokens
			stats.TotalOutput += result.OutputTokens
		}

		// Determine whether the claimed task was completed by the agent.
		shouldUnclaim := false
		decomposed := false
		var lastTriageOutcome *TriageOutcome
		if result.Error != nil {
			log.Log("%sSession error: %v%s", cRed, result.Error, cReset)
			stats.Errors++
			shouldUnclaim = true
		} else if result.UserSkipped {
			shouldUnclaim = true
		} else if result.Status == nil {
			// Fallback: agent didn't output the sentinel, but may have
			// closed the task via ata close. Check directly.
			task, ferr := getTaskStatus(next.ID)
			if ferr == nil && task.Status == "closed" {
				log.Log("Task %s was closed by agent (no structured status, detected via ata)", next.ID)
				result.Status = &RunnerStatus{
					Completed: []string{next.ID},
				}
			} else {
				log.Log("No structured status — running post-session triage for %s", next.ID)
				ev := gatherTriageEvidence(next.ID, next.Title, preSHA, sessionStart, result, cfg)
				tr := runTriage(ev, cfg, log, stdinCh)
				if tr.AgentSpawned {
					stats.TriageSessions++
					stats.TotalCostUSD += tr.TotalCostUSD
					stats.TotalInput += tr.InputTokens
					stats.TotalOutput += tr.OutputTokens
				}
				lastTriageOutcome = &tr.Outcome
				if tr.Outcome == TriageComplete {
					log.Log("Triage: task %s confirmed complete", next.ID)
					result.Status = &RunnerStatus{Completed: []string{next.ID}}
				} else {
					if tr.Outcome == TriagePartial && tr.Comment != "" {
						if err := addComment(next.ID, tr.Comment, "system"); err != nil {
							log.Log("%sFailed to add triage comment to %s: %v%s", cYellow, next.ID, err, cReset)
						} else {
							log.Log("Added triage comment to %s", next.ID)
						}
					}
					shouldUnclaim = true
				}
			}
		} else {
			// Check for task decomposition first.
			if len(result.Status.DecomposedInto) > 0 {
				log.Log("Task %s decomposed into %d subtask(s): %s",
					next.ID, len(result.Status.DecomposedInto),
					strings.Join(result.Status.DecomposedInto, ", "))
				stats.Decomposed++
				shouldUnclaim = true
				decomposed = true
			} else {
				found := false
				for _, cid := range result.Status.Completed {
					if cid == next.ID {
						found = true
						break
					}
				}
				if !found {
					shouldUnclaim = true
				}
			}
		}

		if shouldUnclaim {
			task, ferr := getTaskStatus(next.ID)
			if ferr == nil && task.Status == "closed" {
				log.Log("Task %s is closed (detected on re-check), skipping unclaim", next.ID)
				shouldUnclaim = false
				if result.Status == nil {
					result.Status = &RunnerStatus{Completed: []string{next.ID}}
				}
				stats.TasksCompleted++
			} else {
				if !decomposed {
					if lastTriageOutcome != nil && *lastTriageOutcome == TriageNoProgress {
						h := failHistory[next.ID]
						if h == nil {
							h = &taskHistory{}
							failHistory[next.ID] = h
						}
						h.NoProgressCount++
					} else if lastTriageOutcome != nil && *lastTriageOutcome == TriagePartial {
						delete(failHistory, next.ID)
					}
				}
				reason := "not completed by agent"
				if decomposed {
					reason = "decomposed into subtasks"
				}
				log.Log("Unclaiming %s (%s)", next.ID, reason)
				if err := unclaimTask(next.ID); err != nil {
					log.Log("%sFailed to unclaim %s: %v%s", cRed, next.ID, err, cReset)
				}
			}
		}
		tracker.clear()

		iterCompleted := false
		if result.Status != nil {
			s := result.Status
			completed := len(s.Completed)
			discovered := len(s.Discovered)
			iterCompleted = completed > 0

			stats.TasksCompleted += completed
			stats.Discovered += discovered

			completedStr := "none"
			if completed > 0 {
				completedStr = strings.Join(s.Completed, ", ")
			}

			log.Log("Session result: %d completed [%s], %d discovered",
				completed, completedStr, discovered)

			if s.Error != nil {
				log.Log("%sAgent reported error: %s%s", cRed, *s.Error, cReset)
				stats.Errors++
			}

			if s.RemainingReady == 0 {
				// When filtering by tag or epic, don't trust the agent's count —
				// it only sees tasks at session start, but new tasks may have
				// been created during the run (e.g. subtasks under an epic).
				if cfg.TagFilter != "" || cfg.EpicFilter != "" {
					log.Log("Agent reports queue empty — re-checking for more tasks...")
				} else {
					log.Log("%sAgent reports no remaining ready tasks.%s", cGreen, cReset)
					break
				}
			}

			// Adaptive batch sizing.
			if effectiveBatchSize > 1 {
				if completed < effectiveBatchSize && s.Error == nil {
					effectiveBatchSize = completed
					if effectiveBatchSize < 1 {
						effectiveBatchSize = 1
					}
					log.Log("Reducing effective batch size to %d (agent completed %d of %d)",
						effectiveBatchSize, completed, cfg.BatchSize)
				} else if completed >= effectiveBatchSize {
					effectiveBatchSize = cfg.BatchSize
				}
			}
		}

		// Auto-close any epics whose children are all complete.
		if iterCompleted && !cfg.SkipEpicClose {
			tryCloseEpics()
		}

		if result.UserQuit {
			log.Log("Quitting at user request.")
			break
		}
		if result.UserSkipped {
			log.Log("Task skipped, moving to next.")
		}

		fmt.Printf("\n%s─── Pausing 3s before next session ──────────────────────%s\n",
			cGray, cReset)
		time.Sleep(3 * time.Second)
	}

	// Final epic auto-close check — the loop may have exited (via break)
	// before the in-loop auto-close had a chance to run.
	if !cfg.SkipEpicClose {
		tryCloseEpics()
	}

	if !cfg.SuppressSummary {
		printSummary(log, stats)
	}
	return nil
}

// formatTokens formats a token count with thousands separators.
func formatTokens(n int) string {
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}
	var result []byte
	for i, c := range s {
		if i > 0 && (len(s)-i)%3 == 0 {
			result = append(result, ',')
		}
		result = append(result, byte(c))
	}
	return string(result)
}

func printSummary(log *Logger, stats *RunStats) {
	elapsed := time.Since(stats.StartedAt).Round(time.Second)

	fmt.Println()
	log.Log("%s════════════════════════════════════════%s", cCyan, cReset)
	log.Log("%s  Agent ORchestration Summary%s", cBold, cReset)
	log.Log("%s════════════════════════════════════════%s", cCyan, cReset)
	log.Log("  Tasks completed:   %s%d%s", cGreen, stats.TasksCompleted, cReset)
	if stats.Discovered > 0 {
		log.Log("  Issues discovered: %d", stats.Discovered)
	}
	if stats.Decomposed > 0 {
		log.Log("  Decomposed:        %d", stats.Decomposed)
	}
	if stats.EpicsClosed > 0 {
		log.Log("  Epics closed:      %s%d%s", cGreen, stats.EpicsClosed, cReset)
	}
	if stats.EpicsVerified > 0 {
		log.Log("  Epics verified:    %s%d%s", cGreen, stats.EpicsVerified, cReset)
	}
	if stats.VerifySessions > 0 {
		log.Log("  Verify sessions:   %d", stats.VerifySessions)
	}
	log.Log("  Sessions run:      %d", stats.SessionsRun)
	log.Log("  Errors:            %d", stats.Errors)
	if stats.TriageSessions > 0 {
		log.Log("  Triage sessions:   %d", stats.TriageSessions)
	}
	if stats.TriageSkipped > 0 {
		log.Log("  %sTriage skipped:   %d%s", cYellow, stats.TriageSkipped, cReset)
	}
	if stats.RecoveredTasks > 0 {
		log.Log("  %sRecovered tasks:  %d%s", cYellow, stats.RecoveredTasks, cReset)
	}
	if stats.TotalInput > 0 || stats.TotalOutput > 0 {
		log.Log("  Tokens (in/out):   %s / %s", formatTokens(stats.TotalInput), formatTokens(stats.TotalOutput))
		log.Log("  Total cost:        $%.4f", stats.TotalCostUSD)
	}
	log.Log("  Elapsed:           %s", elapsed)
	log.Log("  Run log:           %s", log.RunLogPath())
	log.Log("%s════════════════════════════════════════%s", cCyan, cReset)
}
