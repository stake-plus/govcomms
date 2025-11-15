package core

import "time"

// MissionKind categorizes the type of work an agent should perform.
type MissionKind string

const (
	// MissionKindSocialProfile directs an agent to analyze social/media presence.
	MissionKindSocialProfile MissionKind = "social_profile"
	// MissionKindAliasHunt requests alias resolution for a subject.
	MissionKindAliasHunt MissionKind = "alias_hunt"
	// MissionKindGrantAbuse evaluates historical grant usage.
	MissionKindGrantAbuse MissionKind = "grant_abuse"
)

// SubjectType differentiates what the agent is investigating.
type SubjectType string

const (
	SubjectUnknown       SubjectType = ""
	SubjectPerson        SubjectType = "person"
	SubjectOrganization  SubjectType = "organization"
	SubjectProject       SubjectType = "project"
	SubjectSocialAccount SubjectType = "social_account"
)

// Subject describes the primary entity under investigation.
type Subject struct {
	Type        SubjectType
	Identifier  string
	DisplayName string
	Platform    string
	URL         string
	Metadata    map[string]string
}

// ArtifactType captures supplemental data with loosely structured payloads.
type ArtifactType string

const (
	ArtifactUnknown        ArtifactType = ""
	ArtifactSocialSnapshot ArtifactType = "social_snapshot"
	ArtifactAliasMapping   ArtifactType = "alias_mapping"
	ArtifactGrantHistory   ArtifactType = "grant_history"
)

// Artifact supplies structured or semi-structured context gathered elsewhere.
type Artifact struct {
	Type       ArtifactType
	Name       string
	Source     string
	CapturedAt time.Time
	Data       map[string]any
}

// Mission contains all information necessary for an agent run.
type Mission struct {
	ID          string
	Kind        MissionKind
	Subject     Subject
	Aliases     []Subject
	Inputs      map[string]any
	Artifacts   []Artifact
	Labels      []string
	RequestedBy string
	CreatedAt   time.Time
	Notes       string
}

// MissionStatus enumerates lifecycle states for agent runs.
type MissionStatus string

const (
	MissionStatusPending   MissionStatus = "pending"
	MissionStatusInFlight  MissionStatus = "in_flight"
	MissionStatusCompleted MissionStatus = "completed"
	MissionStatusFailed    MissionStatus = "failed"
)

// Capability advertises what the agent knows how to do.
type Capability struct {
	Name        string
	Description string
	Signals     []string
}

// Finding communicates a single conclusion from the mission.
type Finding struct {
	Title      string
	Details    string
	Severity   string
	Confidence float64
	Citations  []string
}

// Evidence points to the raw material that supports a finding.
type Evidence struct {
	Label      string
	URL        string
	Excerpt    string
	CapturedAt time.Time
	Source     string
}

// Metric provides a quantitative signal derived from a mission run.
type Metric struct {
	Key   string
	Value float64
	Units string
	Notes string
}

// Result is the structured output returned by an agent.
type Result struct {
	MissionID       string
	Status          MissionStatus
	Summary         string
	Confidence      float64
	Findings        []Finding
	Recommendations []string
	Evidence        []Evidence
	Metrics         []Metric
	Tags            []string
	StartedAt       time.Time
	CompletedAt     time.Time
	Raw             map[string]any
}
