package replicaset

// Orchestrator defines an enumeration of available orchestrators.
type Orchestrator int

//go:generate stringer -type=Orchestrator -trimprefix Orchestrator -linecomment

//nolint:godot // The line comments are the stringer output.
const (
	// OrchestratorUnknown is an unknown orchestrator.
	OrchestratorUnknown Orchestrator = iota // unknown
	// OrchestratorCentralizedConfig is centralized config for Tarantool 3.0.
	OrchestratorCentralizedConfig // centralized config
	// OrchestratorCustom is a custom orchestrator.
	OrchestratorCustom // custom
)
