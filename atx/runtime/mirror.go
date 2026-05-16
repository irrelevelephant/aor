package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"

	"golang.org/x/crypto/ssh"
)

const (
	mirrorRecentBufBytes = 64 * 1024
	defaultCols          = 80
	defaultRows          = 24
)

// Mirror streams one tmux pane's live output to in-process subscribers by
// attaching as a real tmux client over SSH. The SSH session's PTY size
// dictates this client's contribution to tmux's smallest-client window
// resize negotiation, so the pane follows the browser's geometry.
//
// To avoid stealing the user's "current window" pointer in their main tmux
// session, atx attaches to a per-mirror grouped session that shares windows
// with the main session. Killing the grouped session on teardown removes
// atx's resize contribution and lets the pane snap back to the user's
// mosh-only size.
type Mirror struct {
	machineName string
	windowIndex int
	tmuxSession string
	groupedName string

	client *ssh.Client
	cancel context.CancelFunc
	done   chan struct{}

	sshMu      sync.Mutex
	sshSession *ssh.Session
	sshStdin   io.WriteCloser

	mu          sync.Mutex
	subscribers map[chan []byte]struct{}
	recent      []byte
	dead        bool

	// Suppress dispatch during teardown. `tmux attach` emits
	// `\x1b[?1049l` (leave alt-screen) when its tty closes; without this
	// flag those bytes race through to xterm.js after Stop() is called,
	// putting the browser back on the (empty) normal screen and looking
	// like the terminal blanked.
	stopped atomic.Bool
}

func newMirror(machineName string, windowIdx int, tmuxSession string, client *ssh.Client) *Mirror {
	return &Mirror{
		machineName: machineName,
		windowIndex: windowIdx,
		tmuxSession: tmuxSession,
		groupedName: fmt.Sprintf("atx%d", windowIdx),
		client:      client,
		subscribers: make(map[chan []byte]struct{}),
	}
}

// Dead reports whether the mirror's read loop has exited; AcquireMirror
// should discard a dead mirror and acquire a fresh one.
func (m *Mirror) Dead() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dead
}

