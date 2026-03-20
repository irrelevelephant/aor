package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
)

// runSession launches a Claude Code session with the given prompt and streams
// output in real time. It supports user controls (interject, skip, quit) and
// graceful signal handling.
func runSession(cfg *Config, rc *RunContext, prompt string) *SessionResult {
	log := rc.Log
	stdinCh := rc.StdinCh
	result := &SessionResult{}

	if err := log.StartSessionLog(); err != nil {
		result.Error = fmt.Errorf("start session log: %w", err)
		return result
	}

	var args []string
	if cfg.ResumeSessionID != "" {
		// Resume an existing session and inject the prompt as a new user
		// message via --append-system-prompt (--resume ignores -p).
		args = []string{
			"--resume", cfg.ResumeSessionID,
			"--append-system-prompt", prompt,
			"--verbose",
			"--output-format", "stream-json",
		}
	} else {
		args = []string{
			"-p", prompt,
			"--verbose",
			"--output-format", "stream-json",
		}
	}
	if cfg.Yolo {
		args = append(args, "--dangerously-skip-permissions")
	}

	cmd := exec.Command("claude", args...)
	if cfg.WorkDir != "" {
		cmd.Dir = cfg.WorkDir
	}
	if cfg.Workspace != "" {
		cmd.Env = envWithWorkspace(cfg.Workspace)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		result.Error = fmt.Errorf("stdout pipe: %w", err)
		return result
	}
	cmd.Stderr = os.Stderr

	if err := cmd.Start(); err != nil {
		result.Error = fmt.Errorf("start claude: %w", err)
		return result
	}

	var (
		outputDone   = make(chan struct{})
		userAction   = make(chan string, 1)
		rawOutput    strings.Builder
		mu           sync.Mutex
		sessionID    string
		totalCostUSD float64
		inputTokens  int
		outputTokens int
		numTurns     int
		durationMS   int
		resultSeen   = make(chan struct{}, 1) // signaled when a "result" message arrives
	)

	// Signal handler for Ctrl+C — sends SIGINT to the agent and stops the
	// runner loop. Double press (within 2s) kills the agent immediately.
	// Exits when the session ends via outputDone being closed.
	sigCh := make(chan os.Signal, 3)
	signal.Notify(sigCh, syscall.SIGINT)
	defer signal.Stop(sigCh)

	var ctrlCKill atomic.Bool

	go func() {
		var (
			sigCount    int
			lastSigTime time.Time
		)
		for {
			select {
			case <-outputDone:
				return
			case <-sigCh:
			}

			now := time.Now()
			if now.Sub(lastSigTime) < 2*time.Second {
				sigCount++
			} else {
				sigCount = 1
			}
			lastSigTime = now

			if sigCount == 1 {
				fmt.Printf("\n%s[Ctrl+C] Stopping current session (press again to quit runner)...%s\n", cYellow, cReset)
				if cmd.Process != nil {
					cmd.Process.Signal(syscall.SIGINT)
				}
			} else {
				fmt.Printf("\n%s[Ctrl+C x2] Killing session and quitting runner.%s\n", cRed, cReset)
				ctrlCKill.Store(true)
				if cmd.Process != nil {
					cmd.Process.Kill()
				}
				return
			}
		}
	}()

	// Stream and display Claude output.
	go func() {
		defer close(outputDone)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 0, 1024*1024), 1024*1024)

		for scanner.Scan() {
			line := scanner.Text()

			mu.Lock()
			rawOutput.WriteString(line + "\n")
			mu.Unlock()

			log.SessionWrite(line + "\n")

			var msg ClaudeStreamMsg
			if err := json.Unmarshal([]byte(line), &msg); err != nil {
				fmt.Println(line)
				continue
			}

			if msg.SessionID != "" {
				mu.Lock()
				sessionID = msg.SessionID
				mu.Unlock()
			}

			// Capture usage data from result messages.
			if msg.Type == "result" {
				mu.Lock()
				if msg.Usage != nil {
					inputTokens = msg.Usage.InputTokens
					outputTokens = msg.Usage.OutputTokens
				}
				if msg.TotalCostUSD > 0 {
					totalCostUSD = msg.TotalCostUSD
				}
				if msg.NumTurns > 0 {
					numTurns = msg.NumTurns
				}
				if msg.DurationMS > 0 {
					durationMS = msg.DurationMS
				}
				mu.Unlock()

				// Signal that the session has produced a final result.
				select {
				case resultSeen <- struct{}{}:
				default:
				}
			}

			displayStreamMsg(msg)
		}
	}()

	// Watchdog: if a "result" message arrived but the process doesn't exit
	// within a grace period, kill it to prevent the runner from hanging.
	const stuckProcessGrace = 2 * time.Minute
	go func() {
		select {
		case <-resultSeen:
			// Got a result — give the process time to exit cleanly.
			select {
			case <-outputDone:
				return // exited normally
			case <-time.After(stuckProcessGrace):
				fmt.Printf("\n%s[runner] Claude process still alive %s after result — killing stuck process%s\n",
					cYellow, stuckProcessGrace, cReset)
				if cmd.Process != nil {
					cmd.Process.Kill()
				}
			}
		case <-outputDone:
			return // exited before result (shouldn't happen, but safe)
		}
	}()

	// Monitor stdin for user commands via shared channel.
	go func() {
		for {
			select {
			case <-outputDone:
				return
			case line, ok := <-stdinCh:
				if !ok {
					return
				}
				switch line {
				case "i", "interject":
					mu.Lock()
					sid := sessionID
					mu.Unlock()
					if sid == "" {
						fmt.Printf("%s[runner] No session ID yet, can't interject.%s\n", cYellow, cReset)
						continue
					}
					select {
					case userAction <- "interject":
					default:
					}
					return
				case "s", "skip":
					select {
					case userAction <- "skip":
					default:
					}
					return
				case "q", "quit":
					select {
					case userAction <- "quit":
					default:
					}
					return
				default:
					fmt.Printf("%s[runner] Commands: i=interject, s=skip, q=quit%s\n", cGray, cReset)
				}
			}
		}
	}()

	// Wait for output to finish or user action.
	select {
	case <-outputDone:
		cmd.Wait()

	case action := <-userAction:
		switch action {
		case "interject":
			fmt.Printf("\n%s══ Interjecting — dropping to interactive session ══%s\n", cCyan, cReset)
			if cmd.Process != nil {
				cmd.Process.Signal(syscall.SIGINT)
				time.Sleep(500 * time.Millisecond)
				cmd.Process.Kill()
			}
			cmd.Wait()
			<-outputDone

			mu.Lock()
			sid := sessionID
			mu.Unlock()

			if sid != "" {
				runInteractive(sid, cfg.Yolo, cfg.WorkDir, cfg.Workspace)
			} else {
				fmt.Printf("%s[runner] No session ID captured, can't resume interactively.%s\n", cRed, cReset)
			}
			fmt.Printf("\n%s══ Back in runner loop ══%s\n", cCyan, cReset)

		case "skip":
			fmt.Printf("\n%s[runner] Skipping current task.%s\n", cYellow, cReset)
			if cmd.Process != nil {
				cmd.Process.Kill()
			}
			cmd.Wait()
			<-outputDone
			result.UserSkipped = true

		case "quit":
			fmt.Printf("\n%s[runner] Quit requested. Finishing after current session.%s\n", cYellow, cReset)
			if cmd.Process != nil {
				cmd.Process.Signal(syscall.SIGINT)
			}
			cmd.Wait()
			<-outputDone
			result.UserQuit = true
		}
	}

	mu.Lock()
	result.RawOutput = rawOutput.String()
	result.SessionID = sessionID
	result.TotalCostUSD = totalCostUSD
	result.InputTokens = inputTokens
	result.OutputTokens = outputTokens
	result.NumTurns = numTurns
	result.DurationMS = durationMS
	mu.Unlock()

	result.Status = parseSentinelJSON[RunnerStatus](result.RawOutput, "ATA_RUNNER_STATUS:")

	// Double Ctrl+C (kill) should stop the entire runner.
	if ctrlCKill.Load() {
		result.UserQuit = true
	}

	return result
}

