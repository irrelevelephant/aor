package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// findAta verifies that the ata CLI is available in PATH.
func findAta() error {
	if _, err := exec.LookPath("ata"); err != nil {
		return fmt.Errorf("ata not found in PATH — install with: cd ata && go install .")
	}
	return nil
}

// resolveLogDir determines where to write logs.
func resolveLogDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "runner-logs"
	}
	return filepath.Join(home, ".ata", "runner-logs")
}

// getReadyTasks fetches queue tasks from ata, optionally filtered by epic and workspace.
func getReadyTasks(epicFilter, tagFilter, workspace string) ([]AtaTask, error) {
	args := []string{"ready", "--json", "--limit", "0"}
	if workspace != "" {
		args = append(args, "--workspace", workspace)
	}
	if epicFilter != "" {
		args = append(args, "--epic", epicFilter)
	}
	if tagFilter != "" {
		args = append(args, "--tag", tagFilter)
	}

	out, err := exec.Command("ata", args...).Output()
	if err != nil && len(out) == 0 {
		return nil, fmt.Errorf("ata ready failed: %w", err)
	}

	var tasks []AtaTask
	if err := json.Unmarshal(out, &tasks); err != nil {
		return nil, fmt.Errorf("parse ata ready output: %w (raw: %s)", err, strings.TrimSpace(string(out)))
	}

	return tasks, nil
}