// Start creates the grouped tmux session, opens an SSH session with a PTY
// of the requested size, runs `tmux attach`, and begins streaming output.
// The grouped session shares windows with the user's main session but has
// its own "current window" pointer so atx doesn't fight Thomas's mosh
// clients for window selection.
func (m *Mirror) Start(parent context.Context, cols, rows uint32) error {
	if cols == 0 {
		cols = defaultCols
	}
	if rows == 0 {
		rows = defaultRows
	}

	// Scrub any orphan grouped session from a crashed or misconfigured
	// prior mirror — otherwise `new-session` below would either error
	// or silently reuse a session grouped to a different target /
	// pointed at a since-removed window, and every retry would hit the
	// same bad state. Failure here is fine: usually nothing to kill.
	_ = m.runShortCommand("tmux kill-session -t " + shellQuote(m.groupedName))

	// The hook propagates any in-mirror window switch (prefix+n / 0..9 /
	// l typed in the browser terminal) back to the main session, so
	// mosh clients and atx's main-session list-windows refresh stay in
	// sync. run-shell wraps a `tmux select-window` invocation because a
	// bare `select-window -t main:#{window_index}` hook body parses fine
	// into set-hook but never actually moves main's pointer when the
	// hook fires (tmux 3.6a). The run-shell variant expands
	// #{window_index} in the source-session context and re-enters tmux
	// from a shell.
	//
	// Mirror creation also has to move main's current-window pointer
	// (otherwise the post-creation list-windows refresh reports main on
	// the previous window and the active_window push snaps the browser
	// back). The hook-via-run-shell route is async: the refresh's 200ms
	// debounce can fire before the forked shell reconnects to tmux and
	// runs select-window, especially on slow links or when wrapping —
	// LAST→FIRST was the most reliably broken case. So we also drive
	// main's selection synchronously in the same tmux compound below,
	// after the mirror's own select-window. Mirrors are only created
	// via explicit user navigation, so propagating on creation is
	// correct; the hook then covers later in-mirror switches.
	hookBody := shellQuote(fmt.Sprintf(`run-shell "tmux select-window -t %s:#{window_index}"`, m.tmuxSession))
	setup := fmt.Sprintf(
		"tmux new-session -d -t %s -s %s \\; set-hook -t %s session-window-changed %s \\; select-window -t %s:%d \\; select-window -t %s:%d",
		shellQuote(m.tmuxSession), shellQuote(m.groupedName),
		shellQuote(m.groupedName), hookBody,
		shellQuote(m.groupedName), m.windowIndex,
		shellQuote(m.tmuxSession), m.windowIndex,
	)
	if err := m.runShortCommand(setup); err != nil {
		return fmt.Errorf("create grouped session: %w", err)
	}

	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.done = make(chan struct{})

	session, err := m.client.NewSession()
	if err != nil {
		cancel()
		m.killGrouped()
		return fmt.Errorf("ssh session: %w", err)
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          0,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	if err := session.RequestPty("xterm-256color", int(rows), int(cols), modes); err != nil {
		session.Close()
		cancel()
		m.killGrouped()
		return fmt.Errorf("request pty: %w", err)
	}

	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		cancel()
		m.killGrouped()
		return fmt.Errorf("stdout pipe: %w", err)
	}
	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		cancel()
		m.killGrouped()
		return fmt.Errorf("stdin pipe: %w", err)
	}
	session.Stderr = io.Discard

	if err := session.Start("tmux -u attach -t " + shellQuote(m.groupedName)); err != nil {
		session.Close()
		cancel()
		m.killGrouped()
		return fmt.Errorf("attach: %w", err)
	}

	m.sshMu.Lock()
	m.sshSession = session
	m.sshStdin = stdin
	m.sshMu.Unlock()

	go m.readLoop(ctx, session, stdout)
	return nil
}

// SendInput writes user input bytes directly to tmux as the attached
// client. tmux interprets the byte stream at its protocol layer —
// terminal-response sequences (DA, cursor position, etc.) emitted by
// xterm.js back through `term.onData` are consumed by tmux as protocol,
// not forwarded into the pane. That's the difference between this path
// and `tmux send-keys`, which writes literally into the pane and turns
// every DA response into visible garbage.
func (m *Mirror) SendInput(data []byte) error {
	m.sshMu.Lock()
	stdin := m.sshStdin
	m.sshMu.Unlock()
	if stdin == nil {
		return fmt.Errorf("mirror not started")
	}
	_, err := stdin.Write(data)
	return err
}

// Resize updates the SSH PTY size, which propagates to tmux as a SIGWINCH
// and re-runs the smallest-client negotiation for this window.
func (m *Mirror) Resize(cols, rows uint32) error {
	if cols == 0 || rows == 0 {
		return nil
	}
	m.sshMu.Lock()
	session := m.sshSession
	m.sshMu.Unlock()
	if session == nil {
		return nil
	}
	return session.WindowChange(int(rows), int(cols))
}

func (m *Mirror) readLoop(ctx context.Context, session *ssh.Session, stdout io.Reader) {
	defer close(m.done)
	defer session.Close()
	defer func() {
		m.mu.Lock()
		m.dead = true
		subs := m.subscribers
		m.subscribers = nil
		m.mu.Unlock()
		for ch := range subs {
			close(ch)
		}
		m.killGrouped()
	}()

	go func() {
		<-ctx.Done()
		session.Close()
	}()

	buf := make([]byte, 8192)
	for {
		n, err := stdout.Read(buf)
		if n > 0 {
			m.dispatch(buf[:n])
		}
		if err != nil {
			if err != io.EOF && ctx.Err() == nil {
				log.Printf("atx %s/w%d: mirror read: %v", m.machineName, m.windowIndex, err)
			}
			return
		}
	}
}

