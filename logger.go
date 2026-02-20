package main

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"time"
)

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string {
	return ansiRe.ReplaceAllString(s, "")
}

// Logger writes to both stdout and log files for run and session output.
type Logger struct {
	runLog     *os.File
	sessionLog *os.File
	logDir     string
}

// NewLogger creates a Logger and opens the run-level log file.
func NewLogger(logDir string) (*Logger, error) {
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		return nil, fmt.Errorf("create log dir: %w", err)
	}
	ts := time.Now().Format("20060102-150405")
	runLogPath := filepath.Join(logDir, fmt.Sprintf("run-%s.log", ts))
	f, err := os.Create(runLogPath)
	if err != nil {
		return nil, fmt.Errorf("create run log: %w", err)
	}
	return &Logger{runLog: f, logDir: logDir}, nil
}

// StartSessionLog opens a new session-level log file, closing any previous one.
func (l *Logger) StartSessionLog() error {
	if l.sessionLog != nil {
		l.sessionLog.Close()
	}
	ts := time.Now().Format("20060102-150405.000")
	path := filepath.Join(l.logDir, fmt.Sprintf("session-%s.log", ts))
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("create session log: %w", err)
	}
	l.sessionLog = f
	return nil
}

// Log writes a timestamped message to both stdout and the run log.
func (l *Logger) Log(format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	ts := time.Now().Format("15:04:05")
	line := fmt.Sprintf("[%s] %s\n", ts, msg)
	fmt.Print(line)
	if l.runLog != nil {
		l.runLog.WriteString(stripANSI(line))
	}
}

// SessionWrite appends raw data to the current session log.
func (l *Logger) SessionWrite(data string) {
	if l.sessionLog != nil {
		l.sessionLog.WriteString(data)
	}
}

// Close closes all open log files.
func (l *Logger) Close() {
	if l.runLog != nil {
		l.runLog.Close()
	}
	if l.sessionLog != nil {
		l.sessionLog.Close()
	}
}

// RunLogPath returns the path of the current run log file.
func (l *Logger) RunLogPath() string {
	if l.runLog != nil {
		return l.runLog.Name()
	}
	return ""
}
