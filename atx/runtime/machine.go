package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"sort"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/ssh"

	"aor/atx/config"
)

const (
	dialTimeout         = 10 * time.Second
	minBackoff          = 1 * time.Second
	maxBackoff          = 30 * time.Second
	listWindowsTimeout  = 5 * time.Second
	listWindowsDebounce = 200 * time.Millisecond
)

// Machine owns one Tailnet host's SSH + tmux control-mode lifecycle. The
// run loop reconnects forever (until the context is cancelled), updating
// the published State on every change.
type Machine struct {
	cfg    config.Machine
	notify func(machine string)

	mu    sync.RWMutex
	state MachineState

	// connState is everything tied to the current SSH connection — mirrors
	// and the input channel die together with the SSH client on reconnect.
	// Held under connMu, separate from the state RWMutex above.
	connMu  sync.Mutex
	conn    *machineConn
}

type machineConn struct {
	client  *ssh.Client
	input   *inputChannel
	mirrors map[int]*Mirror // window index → mirror
}

func newMachine(c config.Machine, notify func(string)) *Machine {
	return &Machine{
		cfg:    c,
		notify: notify,
		state: MachineState{
			Name:    c.Name,
			Display: c.Display,
			Color:   c.Color,
		},
	}
}

func (m *Machine) setConn(c *machineConn) {
	m.connMu.Lock()
	m.conn = c
	m.connMu.Unlock()
}

func (m *Machine) tearDownConn() {
	m.connMu.Lock()
	c := m.conn
	m.conn = nil
	m.connMu.Unlock()
	if c == nil {
		return
	}
	for _, mir := range c.mirrors {
		mir.Stop()
	}
}

// AcquireMirror returns (and lazily starts) a mirror for the given window
// index. The caller subscribes to receive output and must call
// ReleaseMirror with the same channel when done.
func (m *Machine) AcquireMirror(ctx context.Context, windowIdx int) (*Mirror, chan []byte, error) {
	target := fmt.Sprintf("%s:%d", m.cfg.TmuxSession, windowIdx)

	m.connMu.Lock()
	conn := m.conn
	if conn == nil {
		m.connMu.Unlock()
		return nil, nil, fmt.Errorf("machine %s offline", m.cfg.Name)
	}
	mir, ok := conn.mirrors[windowIdx]
	if ok && mir.Dead() {
		delete(conn.mirrors, windowIdx)
		ok = false
	}
	if !ok {
		mir = newMirror(m.cfg.Name, windowIdx, target, conn.client)
		conn.mirrors[windowIdx] = mir
	}
	m.connMu.Unlock()

	if !ok {
		if err := mir.Start(ctx); err != nil {
			m.connMu.Lock()
			if m.conn == conn {
				delete(conn.mirrors, windowIdx)
			}
			m.connMu.Unlock()
			return nil, nil, err
		}
	}
	return mir, mir.Subscribe(), nil
}

// ReleaseMirror unsubscribes ch from the window's mirror. When the last
// subscriber for a window leaves, the mirror sits idle for 5 minutes
// before tearing down.
func (m *Machine) ReleaseMirror(windowIdx int, ch chan []byte) {
	m.connMu.Lock()
	conn := m.conn
	m.connMu.Unlock()
	if conn == nil {
		return
	}
	mir, ok := conn.mirrors[windowIdx]
	if !ok {
		return
	}
	mir.Unsubscribe(ch, func() {
		m.connMu.Lock()
		defer m.connMu.Unlock()
		if m.conn != conn {
			return // reconnected; the new conn owns its own mirrors
		}
		if cur, ok := conn.mirrors[windowIdx]; ok && cur == mir {
			cur.Stop()
			delete(conn.mirrors, windowIdx)
		}
	})
}

// SendKeys forwards bytes verbatim to the given window via tmux send-keys.
func (m *Machine) SendKeys(windowIdx int, data []byte) error {
	m.connMu.Lock()
	conn := m.conn
	m.connMu.Unlock()
	if conn == nil {
		return fmt.Errorf("machine %s offline", m.cfg.Name)
	}
	target := fmt.Sprintf("%s:%d", m.cfg.TmuxSession, windowIdx)
	return conn.input.SendKeys(target, data)
}

// State returns the latest snapshot. Windows is a copy.
func (m *Machine) State() MachineState {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := m.state
	s.Windows = append([]Window(nil), m.state.Windows...)
	return s
}

func (m *Machine) updateState(fn func(*MachineState)) {
	m.mu.Lock()
	fn(&m.state)
	m.state.LastChange = time.Now()
	m.mu.Unlock()
	if m.notify != nil {
		m.notify(m.cfg.Name)
	}
}

// run reconnects forever with exponential backoff until ctx is cancelled.
func (m *Machine) run(ctx context.Context) {
	backoff := minBackoff
	for {
		if ctx.Err() != nil {
			return
		}

		err := m.runOnce(ctx)
		m.updateState(func(s *MachineState) {
			s.Online = false
			if err != nil && ctx.Err() == nil {
				s.LastError = err.Error()
			}
		})
		if ctx.Err() != nil {
			return
		}

		log.Printf("atx %s: disconnected (%v); retrying in %s", m.cfg.Name, err, backoff)

		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
		}
		backoff *= 2
		if backoff > maxBackoff {
			backoff = maxBackoff
		}
	}
}

