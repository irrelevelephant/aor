package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// ErrScopeViolation is returned when a mutation targets a bead outside the active scope.
var ErrScopeViolation = errors.New("scope violation")

// checkScope verifies that a bead belongs to the active scope before mutating it.
// Returns nil when scope is empty (no enforcement) or the scope label is present.
func checkScope(op, id, scope string, labels []string) error {
	if scope == "" {
		return nil
	}
	for _, l := range labels {
		if l == scope {
			return nil
		}
	}
	return fmt.Errorf("%s %s: label %q not found in %v: %w", op, id, scope, labels, ErrScopeViolation)
}

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
	args := []string{"ready", "--json", "--limit", "0"}
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
// NOTE: We avoid bd's --claim flag because it has a deadlock bug in embedded
// Dolt mode (uses s.db instead of tx for a fallback query when the issue is
// already assigned, blocking on the single-connection pool). Instead we set
// status and assignee directly, which is safe since aor is the sole claimer.
func claimTask(id, scope string, labels []string) error {
	if err := checkScope("claim", id, scope, labels); err != nil {
		return err
	}
	out, err := exec.Command("bd", "update", id,
		"--status", "in_progress",
		"--json").CombinedOutput()
	if err != nil {
		return fmt.Errorf("bd update (claim) failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// unclaimTask resets a task back to open with no assignee.
// Pass empty scope and nil labels to bypass scope enforcement (e.g. signal-handler cleanup).
func unclaimTask(id, scope string, labels []string) error {
	if err := checkScope("unclaim", id, scope, labels); err != nil {
		return err
	}
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

// countUnscopedReadyTasks returns the number of ready tasks globally (no label filter).
// Used to detect scope mismatches when scoped queries return empty.
func countUnscopedReadyTasks() int {
	out, err := exec.Command("bd", "ready", "--json", "--limit", "0").Output()
	if err != nil {
		return 0
	}
	var tasks []BeadTask
	if err := json.Unmarshal(out, &tasks); err != nil {
		return 0
	}
	return len(tasks)
}

// reconcileScope finds issues created during the session that lack the scope
// label and applies it. Returns the number of issues fixed.
func reconcileScope(scope string, startTime time.Time, log *Logger) int {
	if scope == "" {
		return 0
	}

	out, err := exec.Command("bd", "list",
		"--created-after", startTime.Format(time.RFC3339),
		"--json", "--limit", "0").Output()
	if err != nil {
		log.Log("%sScope reconciliation: bd list --created-after failed: %v%s", cYellow, err, cReset)
		return 0
	}

	var tasks []BeadTask
	if err := json.Unmarshal(out, &tasks); err != nil {
		log.Log("%sScope reconciliation: parse error: %v%s", cYellow, err, cReset)
		return 0
	}

	fixed := 0
	for _, t := range tasks {
		hasScope := false
		for _, l := range t.Labels {
			if l == scope {
				hasScope = true
				break
			}
		}
		if hasScope {
			continue
		}

		labelOut, labelErr := exec.Command("bd", "label", "add", t.ID, scope).CombinedOutput()
		if labelErr != nil {
			log.Log("%sScope reconciliation: failed to label %s: %v (%s)%s",
				cYellow, t.ID, labelErr, strings.TrimSpace(string(labelOut)), cReset)
			continue
		}
		log.Log("Scope reconciliation: labeled %s with %q", t.ID, scope)
		fixed++
	}

	return fixed
}

// addComment adds a comment to a bead.
func addComment(id, body, scope string, labels []string) error {
	if err := checkScope("comment", id, scope, labels); err != nil {
		return err
	}
	out, err := exec.Command("bd", "comments", "add", id, body).CombinedOutput()
	if err != nil {
		return fmt.Errorf("bd comments add %s: %w (%s)", id, err, strings.TrimSpace(string(out)))
	}
	return nil
}

// getTaskComments retrieves all comments for a task as a single string.
func getTaskComments(id string) (string, error) {
	out, err := exec.Command("bd", "comments", "list", id).Output()
	if err != nil {
		return "", fmt.Errorf("bd comments list %s: %w", id, err)
	}
	return strings.TrimSpace(string(out)), nil
}

// getBeadsCreatedAfter returns beads created after the given time.
func getBeadsCreatedAfter(after time.Time) ([]BeadTask, error) {
	out, err := exec.Command("bd", "list",
		"--created-after", after.Format(time.RFC3339),
		"--json", "--limit", "0").Output()
	if err != nil {
		return nil, fmt.Errorf("bd list --created-after: %w", err)
	}
	var tasks []BeadTask
	if err := json.Unmarshal(out, &tasks); err != nil {
		return nil, fmt.Errorf("parse bd list output: %w", err)
	}
	return tasks, nil
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
