package replicaset_test

import (
	"testing"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/v3/cli/replicaset"
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

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			adder := replicaset.RolesAdder{}

			require.Equal(t, replicaset.AddAction, adder.Action())

			res, err := adder.Change(testCase.roles, testCase.roleToAdd)
			if testCase.errMsg != "" {
				require.EqualError(t, err, testCase.errMsg)
			} else {
				require.NoError(t, err)
				require.Equal(t, testCase.expected, res)
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

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			remover := replicaset.RolesRemover{}

			require.Equal(t, replicaset.RemoveAction, remover.Action())

			res, err := remover.Change(testCase.roles, testCase.roleToRemove)
			if testCase.errMsg != "" {
				require.EqualError(t, err, testCase.errMsg)
			} else {
				require.NoError(t, err)
				require.Equal(t, testCase.expected, res)
			}
		})
	}
}
