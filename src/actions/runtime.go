package actions

import "github.com/stake-plus/govcomms/src/actions/core"

type (
	// Manager re-exports the core.Manager for consumers outside the actions package.
	Manager = core.Manager
	// Module re-exports the core.Module interface.
	Module = core.Module
)

// NewManager is a helper that forwards to core.NewManager.
func NewManager(mods ...Module) *Manager {
	return core.NewManager(mods...)
}
