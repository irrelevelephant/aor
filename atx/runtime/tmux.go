package runtime

import (
	"bufio"
	"io"
	"strconv"
	"strings"
)

// tmuxEvent is one async notification from `tmux -CC`. The runtime only
// consumes structural events (window-add / -close / -renamed) plus exit;
// the parser drops everything else (including %output and %begin/%end
// command-response blocks) to keep the channel quiet.
type tmuxEvent struct {
	Type string
	Args []string
}

// parseTmuxCC reads tmux -CC output line-by-line and yields events to ch.
// Closes ch when r is exhausted or returns an error.
func parseTmuxCC(r io.Reader, ch chan<- tmuxEvent) error {
	defer close(ch)

	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)

	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "%") {
			continue
		}

		typ, args := splitFirst(strings.TrimPrefix(line, "%"))
		switch typ {
		case "window-add", "window-close", "window-renamed",
			"session-window-changed", "unlinked-window-add",
			"unlinked-window-close", "unlinked-window-renamed",
			"exit":
			ch <- tmuxEvent{Type: typ, Args: args}
		}
	}

	if err := sc.Err(); err != nil && err != io.EOF {
		return err
	}
	return nil
}

// splitFirst returns ("window-add", ["@5"]) for "window-add @5".
func splitFirst(s string) (string, []string) {
	parts := strings.Fields(s)
	if len(parts) == 0 {
		return "", nil
	}
	return parts[0], parts[1:]
}

// parseWindowListLine parses one row of:
//   list-windows -F '#{window_index} #{window_id} #{window_name}'
// e.g. "2 @137 build". Window names can contain spaces, so split on the first
// two whitespace runs only.
func parseWindowListLine(line string) (Window, bool) {
	line = strings.TrimSpace(line)
	if line == "" {
		return Window{}, false
	}
	idx, rest, ok := splitFirstField(line)
	if !ok {
		return Window{}, false
	}
	id, name, _ := splitFirstField(rest)
	i, err := strconv.Atoi(idx)
	if err != nil {
		return Window{}, false
	}
	return Window{Index: i, ID: id, Name: name}, true
}

// splitFirstField returns (first-whitespace-token, rest, ok).
func splitFirstField(s string) (string, string, bool) {
	s = strings.TrimLeft(s, " \t")
	i := strings.IndexAny(s, " \t")
	if i < 0 {
		if s == "" {
			return "", "", false
		}
		return s, "", true
	}
	return s[:i], strings.TrimLeft(s[i:], " \t"), true
}
