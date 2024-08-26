package replicaset

import (
	"fmt"
	"slices"
)

// RoleAction is a type that describes an action that will
// happen with Roles.
// It can be "add" action which corresponds 0 or "remove" which is 1.
type RoleAction uint

const (
	AddAction RoleAction = iota
	RemoveAction
)

// RolesChangerAction implements changing (add or remove) of roles.
type RolesChangerAction interface {
	// ChangeRoleFunc is a function type for addition or removing a role.
	Change([]string, string) ([]string, error)
	// Action is a method that returns RoleAction.
	Action() RoleAction
}

// RolesAdder is a struct that implements addition of role.
type RolesAdder struct{}

// Change implements addition of role.
func (RolesAdder) Change(roles []string, r string) ([]string, error) {
	if len(roles) > 0 && slices.Index(roles, r) != -1 {
		return []string{}, fmt.Errorf("role %q already exists", r)
	}
	return append(roles, r), nil
}

// Action implements getter of add action.
func (RolesAdder) Action() RoleAction {
	return AddAction
}

// RolesRemover is a struct that implements removing of role.
type RolesRemover struct{}

// Change implements removing of role.
func (RolesRemover) Change(roles []string, r string) ([]string, error) {
	idx := slices.Index(roles, r)
	if idx == -1 {
		return []string{}, fmt.Errorf("role %q not found", r)
	}
	if len(roles) == 1 {
		return []string{}, nil
	}
	return append(roles[:idx], roles[idx+1:]...), nil
}

// Action implements getter of remove action.
func (RolesRemover) Action() RoleAction {
	return RemoveAction
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

// RolesChanger is an interface for adding/removing roles for a replicaset.
type RolesChanger interface {
	// RolesChange adds/removes role for a replicasets by its name.
	RolesChange(ctx RolesChangeCtx, action RolesChangerAction) error
}

// newErrRolesChangeByInstanceNotSupported creates a new error that 'roles add/remove' is not
// supported by the orchestrator for a single instance.
func newErrRolesChangeByInstanceNotSupported(orchestrator Orchestrator,
	changeRoleAction RolesChangerAction) error {
	msg := "roles %s is not supported for a single instance by %q orchestrator"
	if changeRoleAction.Action() == RemoveAction {
		return fmt.Errorf(msg, "remove", orchestrator)
	}
	return fmt.Errorf(msg, "add", orchestrator)
}

// newErrRolesChangeByAppNotSupported creates a new error that 'roles add/remove' by URI is not
// supported by the orchestrator for an application.
func newErrRolesChangeByAppNotSupported(orchestrator Orchestrator,
	changeRoleAction RolesChangerAction) error {
	msg := "roles %s is not supported for an application by %q orchestrator"
	if changeRoleAction.Action() == RemoveAction {
		return fmt.Errorf(msg, "remove", orchestrator)
	}
	return fmt.Errorf(msg, "add", orchestrator)
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
