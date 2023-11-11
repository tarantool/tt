package replicaset

import (
	"strings"
)

// StateProvider defines an enumeration of supported state providers.
type StateProvider int

//go:generate stringer -type=StateProvider -trimprefix StateProvider -linecomment

const (
	// StateProviderUnknown is unknown type of a state provider.
	StateProviderUnknown StateProvider = iota // unknown
	// StateProviderNone is used where there is no state provider.
	StateProviderNone // none
	// StateProviderTarantool is used when a state provider is tarantool.
	StateProviderTarantool // tarantool
	// StateProviderEtcdv2 is used when a state provider is etcdv2.
	StateProviderEtcd2 // etcd2
)

// ParseStateProvider returns a state provider type from a string
// representation.
func ParseStateProvider(str string) StateProvider {
	switch strings.ToLower(str) {
	case "none":
		return StateProviderNone
	case "tarantool":
		return StateProviderTarantool
	case "etcd2":
		return StateProviderEtcd2
	default:
		return StateProviderUnknown
	}
}
