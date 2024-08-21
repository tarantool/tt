package replicaset_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/replicaset"
)

func TestRoles_AddRole(t *testing.T) {
	cases := []struct {
		name      string
		roles     []string
		roleToAdd string
		expected  []string
		errMsg    string
	}{
		{"ok", []string{"role"}, "other_role", []string{"role", "other_role"}, ""},
		{"already exists", []string{"role"}, "role", []string{}, "role \"role\" already exists"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := replicaset.AddRole(tc.roles, tc.roleToAdd)
			if tc.errMsg != "" {
				require.EqualError(t, err, tc.errMsg)
			} else {
				require.NoError(t, err)
				require.Equal(t, res, tc.expected)
			}
		})
	}
}

func TestRoles_RemoveRole(t *testing.T) {
	cases := []struct {
		name         string
		roles        []string
		roleToRemove string
		expected     []string
		errMsg       string
	}{
		{"ok one role", []string{"role"}, "role", []string{}, ""},
		{"ok many roles", []string{"role_1", "role_2"}, "role_1", []string{"role_2"}, ""},
		{"not found", []string{"role"}, "other_role", []string{}, "role \"other_role\" not found"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := replicaset.RemoveRole(tc.roles, tc.roleToRemove)
			if tc.errMsg != "" {
				require.EqualError(t, err, tc.errMsg)
			} else {
				require.NoError(t, err)
				require.Equal(t, res, tc.expected)
			}
		})
	}
}
