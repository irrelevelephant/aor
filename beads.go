package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// ErrScopeViolation is returned when a mutation targets a bead outside the active scope.
var ErrScopeViolation = errors.New("scope violation")

// checkScope verifies that a bead belongs to the active scope before mutating it.
// Returns nil when scope is empty (no enforcement), the scope label is present,
// or labels are unknown (nil/empty) — since bd ready --json does not populate
// labels, tasks fetched via --label filtering will have empty labels here.
// The guard only rejects when labels are populated AND the scope label is absent.
func checkScope(op, id, scope string, labels []string) error {
	if scope == "" {
		return nil
	}
	if len(labels) == 0 {
		return nil // labels unknown — trust upstream filtering
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

// closeEligibleEpics runs bd epic close-eligible to auto-close any epics
// whose children are all complete. Returns the IDs of closed epics.
func closeEligibleEpics() ([]string, error) {
	out, err := exec.Command("bd", "epic", "close-eligible", "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("bd epic close-eligible: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	var closed []struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	}
	if err := json.Unmarshal(out, &closed); err != nil {
		return nil, fmt.Errorf("parse bd epic close-eligible output: %w", err)
	}

	var ids []string
	for _, e := range closed {
		ids = append(ids, e.ID)
	}
	return ids, nil
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

// --- PID lock file functions for stuck-task recovery ---

// resolveLockDir determines where to write PID lock files. Mirrors resolveLogDir.
func resolveLockDir() string {
	if info, err := os.Stat(".beads"); err == nil && info.IsDir() {
		return filepath.Join(".beads", "runner-locks")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".beads", "runner-locks")
	}
	return filepath.Join(home, ".beads", "runner-locks")
}

// writeLockFile atomically writes a PID lock file for the given task.
func writeLockFile(lockDir, taskID, scope string) error {
	if err := os.MkdirAll(lockDir, 0o755); err != nil {
		return fmt.Errorf("create lock dir: %w", err)
	}

	hostname, _ := os.Hostname()
	pid := os.Getpid()
	startTime, _ := procStartTime(pid)

	info := LockInfo{
		PID:       pid,
		StartTime: startTime,
		Hostname:  hostname,
		Scope:     scope,
		ClaimedAt: time.Now().UTC().Format(time.RFC3339),
	}

	data, err := json.Marshal(info)
	if err != nil {
		return fmt.Errorf("marshal lock info: %w", err)
	}

	// Atomic write: tmp file + rename.
	tmp := filepath.Join(lockDir, taskID+".lock.tmp")
	target := filepath.Join(lockDir, taskID+".lock")

	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("write tmp lock file: %w", err)
	}
	if err := os.Rename(tmp, target); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("rename lock file: %w", err)
	}
	return nil
}

// removeLockFile removes the lock file for a task. No-op if absent.
func removeLockFile(lockDir, taskID string) error {
	path := filepath.Join(lockDir, taskID+".lock")
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// readLockFile parses a lock file at the given path.
func readLockFile(path string) (*LockInfo, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var info LockInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// procStartTime reads the process start time from /proc/<pid>/stat.
// Returns 0 on any error (non-Linux, process gone, etc.).
func procStartTime(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	// Format: pid (comm) state ... field22=starttime
	// The comm field can contain spaces and parens, so find the last ')'.
	s := string(data)
	idx := strings.LastIndex(s, ")")
	if idx < 0 || idx+2 >= len(s) {
		return 0, fmt.Errorf("cannot parse /proc/%d/stat", pid)
	}
	fields := strings.Fields(s[idx+2:])
	// After ')' and state, starttime is field index 19 (0-based from after comm).
	// Fields after ')': state(0) ppid(1) pgrp(2) session(3) tty_nr(4) tpgid(5)
	// flags(6) minflt(7) cminflt(8) majflt(9) cmajflt(10) utime(11) stime(12)
	// cutime(13) cstime(14) priority(15) nice(16) num_threads(17) itrealvalue(18) starttime(19)
	if len(fields) < 20 {
		return 0, fmt.Errorf("too few fields in /proc/%d/stat", pid)
	}
	st, err := strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse starttime: %w", err)
	}
	return st, nil
}

// isProcessAlive checks whether a process with the given PID and start time
// is still running. The start time comparison guards against PID reuse.
func isProcessAlive(pid int, startTime uint64) bool {
	// Check if process exists.
	err := syscall.Kill(pid, 0)
	if err != nil {
		return false
	}
	// If we recorded a start time, verify it matches to guard PID reuse.
	if startTime > 0 {
		currentStart, err := procStartTime(pid)
		if err != nil {
			return false
		}
		if currentStart != startTime {
			return false
		}
	}
	return true
}

// recoverStuckTasks scans the lock directory for orphaned tasks (owner PID dead)
// and unclaims them. Returns the number of tasks recovered.
func recoverStuckTasks(lockDir, scope string, log *Logger) int {
	entries, err := os.ReadDir(lockDir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0
		}
		log.Log("%sStuck-task recovery: cannot read lock dir: %v%s", cYellow, err, cReset)
		return 0
	}

	hostname, _ := os.Hostname()
	recovered := 0

	for _, e := range entries {
		if !strings.HasSuffix(e.Name(), ".lock") {
			continue
		}

		path := filepath.Join(lockDir, e.Name())
		info, err := readLockFile(path)
		if err != nil {
			log.Log("%sStuck-task recovery: corrupt lock file %s: %v — removing%s", cYellow, e.Name(), err, cReset)
			os.Remove(path)
			continue
		}

		// Skip lock files from different hosts (shared-filesystem safety).
		if info.Hostname != hostname {
			continue
		}

		// Skip if the owning process is still alive.
		if isProcessAlive(info.PID, info.StartTime) {
			continue
		}

		taskID := strings.TrimSuffix(e.Name(), ".lock")

		// Verify task is still in_progress before unclaiming.
		task, ferr := getTaskStatus(taskID)
		if ferr != nil {
			log.Log("%sStuck-task recovery: cannot check %s: %v — removing stale lock%s", cYellow, taskID, ferr, cReset)
			os.Remove(path)
			continue
		}
		if task.Status != "in_progress" {
			// Task was resolved by other means; remove stale lock.
			os.Remove(path)
			continue
		}

		log.Log("Stuck-task recovery: %s (PID %d dead) — unclaiming", taskID, info.PID)
		if err := unclaimTask(taskID, "", nil); err != nil {
			log.Log("%sStuck-task recovery: failed to unclaim %s: %v%s", cRed, taskID, err, cReset)
			continue
		}
		os.Remove(path)
		recovered++
	}

	return recovered
}
