package core

import "context"

// Agent is the unit of executable intelligence that can fulfill a mission.
type Agent interface {
	Name() string
	Synopsis() string
	Categories() []string
	Capabilities() []Capability
	Execute(ctx context.Context, mission Mission) (*Result, error)
}

// Lifecycle allows agents with external resources to be started/stopped.
type Lifecycle interface {
	Start(ctx context.Context) error
	Stop(ctx context.Context)
}

// Module represents an agent with lifecycle hooks.
type Module interface {
	Agent
	Lifecycle
}
