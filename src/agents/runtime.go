package agents

import (
	"github.com/stake-plus/govcomms/src/agents/core"
)

type (
	// Manager re-exports the core manager for convenience.
	Manager = core.Manager
	// Agent represents an executable investigator.
	Agent = core.Agent
	// Mission holds agent input data.
	Mission = core.Mission
	// Result is the structured agent output.
	Result = core.Result
	// Subject references a person/project/account under review.
	Subject = core.Subject
	// Artifact describes supplemental mission data.
	Artifact = core.Artifact
	// Capability advertises available sub-skills.
	Capability = core.Capability
	// MissionKind identifies the type of mission request.
	MissionKind = core.MissionKind
	// MissionStatus captures state transitions.
	MissionStatus = core.MissionStatus
	// ArtifactType categories attachments.
	ArtifactType = core.ArtifactType
	// Finding is a structured output item.
	Finding = core.Finding
	// Evidence references supporting materials.
	Evidence = core.Evidence
	// Metric encodes a quantitative signal.
	Metric = core.Metric
	// RuntimeDeps bundles shared resources for agents.
	RuntimeDeps = core.RuntimeDeps
)

var (
	// ErrUnknownAgent indicates no agent was registered with the provided key.
	ErrUnknownAgent = core.ErrUnknownAgent
)

// NewManager forwards to core.NewManager.
func NewManager() *Manager {
	return core.NewManager()
}
