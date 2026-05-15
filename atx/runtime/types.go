// Package runtime owns per-machine SSH + tmux control-mode connections and
// publishes the live machine/window state consumed by the web layer.
package runtime

import "time"

// Window mirrors a tmux window: index in its session, tmux window id (@N),
// and current name.
type Window struct {
	Index int
	ID    string
	Name  string
}

// MachineState is the snapshot the web layer renders for one machine.
// ActiveWindow is the index of the window the underlying tmux session is
// currently focused on (i.e. what a non-atx client like mosh would show).
// -1 means "unknown" — used before the first list-windows refresh and for
// sessions with no windows, so 0 stays a valid window index (tmux's
// base-index can legitimately be 0).
type MachineState struct {
	Name         string
	Display      string
	Color        string
	Online       bool
	LastError    string
	Windows      []Window
	ActiveWindow int
	LastChange   time.Time
}
