package main

import (
	"fmt"
	"strings"
)

// verifyEpic runs the epic verification loop: verify spec criteria, file tasks
// for gaps, run them, and re-verify up to cfg.MaxRounds times.
func verifyEpic(epicID string, cfg *Config, rc *RunContext) (bool, error) {
	maxRounds := cfg.MaxRounds
	if maxRounds <= 0 {
		maxRounds = 3
	}

	for round := 1; round <= maxRounds; round++ {
		rc.Log.Log("Epic %s verification round %d/%d", epicID, round, maxRounds)

		// 1. Get current epic details.
		epic, err := getTaskStatus(epicID)
		if err != nil {
			return false, fmt.Errorf("get epic status: %w", err)
		}
		if epic.Status == "closed" {
			rc.Log.Log("Epic %s already closed", epicID)
			return true, nil
		}

		// 2. Get all children — confirm all closed.
		children, err := getEpicChildren(epicID)
		if err != nil {
			return false, fmt.Errorf("get epic children: %w", err)
		}

		for _, c := range children {
			if c.Status != "closed" {
				rc.Log.Log("Epic %s has unclosed child %s (%s) — cannot verify yet", epicID, c.ID, c.Status)
				return false, nil
			}
		}

		// 3. Build verification prompt.
		prompt := buildEpicVerifyPrompt(epic, children, cfg)

		// 4. Run verification session (yolo mode — needs to read files/run tests).
		fmt.Printf("\n%s─── Epic Verify %s (round %d/%d) ──────────────────────%s\n\n",
			cCyan, epicID, round, maxRounds, cReset)

		sessionCfg := &Config{
			Yolo:    cfg.Yolo,
			LogDir:  cfg.LogDir,
			WorkDir: cfg.WorkDir,
		}
		result := runSession(sessionCfg, rc, prompt)

		// Pause and retry this round if rate limited.
		if waitForRateLimit(result.RateLimitReset, rc) {
			round--
			continue
		}

		rc.Stats.VerifySessions++

		if result.InputTokens > 0 || result.OutputTokens > 0 {
			rc.Stats.TotalCostUSD += result.TotalCostUSD
			rc.Stats.TotalInput += result.InputTokens
			rc.Stats.TotalOutput += result.OutputTokens
		}

		if result.Error != nil {
			return false, fmt.Errorf("verification session error: %w", result.Error)
		}
		if result.UserQuit {
			return false, nil
		}

		// 5. Parse sentinel.
		status := parseSentinelJSON[EpicVerifyStatus](result.RawOutput, "EPIC_VERIFY_STATUS:")
		if status == nil {
			snippet := result.RawOutput
			if len(snippet) > 200 {
				snippet = snippet[len(snippet)-200:]
			}
			rc.Log.Log("%sWARNING: No EPIC_VERIFY_STATUS sentinel from verification agent (tail: %s)%s", cYellow, snippet, cReset)
			return false, nil
		}

		if status.Error != nil {
			rc.Log.Log("%sVerification agent error: %s%s", cRed, *status.Error, cReset)
			return false, nil
		}

		// 6. If passed — close epic.
		if status.Passed {
			reason := "verification passed"
			if status.Summary != "" {
				reason = fmt.Sprintf("verification passed: %s", status.Summary)
			}
			if err := closeTask(epicID, reason); err != nil {
				return false, fmt.Errorf("close epic: %w", err)
			}
			rc.Stats.EpicsVerified++
			rc.Stats.EpicsClosed++
			return true, nil
		}

		// 7. Failed with tasks filed — propagate deps, run inner orchestration, then re-verify.
		if len(status.TasksFiled) > 0 {
			var taskIDs []string
			for _, t := range status.TasksFiled {
				taskIDs = append(taskIDs, t.ID)
			}
			rc.Log.Log("Verification filed %d task(s): %s", len(status.TasksFiled), strings.Join(taskIDs, ", "))

			// Propagate: anything that depends on the epic should also
			// depend on each newly filed verification task.
			propagateDiscoveredDeps(rc.Log, epicID, taskIDs)
			rc.Log.Log("Summary: %s", status.Summary)

			// Run inner orchestration loop to complete the filed tasks.
			runCfg := &Config{
				EpicFilter:      epicID,
				Workspace:       cfg.Workspace,
				WorkDir:         cfg.WorkDir,
				Yolo:            cfg.Yolo,
				LogDir:          cfg.LogDir,
				BatchSize:       1,
				StdinCh:         rc.StdinCh,
				Log:             rc.Log,
				Stats:           rc.Stats,
				SuppressSummary: true,
				SkipEpicClose:   true,
			}
			if err := run(runCfg); err != nil {
				rc.Log.Log("%sInner orchestration error: %v%s", cRed, err, cReset)
			}

			// Loop back to re-verify.
			continue
		}

		// 8. Failed with no tasks and no error — nothing to do.
		rc.Log.Log("Verification failed but no tasks filed. Summary: %s", status.Summary)
		return false, nil
	}

	rc.Log.Log("Epic %s: max verification rounds (%d) reached", epicID, maxRounds)
	return false, nil
}

// tryVerifyFilteredEpic checks if the filtered epic is eligible for verification
// and runs verifyEpic if so. Returns true if verification was attempted.
func tryVerifyFilteredEpic(epicID string, cfg *Config, rc *RunContext) bool {
	epic, err := getTaskStatus(epicID)
	if err != nil || epic == nil {
		return false
	}

	// Only verify open epics with a spec.
	if epic.Status == "closed" || epic.Spec == "" || !epic.IsEpic {
		return false
	}

	// Check that all children are closed.
	children, err := getEpicChildren(epicID)
	if err != nil {
		return false
	}
	for _, c := range children {
		if c.Status != "closed" {
			return false
		}
	}
	// Must have at least one child.
	if len(children) == 0 {
		return false
	}

	rc.Log.Log("Epic %s children all closed — verifying...", epicID)
	passed, err := verifyEpic(epicID, cfg, rc)
	if err != nil {
		rc.Log.Log("Epic %s verification error: %v", epicID, err)
	} else if passed {
		rc.Log.Log("Epic %s verified and closed", epicID)
	} else {
		rc.Log.Log("Epic %s did not pass verification", epicID)
	}
	return true
}
