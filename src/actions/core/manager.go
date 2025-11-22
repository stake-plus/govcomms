package core

import (
	"context"
	"fmt"
	"sync"
)

// Module represents a self-contained action that can be started and stopped.
type Module interface {
	Name() string
	Start(ctx context.Context) error
	Stop(ctx context.Context)
}

// Manager coordinates lifecycle of all registered modules.
type Manager struct {
	modules []Module
	mu      sync.Mutex
	started bool
}

// NewManager creates a new manager with the provided modules.
func NewManager(mods ...Module) *Manager {
	return &Manager{
		modules: mods,
	}
}

// Add registers additional modules before Start is invoked.
func (m *Manager) Add(mod Module) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return fmt.Errorf("actions.Manager: cannot add modules after start")
	}
	m.modules = append(m.modules, mod)
	return nil
}

// Start initializes all modules. If any module fails, previously started modules are stopped.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.started {
		return fmt.Errorf("actions.Manager already started")
	}

	started := make([]Module, 0, len(m.modules))
	for _, mod := range m.modules {
		if mod == nil {
			continue
		}
		if err := mod.Start(ctx); err != nil {
			for i := len(started) - 1; i >= 0; i-- {
				started[i].Stop(ctx)
			}
			return fmt.Errorf("module %s failed: %w", mod.Name(), err)
		}
		started = append(started, mod)
	}

	m.started = true
	return nil
}

// Stop shuts down all modules in reverse order.
func (m *Manager) Stop(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.modules) - 1; i >= 0; i-- {
		if mod := m.modules[i]; mod != nil {
			mod.Stop(ctx)
		}
	}
	m.started = false
}
