package replicaset

import "fmt"

// RolesAddCtx describes a context for adding roles.
type RolesAddCtx struct {
	// InstName is an instance name in which add role.
	InstName string
	// GroupName is an instance name in which add role.
	GroupName string
	// ReplicasetName is an instance name in which add role.
	ReplicasetName string
	// IsGlobal is an boolean value if role needs to add in global scope.
	IsGlobal bool
	// RoleName is a role name which needs to add into config.
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
