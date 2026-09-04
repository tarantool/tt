package replicaset

import (
	"github.com/tarantool/tt/cli/running"
)

// Instance describes an instance in a replicaset.
type Instance struct {
	// Alias is a human-readable instance name.
	Alias string `mapstructure:"alias"`
	// UUID of the instance.
	UUID string `mapstructure:"uuid"`
	// URI of the instance.
	URI string `mapstructure:"uri"`
	// Mode of the instance.
	Mode Mode `mapstructure:"mode"`
	// InstanceCtx is an instance application context. It is configured if
	// InstanceCtxFound == true.
	InstanceCtx running.InstanceCtx `mapstructure:"-"`
	// InstanceCtxFound is true if an instance is connectable and could be
	// determined.
	InstanceCtxFound bool `mapstructure:"-"`
}
