package main

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestCheckScope_EmptyScope(t *testing.T) {
	if err := checkScope("claim", "T-1", "", []string{"foo"}); err != nil {
		t.Fatalf("empty scope should pass, got: %v", err)
	}
}

func TestCheckScope_MatchingLabel(t *testing.T) {
	if err := checkScope("claim", "T-1", "wt-abc", []string{"wt-abc", "other"}); err != nil {
		t.Fatalf("matching label should pass, got: %v", err)
	}
}

func TestCheckScope_MissingLabel(t *testing.T) {
	err := checkScope("claim", "T-1", "wt-abc", []string{"other"})
	if err == nil {
		t.Fatal("expected error for missing scope label")
	}
	if !errors.Is(err, ErrScopeViolation) {
		t.Fatalf("expected ErrScopeViolation, got: %v", err)
	}
}

func TestCheckScope_EmptyLabels(t *testing.T) {
	// Empty labels = unknown (bd ready --json doesn't populate labels).
	// Trust upstream filtering; do not reject.
	if err := checkScope("claim", "T-1", "wt-abc", []string{}); err != nil {
		t.Fatalf("empty labels should pass (unknown), got: %v", err)
	}
}

func TestCheckScope_NilLabels(t *testing.T) {
	// Nil labels = unknown; same as empty.
	if err := checkScope("claim", "T-1", "wt-abc", nil); err != nil {
		t.Fatalf("nil labels should pass (unknown), got: %v", err)
	}
}

// --- Lock file tests ---

func TestWriteReadLockFile(t *testing.T) {
	dir := t.TempDir()
	taskID := "TEST-42"

	if err := writeLockFile(dir, taskID, "wt-test"); err != nil {
		t.Fatalf("writeLockFile: %v", err)
	}

	path := filepath.Join(dir, taskID+".lock")
	info, err := readLockFile(path)
	if err != nil {
		t.Fatalf("readLockFile: %v", err)
	}

	if info.PID != os.Getpid() {
		t.Errorf("PID = %d, want %d", info.PID, os.Getpid())
	}
	if info.Scope != "wt-test" {
		t.Errorf("Scope = %q, want %q", info.Scope, "wt-test")
	}
	if info.ClaimedAt == "" {
		t.Error("ClaimedAt is empty")
	}
	if info.Hostname == "" {
		t.Error("Hostname is empty")
	}

	// Verify it's valid JSON by re-marshalling.
	data, _ := os.ReadFile(path)
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("lock file is not valid JSON: %v", err)
	}
}

func TestRemoveLockFile(t *testing.T) {
	dir := t.TempDir()
	taskID := "TEST-99"

	// Write then remove.
	if err := writeLockFile(dir, taskID, ""); err != nil {
		t.Fatalf("writeLockFile: %v", err)
	}
	if err := removeLockFile(dir, taskID); err != nil {
		t.Fatalf("removeLockFile: %v", err)
	}

	// File should be gone.
	path := filepath.Join(dir, taskID+".lock")
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("lock file still exists after remove")
	}

	// Removing non-existent file should be a no-op.
	if err := removeLockFile(dir, "DOES-NOT-EXIST"); err != nil {
		t.Fatalf("removeLockFile on non-existent should not error: %v", err)
	}
}

func TestIsProcessAlive_CurrentPID(t *testing.T) {
	pid := os.Getpid()
	st, err := procStartTime(pid)
	if err != nil {
		t.Skipf("procStartTime not available (non-Linux?): %v", err)
	}

	if !isProcessAlive(pid, st) {
		t.Fatal("current process should be alive")
	}
}

func TestIsProcessAlive_DeadPID(t *testing.T) {
	// PID 2^22-1 is unlikely to exist; kill -0 should fail.
	if isProcessAlive(4194303, 0) {
		t.Skip("PID 4194303 unexpectedly exists")
	}
	// Expected: not alive.
}

func TestProcStartTime_CurrentProcess(t *testing.T) {
	st, err := procStartTime(os.Getpid())
	if err != nil {
		t.Skipf("procStartTime not available (non-Linux?): %v", err)
	}
	if st == 0 {
		t.Fatal("procStartTime returned 0 for current process")
	}
}
