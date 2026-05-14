package cmd

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"

	"aor/ata/model"
)

func outputJSON(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// GitToplevel returns the git rev-parse --show-toplevel path, or "" if not in a git repo.
func GitToplevel() string {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// rawWorkingDir returns the git toplevel or cwd.
// Used for created_in to record where the task was actually created.
func rawWorkingDir() string {
	toplevel := GitToplevel()
	if toplevel != "" {
		return toplevel
	}
	cwd, _ := os.Getwd()
	return cwd
}

// flagWasSet returns true if a flag was explicitly provided on the command line.
func flagWasSet(fs *flag.FlagSet, name string) bool {
	found := false
	fs.Visit(func(f *flag.Flag) {
		if f.Name == name {
			found = true
		}
	})
	return found
}

// readFileString reads a file and returns its contents as a string.
func readFileString(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// resolveBody returns the body string sourced from --body, --body-file, or
// (when allowStdin is true and stdin is piped) stdin. The bool reports whether
// any source set the body. Mutual-exclusion of --body and --body-file is
// enforced.
func resolveBody(fs *flag.FlagSet, body, bodyFile *string, allowStdin bool) (string, bool, error) {
	bodySet := flagWasSet(fs, "body")
	fileSet := flagWasSet(fs, "body-file")
	if bodySet && fileSet {
		return "", false, fmt.Errorf("--body and --body-file are mutually exclusive")
	}
	if fileSet {
		s, err := readFileString(*bodyFile)
		if err != nil {
			return "", false, fmt.Errorf("read body file: %w", err)
		}
		return s, true, nil
	}
	if bodySet {
		return *body, true, nil
	}
	if allowStdin {
		data, err := ReadStdinIfReady()
		if err != nil {
			return "", false, err
		}
		if len(data) > 0 {
			return string(data), true, nil
		}
	}
	return "", false, nil
}

// ReadStdinIfReady returns stdin's contents when a read would not block —
// data is available, or the peer has closed (EOF). Returns (nil, nil) when
// stdin is a TTY with no pending input or an open pipe with no producer.
//
// We poll rather than just checking for a TTY because agent shells (e.g.
// Claude Code's Bash tool) often run subprocesses with an open pipe as
// stdin — neither a TTY nor a closed/written-to fd. A naive io.ReadAll
// would block forever waiting for an EOF that never comes.
func ReadStdinIfReady() ([]byte, error) {
	if !StdinHasInput() {
		return nil, nil
	}
	data, err := io.ReadAll(os.Stdin)
	if err != nil {
		return nil, fmt.Errorf("read stdin: %w", err)
	}
	return data, nil
}

func readIDsFromStdin() ([]string, error) {
	data, err := ReadStdinIfReady()
	if err != nil {
		return nil, err
	}
	return strings.Fields(string(data)), nil
}

// resolveIDsFlag returns IDs from --ids when set, else from stdin. Used by
// close and tag where positional args carry non-ID meaning, so the remote
// proxy must pass a flag rather than appending positional.
func resolveIDsFlag(fs *flag.FlagSet, ids *string) ([]string, error) {
	if flagWasSet(fs, "ids") {
		return strings.Fields(*ids), nil
	}
	return readIDsFromStdin()
}

// collectTasks applies fn to each ID, returning the produced tasks. Stops at
// the first error.
func collectTasks(ids []string, fn func(string) (*model.Task, error)) ([]model.Task, error) {
	out := make([]model.Task, 0, len(ids))
	for _, id := range ids {
		t, err := fn(id)
		if err != nil {
			return nil, err
		}
		out = append(out, *t)
	}
	return out, nil
}

// emitTasks prints "<verb> id: title" per task, or JSON. A single task emits
// as a JSON object; multiple tasks emit as an array.
func emitTasks(verb string, tasks []model.Task, jsonOut bool) error {
	if jsonOut {
		if len(tasks) == 1 {
			return outputJSON(tasks[0])
		}
		return outputJSON(tasks)
	}
	for _, t := range tasks {
		fmt.Printf("%s %s: %s\n", verb, t.ID, t.Title)
	}
	return nil
}

// isStdinTTY reports whether stdin is connected to a terminal (vs. piped).
func isStdinTTY() bool {
	fi, err := os.Stdin.Stat()
	if err != nil {
		return true
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

func exitUsage(msg string) error {
	return fmt.Errorf("%s\nRun 'ata <command> --help' for usage", msg)
}

// promptConfirm prints prompt and reads a line from stdin. Returns true if the
// trimmed, lowercased reply equals expect.
func promptConfirm(prompt, expect string) bool {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	answer, _ := reader.ReadString('\n')
	return strings.TrimSpace(strings.ToLower(answer)) == expect
}

// splitFlagsAndPositional separates flag arguments from positional arguments.
// flagsWithValue is a set of flag names (without --) that take a value argument.
func splitFlagsAndPositional(args []string, flagsWithValue map[string]bool) (flags, positional []string) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if arg == "--" {
			positional = append(positional, args[i+1:]...)
			break
		}
		if strings.HasPrefix(arg, "--") || strings.HasPrefix(arg, "-") {
			name := strings.TrimLeft(arg, "-")
			// Handle --flag=value
			if idx := strings.Index(name, "="); idx >= 0 {
				flags = append(flags, arg)
				continue
			}
			flags = append(flags, arg)
			// If the flag takes a value, consume the next arg too.
			if flagsWithValue[name] && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else {
			positional = append(positional, arg)
		}
	}
	return
}
