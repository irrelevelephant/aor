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
	if allowStdin && !IsStdinTTY() {
		data, err := io.ReadAll(os.Stdin)
		if err != nil {
			return "", false, fmt.Errorf("read stdin: %w", err)
		}
		if len(data) == 0 {
			return "", false, nil
		}
		return string(data), true, nil
	}
	return "", false, nil
}

// IsStdinTTY reports whether stdin is connected to a terminal (vs. piped).
func IsStdinTTY() bool {
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
