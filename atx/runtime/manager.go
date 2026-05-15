package runtime

import (
	"context"
	"fmt"
	"sync"

	"aor/atx/config"
)

// Broadcaster matches aor's shared SSE hub.
type Broadcaster interface {
	Broadcast(event, data string)
}

// Manager owns one *Machine per atx.toml entry and surfaces their state to
// the web layer. Start kicks off the per-machine goroutines; Stop signals
// them all to exit.
type Manager struct {
	machines map[string]*Machine
	order    []string // preserves atx.toml order for stable UI rendering

	hub Broadcaster

	mu     sync.Mutex
	cancel context.CancelFunc

	// In-process per-machine subscribers. Used by wsClients that want a
	// direct callback (with the fresh snapshot) instead of going through
	// the SSE broadcast — needed so a per-tab WebSocket can react to the
	// machine's active window changing.
	subMu sync.Mutex
	subs  map[string]map[*stateSub]struct{}
}

type stateSub struct{ fn func(MachineState) }

func NewManager(cfg *config.Config, hub Broadcaster) *Manager {
	m := &Manager{
		machines: make(map[string]*Machine, len(cfg.Machines)),
		hub:      hub,
	}
	notify := m.broadcastChange
	for _, mc := range cfg.Machines {
		mch := newMachine(mc, notify)
		m.machines[mc.Name] = mch
		m.order = append(m.order, mc.Name)
	}
	return m
}

func (m *Manager) broadcastChange(name string) {
	if m.hub != nil {
		m.hub.Broadcast("atx_machine_changed", name)
	}

	m.subMu.Lock()
	set := m.subs[name]
	subs := make([]*stateSub, 0, len(set))
	for s := range set {
		subs = append(subs, s)
	}
	m.subMu.Unlock()
	if len(subs) == 0 {
		return
	}

	snap, ok := m.MachineState(name)
	if !ok {
		return
	}
	// Fan out outside the lock so a slow subscriber can't wedge updates
	// for other machines. Subscribers are expected to be cheap (the wsClient
	// version writes one JSON frame under its own writeMu).
	for _, s := range subs {
		s.fn(snap)
	}
}

// Subscribe registers fn for in-process state-change notifications for the
// named machine. Returns an unsubscribe function. Safe to call concurrently;
// callbacks fire on the runtime goroutine and must not block.
func (m *Manager) Subscribe(machine string, fn func(MachineState)) func() {
	sub := &stateSub{fn: fn}
	m.subMu.Lock()
	if m.subs == nil {
		m.subs = make(map[string]map[*stateSub]struct{})
	}
	if m.subs[machine] == nil {
		m.subs[machine] = make(map[*stateSub]struct{})
	}
	m.subs[machine][sub] = struct{}{}
	m.subMu.Unlock()
	return func() {
		m.subMu.Lock()
		if set, ok := m.subs[machine]; ok {
			delete(set, sub)
			if len(set) == 0 {
				delete(m.subs, machine)
			}
		}
		m.subMu.Unlock()
	}
}

func (m *Manager) Start(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		return
	}
	ctx, cancel := context.WithCancel(ctx)
	m.cancel = cancel
	for _, mch := range m.machines {
		go mch.run(ctx)
	}
}

func (m *Manager) Stop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.cancel != nil {
		m.cancel()
		m.cancel = nil
	}
}

// Snapshot returns every machine's current state, in atx.toml order.
func (m *Manager) Snapshot() []MachineState {
	out := make([]MachineState, 0, len(m.order))
	for _, name := range m.order {
		out = append(out, m.machines[name].State())
	}
	return out
}

// MachineState returns one machine's state by name, or (zero, false) if unknown.
func (m *Manager) MachineState(name string) (MachineState, bool) {
	mch, ok := m.machines[name]
	if !ok {
		return MachineState{}, false
	}
	return mch.State(), true
}

// AcquireMirror starts (or attaches to) a per-window output mirror at the
// given PTY size.
func (m *Manager) AcquireMirror(ctx context.Context, machine string, windowIdx int, cols, rows uint32) (*Mirror, chan []byte, error) {
	mch, ok := m.machines[machine]
	if !ok {
		return nil, nil, fmt.Errorf("unknown machine: %s", machine)
	}
	return mch.AcquireMirror(ctx, windowIdx, cols, rows)
}

// ReleaseMirror unsubscribes ch from a previously acquired mirror.
func (m *Manager) ReleaseMirror(machine string, windowIdx int, ch chan []byte) {
	mch, ok := m.machines[machine]
	if !ok {
		return
	}
	mch.ReleaseMirror(windowIdx, ch)
}

// ResizeMirror updates the PTY size of an active mirror.
func (m *Manager) ResizeMirror(machine string, windowIdx int, cols, rows uint32) error {
	mch, ok := m.machines[machine]
	if !ok {
		return fmt.Errorf("unknown machine: %s", machine)
	}
	return mch.ResizeMirror(windowIdx, cols, rows)
}