func (m *Mirror) dispatch(data []byte) {
	if m.stopped.Load() {
		return
	}
	cp := append([]byte(nil), data...)
	m.mu.Lock()
	m.recent = append(m.recent, cp...)
	if over := len(m.recent) - mirrorRecentBufBytes; over > 0 {
		m.recent = m.recent[over:]
	}
	subs := make([]chan []byte, 0, len(m.subscribers))
	for ch := range m.subscribers {
		subs = append(subs, ch)
	}
	m.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- cp:
		default:
			// Slow subscriber; drop. xterm.js will be missing data but
			// we'd rather drop than buffer unbounded server-side.
		}
	}
}

// Subscribe attaches a viewer. The returned channel receives the recent
// in-memory buffer immediately, then live output. The recent buffer doubles
// as a backfill — `tmux attach` itself emits a full repaint on connect, so
// the most recent attach repaint is replayed to late subscribers.
func (m *Mirror) Subscribe() chan []byte {
	ch := make(chan []byte, 64)
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dead {
		close(ch)
		return ch
	}
	m.subscribers[ch] = struct{}{}
	if len(m.recent) > 0 {
		ch <- append([]byte(nil), m.recent...)
	}
	return ch
}

// Unsubscribe removes ch. When the last subscriber leaves, onLast fires —
// callers use it to immediately tear down the mirror so atx's tmux client
// (and its size constraint) goes away.
func (m *Mirror) Unsubscribe(ch chan []byte, onLast func()) {
	m.mu.Lock()
	if _, ok := m.subscribers[ch]; !ok {
		m.mu.Unlock()
		return
	}
	delete(m.subscribers, ch)
	close(ch)
	last := len(m.subscribers) == 0 && !m.dead
	m.mu.Unlock()
	if last && onLast != nil {
		onLast()
	}
}

// Stop tears down the mirror immediately, regardless of viewer count.
func (m *Mirror) Stop() {
	m.stopped.Store(true)
	if m.cancel != nil {
		m.cancel()
	}
	if m.done != nil {
		<-m.done
	}
}

func (m *Mirror) killGrouped() {
	_ = m.runShortCommand("tmux kill-session -t " + shellQuote(m.groupedName))
}

func (m *Mirror) runShortCommand(cmd string) error {
	session, err := m.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	var stderr bytes.Buffer
	session.Stderr = &stderr
	if err := session.Run(cmd); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%w: %s", err, msg)
		}
		return err
	}
	return nil
}

func (m *Mirror) runShortOutput(cmd string) ([]byte, error) {
	session, err := m.client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var stderr bytes.Buffer
	session.Stderr = &stderr
	out, err := session.Output(cmd)
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return nil, fmt.Errorf("%w: %s", err, msg)
		}
		return nil, err
	}
	return out, nil
}

// runShortStdin runs cmd with `in` piped to its stdin. Used for paste, so the
// raw bytes are never interpolated into a shell string (which would be a
// quote-escaping disaster for arbitrary clipboard content).
func (m *Mirror) runShortStdin(cmd string, in []byte) error {
	session, err := m.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	var stderr bytes.Buffer
	session.Stderr = &stderr
	stdin, err := session.StdinPipe()
	if err != nil {
		return err
	}
	if err := session.Start(cmd); err != nil {
		_ = stdin.Close()
		return err
	}
	writeErr := func() error {
		defer stdin.Close()
		if _, err := stdin.Write(in); err != nil {
			return err
		}
		return nil
	}()
	waitErr := session.Wait()
	if writeErr != nil {
		return writeErr
	}
	if waitErr != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%w: %s", waitErr, msg)
		}
		return waitErr
	}
	return nil
}

// CopyState mirrors tmux's copy-mode cursor state for one pane. The JSON tags
// match what the browser expects on the wire.
type CopyState struct {
	InMode bool `json:"inMode"`
	Row    int  `json:"row"`
	Col    int  `json:"col"`
	Width  int  `json:"width"`
	Height int  `json:"height"`
}

