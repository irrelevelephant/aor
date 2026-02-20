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
func buildPrompt(batchSize, maxTurns int, epicFilter, scope string, claimedTask *BeadTask) string {
	scopeInstruction := ""
	if epicFilter != "" {
		scopeInstruction = fmt.Sprintf("Only work on tasks under epic %s. Ignore unrelated ready items.\n\n", epicFilter)
	}

	scopeLabelInstruction := ""
	if scope != "" {
		scopeLabelInstruction = fmt.Sprintf(`Worktree scope: %s
- Add --labels "%s" on ALL bd create / bd q calls so new beads stay in this scope.
- When fetching additional batch work, use bd ready --label "%s" --json.

`, scope, scope, scope)
	}

	claimedInstruction := fmt.Sprintf(`Your first task is already claimed: %s — %s (P%d, %s)
Work on it immediately. Do not run bd ready or bd update --claim for this task.`, claimedTask.ID, claimedTask.Title, claimedTask.Priority, claimedTask.Type)

	if claimedTask.Description != "" {
		claimedInstruction += fmt.Sprintf("\n\nTask description:\n%s", claimedTask.Description)
	}

	additionalTasks := ""
	if batchSize > 1 {
		readyCmd := "bd ready --json"
		if scope != "" {
			readyCmd = fmt.Sprintf("bd ready --label \"%s\" --json", scope)
		}
		additionalTasks = fmt.Sprintf(`
After completing the claimed task, run %s for up to %d additional task(s).
For each additional task, claim it with bd update <id> --claim --json before working on it.

You have %d tasks to complete in this session.`, readyCmd, batchSize-1, batchSize)
	}

	earlyExitTurn := maxTurns * 80 / 100
	additionalTasks += fmt.Sprintf(`

Turn budget: You have %d turns for this session.
- If you reach turn ~%d without finishing: commit your progress, output the BEADS_RUNNER_STATUS sentinel with what you've completed, and stop.
- The orchestrator will continue with a fresh session — do not try to rush or skip steps.`, maxTurns, earlyExitTurn)

	return fmt.Sprintf(`You are working through beads tasks. Follow the @task-agent protocol in CLAUDE.md exactly.

%s%s%s%s

For each task:
1. Implement the work. Make atomic commits with descriptive messages (NO bead IDs in commits — stealth mode).
2. File discovered-from beads for any new issues found outside current scope.
3. Close the task with a descriptive reason.

CRITICAL — After completing %d task(s) or if bd ready is empty, you MUST do this:
1. Run bd sync.
2. Output the following status line EXACTLY on its own line (no markdown fences, no extra text on the same line):

BEADS_RUNNER_STATUS:{"completed": ["<bead-ids>"], "discovered": ["<bead-ids>"], "review_beads": ["<bead-ids>"], "decomposed_into": [], "remaining_ready": <number>, "error": null}

If you encounter an unrecoverable error:
BEADS_RUNNER_STATUS:{"completed": [], "discovered": [], "review_beads": [], "decomposed_into": [], "remaining_ready": -1, "error": "<description>"}

The orchestrator CANNOT parse your session without this line. Always output it as your final action.

Important: Stealth mode — do NOT commit or push .beads/ files. Do NOT reference bead IDs in commits.

Context management:
- Conserve context — delegate exploration to Task subagents, avoid verbose tool output.
- Prefer targeted file reads over reading entire large files.
- Do NOT run bd show or bd ready for the claimed task — all context is above.
- Make atomic commits as you go — do not accumulate a large uncommitted diff.
- Do NOT read files speculatively. Search first (grep/glob), then read only what you need.
- If context feels constrained, output BEADS_RUNNER_STATUS with what you've completed so far and stop. The orchestrator will continue with a fresh session.
- Always output the BEADS_RUNNER_STATUS sentinel as your final action, even if you feel the conversation is getting long.

Task decomposition:
- If a task is too complex for this session, break it into subtasks:
  1. Create child beads: bd create --deps "blocks:<parent-id>" --title "Subtask: ..." --type task [--labels "<scope>"]
  2. Commit any progress you've made so far.
  3. Output BEADS_RUNNER_STATUS with "decomposed_into": ["<child-ids>"] and "completed": [].
- The orchestrator will work the subtasks in subsequent sessions, then return to the parent.
- Only decompose when genuinely necessary — most tasks should complete in one session.

Start now.`, scopeInstruction, scopeLabelInstruction, claimedInstruction, additionalTasks, batchSize)
}

