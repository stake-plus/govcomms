package core

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ErrUnknownAgent is returned when a caller asks for an unregistered agent.
var ErrUnknownAgent = errors.New("agents: unknown agent")

// Manager coordinates registration, lifecycle, and dispatch for agents.
type Manager struct {
	mu         sync.RWMutex
	agents     map[string]Agent
	lifecycle  []Lifecycle
	started    bool
	startedAt  time.Time
	descriptor map[string]Descriptor
}

// Descriptor captures static metadata about a registered agent.
type Descriptor struct {
	Name         string
	Synopsis     string
	Categories   []string
	Capabilities []Capability
}

// NewManager returns an empty manager ready for registration.
func NewManager() *Manager {
	return &Manager{
		agents:     map[string]Agent{},
		descriptor: map[string]Descriptor{},
	}
}

// Add registers an agent. It must be invoked before Start.
func (m *Manager) Add(agent Agent) error {
	if agent == nil {
		return fmt.Errorf("agents.Manager: nil agent provided")
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return fmt.Errorf("agents.Manager: already started")
	}

	name := normalizeKey(agent.Name())
	if name == "" {
		return fmt.Errorf("agents.Manager: agent missing name")
	}
	if _, exists := m.agents[name]; exists {
		return fmt.Errorf("agents.Manager: agent %q already registered", agent.Name())
	}

	m.agents[name] = agent
	m.descriptor[name] = Descriptor{
		Name:         agent.Name(),
		Synopsis:     agent.Synopsis(),
		Categories:   cloneStrings(agent.Categories()),
		Capabilities: cloneCapabilities(agent.Capabilities()),
	}
	if lifecycle, ok := agent.(Lifecycle); ok {
		m.lifecycle = append(m.lifecycle, lifecycle)
	}
	return nil
}

// Start initializes all lifecycle-aware agents.
func (m *Manager) Start(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.started {
		return fmt.Errorf("agents.Manager: already started")
	}

	started := make([]Lifecycle, 0, len(m.lifecycle))
	for _, lifecycle := range m.lifecycle {
		if lifecycle == nil {
			continue
		}
		if err := lifecycle.Start(ctx); err != nil {
			for i := len(started) - 1; i >= 0; i-- {
				started[i].Stop(ctx)
			}
			return err
		}
		started = append(started, lifecycle)
	}

	m.started = true
	m.startedAt = time.Now().UTC()
	return nil
}

// Stop tears down all registered lifecycle modules in reverse order.
func (m *Manager) Stop(ctx context.Context) {
	m.mu.Lock()
	defer m.mu.Unlock()

	for i := len(m.lifecycle) - 1; i >= 0; i-- {
		if module := m.lifecycle[i]; module != nil {
			module.Stop(ctx)
		}
	}
	m.started = false
}

// Execute dispatches a mission to the named agent.
func (m *Manager) Execute(ctx context.Context, name string, mission Mission) (*Result, error) {
	agent, err := m.Agent(name)
	if err != nil {
		return nil, err
	}
	result, err := agent.Execute(ctx, mission)
	if err != nil {
		return nil, err
	}
	if result != nil && result.CompletedAt.IsZero() {
		result.CompletedAt = time.Now().UTC()
	}
	return result, nil
}

// Agent fetches a registered agent by name.
func (m *Manager) Agent(name string) (Agent, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	agent := m.agents[normalizeKey(name)]
	if agent == nil {
		return nil, ErrUnknownAgent
	}
	return agent, nil
}

// Describe returns metadata for all registered agents.
func (m *Manager) Describe() []Descriptor {
	m.mu.RLock()
	defer m.mu.RUnlock()

	out := make([]Descriptor, 0, len(m.descriptor))
	for _, desc := range m.descriptor {
		out = append(out, desc)
	}
	return out
}

func normalizeKey(name string) string {
	return strings.TrimSpace(strings.ToLower(name))
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func cloneCapabilities(in []Capability) []Capability {
	if len(in) == 0 {
		return nil
	}
	out := make([]Capability, len(in))
	copy(out, in)
	return out
}