// envAtaWorkspace is the environment variable that overrides workspace
// detection in ata commands. Set on child Claude processes so tasks created
// from worktrees resolve to the main repo's registered workspace.
const envAtaWorkspace = "ATA_WORKSPACE"

// envWithWorkspace returns the current environment with ATA_WORKSPACE set.
func envWithWorkspace(workspace string) []string {
	return append(os.Environ(), envAtaWorkspace+"="+workspace)
}

// toolGutter is the left-border prefix for all tool-related output,
// visually separating tool activity from model text.
const toolGutter = "  │ "

func displayStreamMsg(msg ClaudeStreamMsg) {
	switch msg.Type {
	case "system":
		if msg.Subtype == "init" {
			fmt.Printf("%s[system] Session initialized%s\n", cGray, cReset)
		}
	case "assistant":
		if msg.Message != nil {
			for _, b := range msg.Message.Content {
				switch b.Type {
				case "text":
					if b.Text != "" {
						fmt.Printf("%s%s%s", cBold, b.Text, cReset)
						if !strings.HasSuffix(b.Text, "\n") {
							fmt.Println()
						}
					}
				case "tool_use":
					displayToolUse(b.Name, b.Input)
				}
			}
		} else if text := extractText(msg); text != "" {
			fmt.Printf("%s%s%s", cBold, text, cReset)
			if !strings.HasSuffix(text, "\n") {
				fmt.Println()
			}
		}
	case "user":
		// Tool results — suppress to avoid verbose output.
	case "result":
		if text := extractText(msg); text != "" {
			fmt.Printf("\n%s%s── Result ──%s\n", cBold, cGreen, cReset)
			fmt.Printf("%s\n", text)
		}
	default:
		if text := extractText(msg); text != "" {
			fmt.Print(text)
			if !strings.HasSuffix(text, "\n") {
				fmt.Println()
			}
		}
	}
}

