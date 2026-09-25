package cluster

import (
	"errors"

	goconfig "github.com/tarantool/go-config/v2"

	"github.com/tarantool/tt/sdk"
)

var (
	errInstanceNotFound = errors.New("instance ")
)

const instancePathSegments = 6

// splitInstancePath parses a full structural path of the form
// "groups/<g>/replicasets/<r>/instances/<i>" and returns the group,
// replicaset, and instance name segments.
//
// Returns zero-value strings and false if the path does not match the
// expected 6-segment layout (indices 0=="groups", 2=="replicasets",
// 4=="instances").
func splitInstancePath(path string) (string, string, string) {
	keyPath := goconfig.NewKeyPath(path)
	if len(keyPath) != instancePathSegments {
		return "", "", ""
	}

	if keyPath[0] != "groups" || keyPath[2] != "replicasets" || keyPath[4] != "instances" {
		return "", "", ""
	}

	return keyPath[1], keyPath[3], keyPath[5]
}

// Instances returns a sorted list of instance names found in cfg: sdk.Instances.
func Instances(cfg goconfig.Config) ([]string, error) {
	return sdk.Instances(cfg)
}

// HasInstance reports whether an instance with the given name exists in cfg.
func HasInstance(cfg goconfig.Config, name string) bool {
	_, _, found := FindInstance(cfg, name)
	return found
}

// FindInstance scans EffectiveAll() keys to locate the instance with the given
// name, returning its containing group and replicaset names.
func FindInstance(cfg goconfig.Config, name string) (string, string, bool) {
	all, err := cfg.EffectiveAll()
	if err != nil {
		return "", "", false
	}

	for path := range all {
		g, r, inst := splitInstancePath(path)
		if inst == name {
			return g, r, true
		}
	}

	return "", "", false
}

// FindGroupByReplicaset scans EffectiveAll() keys and returns the group that
// contains the given replicaset name.
func FindGroupByReplicaset(cfg goconfig.Config, replicaset string) (string, bool) {
	all, err := cfg.EffectiveAll()
	if err != nil {
		return "", false
	}

	for path := range all {
		g, r, _ := splitInstancePath(path)
		if r == replicaset {
			return g, true
		}
	}

	return "", false
}

// InstanceConfig returns the inheritance-resolved configuration of the
// instance named name in cfg: sdk.InstanceConfig. An unknown instance wraps
// sdk.ErrNotFound.
func InstanceConfig(cfg goconfig.Config, name string) (goconfig.Config, error) {
	return sdk.InstanceConfig(cfg, name)
}