// claimTask marks a task as in_progress, storing the aor process PID so that
// RecoverStuckTasks checks the right (long-lived) process.
func claimTask(id string) error {
	pid := strconv.Itoa(os.Getpid())
	out, err := exec.Command("ata", "claim", id, "--json", "--pid", pid).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ata claim failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// closeTask marks a task as closed with the given reason.
func closeTask(id, reason string) error {
	out, err := exec.Command("ata", "close", id, reason, "--json").CombinedOutput()
	if err != nil {
		return fmt.Errorf("ata close failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// unclaimTask resets a task back to queue.
func unclaimTask(id string) error {
	out, err := exec.Command("ata", "unclaim", id, "--json").CombinedOutput()
	if err != nil {
		return fmt.Errorf("ata unclaim failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// getTaskStatus fetches the current state of a single task.
func getTaskStatus(id string) (*AtaTask, error) {
	out, err := exec.Command("ata", "show", id, "--json").Output()
	if err != nil {
		return nil, fmt.Errorf("ata show failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	// ata show --json returns a TaskWithComments; we parse just the task fields.
	var task AtaTask
	if err := json.Unmarshal(out, &task); err != nil {
		return nil, fmt.Errorf("parse ata show output: %w", err)
	}
	return &task, nil
}

// addComment adds a comment to a task.
func addComment(id, body, author string) error {
	out, err := exec.Command("ata", "comment", id, body, "--author", author, "--json").CombinedOutput()
	if err != nil {
		return fmt.Errorf("ata comment failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// getTaskComments retrieves comments for a task via ata show --json.
func getTaskComments(id string) (string, error) {
	out, err := exec.Command("ata", "show", id, "--json").Output()
	if err != nil {
		return "", fmt.Errorf("ata show failed: %w", err)
	}

	var twc struct {
		Comments []struct {
			Body      string `json:"body"`
			Author    string `json:"author"`
			CreatedAt string `json:"created_at"`
		} `json:"comments"`
	}
	if err := json.Unmarshal(out, &twc); err != nil {
		return "", nil
	}

	if len(twc.Comments) == 0 {
		return "", nil
	}

	var b strings.Builder
	for _, c := range twc.Comments {
		fmt.Fprintf(&b, "[%s] %s: %s\n", c.CreatedAt, c.Author, c.Body)
	}
	return b.String(), nil
}

// getTasksCreatedAfter returns tasks created after the given time for a workspace.
func getTasksCreatedAfter(after time.Time, workspace string) ([]AtaTask, error) {
	args := []string{"list", "--json"}
	if workspace != "" {
		args = append(args, "--workspace", workspace)
	}

	out, err := exec.Command("ata", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("ata list failed: %w", err)
	}

	var tasks []AtaTask
	if err := json.Unmarshal(out, &tasks); err != nil {
		return nil, fmt.Errorf("parse ata list output: %w", err)
	}

	// Client-side filter by created_at.
	afterStr := after.UTC().Format(time.RFC3339)
	var filtered []AtaTask
	for _, t := range tasks {
		if t.CreatedAt >= afterStr {
			filtered = append(filtered, t)
		}
	}
	return filtered, nil
}

// closeEligibleEpics runs ata epic-close-eligible to auto-close epics.
func closeEligibleEpics(workspace string) ([]string, error) {
	args := []string{"epic-close-eligible", "--json"}
	if workspace != "" {
		args = append(args, "--workspace", workspace)
	}

	out, err := exec.Command("ata", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("ata epic-close-eligible: %w (%s)", err, strings.TrimSpace(string(out)))
	}

	var result struct {
		Closed []string `json:"closed"`
		Count  int      `json:"count"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return nil, nil // Gracefully handle empty/null
	}

	return result.Closed, nil
}

// topTask returns the task with the lowest sort_order, breaking ties by creation date.
func topTask(tasks []AtaTask) *AtaTask {
	if len(tasks) == 0 {
		return nil
	}
	best := &tasks[0]
	for i := 1; i < len(tasks); i++ {
		t := &tasks[i]
		if t.SortOrder < best.SortOrder || (t.SortOrder == best.SortOrder && t.CreatedAt < best.CreatedAt) {
			best = t
		}
	}
	return best
}

// recoverStuckTasks recovers tasks with dead PIDs.
func recoverStuckTasks(workspace string, log *Logger) int {
	args := []string{"recover", "--json"}
	if workspace != "" {
		args = append(args, "--workspace", workspace)
	}
	out, err := exec.Command("ata", args...).Output()
	if err != nil {
		return 0
	}
	var recovered []AtaTask
	if err := json.Unmarshal(out, &recovered); err != nil {
		return 0
	}
	for _, t := range recovered {
		log.Log("Recovered stuck task: %s — %s", t.ID, t.Title)
	}
	return len(recovered)
}

// runUnclaim resets all in-progress tasks for the workspace.
func runUnclaim(cfg *Config) error {
	args := []string{"unclaim", "--json"}
	if cfg.Workspace != "" {
		args = append(args, "--workspace", cfg.Workspace)
	}

	out, err := exec.Command("ata", args...).Output()
	if err != nil && len(out) == 0 {
		return fmt.Errorf("ata unclaim failed: %w", err)
	}

	var tasks []AtaTask
	if err := json.Unmarshal(out, &tasks); err != nil {
		// May be a single task or error message.
		fmt.Println(strings.TrimSpace(string(out)))
		return nil
	}

	if len(tasks) == 0 {
		fmt.Println("no in-progress tasks found")
		return nil
	}

	for _, t := range tasks {
		fmt.Printf("  unclaimed %s  %s\n", t.ID, t.Title)
	}
	fmt.Printf("\nunclaimed %d task(s)\n", len(tasks))
	return nil
}

// addTagToTask adds a tag to a task (safety net for prompt-based tagging).
func addTagToTask(taskID, tag string) error {
	out, err := exec.Command("ata", "tag", "add", taskID, tag).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ata tag add failed: %w (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// formatAttachments returns a prompt section listing attachment file paths.
// Returns empty string if there are no attachments.
func formatAttachments(attachments []AtaAttachment, taskID string) string {
	if len(attachments) == 0 {
		return ""
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	baseDir := filepath.Join(home, ".ata", "attachments", taskID)

	var b strings.Builder
	fmt.Fprintf(&b, "\n## Attachments\nThis task has %d attachment(s). Use the Read tool to view them:\n", len(attachments))
	for _, a := range attachments {
		size := formatHumanSize(a.SizeBytes)
		absPath := filepath.Join(baseDir, a.StoredName)
		fmt.Fprintf(&b, "- %s (%s, %s): %s\n", a.Filename, a.MimeType, size, absPath)
	}
	return b.String()
}

// formatHumanSize returns a human-readable file size.
func formatHumanSize(b int64) string {
	switch {
	case b >= 1<<20:
		return fmt.Sprintf("%.1f MB", float64(b)/float64(1<<20))
	case b >= 1<<10:
		return fmt.Sprintf("%.1f KB", float64(b)/float64(1<<10))
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// getEpicSpec retrieves the spec for an epic.
func getEpicSpec(epicID string) string {
	out, err := exec.Command("ata", "spec", epicID, "--json").Output()
	if err != nil {
		return ""
	}
	var result struct {
		Spec string `json:"spec"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return ""
	}
	return result.Spec
}