// stateFmt is the tmux format string we pass to `display-message -p` so we can
// parse the result back with parseCopyState. The fields are CSV-ordered:
//
//	pane_in_mode , copy_cursor_x , copy_cursor_y , pane_width , pane_height
//
// `copy_cursor_x` / `copy_cursor_y` are empty when the pane is not in copy
// mode, which parseCopyState handles.
const stateFmt = `'#{pane_in_mode},#{copy_cursor_x},#{copy_cursor_y},#{pane_width},#{pane_height}'`

func (m *Mirror) target() string {
	return fmt.Sprintf("%s:%d", m.groupedName, m.windowIndex)
}

func parseCopyState(out []byte) (CopyState, error) {
	s := strings.TrimSpace(string(out))
	parts := strings.Split(s, ",")
	if len(parts) != 5 {
		return CopyState{}, fmt.Errorf("bad copy state: %q", s)
	}
	atoi := func(p string) int {
		if p == "" {
			return 0
		}
		n, _ := strconv.Atoi(p)
		return n
	}
	return CopyState{
		InMode: parts[0] == "1",
		Col:    atoi(parts[1]),
		Row:    atoi(parts[2]),
		Width:  atoi(parts[3]),
		Height: atoi(parts[4]),
	}, nil
}

// copyActions is the allowlist of `send-keys -X` action names we accept from
// the client. tmux action names are mode-agnostic (work under both vi and
// emacs copy-mode key tables), so the same names cover every user.
var copyActions = map[string]struct{}{
	"cursor-up": {}, "cursor-down": {}, "cursor-left": {}, "cursor-right": {},
	"next-word": {}, "previous-word": {}, "next-word-end": {},
	"page-up": {}, "page-down": {},
	"history-top": {}, "history-bottom": {},
	"top-line": {}, "bottom-line": {},
	"scroll-up": {}, "scroll-down": {},
	"begin-selection": {}, "clear-selection": {},
	"cancel": {},
}

// CopyEnter puts the pane into copy mode and returns the initial cursor /
// pane geometry. Safe to call when the pane is already in copy mode (tmux
// no-ops).
func (m *Mirror) CopyEnter() (CopyState, error) {
	target := shellQuote(m.target())
	cmd := fmt.Sprintf(
		"tmux copy-mode -t %s \\; display-message -p -t %s %s",
		target, target, stateFmt,
	)
	out, err := m.runShortOutput(cmd)
	if err != nil {
		return CopyState{}, err
	}
	return parseCopyState(out)
}

// CopyMove drives the copy-mode cursor from (fromRow, fromCol) — what the
// client last heard — to (toRow, toCol). tmux's copy-mode has no absolute
// positioning, so deltas are computed in Go and batched as one chained
// `send-keys -X` invocation.
func (m *Mirror) CopyMove(fromRow, fromCol, toRow, toCol int) (CopyState, error) {
	target := shellQuote(m.target())
	parts := []string{}
	if dr := toRow - fromRow; dr < 0 {
		parts = append(parts, fmt.Sprintf("send-keys -t %s -X -N %d cursor-up", target, -dr))
	} else if dr > 0 {
		parts = append(parts, fmt.Sprintf("send-keys -t %s -X -N %d cursor-down", target, dr))
	}
	if dc := toCol - fromCol; dc < 0 {
		parts = append(parts, fmt.Sprintf("send-keys -t %s -X -N %d cursor-left", target, -dc))
	} else if dc > 0 {
		parts = append(parts, fmt.Sprintf("send-keys -t %s -X -N %d cursor-right", target, dc))
	}
	parts = append(parts, fmt.Sprintf("display-message -p -t %s %s", target, stateFmt))
	cmd := "tmux " + strings.Join(parts, " \\; ")
	out, err := m.runShortOutput(cmd)
	if err != nil {
		return CopyState{}, err
	}
	return parseCopyState(out)
}

