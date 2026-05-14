package runtime

import (
	"context"
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
}

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