// displayToolUse prints a tool call with the gutter prefix.
// Edit calls show a colored diff; other tools show a one-line summary.
func displayToolUse(name string, rawInput json.RawMessage) {
	var input map[string]interface{}
	json.Unmarshal(rawInput, &input)

	str := func(key string) string {
		if v, ok := input[key]; ok {
			if s, ok := v.(string); ok {
				return s
			}
		}
		return ""
	}

	g := cGray + toolGutter

	switch name {
	case "Edit":
		fmt.Printf("%s%sEdit%s %s%s%s\n", g, cYellow, cReset+cGray, cCyan, str("file_path"), cReset)
		displayDiff(str("old_string"), str("new_string"), str("file_path"))
	case "Write":
		fmt.Printf("%s%sWrite%s %s%s%s\n", g, cYellow, cReset+cGray, cCyan, str("file_path"), cReset)
	case "Read":
		fmt.Printf("%sRead %s%s%s\n", g, cCyan, str("file_path"), cReset)
	case "Bash":
		cmd := str("command")
		if len(cmd) > 120 {
			cmd = cmd[:120] + "..."
		}
		fmt.Printf("%s$ %s%s\n", g, cReset+cmd, cReset)
	case "Grep":
		pattern := str("pattern")
		path := str("path")
		if path != "" {
			fmt.Printf("%sGrep %s%q%s in %s%s%s\n", g, cReset, pattern, cGray, cCyan, path, cReset)
		} else {
			fmt.Printf("%sGrep %s%q%s\n", g, cReset, pattern, cReset)
		}
	case "Glob":
		fmt.Printf("%sGlob %s%s%s\n", g, cCyan, str("pattern"), cReset)
	case "Task":
		fmt.Printf("%sTask %s%s%s\n", g, cReset+cDim, str("description"), cReset)
	default:
		fmt.Printf("%s%s%s\n", g, name, cReset)
	}
}

const maxDiffLines = 40

// withBg re-applies a background color after every ANSI reset in text,
// so the background persists through syntax-highlighted content.
func withBg(text, bg string) string {
	return strings.ReplaceAll(text, cReset, cReset+bg)
}

// displayDiff prints removed/added lines with syntax highlighting,
// the gutter prefix, and red/green diff markers.
func displayDiff(old, new, filename string) {
	// Syntax-highlight both sides using the file extension.
	oldHL := highlightCode(old, filename)
	newHL := highlightCode(new, filename)

	oldLines := strings.Split(oldHL, "\n")
	newLines := strings.Split(newHL, "\n")

	total := len(oldLines) + len(newLines)
	truncated := false
	if total > maxDiffLines {
		truncated = true
		maxPer := maxDiffLines / 2
		if len(oldLines) > maxPer {
			oldLines = oldLines[:maxPer]
		}
		if len(newLines) > maxPer {
			newLines = newLines[:maxPer]
		}
	}

	g := cGray + toolGutter
	for _, line := range oldLines {
		fmt.Printf("%s%s%s- %s%s\n", g, cBgDarkRed, cRed, withBg(line, cBgDarkRed), cReset)
	}
	for _, line := range newLines {
		fmt.Printf("%s%s%s+ %s%s\n", g, cBgDarkGreen, cGreen, withBg(line, cBgDarkGreen), cReset)
	}
	if truncated {
		fmt.Printf("%s%s... (%d lines truncated)%s\n", g, cDim, total-maxDiffLines, cReset)
	}
}