// buildWrapUpPrompt constructs a focused prompt for resuming a session that
// hit the max-turns limit. The resumed session has the agent's full context,
// so it can commit progress, decompose if needed, and emit the sentinel.
func buildWrapUpPrompt(taskID, taskTitle, scope string) string {
	scopeLabel := ""
	if scope != "" {
		scopeLabel = fmt.Sprintf(` --labels "%s"`, scope)
	}
	return fmt.Sprintf(`Your previous session ran out of turns while working on %s — %s.

You MUST wrap up immediately. Do NOT continue working on the task. You have 5 turns.

1. If you have uncommitted changes, commit them now with a descriptive message.
2. Determine outcome:
   a. If the task is COMPLETE: close it with bd close %s "<reason>".
   b. If more work remains and it's too complex: decompose it — create child beads with bd create --deps "blocks:%s" --title "Subtask: ..."%s --type task, then run bd sync.
   c. If you made partial progress but it's a single remaining step: just note what's left.
3. Output the BEADS_RUNNER_STATUS sentinel as your final action:
   BEADS_RUNNER_STATUS:{"completed": ["<ids>"], "discovered": [], "review_beads": [], "decomposed_into": ["<child-ids-if-any>"], "remaining_ready": -1, "error": null}

This is mandatory. The orchestrator cannot continue without it.`, taskID, taskTitle, taskID, taskID, scopeLabel)
}