// CopyAction sends one `send-keys -X` action `count` times. `name` must be
// in the copyActions allowlist (no arbitrary action injection from the
// browser). count <= 0 is normalized to 1.
func (m *Mirror) CopyAction(name string, count int) (CopyState, error) {
	if _, ok := copyActions[name]; !ok {
		return CopyState{}, fmt.Errorf("disallowed copy action: %q", name)
	}
	if count <= 0 {
		count = 1
	}
	target := shellQuote(m.target())
	cmd := fmt.Sprintf(
		"tmux send-keys -t %s -X -N %d %s \\; display-message -p -t %s %s",
		target, count, name, target, stateFmt,
	)
	out, err := m.runShortOutput(cmd)
	if err != nil {
		return CopyState{}, err
	}
	return parseCopyState(out)
}

// CopyYank yanks the current copy-mode selection into tmux's top paste buffer
// and exits copy mode. Returns the yanked text so the WS handler can also
// drop it in the OS clipboard. Goes to the default (top) buffer rather than a
// named one because tmux's `copy-selection-and-cancel` action doesn't accept
// a buffer name; the user's prior top buffer is pushed to position 1 (and is
// recoverable via `tmux choose-buffer`).
func (m *Mirror) CopyYank() (string, error) {
	target := shellQuote(m.target())
	cmd := fmt.Sprintf(
		"tmux send-keys -t %s -X copy-selection-and-cancel \\; show-buffer",
		target,
	)
	out, err := m.runShortOutput(cmd)
	if err != nil {
		return "", err
	}
	// `show-buffer` doesn't append a trailing newline if the buffer doesn't
	// end with one, but SSH session output may still have CRLF or a final
	// LF from the remote tty. Trim one trailing LF (and accompanying CR) so
	// single-line selections don't paste with a stray newline.
	s := string(out)
	if strings.HasSuffix(s, "\r\n") {
		s = s[:len(s)-2]
	} else if strings.HasSuffix(s, "\n") {
		s = s[:len(s)-1]
	}
	return s, nil
}

// CopyResync clears any stale copy-mode state on the grouped session and
// reports the current pane geometry. Called when a viewer first attaches so
// a prior tab's abandoned copy mode doesn't leak across reconnects.
func (m *Mirror) CopyResync() (CopyState, error) {
	target := shellQuote(m.target())
	// `send-keys -X cancel` errors out if the pane isn't in a mode. Run it
	// in a sub-`if` via tmux's `if-shell -F` would be heavy; the cheaper
	// route is to query state first and only cancel if needed.
	out, err := m.runShortOutput(fmt.Sprintf("tmux display-message -p -t %s %s", target, stateFmt))
	if err != nil {
		return CopyState{}, err
	}
	st, err := parseCopyState(out)
	if err != nil {
		return CopyState{}, err
	}
	if st.InMode {
		_ = m.runShortCommand(fmt.Sprintf("tmux send-keys -t %s -X cancel", target))
		st.InMode = false
		st.Row, st.Col = 0, 0
	}
	return st, nil
}

// PasteClipboard pipes `text` into a named tmux buffer and pastes it into
// the active pane with bracketed paste enabled (so multi-line input is
// delivered atomically to apps like vim).
func (m *Mirror) PasteClipboard(text string) error {
	target := shellQuote(m.target())
	cmd := fmt.Sprintf(
		"tmux load-buffer -b atx-paste - \\; paste-buffer -p -d -b atx-paste -t %s",
		target,
	)
	return m.runShortStdin(cmd, []byte(text))
}

// WindowAction selects which window-level tmux command WindowCommand runs.
// The allowlist gates user-supplied action strings.
type WindowAction string

const (
	WindowActionNew      WindowAction = "new"
	WindowActionRename   WindowAction = "rename"
	WindowActionClose    WindowAction = "close"
	WindowActionSwapPrev WindowAction = "swap-prev"
	WindowActionSwapNext WindowAction = "swap-next"
	WindowActionRenumber WindowAction = "renumber"
)

// WindowCommandResult reports the user's main-session active window index
// after the command ran, so the browser can navigate to it.
type WindowCommandResult struct {
	ActiveWindow int `json:"activeWindow"`
}

