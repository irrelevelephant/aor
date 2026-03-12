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
// claimedTask is the task already claimed by the runner before launching the session.
// maxTurns must match the --max-turns value passed to the session so the agent's
// turn-budget guidance is accurate.
func buildPrompt(batchSize, maxTurns int, epicFilter, tagFilter, workspace string, claimedTask *AtaTask) string {
	filterInstruction := ""
	if epicFilter != "" {
		filterInstruction += fmt.Sprintf("Only work on tasks under epic %s. Ignore unrelated ready items.\n\n", epicFilter)
	}
	if tagFilter != "" {
		filterInstruction += fmt.Sprintf("Only work on tasks tagged \"%s\". Ignore unrelated ready items.\n\n", tagFilter)
	}

	// Inject epic spec if the task belongs to an epic.
	specInstruction := ""
	if claimedTask.EpicID != "" {
		spec := getEpicSpec(claimedTask.EpicID)
		if spec != "" {
			specInstruction = fmt.Sprintf("## Epic Spec (epic %s)\n\n%s\n\n---\n\n", claimedTask.EpicID, spec)
		}
	}

	workspaceInstruction := ""
	if workspace != "" {
		workspaceInstruction = fmt.Sprintf("Workspace: %s\n- When creating tasks, use: ata create \"title\" --workspace \"%s\" --json\n- When creating tasks under an epic, add: --epic EPIC_ID\n\n", workspace, workspace)
	}

	claimedInstruction := fmt.Sprintf(`Your first task is already claimed: %s — %s
Work on it immediately. Do not run ata ready or ata claim for this task.`, claimedTask.ID, claimedTask.Title)

	if claimedTask.Body != "" {
		claimedInstruction += fmt.Sprintf("\n\nTask description:\n%s", claimedTask.Body)
	}

	additionalTasks := ""
	if batchSize > 1 {
		readyCmd := "ata ready --json"
		if workspace != "" {
			readyCmd = fmt.Sprintf("ata ready --workspace \"%s\" --json", workspace)
		}
		if epicFilter != "" {
			readyCmd += fmt.Sprintf(" --epic \"%s\"", epicFilter)
		}
		if tagFilter != "" {
			readyCmd += fmt.Sprintf(" --tag \"%s\"", tagFilter)
		}
		additionalTasks = fmt.Sprintf(`
After completing the claimed task, run %s for up to %d additional task(s).
For each additional task, claim it with ata claim <id> --json before working on it.

You have %d tasks to complete in this session.`, readyCmd, batchSize-1, batchSize)
	}

	// Build discovered task instruction.
	discoveredInstruction := "5. File discovered issues for any new problems found outside current scope."
	createCmd := `ata create "<issue>" --status queue`
	if workspace != "" {
		createCmd += fmt.Sprintf(` --workspace "%s"`, workspace)
	}
	if claimedTask.EpicID != "" {
		createCmd += fmt.Sprintf(` --epic %s`, claimedTask.EpicID)
	}
	if tagFilter != "" {
		createCmd += fmt.Sprintf(` --tag "%s"`, tagFilter)
	}
	discoveredInstruction = fmt.Sprintf(`5. File discovered issues for any new problems found outside current scope.
   Use: %s --json`, createCmd)

	decomposeCmd := fmt.Sprintf(`ata create "Subtask: ..." --status queue --epic %s`, claimedTask.ID)
	if workspace != "" {
		decomposeCmd += fmt.Sprintf(` --workspace "%s"`, workspace)
	}
	if tagFilter != "" {
		decomposeCmd += fmt.Sprintf(` --tag "%s"`, tagFilter)
	}

	earlyExitTurn := maxTurns * 80 / 100
	additionalTasks += fmt.Sprintf(`

Turn budget: You have %d turns for this session.
- If you reach turn ~%d without finishing: commit your progress, output the ATA_RUNNER_STATUS sentinel with what you've completed, and stop.
- The orchestrator will continue with a fresh session — do not try to rush or skip steps.`, maxTurns, earlyExitTurn)

	return fmt.Sprintf(`You are working through tasks. Follow the @task-agent protocol in CLAUDE.md exactly.

%s%s%s%s%s

For each task:
1. Implement the work.
2. Self-review: run git diff to inspect your changes. Look for correctness, bugs, security, error handling, performance, and code quality issues. Fix anything you find.
3. Run /simplify to check for reuse, quality, and efficiency issues. Fix anything it finds.
4. Make atomic commits with descriptive messages.
%s
6. Close the task: ata close <id> "reason" --json

CRITICAL — After completing %d task(s) or if ata ready is empty, you MUST output the following status line EXACTLY on its own line (no markdown fences, no extra text on the same line):

ATA_RUNNER_STATUS:{"completed": ["<task-ids>"], "discovered": ["<task-ids>"], "review_tasks": ["<task-ids>"], "decomposed_into": [], "remaining_ready": <number>, "error": null}

If you encounter an unrecoverable error:
ATA_RUNNER_STATUS:{"completed": [], "discovered": [], "review_tasks": [], "decomposed_into": [], "remaining_ready": -1, "error": "<description>"}

The orchestrator CANNOT parse your session without this line. Always output it as your final action.

Context management:
- Conserve context — delegate exploration to Task subagents, avoid verbose tool output.
- Prefer targeted file reads over reading entire large files.
- Do NOT run ata show or ata ready for the claimed task — all context is above.
- Make atomic commits as you go — do not accumulate a large uncommitted diff.
- Do NOT read files speculatively. Search first (grep/glob), then read only what you need.
- If context feels constrained, output ATA_RUNNER_STATUS with what you've completed so far and stop. The orchestrator will continue with a fresh session.
- Always output the ATA_RUNNER_STATUS sentinel as your final action, even if you feel the conversation is getting long.

Task decomposition:
- If a task is too complex for this session, break it into subtasks:
  1. Create child tasks: %s --json
  2. Commit any progress you've made so far.
  3. Output ATA_RUNNER_STATUS with "decomposed_into": ["<child-ids>"] and "completed": [].
- The orchestrator will work the subtasks in subsequent sessions, then return to the parent.
- Only decompose when genuinely necessary — most tasks should complete in one session.

Start now.`, specInstruction, filterInstruction, workspaceInstruction, claimedInstruction, additionalTasks, discoveredInstruction, batchSize, decomposeCmd)
}