func (m *Machine) runOnce(ctx context.Context) error {
	client, err := dialSSH(m.cfg.SSHHost, m.cfg.SSHUser, dialTimeout)
	if err != nil {
		return fmt.Errorf("ssh dial: %w", err)
	}
	defer client.Close()

	// Tear down on parent ctx cancel. A derived ctx + defer cancel ensures
	// the watcher goroutine exits cleanly when runOnce returns normally,
	// not just on shutdown — otherwise we leak one watcher per reconnect.
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	go func() {
		<-runCtx.Done()
		client.Close()
	}()

	control, err := startControlSession(client, m.cfg)
	if err != nil {
		return fmt.Errorf("control session: %w", err)
	}
	defer control.Close()

	// One persistent input channel per machine. Mirror state is per-window
	// and lazily created on first viewer.
	input, err := startInputChannel(client)
	if err != nil {
		return fmt.Errorf("input channel: %w", err)
	}
	defer input.Close()

	m.setConn(&machineConn{client: client, input: input, mirrors: make(map[int]*Mirror)})
	defer m.tearDownConn()

	events := make(chan tmuxEvent, 64)
	parseErr := make(chan error, 1)
	go func() { parseErr <- parseTmuxCC(control.stdout, events) }()

	if err := m.refreshWindows(client); err != nil {
		return fmt.Errorf("initial list-windows: %w", err)
	}
	m.updateState(func(s *MachineState) {
		s.Online = true
		s.LastError = ""
	})

	return m.consume(ctx, client, events, parseErr)
}

func (m *Machine) consume(ctx context.Context, client *ssh.Client, events <-chan tmuxEvent, parseErr <-chan error) error {
	// Debounce list-windows refreshes: many window events can fire in a
	// burst (e.g. a script opening 5 tabs), but one re-list at the end
	// captures the final state.
	var refreshTimer *time.Timer
	scheduleRefresh := func() {
		if refreshTimer != nil {
			refreshTimer.Stop()
		}
		refreshTimer = time.AfterFunc(listWindowsDebounce, func() {
			if err := m.refreshWindows(client); err != nil {
				log.Printf("atx %s: refresh windows: %v", m.cfg.Name, err)
			}
		})
	}
	defer func() {
		if refreshTimer != nil {
			refreshTimer.Stop()
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case err := <-parseErr:
			if err != nil {
				return fmt.Errorf("tmux parse: %w", err)
			}
			return io.EOF
		case ev, ok := <-events:
			if !ok {
				return io.EOF
			}
			switch ev.Type {
			case "exit":
				return fmt.Errorf("tmux exited: %s", strings.Join(ev.Args, " "))
			case "window-add", "window-close", "window-renamed",
				"session-window-changed":
				scheduleRefresh()
			}
		}
	}
}

// refreshWindows runs `tmux list-windows` on a fresh SSH session and
// updates the in-memory window list.
func (m *Machine) refreshWindows(client *ssh.Client) error {
	session, err := client.NewSession()
	if err != nil {
		return fmt.Errorf("new session: %w", err)
	}
	defer session.Close()

	var buf bytes.Buffer
	session.Stdout = &buf
	session.Stderr = io.Discard

	cmd := fmt.Sprintf("tmux list-windows -t %s -F '#{window_index} #{window_id} #{window_name}'", shellQuote(m.cfg.TmuxSession))

	done := make(chan error, 1)
	go func() { done <- session.Run(cmd) }()
	select {
	case err := <-done:
		if err != nil {
			return fmt.Errorf("list-windows: %w", err)
		}
	case <-time.After(listWindowsTimeout):
		session.Close()
		return fmt.Errorf("list-windows timeout")
	}

	ws := make([]Window, 0, 16)
	for _, line := range strings.Split(buf.String(), "\n") {
		w, ok := parseWindowListLine(line)
		if !ok {
			continue
		}
		w.LastActivity = time.Now()
		ws = append(ws, w)
	}
	sort.Slice(ws, func(i, j int) bool { return ws[i].Index < ws[j].Index })

	m.updateState(func(s *MachineState) {
		s.Windows = ws
	})
	return nil
}

type controlSession struct {
	session *ssh.Session
	stdout  io.Reader
	stdin   io.WriteCloser
}

func (c *controlSession) Close() error {
	if c.stdin != nil {
		c.stdin.Close()
	}
	return c.session.Close()
}

func startControlSession(client *ssh.Client, cfg config.Machine) (*controlSession, error) {
	session, err := client.NewSession()
	if err != nil {
		return nil, err
	}

	modes := ssh.TerminalModes{
		ssh.ECHO:          0,
		ssh.TTY_OP_ISPEED: 14400,
		ssh.TTY_OP_OSPEED: 14400,
	}
	// Request a generous PTY so atx never becomes the smallest attached
	// tmux client when Thomas's mosh sessions are smaller.
	if err := session.RequestPty("xterm-256color", 80, 200, modes); err != nil {
		session.Close()
		return nil, fmt.Errorf("request pty: %w", err)
	}

	stdin, err := session.StdinPipe()
	if err != nil {
		session.Close()
		return nil, err
	}
	stdout, err := session.StdoutPipe()
	if err != nil {
		session.Close()
		return nil, err
	}
	session.Stderr = io.Discard

	cmd := tmuxLaunchCommand(cfg)
	if err := session.Start(cmd); err != nil {
		session.Close()
		return nil, fmt.Errorf("start tmux -CC: %w", err)
	}

	return &controlSession{session: session, stdout: stdout, stdin: stdin}, nil
}

func tmuxLaunchCommand(c config.Machine) string {
	if c.AutoCreate {
		return fmt.Sprintf("tmux -CC new-session -A -s %s", shellQuote(c.TmuxSession))
	}
	return fmt.Sprintf("tmux -CC attach -t %s", shellQuote(c.TmuxSession))
}

// shellQuote wraps s in single quotes, escaping any embedded single quotes
// via the '...'\''...' pattern, so the result is safe to interpolate into
// a shell command line.
func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'"
}
