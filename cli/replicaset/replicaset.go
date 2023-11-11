package replicaset

// Replicaset describes a single replicaset.
type Replicaset struct {
	// UUID is UUID of the replicaset.
	UUID string
	// LeaderUUID is UUID of the leader in the replicaset. Could be "" if there
	// is no configured leader.
	LeaderUUID string
	// Alias is an alias name of the replicaset.
	Alias string
	// Roles is a list of roles of the replicaset.
	Roles []string
	// Master is a current master mode.
	Master Master
	// Failover is a configured failover.
	Failover Failover
	// StateProvider is a configured state provider.
	StateProvider StateProvider
	// Instances is a list of instances in the replicaset.
	Instances []Instance
}

// Replicasets describes a set of replicasets.
type Replicasets struct {
	// State is a current state.
	State State
	// Orchestrator is a used orchestrator.
	Orchestrator Orchestrator
	// Replicasets is a list of replicasets.
	Replicasets []Replicaset
}

// ReplicasetsGetter is an interface for retrieving information about
// replicasets.
type ReplicasetsGetter interface {
	// GetReplicasets returns replicasets information or an error.
	GetReplicasets() (Replicasets, error)
}

// recalculateMaster recalculates Master field for the replicaset according
// to instances information.
func recalculateMaster(replicaset *Replicaset) {
	masters := 0
	unknown := 0
	for _, instance := range replicaset.Instances {
		switch instance.Mode {
		case ModeRW:
			masters++
		case ModeUnknown:
			unknown++
		}
	}

	if masters > 1 {
		replicaset.Master = MasterMulti
	} else if masters == 1 {
		if unknown == 0 {
			replicaset.Master = MasterSingle
		} else {
			replicaset.Master = MasterUnknown
		}
	} else if unknown == 0 {
		replicaset.Master = MasterNo
	} else {
		replicaset.Master = MasterUnknown
	}
}

// recalculateMasters recalculates Replicaset.Master field for all replicasets
// according to instances information.
func recalculateMasters(replicasets Replicasets) Replicasets {
	for i, _ := range replicasets.Replicasets {
		recalculateMaster(&replicasets.Replicasets[i])
	}

	return replicasets
}
