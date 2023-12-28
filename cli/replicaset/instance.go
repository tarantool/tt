package replicaset

import (
	"github.com/tarantool/tt/cli/running"
)

// Instance describes an instance in a replicaset.
type Instance struct {
	// Alias is a human-readable instance name.
	Alias string
	// UUID of the instance.
	UUID string
	// URI of the instance.
	URI string
	// Mode of the instance.
	Mode Mode
	// InstanceCtx is an instance application context. It is configured if
	// InstanceCtxFound == true.
	InstanceCtx running.InstanceCtx
	// InstanceCtxFound is true if an instance is connectable and could be
	// determined.
	InstanceCtxFound bool
}