// buildWrapUpPrompt constructs a focused prompt for resuming a session that
// hit the max-turns limit.
func buildWrapUpPrompt(taskID, taskTitle, workspace string) string {
	createCmd := fmt.Sprintf(`ata create "Subtask: ..." --status queue --epic %s`, taskID)
	if workspace != "" {
		createCmd += fmt.Sprintf(` --workspace "%s"`, workspace)
	}

	return fmt.Sprintf(`Your previous session ran out of turns while working on %s — %s.

You MUST wrap up immediately. Do NOT continue working on the task. You have 5 turns.

1. If you have uncommitted changes, commit them now with a descriptive message.
2. Determine outcome:
   a. If the task is COMPLETE: close it with ata close %s "<reason>" --json.
   b. If more work remains and it's too complex: decompose it — create child tasks with %s --json.
   c. If you made partial progress but it's a single remaining step: just note what's left.
3. Output the ATA_RUNNER_STATUS sentinel as your final action:
   ATA_RUNNER_STATUS:{"completed": ["<ids>"], "discovered": [], "review_tasks": [], "decomposed_into": ["<child-ids-if-any>"], "remaining_ready": -1, "error": null}

This is mandatory. The orchestrator cannot continue without it.`, taskID, taskTitle, taskID, createCmd)
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

	log, err := NewLogger(cfg.LogDir)
	if err != nil {
		return err
	}
	defer log.Close()

	// Track current claim for cleanup on unexpected exit.
	tracker := &claimTracker{}

	// Catch SIGTERM and SIGHUP (terminal close) at the outer level so we
	// can unclaim the in-flight task before exiting.
	exitSigCh := make(chan os.Signal, 1)
	signal.Notify(exitSigCh, syscall.SIGTERM, syscall.SIGHUP)

	go func() {
		sig := <-exitSigCh
		if id := tracker.get(); id != "" {
			fmt.Fprintf(os.Stderr, "\n%s[aor] Caught %s — unclaiming %s before exit%s\n", cYellow, sig, id, cReset)
			_ = unclaimTask(id)
		}
		os.Exit(1)
	}()
	defer signal.Stop(exitSigCh)

	stdinCh := startStdinReader()
	stats := &RunStats{StartedAt: time.Now()}
	type taskHistory struct {
		NoProgressCount int
	}
	failHistory := map[string]*taskHistory{}
	alreadySkipped := map[string]bool{}
	effectiveBatchSize := cfg.BatchSize

	log.Log("Agent orchestration runner started")
	log.Log("Config: batch_size=%d max_tasks=%d max_turns=%d yolo=%v",
		cfg.BatchSize, cfg.MaxTasks, cfg.MaxTurns, cfg.Yolo)
	if cfg.EpicFilter != "" {
		log.Log("Config: epic_filter=%s", cfg.EpicFilter)
	}
	if cfg.TagFilter != "" {
		log.Log("Config: tag_filter=%s", cfg.TagFilter)
	}
	if cfg.Workspace != "" {
		log.Log("Config: workspace=%s", cfg.Workspace)
	}
	log.Log("Controls: i=interject, s=skip, q=quit, Ctrl+C=stop & exit")
	fmt.Println()

	for {
		// Recover any tasks orphaned by a previous crashed runner.
		if n := recoverStuckTasks(cfg.Workspace, log); n > 0 {
			stats.RecoveredTasks += n
		}

		tasks, err := getReadyTasks(cfg.EpicFilter, cfg.TagFilter, cfg.Workspace)
		if err != nil {
			log.Log("%sError checking ready tasks: %v%s", cRed, err, cReset)
			stats.Errors++
			break
		}

		if len(tasks) == 0 {
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

		// Inject previous-attempt context so the next agent knows what happened.
		if comments, err := getTaskComments(next.ID); err == nil && comments != "" {
			next.Body += "\n\n## Previous Attempt Notes\n" + comments
		}

		prompt := buildPrompt(effectiveBatchSize, cfg.MaxTurns, cfg.EpicFilter, cfg.TagFilter, cfg.Workspace, next)

		// Capture pre-task HEAD for post-task review diffing.
		preSHA, _ := headSHA()

		fmt.Printf("\n%s─── Session %d ──────────────────────────────────────────%s\n\n",
			cBlue, stats.SessionsRun, cReset)

		sessionStart := time.Now()
		result := runSession(cfg, log, prompt, stdinCh)

		// Log session usage if available.
		if result.InputTokens > 0 || result.OutputTokens > 0 {
			turnPct := 0
			if cfg.MaxTurns > 0 {
				turnPct = result.NumTurns * 100 / cfg.MaxTurns
			}
			log.Log("Session usage: %s input + %s output tokens, $%.4f, %d/%d turns (%d%%)",
				formatTokens(result.InputTokens), formatTokens(result.OutputTokens),
				result.TotalCostUSD, result.NumTurns, cfg.MaxTurns, turnPct)
			if turnPct >= 100 {
				stats.MaxTurnsHitCount++
			} else if turnPct >= 80 {
				log.Log("%sNOTE: Session used %d%% of turn budget (%d/%d turns)%s",
					cYellow, turnPct, result.NumTurns, cfg.MaxTurns, cReset)
			}
			stats.TotalCostUSD += result.TotalCostUSD
			stats.TotalInput += result.InputTokens
			stats.TotalOutput += result.OutputTokens
			stats.TotalTurns += result.NumTurns
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
			} else if result.NumTurns > 0 && result.NumTurns >= cfg.MaxTurns && result.SessionID != "" {
				log.Log("Session hit max turns (%d) — attempting wrap-up resumption...", cfg.MaxTurns)

				wrapUpPrompt := buildWrapUpPrompt(next.ID, next.Title, cfg.Workspace)
				wrapUpCfg := &Config{
					MaxTurns:        5,
					Yolo:            cfg.Yolo,
					LogDir:          cfg.LogDir,
					ResumeSessionID: result.SessionID,
				}

				fmt.Printf("\n%s─── Wrap-up: %s ──────────────────────────────────────%s\n\n",
					cYellow, next.ID, cReset)

				wrapResult := runSession(wrapUpCfg, log, wrapUpPrompt, stdinCh)
				stats.WrapUpSessions++

				// Accumulate wrap-up costs.
				if wrapResult.InputTokens > 0 || wrapResult.OutputTokens > 0 {
					log.Log("Wrap-up usage: %s input + %s output tokens, $%.4f, %d turns",
						formatTokens(wrapResult.InputTokens), formatTokens(wrapResult.OutputTokens),
						wrapResult.TotalCostUSD, wrapResult.NumTurns)
					stats.TotalCostUSD += wrapResult.TotalCostUSD
					stats.TotalInput += wrapResult.InputTokens
					stats.TotalOutput += wrapResult.OutputTokens
					stats.TotalTurns += wrapResult.NumTurns
				}

				// If wrap-up produced a sentinel, use it.
				if wrapResult.Status != nil {
					result.Status = wrapResult.Status
					if len(result.Status.DecomposedInto) > 0 {
						log.Log("Task %s decomposed during wrap-up into %d subtask(s): %s",
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
				} else {
					task, ferr := getTaskStatus(next.ID)
					if ferr == nil && task.Status == "closed" {
						log.Log("Task %s closed during wrap-up (no sentinel, detected via ata)", next.ID)
						result.Status = &RunnerStatus{
							Completed: []string{next.ID},
						}
					} else {
						log.Log("Wrap-up produced no status — running triage for %s", next.ID)
						ev := gatherTriageEvidence(next.ID, next.Title, preSHA, sessionStart, result, cfg)
						tr := runTriage(ev, cfg, log, stdinCh)
						if tr.AgentSpawned {
							stats.TriageSessions++
							stats.TotalCostUSD += tr.TotalCostUSD
							stats.TotalInput += tr.InputTokens
							stats.TotalOutput += tr.OutputTokens
							stats.TotalTurns += tr.NumTurns
						}
						lastTriageOutcome = &tr.Outcome
						if tr.Outcome == TriageComplete {
							log.Log("Triage: task %s confirmed complete after wrap-up", next.ID)
							result.Status = &RunnerStatus{Completed: []string{next.ID}}
						} else {
							if tr.Outcome == TriagePartial && tr.Comment != "" {
								if err := addComment(next.ID, tr.Comment, "system"); err != nil {
									log.Log("%sFailed to add triage comment to %s: %v%s", cYellow, next.ID, err, cReset)
								}
							}
							shouldUnclaim = true
						}
					}
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
					stats.TotalTurns += tr.NumTurns
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

		if result.Status != nil {
			s := result.Status
			completed := len(s.Completed)
			discovered := len(s.Discovered)
			review := len(s.ReviewTasks)

			stats.TasksCompleted += completed
			stats.Discovered += discovered
			stats.ReviewTasks += review

			completedStr := "none"
			if len(s.Completed) > 0 {
				completedStr = strings.Join(s.Completed, ", ")
			}

			log.Log("Session result: %d completed [%s], %d discovered, %d review tasks",
				completed, completedStr, discovered, review)

			if s.Error != nil {
				log.Log("%sAgent reported error: %s%s", cRed, *s.Error, cReset)
				stats.Errors++
			}

			if s.RemainingReady == 0 {
				// When filtering by tag, don't trust the agent's count —
				// it only sees tasks within the current scope (e.g. one epic),
				// but other tagged tasks/epics may still be waiting.
				if cfg.TagFilter != "" {
					log.Log("Agent reports queue empty — re-checking for more tagged tasks...")
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
		if stats.TasksCompleted > 0 {
			if closed, err := closeEligibleEpics(cfg.Workspace); err != nil {
				log.Log("%sEpic auto-close failed: %v%s", cYellow, err, cReset)
			} else if len(closed) > 0 {
				stats.EpicsClosed += len(closed)
				log.Log("Auto-closed %d epic(s): %s", len(closed), strings.Join(closed, ", "))
			}
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

	printSummary(log, stats)
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
	log.Log("  Issues discovered: %d", stats.Discovered)
	log.Log("  Review tasks:      %d", stats.ReviewTasks)
	if stats.Decomposed > 0 {
		log.Log("  Decomposed:        %d", stats.Decomposed)
	}
	if stats.EpicsClosed > 0 {
		log.Log("  Epics closed:      %s%d%s", cGreen, stats.EpicsClosed, cReset)
	}
	log.Log("  Sessions run:      %d", stats.SessionsRun)
	log.Log("  Errors:            %d", stats.Errors)
	if stats.WrapUpSessions > 0 {
		log.Log("  Wrap-up sessions:  %d", stats.WrapUpSessions)
	}
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
		log.Log("  Total turns:       %d", stats.TotalTurns)
		if stats.SessionsRun > 0 {
			log.Log("  Avg turns/task:    %.1f", float64(stats.TotalTurns)/float64(stats.SessionsRun))
		}
		if stats.MaxTurnsHitCount > 0 {
			log.Log("  %sTurn limit hits:  %d%s", cYellow, stats.MaxTurnsHitCount, cReset)
		}
	}
	log.Log("  Elapsed:           %s", elapsed)
	log.Log("  Run log:           %s", log.RunLogPath())
	log.Log("%s════════════════════════════════════════%s", cCyan, cReset)
}
