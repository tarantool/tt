package replicaset

import (
	"fmt"
	"slices"
)

// ChangeRoleFunc is a function type for addition or removing a role.
type ChangeRoleFunc func([]string, string) ([]string, error)

// AddRole is a function that implements addition of role.
func AddRole(roles []string, r string) ([]string, error) {
	if len(roles) > 0 && slices.Index(roles, r) != -1 {
		return []string{}, fmt.Errorf("role %q already exists", r)
	}
	return append(roles, r), nil
}

// RemoveRole is a function that implements removing of role.
func RemoveRole(roles []string, r string) ([]string, error) {
	idx := slices.Index(roles, r)
	if idx == -1 {
		return []string{}, fmt.Errorf("role %q not found", r)
	}
	if len(roles) == 1 {
		return []string{}, nil
	}
	return append(roles[:idx], roles[idx+1:]...), nil
}

// RolesChangeCtx describes a context for adding/removing roles.
type RolesChangeCtx struct {
	// InstName is an instance name in which add/remove role.
	InstName string
	// GroupName is an instance name in which add/remove role.
	GroupName string
	// ReplicasetName is an instance name in which add/remove role.
	ReplicasetName string
	// IsGlobal is an boolean value if role needs to add/remove in global scope.
	IsGlobal bool
	// RoleName is a role name which needs to add/remove into config.
	RoleName string
	// Force is true when promoting can skip
	// some non-critical checks.
	Force bool
	// Timeout is a timeout for promoting waitings in seconds.
	// Keep int, because it can be passed to the target instance.
	Timeout int
}

// parseRoles is a function to convert roles type 'any'
// from yaml config. Returns slice of roles and error.
func parseRoles(value any) ([]string, error) {
	sliceVal, ok := value.([]interface{})
	if !ok {
		return []string{}, fmt.Errorf("%v is not a slice", value)
	}
	existingRoles := make([]string, 0, len(sliceVal)+1)
	for _, v := range sliceVal {
		vStr, ok := v.(string)
		if !ok {
			return []string{}, fmt.Errorf("%v is not a string", v)
		}
		existingRoles = append(existingRoles, vStr)
	}
	return existingRoles, nil
}
