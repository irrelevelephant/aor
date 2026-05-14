package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"
)

const (
	mirrorIdleTimeout    = 5 * time.Minute
	mirrorBackfillBytes  = 256 * 1024 // capture-pane history cap
	mirrorRecentBufBytes = 64 * 1024  // bytes held in-memory for late subscribers
)

// Mirror streams one tmux pane's live output to in-process subscribers via
// a remote FIFO: tmux pipe-pane writes to it on the remote, a long-lived
// `cat $FIFO` SSH session reads it and ships bytes back here.
type Mirror struct {
	machineName string
	windowIndex int
	tmuxTarget  string // e.g. "0:2"

	client *ssh.Client
	cancel context.CancelFunc
	done   chan struct{}

	mu          sync.Mutex
	subscribers map[chan []byte]struct{}
	backfill    []byte
	recent      []byte // ring of recent bytes for late-arrivers (oldest dropped)
	idleTimer   *time.Timer
	fifoPath    string
	dead        bool // set when readLoop exits; AcquireMirror creates a fresh one
}

// Dead reports whether the mirror's read loop has exited (SSH disconnect,
// pipe-pane gone, etc). Callers should discard a dead mirror and acquire a
// fresh one.
func (m *Mirror) Dead() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.dead
}

func newMirror(machineName string, windowIdx int, target string, client *ssh.Client) *Mirror {
	return &Mirror{
		machineName: machineName,
		windowIndex: windowIdx,
		tmuxTarget:  target,
		client:      client,
		subscribers: make(map[chan []byte]struct{}),
	}
}

// Start opens the FIFO + pipe-pane plumbing and begins streaming. Returns
// once the initial capture-pane backfill is in memory; the live stream
// continues in the background until Stop is called or ctx is cancelled.
func (m *Mirror) Start(parent context.Context) error {
	ctx, cancel := context.WithCancel(parent)
	m.cancel = cancel
	m.done = make(chan struct{})

	// 1. Capture-pane backfill (rendered state, with SGR).
	altScreen, err := m.captureAltScreen()
	if err != nil {
		cancel()
		return fmt.Errorf("alt-screen probe: %w", err)
	}
	backfill, err := m.capturePaneBackfill()
	if err != nil {
		cancel()
		return fmt.Errorf("capture-pane: %w", err)
	}
	if altScreen {
		// Prepend the alt-screen enter code so xterm.js renders capture-pane
		// (rendered alt-screen contents) on the right surface.
		backfill = append([]byte("\x1b[?1049h"), backfill...)
	}
	m.backfill = backfill

	// 2. Open the FIFO reader and the pipe-pane registrar, then start the
	// reader goroutine that forwards bytes to subscribers.
	fifoPath := fmt.Sprintf("/tmp/atx-%s-w%d-%d.fifo", m.machineName, m.windowIndex, time.Now().UnixNano())

	// Create the FIFO synchronously so we know it exists before either the
	// reader cat or the tmux pipe-pane shell-command tries to open it.
	if err := m.runShortCommand("mkfifo " + shellQuote(fifoPath)); err != nil {
		cancel()
		return fmt.Errorf("mkfifo: %w", err)
	}

	readerSession, err := m.client.NewSession()
	if err != nil {
		m.runShortCommand("rm -f " + shellQuote(fifoPath))
		cancel()
		return fmt.Errorf("reader session: %w", err)
	}
	readerOut, err := readerSession.StdoutPipe()
	if err != nil {
		readerSession.Close()
		m.runShortCommand("rm -f " + shellQuote(fifoPath))
		cancel()
		return fmt.Errorf("reader stdout: %w", err)
	}
	readerSession.Stderr = io.Discard

	if err := readerSession.Start("exec cat " + shellQuote(fifoPath)); err != nil {
		readerSession.Close()
		m.runShortCommand("rm -f " + shellQuote(fifoPath))
		cancel()
		return fmt.Errorf("start reader: %w", err)
	}

	// pipe-pane WITHOUT -o: replace any existing pipe on the pane. The -o
	// flag means "only open if no pipe exists" — it would silently no-op
	// against a stale registration from a previous (cleanly-torn-down or
	// not) mirror.
	if err := m.runShortCommand(fmt.Sprintf(
		"tmux pipe-pane -t %s 'cat > %s'",
		shellQuote(m.tmuxTarget), shellQuote(fifoPath),
	)); err != nil {
		readerSession.Close()
		m.runShortCommand("rm -f " + shellQuote(fifoPath))
		cancel()
		return fmt.Errorf("register pipe-pane: %w", err)
	}

	m.fifoPath = fifoPath

	go m.readLoop(ctx, readerSession, readerOut, fifoPath)
	return nil
}

