package cluster

import (
	"fmt"
	"sort"

	goconfig "github.com/tarantool/go-config"
)

// splitInstancePath parses a full structural path of the form
// "groups/<g>/replicasets/<r>/instances/<i>" and returns the group,
// replicaset, and instance name segments.
//
// Returns zero-value strings and false if the path does not match the
// expected 6-segment layout (indices 0=="groups", 2=="replicasets",
// 4=="instances").
func splitInstancePath(path string) (group, replicaset, instance string) {
	kp := goconfig.NewKeyPath(path)
	if len(kp) != 6 {
		return "", "", ""
	}
	if kp[0] != "groups" || kp[2] != "replicasets" || kp[4] != "instances" {
		return "", "", ""
	}
	return kp[1], kp[3], kp[5]
}

// Instances returns a sorted list of instance names found in cfg via
// EffectiveAll(). Keys are full structural paths like
// "groups/g1/replicasets/r1/instances/i1".
func Instances(cfg goconfig.Config) ([]string, error) {
	all, err := cfg.EffectiveAll()
	if err != nil {
		return nil, fmt.Errorf("instances: %w", err)
	}

	names := make([]string, 0, len(all))
	for path := range all {
		_, _, inst := splitInstancePath(path)
		if inst == "" {
			continue
		}
		names = append(names, inst)
	}
	sort.Strings(names)
	return names, nil
}

// HasInstance reports whether an instance with the given name exists in cfg.
func HasInstance(cfg goconfig.Config, name string) bool {
	_, _, found := FindInstance(cfg, name)
	return found
}

// FindInstance scans EffectiveAll() keys to locate the instance with the given
// name, returning its containing group and replicaset names.
func FindInstance(cfg goconfig.Config, name string) (group, replicaset string, found bool) {
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
func FindGroupByReplicaset(cfg goconfig.Config, replicaset string) (group string, found bool) {
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

// InstanceConfig locates the instance named name in cfg and returns its
// inheritance-resolved goconfig.Config (as returned by EffectiveAll).
//
// Returns an error if the instance is not found or if EffectiveAll fails.
func InstanceConfig(cfg goconfig.Config, name string) (goconfig.Config, error) {
	all, err := cfg.EffectiveAll()
	if err != nil {
		return goconfig.Config{}, fmt.Errorf("instance config: %w", err)
	}

	for path, instCfg := range all {
		_, _, inst := splitInstancePath(path)
		if inst == name {
			return instCfg, nil
		}
	}
	return goconfig.Config{}, fmt.Errorf("instance %q not found", name)
}
