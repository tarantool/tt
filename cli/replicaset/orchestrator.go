package replicaset

import (
	_ "embed"
	"fmt"
	"strings"

	"github.com/tarantool/tt/cli/connector"
)

//go:embed lua/get_orchestrator.lua
var getOrchestratorBody string

// Orchestrator defines an enumeration of available orchestrators.
type Orchestrator int

//go:generate stringer -type=Orchestrator -trimprefix Orchestrator -linecomment

const (
	// OrchestratorUnknown is an unknown orchestrator.
	OrchestratorUnknown Orchestrator = iota // unknown
	// OrchestratorCentralizedConfig is centralized config for Tarantool 3.0.
	OrchestratorCentralizedConfig // centralized config
	// OrchestratorCartridge is the cartridge orchestrator.
	OrchestratorCartridge // cartridge
	// OrchestratorCustom is a custom orchestrator.
	OrchestratorCustom // custom
)

var knownOrchestrators = []Orchestrator{
	OrchestratorCartridge,
	OrchestratorCentralizedConfig,
	OrchestratorCustom,
}

// PraseOrchestrator parses an orchestrator from the string.
func ParseOrchestrator(str string) Orchestrator {
	for _, o := range knownOrchestrators {
		if strings.ToLower(str) == strings.ToLower(o.String()) {
			return o
		}
	}

	return OrchestratorUnknown
}

// EvalOrchestrator returns an orchestrator version determined from an
// evaler.
func EvalOrchestrator(evaler connector.Evaler) (Orchestrator, error) {
	opts := connector.RequestOpts{}
	data, err := evaler.Eval(getOrchestratorBody, []any{}, opts)
	if err != nil {
		return OrchestratorCustom,
			fmt.Errorf("failed to recognize orchestrator: %s", err)
	}
	if len(data) == 1 {
		if str, ok := data[0].(string); ok {
			parsed := ParseOrchestrator(str)
			if parsed == OrchestratorUnknown {
				return parsed, fmt.Errorf("unknown orchestrator: %s", str)
			}
			return parsed, nil
		}
	}
	return OrchestratorCustom, fmt.Errorf("unexpected response")
}