// WindowCommand runs one allowlisted window-level tmux command against the
// user's main session and returns the post-action active window index so
// the browser can update its view if the focused window changed (move,
// renumber) or moved to a freshly-created/neighbouring window
// (new, close). `windows` is the most recent known window list — used to
// compute prev/next neighbours for swap-window (wrap at edges).
func (m *Mirror) WindowCommand(action WindowAction, newName string, windows []Window) (WindowCommandResult, error) {
	sess := shellQuote(m.tmuxSession)
	target := fmt.Sprintf("%s:%d", sess, m.windowIndex)

	var cmd string
	switch action {
	case WindowActionNew:
		// Trailing colon forces tmux to parse `sess` as a session name; a
		// bare `-t SESS` is ambiguous when the session name is a numeric
		// string (e.g. tmux_session = "0") and tmux falls back to treating
		// it as a window index, which fails with "index 0 in use".
		cmd = fmt.Sprintf("tmux new-window -t %s: -c '#{pane_current_path}'", sess)
	case WindowActionRename:
		name := strings.TrimSpace(newName)
		if name == "" {
			return WindowCommandResult{}, fmt.Errorf("rename: empty name")
		}
		cmd = fmt.Sprintf("tmux rename-window -t %s %s", target, shellQuote(name))
	case WindowActionClose:
		cmd = fmt.Sprintf("tmux kill-window -t %s", target)
	case WindowActionSwapPrev, WindowActionSwapNext:
		if len(windows) < 2 {
			return WindowCommandResult{}, fmt.Errorf("only one window")
		}
		pos := -1
		var ourID string
		for i, w := range windows {
			if w.Index == m.windowIndex {
				pos = i
				ourID = w.ID
				break
			}
		}
		if pos < 0 {
			return WindowCommandResult{}, fmt.Errorf("current window not in list")
		}
		if ourID == "" {
			return WindowCommandResult{}, fmt.Errorf("current window @id missing")
		}
		var neighbor int
		if action == WindowActionSwapPrev {
			neighbor = windows[(pos-1+len(windows))%len(windows)].Index
		} else {
			neighbor = windows[(pos+1)%len(windows)].Index
		}
		if neighbor == m.windowIndex {
			return WindowCommandResult{ActiveWindow: m.windowIndex}, nil
		}
		// -d keeps tmux's current-window pointer where it is during the
		// swap; then we explicitly select-window by @id so the user's
		// window stays current at its new index, instead of leaving the
		// neighbouring window (which now sits at our old index) focused.
		// Targeting select-window by @id is the only way to follow the
		// window across the index reshuffle — using the new index would
		// race with tmux's internal winlink update.
		id := shellQuote(ourID)
		cmd = fmt.Sprintf(
			"tmux swap-window -d -s %s -t %s:%d \\; select-window -t %s",
			target, sess, neighbor, id,
		)
	case WindowActionRenumber:
		cmd = fmt.Sprintf("tmux move-window -r -t %s:", sess)
	default:
		return WindowCommandResult{}, fmt.Errorf("unknown action: %s", action)
	}

	if err := m.runShortCommand(cmd); err != nil {
		return WindowCommandResult{}, err
	}

	// Query the main session's current window index. After new/close/swap
	// the focused window pointer has moved; after renumber it stays on
	// the same window but with a new index; after rename it's unchanged.
	// Trailing colon forces session-target interpretation (numeric session
	// names like "0" otherwise resolve ambiguously when a window also has
	// that index, and tmux can return the window's index instead of the
	// session's active window's index).
	out, err := m.runShortOutput(fmt.Sprintf("tmux display-message -p -t %s: '#{window_index}'", sess))
	if err != nil {
		return WindowCommandResult{}, err
	}
	idx, err := strconv.Atoi(strings.TrimSpace(string(out)))
	if err != nil {
		return WindowCommandResult{}, fmt.Errorf("parse window_index: %w", err)
	}
	return WindowCommandResult{ActiveWindow: idx}, nil
}
