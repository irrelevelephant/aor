package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// findBeadsDB verifies that the bd CLI can find a beads database,
// either project-local (.beads/) or global (~/.beads/).
func findBeadsDB() error {
	out, err := exec.Command("bd", "info", "--json").CombinedOutput()
	if err != nil {
		// Fallback: check if bd ready works (bd info may not exist in older versions).
		out2, err2 := exec.Command("bd", "ready", "--json").CombinedOutput()
		if err2 != nil {
			combined := string(out) + string(out2)
			if strings.Contains(combined, "no database") || strings.Contains(combined, "not initialized") {
				return fmt.Errorf("no beads database found\n"+
					"  Run 'bd init --stealth' for local-only tracking, or\n"+
					"  Run 'bd init' for git-tracked tracking")
			}
			return fmt.Errorf("bd not working: %s", strings.TrimSpace(combined))
		}
	}
	return nil
}

// resolveLogDir determines where to write logs. Prefers project-local .beads/
// if it exists, otherwise falls back to ~/.beads/.
func resolveLogDir() string {
	if info, err := os.Stat(".beads"); err == nil && info.IsDir() {
		return filepath.Join(".beads", "runner-logs")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".beads", "runner-logs")
	}
	return filepath.Join(home, ".beads", "runner-logs")
}

// getReadyTasks fetches unblocked tasks from beads, optionally filtered by epic prefix
// and scope label.
func getReadyTasks(epicFilter, scope string) ([]BeadTask, error) {
	args := []string{"ready", "--json"}
	if scope != "" {
		args = append(args, "--label", scope)
	}
	out, err := exec.Command("bd", args...).Output()
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("bd ready failed: %w", err)
	}
	// If bd returned a non-zero exit but produced output, try parsing it anyway.

	var tasks []BeadTask
	if err := json.Unmarshal(out, &tasks); err != nil {
		return nil, fmt.Errorf("parse bd ready output: %w (raw: %s)", err, strings.TrimSpace(string(out)))
	}

	if epicFilter == "" {
		return tasks, nil
	}

	var filtered []BeadTask
	for _, t := range tasks {
		if strings.HasPrefix(t.ID, epicFilter) {
			filtered = append(filtered, t)
		}
	}
	return filtered, nil
}

// claimTask marks a task as in_progress and assigned to the current runner.
func claimTask(id string) error {
	out, err := exec.Command("bd", "update", id, "--claim", "--json").CombinedOutput()
	if err != nil {
		return fmt.Errorf("bd update --claim failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// unclaimTask resets a task back to open with no assignee.
func unclaimTask(id string) error {
	out, err := exec.Command("bd", "update", id, "--status", "open", "--assignee", "", "--json").CombinedOutput()
	if err != nil {
		return fmt.Errorf("bd update --unclaim failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// getTaskStatus fetches the current state of a single task from beads.
// Used as a fallback when the agent doesn't output structured status.
func getTaskStatus(id string) (*BeadTask, error) {
	out, err := exec.Command("bd", "show", id, "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("bd show failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	var task BeadTask
	if err := json.Unmarshal(out, &task); err != nil {
		return nil, fmt.Errorf("parse bd show output: %w", err)
	}
	return &task, nil
}

// topTask returns the highest-priority task (lowest priority number),
// breaking ties by earliest creation date. CreatedAt is compared
// lexicographically, so it must be in a sortable format (e.g. ISO 8601).
func topTask(tasks []BeadTask) *BeadTask {
	if len(tasks) == 0 {
		return nil
	}
	best := &tasks[0]
	for i := 1; i < len(tasks); i++ {
		t := &tasks[i]
		if t.Priority < best.Priority || (t.Priority == best.Priority && t.CreatedAt < best.CreatedAt) {
			best = t
		}
	}
	return best
}
