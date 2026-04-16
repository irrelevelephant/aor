package web

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"sync"

	"aor/ata/api"
)

// execMu serializes API exec calls since we redirect os.Stdout/os.Stderr.
var execMu sync.Mutex

// deniedCommands are commands that must not be run via the exec API.
// All other commands recognized by cmd.Dispatch are allowed.
var deniedCommands = map[string]bool{
	"serve":    true,
	"snapshot": true,
	"restore":  true,
}

func (s *Server) handleAPIExec(w http.ResponseWriter, r *http.Request) {
	if s.dispatch == nil {
		http.Error(w, "exec API not configured", http.StatusServiceUnavailable)
		return
	}

	var req api.ExecRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, api.ExecResponse{
			ExitCode: 1,
			Stderr:   "invalid request: " + err.Error(),
		})
		return
	}

	if deniedCommands[req.Command] {
		writeJSON(w, http.StatusBadRequest, api.ExecResponse{
			ExitCode: 1,
			Stderr:   "command not allowed: " + req.Command,
		})
		return
	}

	stdout, stderr, exitCode := s.execCommand(req.Command, req.Args)

	// Broadcast a generic refresh event so the web UI updates.
	s.hub.Broadcast("task_updated", "api")

	writeJSON(w, http.StatusOK, api.ExecResponse{
		ExitCode: exitCode,
		Stdout:   stdout,
		Stderr:   stderr,
	})
}

// execCommand runs a command in-process, capturing stdout and stderr.
func (s *Server) execCommand(command string, args []string) (stdout, stderr string, exitCode int) {
	execMu.Lock()
	defer execMu.Unlock()

	// Capture stdout.
	oldStdout := os.Stdout
	outR, outW, err := os.Pipe()
	if err != nil {
		return "", "pipe error: " + err.Error(), 1
	}
	os.Stdout = outW

	// Capture stderr.
	oldStderr := os.Stderr
	errR, errW, err := os.Pipe()
	if err != nil {
		outW.Close()
		outR.Close()
		os.Stdout = oldStdout
		return "", "pipe error: " + err.Error(), 1
	}
	os.Stderr = errW

	// Ensure stdout/stderr are restored even if dispatch panics.
	defer func() {
		os.Stdout = oldStdout
		os.Stderr = oldStderr
	}()

	// Read pipes concurrently to avoid blocking. Child processes spawned
	// during dispatch inherit pipe fds; reading concurrently prevents
	// deadlock if a child holds an fd open momentarily after the parent
	// closes its write end.
	var outBuf, errBuf bytes.Buffer
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); io.Copy(&outBuf, outR) }()
	go func() { defer wg.Done(); io.Copy(&errBuf, errR) }()

	// Run the command via injected dispatch function.
	cmdErr := s.dispatch(s.db, command, args)

	// Close write ends so readers see EOF, then wait for readers to finish.
	outW.Close()
	errW.Close()
	wg.Wait()
	outR.Close()
	errR.Close()

	code := 0
	errStr := errBuf.String()
	if cmdErr != nil {
		code = 1
		if errStr == "" {
			errStr = cmdErr.Error()
		}
	}

	return outBuf.String(), errStr, code
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
