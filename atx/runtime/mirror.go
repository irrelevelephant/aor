package runtime

import (
	"context"
	"fmt"
	"io"
	"log"
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
		groupedName: fmt.Sprintf("atx-mirror-%s-w%d", machineName, windowIdx),
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

	// Create the grouped session and aim it at the right window. -A so a
	// stale grouped session from a crashed previous mirror is reused
	// instead of erroring.
	setup := fmt.Sprintf(
		"tmux new-session -A -d -t %s -s %s \\; select-window -t %s:%d",
		shellQuote(m.tmuxSession), shellQuote(m.groupedName),
		shellQuote(m.groupedName), m.windowIndex,
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
	session.Stderr = io.Discard

	if err := session.Start("tmux attach -t " + shellQuote(m.groupedName)); err != nil {
		session.Close()
		cancel()
		m.killGrouped()
		return fmt.Errorf("attach: %w", err)
	}

	m.sshMu.Lock()
	m.sshSession = session
	m.sshMu.Unlock()

	go m.readLoop(ctx, session, stdout)
	return nil
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
	session.Stderr = io.Discard
	return session.Run(cmd)
}