func (m *Mirror) readLoop(ctx context.Context, session *ssh.Session, stdout io.Reader, fifoPath string) {
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
		// Best-effort cleanup: stop tmux piping and remove the FIFO file.
		// Both may fail if the SSH client is already torn down, which is
		// fine — a stale FIFO in /tmp is harmless until reboot.
		_ = m.runShortCommand(fmt.Sprintf("tmux pipe-pane -t %s", shellQuote(m.tmuxTarget)))
		_ = m.runShortCommand("rm -f " + shellQuote(fifoPath))
	}()

	// Tear the reader session down if the parent ctx is cancelled.
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
	cp := append([]byte(nil), data...)
	m.mu.Lock()
	m.appendRecent(cp)
	subs := make([]chan []byte, 0, len(m.subscribers))
	for ch := range m.subscribers {
		subs = append(subs, ch)
	}
	m.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- cp:
		default:
			// Subscriber is slow; drop. Their xterm.js will be missing data
			// but the alternative is unbounded buffering server-side.
		}
	}
}

func (m *Mirror) appendRecent(data []byte) {
	m.recent = append(m.recent, data...)
	if over := len(m.recent) - mirrorRecentBufBytes; over > 0 {
		m.recent = m.recent[over:]
	}
}

// Subscribe attaches a new viewer. The returned channel receives the
// backfill + the recent in-memory buffer immediately, then live output.
// Caller must Unsubscribe when done.
func (m *Mirror) Subscribe() chan []byte {
	ch := make(chan []byte, 64)

	m.mu.Lock()
	defer m.mu.Unlock()
	if m.dead {
		close(ch)
		return ch
	}
	if m.idleTimer != nil {
		m.idleTimer.Stop()
		m.idleTimer = nil
	}
	m.subscribers[ch] = struct{}{}

	// Replay state to the new subscriber: backfill (full alt-screen snapshot
	// or capture of normal screen) + recent live bytes. Channel cap is 64
	// so this single send never blocks while we still hold mu.
	initial := append(append([]byte(nil), m.backfill...), m.recent...)
	if len(initial) > 0 {
		ch <- initial
	}

	return ch
}

// Unsubscribe removes ch. When the last subscriber leaves, the mirror
// stays alive for mirrorIdleTimeout before tearing down — onIdle is
// called when the timer fires so the owner can clean up.
func (m *Mirror) Unsubscribe(ch chan []byte, onIdle func()) {
	m.mu.Lock()
	if _, ok := m.subscribers[ch]; !ok {
		m.mu.Unlock()
		return
	}
	delete(m.subscribers, ch)
	close(ch)
	startTimer := len(m.subscribers) == 0 && m.idleTimer == nil && !m.dead
	if startTimer {
		m.idleTimer = time.AfterFunc(mirrorIdleTimeout, onIdle)
	}
	m.mu.Unlock()
}

// Stop tears down the mirror immediately, regardless of viewer count.
func (m *Mirror) Stop() {
	m.mu.Lock()
	if m.idleTimer != nil {
		m.idleTimer.Stop()
		m.idleTimer = nil
	}
	subs := m.subscribers
	m.subscribers = nil
	m.mu.Unlock()
	for ch := range subs {
		close(ch)
	}
	if m.cancel != nil {
		m.cancel()
	}
	if m.done != nil {
		<-m.done
	}
}

// runShortCommand runs a one-shot command on a fresh SSH session and
// returns once it exits.
func (m *Mirror) runShortCommand(cmd string) error {
	session, err := m.client.NewSession()
	if err != nil {
		return err
	}
	defer session.Close()
	session.Stderr = io.Discard
	return session.Run(cmd)
}

func (m *Mirror) captureAltScreen() (bool, error) {
	session, err := m.client.NewSession()
	if err != nil {
		return false, err
	}
	defer session.Close()
	var out bytes.Buffer
	session.Stdout = &out
	session.Stderr = io.Discard
	if err := session.Run(fmt.Sprintf("tmux display-message -p -t %s '#{alternate_on}'", shellQuote(m.tmuxTarget))); err != nil {
		return false, err
	}
	return strings.TrimSpace(out.String()) == "1", nil
}

func (m *Mirror) capturePaneBackfill() ([]byte, error) {
	session, err := m.client.NewSession()
	if err != nil {
		return nil, err
	}
	defer session.Close()
	var out bytes.Buffer
	session.Stdout = &out
	session.Stderr = io.Discard
	// -p stdout, -e include escapes, -J preserve trailing spaces+joins wrapped lines.
	// -S - means start from the oldest visible line (no scrollback dump, just current view).
	if err := session.Run(fmt.Sprintf("tmux capture-pane -p -e -J -t %s", shellQuote(m.tmuxTarget))); err != nil {
		return nil, err
	}
	data := out.Bytes()
	if len(data) > mirrorBackfillBytes {
		data = data[len(data)-mirrorBackfillBytes:]
	}
	return data, nil
}