func extractText(msg ClaudeStreamMsg) string {
	if msg.Content != "" {
		return msg.Content
	}
	var parts []string
	for _, b := range msg.ContentBlocks {
		if b.Text != "" {
			parts = append(parts, b.Text)
		}
	}
	if msg.Message != nil {
		for _, b := range msg.Message.Content {
			if b.Text != "" {
				parts = append(parts, b.Text)
			}
		}
	}
	return strings.Join(parts, "")
}

// tryParseSentinel searches text for a sentinel string followed by a JSON
// object. Returns the decoded object or nil if not found/invalid.
func tryParseSentinel[T any](text, sentinel string) *T {
	idx := strings.Index(text, sentinel)
	if idx == -1 {
		return nil
	}
	after := text[idx+len(sentinel):]
	braceIdx := strings.Index(after, "{")
	if braceIdx == -1 {
		return nil
	}
	var result T
	dec := json.NewDecoder(strings.NewReader(after[braceIdx:]))
	if err := dec.Decode(&result); err == nil {
		return &result
	}
	return nil
}

// parseSentinelJSON scans raw output for a sentinel string and extracts
// the first valid JSON object after it. Uses json.Decoder for robust parsing
// (correctly handles braces inside string values).
//
// Tries two strategies: per-line scanning (fast, works when the sentinel is
// in a single stream-json message) and concatenated text scanning (handles
// the case where streaming splits the sentinel across multiple messages).
func parseSentinelJSON[T any](output, sentinel string) *T {
	lines := strings.Split(output, "\n")

	// Strategy 1: check each line individually.
	for _, line := range lines {
		// Try the raw line (non-JSON or sentinel visible in raw text).
		if result := tryParseSentinel[T](line, sentinel); result != nil {
			return result
		}
		// Try extracting text from a stream-json message.
		var msg ClaudeStreamMsg
		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			if result := tryParseSentinel[T](extractText(msg), sentinel); result != nil {
				return result
			}
		}
	}

	// Strategy 2: concatenate all extracted text and search the combined
	// output. This handles streaming where the sentinel text is split
	// across multiple stream-json messages.
	var allText strings.Builder
	for _, line := range lines {
		if line == "" {
			continue
		}
		var msg ClaudeStreamMsg
		if err := json.Unmarshal([]byte(line), &msg); err == nil {
			allText.WriteString(extractText(msg))
		}
	}
	return tryParseSentinel[T](allText.String(), sentinel)
}

// runInteractive drops the user into an interactive Claude Code session
// that resumes from the given session ID.
func runInteractive(sessionID string, yolo bool, workDir, workspace string) {
	fmt.Printf("%s[runner] Launching: claude --resume %s%s\n", cCyan, sessionID, cReset)
	fmt.Printf("%s[runner] Exit the interactive session (Ctrl+D or /exit) to return to the runner.%s\n", cGray, cReset)

	runInteractiveClaude([]string{"--resume", sessionID}, yolo, workDir, workspace)
}

// runInteractiveClaude launches Claude Code with stdio piped directly to the
// terminal. Used by pull, merge, and the interject flow.
func runInteractiveClaude(claudeArgs []string, yolo bool, workDir, workspace string) {
	if yolo {
		claudeArgs = append(claudeArgs, "--dangerously-skip-permissions")
	}
	cmd := exec.Command("claude", claudeArgs...)
	if workDir != "" {
		cmd.Dir = workDir
	}
	if workspace != "" {
		cmd.Env = envWithWorkspace(workspace)
	}
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// Ignore SIGINT while the interactive child is running.
	// The terminal delivers these to the entire foreground process group;
	// Claude Code handles Ctrl+C internally. If we don't ignore it here,
	// Go's default handler kills aor while claude keeps running, leaving
	// the terminal in a broken state.
	// SIGTSTP is left at its default (system stop) so Ctrl+Z works
	// normally — both aor and claude suspend, and `fg` resumes them.
	signal.Ignore(syscall.SIGINT)
	defer signal.Reset(syscall.SIGINT)

	if err := cmd.Run(); err != nil {
		fmt.Printf("\nClaude session ended: %v\n", err)
	}
}

// startStdinReader spawns a goroutine that reads lines from os.Stdin and
// sends them on a channel. A single reader avoids contention from multiple
// bufio.Readers wrapping the same file descriptor, and the channel-based
// approach allows clean cancellation via select.
func startStdinReader() <-chan string {
	ch := make(chan string, 1)
	go func() {
		reader := bufio.NewReader(os.Stdin)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				close(ch)
				return
			}
			line = strings.TrimSpace(strings.ToLower(line))
			if line == "" {
				continue
			}
			ch <- line
		}
	}()
	return ch
}