// reviewTurnsForDiff returns an adaptive MaxTurns for post-task review
// based on the number of lines in the diff.
func reviewTurnsForDiff(diff string) int {
	lines := strings.Count(diff, "\n")
	switch {
	case lines < 50:
		return 10
	case lines < 200:
		return 15
	case lines < 500:
		return 25
	default:
		return 30
	}
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

// run is the main orchestration loop. It fetches ready tasks from beads
// and launches Claude Code sessions to work through them.
func run(cfg *Config) error {
	for _, tool := range []string{"claude", "bd"} {
		if _, err := exec.LookPath(tool); err != nil {
			return fmt.Errorf("%s not found in PATH", tool)
		}
	}

	if err := findBeadsDB(); err != nil {
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
	failedIDs := map[string]int{} // track tasks that failed detection to prevent infinite retry
	effectiveBatchSize := cfg.BatchSize

	log.Log("Agent orchestration runner started (stealth mode)")
	log.Log("Config: batch_size=%d max_tasks=%d max_turns=%d yolo=%v skip_review=%v",
		cfg.BatchSize, cfg.MaxTasks, cfg.MaxTurns, cfg.Yolo, cfg.SkipReview)
	if cfg.EpicFilter != "" {
		log.Log("Config: epic_filter=%s", cfg.EpicFilter)
	}
	if cfg.Scope != "" {
		log.Log("Config: scope=%s", cfg.Scope)
	}
	log.Log("Controls: i=interject, s=skip, q=quit, Ctrl+C=stop & exit")
	fmt.Println()

	for {
		tasks, err := getReadyTasks(cfg.EpicFilter, cfg.Scope)
		if err != nil {
			log.Log("%sError checking ready tasks: %v%s", cRed, err, cReset)
			stats.Errors++
			break
		}

		if len(tasks) == 0 {
			if cfg.Scope != "" {
				if total := countUnscopedReadyTasks(); total > 0 {
					log.Log("%sNo tasks matching scope %q, but %d unscoped task(s) exist. Add label %q to include them.%s",
						cYellow, cfg.Scope, total, cfg.Scope, cReset)
				} else {
					log.Log("%sNo ready tasks. All done!%s", cGreen, cReset)
				}
			} else {
				log.Log("%sNo ready tasks. All done!%s", cGreen, cReset)
			}
			break
		}

		log.Log("Ready queue: %d task(s) available", len(tasks))
		next := topTask(tasks)
		if next == nil {
			break
		}

		log.Log("Next: %s%s%s — %s (P%d, %s)",
			cBold, next.ID, cReset, next.Title, next.Priority, next.Type)

		if failedIDs[next.ID] >= 2 {
			log.Log("%sSkipping %s — failed %d times, likely already resolved or stuck%s",
				cYellow, next.ID, failedIDs[next.ID], cReset)
			stats.Errors++
			break
		}

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
		prompt := buildPrompt(effectiveBatchSize, cfg.MaxTurns, cfg.EpicFilter, cfg.Scope, next)

		// Capture pre-task HEAD for post-task review diffing.
		preSHA, _ := headSHA()

		fmt.Printf("\n%s─── Session %d ──────────────────────────────────────────%s\n\n",
			cBlue, stats.SessionsRun, cReset)

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
		if result.Error != nil {
			log.Log("%sSession error: %v%s", cRed, result.Error, cReset)
			stats.Errors++
			shouldUnclaim = true
		} else if result.UserSkipped {
			shouldUnclaim = true
		} else if result.Status == nil {
			// Fallback: agent didn't output the sentinel, but may have
			// closed the task via bd close. Check beads directly.
			task, ferr := getTaskStatus(next.ID)
			if ferr == nil && task.Status == "closed" {
				log.Log("Task %s was closed by agent (no structured status, detected via beads)", next.ID)
				result.Status = &RunnerStatus{
					Completed: []string{next.ID},
				}
			} else if result.NumTurns > 0 && result.NumTurns >= cfg.MaxTurns && result.SessionID != "" {
				log.Log("Session hit max turns (%d) — attempting wrap-up resumption...", cfg.MaxTurns)

				wrapUpPrompt := buildWrapUpPrompt(next.ID, next.Title, cfg.Scope)
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
					// Re-evaluate: check if the task is now completed or decomposed.
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
					// No sentinel from wrap-up either — check if the agent
					// managed to close the task via bd close during wrap-up.
					task, ferr := getTaskStatus(next.ID)
					if ferr == nil && task.Status == "closed" {
						log.Log("Task %s closed during wrap-up (no sentinel, detected via beads)", next.ID)
						result.Status = &RunnerStatus{
							Completed: []string{next.ID},
						}
					} else {
						log.Log("%sWrap-up session did not produce structured status either.%s", cYellow, cReset)
						shouldUnclaim = true
					}
				}
			} else if result.NumTurns > 0 && result.NumTurns >= cfg.MaxTurns {
				log.Log("%sWARNING: Session used all %d turns without completing (no session ID for wrap-up).%s",
					cYellow, cfg.MaxTurns, cReset)
				shouldUnclaim = true
			} else {
				log.Log("%sWARNING: No structured status from agent and task not closed. Check session log.%s",
					cYellow, cReset)
				shouldUnclaim = true
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
				// Check if the claimed task appears in the completed list.
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
			// Safety: re-check task status before unclaiming — the agent
			// may have closed it even though we failed to parse the sentinel.
			task, ferr := getTaskStatus(next.ID)
			if ferr == nil && task.Status == "closed" {
				log.Log("Task %s is closed (detected on re-check), skipping unclaim", next.ID)
				shouldUnclaim = false
				stats.TasksCompleted++
			} else {
				if !decomposed {
					failedIDs[next.ID]++
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
			review := len(s.ReviewBeads)

			stats.TasksCompleted += completed
			stats.Discovered += discovered
			stats.ReviewBeads += review

			completedStr := "none"
			if len(s.Completed) > 0 {
				completedStr = strings.Join(s.Completed, ", ")
			}

			log.Log("Session result: %d completed [%s], %d discovered, %d review beads",
				completed, completedStr, discovered, review)

			if s.Error != nil {
				log.Log("%sAgent reported error: %s%s", cRed, *s.Error, cReset)
				stats.Errors++
			}

			if s.RemainingReady == 0 {
				log.Log("%sAgent reports no remaining ready tasks.%s", cGreen, cReset)
				break
			}

			// Adaptive batch sizing: adjust based on how many tasks the agent completed.
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

		// Post-task review: launch independent review sub-agent.
		if !cfg.SkipReview && !result.UserQuit && !result.UserSkipped &&
			result.Status != nil && len(result.Status.Completed) > 0 {
			postSHA, _ := headSHA()
			if postSHA != preSHA {
				diff, diffErr := diffBetween(preSHA, postSHA)
				if diffErr != nil {
					log.Log("%sPost-task review: diff error: %v%s", cYellow, diffErr, cReset)
				} else if strings.TrimSpace(diff) != "" {
					reviewPrompt := buildPostTaskReviewPrompt(diff, next.ID, next.Title, cfg.Scope)
					reviewCfg := &Config{
						MaxTurns: reviewTurnsForDiff(diff),
						Yolo:     cfg.Yolo,
						LogDir:   cfg.LogDir,
					}

					fmt.Printf("\n%s─── Review: %s ──────────────────────────────────────%s\n\n",
						cMagenta, next.ID, cReset)
					log.Log("Launching post-task review for %s ...", next.ID)

					reviewResult := runSession(reviewCfg, log, reviewPrompt, stdinCh)
					stats.ReviewSessions++

					// Accumulate review session costs.
					if reviewResult.InputTokens > 0 || reviewResult.OutputTokens > 0 {
						log.Log("Review usage: %s input + %s output tokens, $%.4f, %d turns",
							formatTokens(reviewResult.InputTokens), formatTokens(reviewResult.OutputTokens),
							reviewResult.TotalCostUSD, reviewResult.NumTurns)
						stats.TotalCostUSD += reviewResult.TotalCostUSD
						stats.TotalInput += reviewResult.InputTokens
						stats.TotalOutput += reviewResult.OutputTokens
						stats.TotalTurns += reviewResult.NumTurns
					}

					// Parse REVIEW_STATUS sentinel.
					reviewStatus := parseSentinelJSON[ReviewStatus](reviewResult.RawOutput, "REVIEW_STATUS:")
					if reviewStatus != nil {
						stats.ReviewBeadsFromPost += len(reviewStatus.BeadsFiled)
						stats.ReviewFixesApplied += len(reviewStatus.FixesApplied)
						log.Log("Post-task review: %d beads filed, %d fixes applied, severity=%s",
							len(reviewStatus.BeadsFiled), len(reviewStatus.FixesApplied), reviewStatus.Severity)
					} else if reviewResult.Error != nil {
						log.Log("%sReview session error: %v%s", cRed, reviewResult.Error, cReset)
					} else {
						log.Log("%sWARNING: No structured status from review agent.%s", cYellow, cReset)
					}

					if reviewResult.UserQuit {
						result.UserQuit = true
					}
				}
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
	log.Log("%s  Agent Orchestration Runner Summary%s", cBold, cReset)
	log.Log("%s════════════════════════════════════════%s", cCyan, cReset)
	log.Log("  Tasks completed:   %s%d%s", cGreen, stats.TasksCompleted, cReset)
	log.Log("  Issues discovered: %d", stats.Discovered)
	log.Log("  Review beads:      %d", stats.ReviewBeads)
	if stats.Decomposed > 0 {
		log.Log("  Decomposed:        %d", stats.Decomposed)
	}
	log.Log("  Sessions run:      %d", stats.SessionsRun)
	log.Log("  Errors:            %d", stats.Errors)
	if stats.WrapUpSessions > 0 {
		log.Log("  Wrap-up sessions:  %d", stats.WrapUpSessions)
	}
	if stats.ReviewSessions > 0 {
		log.Log("  Review sessions:   %d", stats.ReviewSessions)
		log.Log("  Review beads:      %d", stats.ReviewBeadsFromPost)
		log.Log("  Review fixes:      %d", stats.ReviewFixesApplied)
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
