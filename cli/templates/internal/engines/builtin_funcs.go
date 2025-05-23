package engines

import (
	"fmt"
)

// genState describes template generation state.
type genState struct {
	port, metricsPort int
}

// newGenState creates genState.
func newGenState() *genState {
	return &genState{port: 3301, metricsPort: 8081}
}

// genPort generates port.
func (state *genState) genPort() int {
	ret := state.port
	state.port++
	return ret
}

// genMetricsPort generates metrics port.
func (state *genState) genMetricsPort() int {
	ret := state.metricsPort
	state.metricsPort++
	return ret
}

// replicaset describes generated replicaset information.
type replicaset struct {
	Name      string
	InstNames []string
}

const (
	maxReplicasetsNumber = 1000
	maxReplicasetSize    = 32
)

// genReplicasets generates replicasets bunch.
func genReplicasets(
	baseName string,
	replicasetsNumber int,
	replicasetSize int,
) ([]replicaset, error) {
	if replicasetsNumber <= 0 || replicasetsNumber > maxReplicasetsNumber {
		return nil, fmt.Errorf("replicasetsNumber must be in [%d, %d]", 0, maxReplicasetsNumber)
	}
	if replicasetSize <= 0 || replicasetSize > maxReplicasetSize {
		return nil, fmt.Errorf("replicasetSize must be in [%d, %d]", 0, maxReplicasetSize)
	}

	replicasets := make([]replicaset, replicasetsNumber)
	for i := range replicasets {
		replicasetName := fmt.Sprintf("%s-%03d", baseName, i+1)
		instNames := make([]string, replicasetSize)
		for j := range instNames {
			if replicasetSize <= 26 {
				instNames[j] = fmt.Sprintf("%s-%c", replicasetName, 'a'+byte(j))
			} else {
				instNames[j] = fmt.Sprintf("%s-%03d", replicasetName, j+1)
			}
		}
		replicasets[i].Name = replicasetName
		replicasets[i].InstNames = instNames
	}
	return replicasets, nil
}
