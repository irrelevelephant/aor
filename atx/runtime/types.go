// Package runtime owns per-machine SSH + tmux control-mode connections and
// publishes the live machine/window state consumed by the web layer.
package runtime

import "time"

// Window mirrors a tmux window: index in its session, tmux window id (@N),
// current name, and a coarse last-activity timestamp updated on rename/add.
type Window struct {
	Index        int
	ID           string
	Name         string
	LastActivity time.Time
}

// MachineState is the snapshot the web layer renders for one machine.
type MachineState struct {
	Name        string
	Display     string
	Color       string
	Online      bool
	LastError   string
	Windows     []Window
	LastChange  time.Time
}
