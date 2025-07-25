package replicaset_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
)

// spell-checker:ignore somealias someinstanceuuid someleaderuuid somereplicasetuuid anyuri
// spell-checker:ignore otherinstanceuuid instanceuuid instancerw

var (
	_ replicaset.Discoverer         = &replicaset.CConfigInstance{}
	_ replicaset.Promoter           = &replicaset.CConfigInstance{}
	_ replicaset.Demoter            = &replicaset.CConfigInstance{}
	_ replicaset.Expeller           = &replicaset.CConfigInstance{}
	_ replicaset.VShardBootstrapper = &replicaset.CConfigInstance{}
	_ replicaset.Bootstrapper       = &replicaset.CConfigInstance{}
	_ replicaset.RolesChanger       = &replicaset.CConfigInstance{}
)

var (
	_ replicaset.Discoverer         = &replicaset.CConfigApplication{}
	_ replicaset.Promoter           = &replicaset.CConfigApplication{}
	_ replicaset.Demoter            = &replicaset.CConfigApplication{}
	_ replicaset.Expeller           = &replicaset.CConfigApplication{}
	_ replicaset.VShardBootstrapper = &replicaset.CConfigApplication{}
	_ replicaset.Bootstrapper       = &replicaset.CConfigApplication{}
	_ replicaset.RolesChanger       = &replicaset.CConfigApplication{}
)

func TestCconfigApplication_Bootstrap(t *testing.T) {
	app := replicaset.NewCConfigApplication(running.RunningCtx{}, nil, nil)
	err := app.Bootstrap(replicaset.BootstrapCtx{})
	assert.EqualError(t, err,
		`bootstrap is not supported for an application by "centralized config" orchestrator`)
}

func TestCConfigInstance_Bootstrap(t *testing.T) {
	app := replicaset.NewCConfigInstance(nil)
	err := app.Bootstrap(replicaset.BootstrapCtx{})
	assert.EqualError(t, err,
		`bootstrap is not supported for a single instance by "centralized config" orchestrator`)
}

func TestCConfigInstance_Discovery(t *testing.T) {
	cases := []struct {
		Name     string
		Evaler   *instanceMockEvaler
		Expected replicaset.Replicasets
	}{
		{
			Name: "simplest",
			Evaler: &instanceMockEvaler{
				Ret: [][]any{
					{
						map[any]any{
							"uuid": "foo",
						},
					},
				},
			},
			Expected: replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCentralizedConfig,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:   "foo",
						Master: replicaset.MasterNo,
					},
				},
			},
		},
		{
			Name: "no_instances",
			Evaler: &instanceMockEvaler{
				Ret: [][]any{
					{
						map[any]any{
							"uuid":         "somereplicasetuuid",
							"leaderuuid":   "someleaderuuid",
							"alias":        "somealias",
							"instanceuuid": "someinstanceuuid",
							"instancerw":   true,
							"failover":     "off",
						},
					},
				},
			},
			Expected: replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCentralizedConfig,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:       "somereplicasetuuid",
						LeaderUUID: "someleaderuuid",
						Alias:      "somealias",
						Master:     replicaset.MasterNo,
						Failover:   replicaset.FailoverOff,
					},
				},
			},
		},
		{
			Name: "single_instance",
			Evaler: &instanceMockEvaler{
				Ret: [][]any{
					{
						map[any]any{
							"uuid":         "somereplicasetuuid",
							"leaderuuid":   "someleaderuuid",
							"alias":        "somealias",
							"instanceuuid": "someinstanceuuid",
							"instancerw":   true,
							"failover":     "election",
							"instances": []any{
								map[any]any{
									"alias": "instance",
									"uuid":  "otherinstanceuuid",
									"uri":   "anyuri",
								},
							},
						},
					},
				},
			},
			Expected: replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCentralizedConfig,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:       "somereplicasetuuid",
						LeaderUUID: "someleaderuuid",
						Alias:      "somealias",
						Master:     replicaset.MasterUnknown,
						Failover:   replicaset.FailoverElection,
						Instances: []replicaset.Instance{
							{
								Alias: "instance",
								UUID:  "otherinstanceuuid",
								URI:   "anyuri",
							},
						},
					},
				},
			},
		},
		{
			Name: "single_master",
			Evaler: &instanceMockEvaler{
				Ret: [][]any{
					{
						map[any]any{
							"uuid":         "somereplicasetuuid",
							"leaderuuid":   "someleaderuuid",
							"alias":        "somealias",
							"instanceuuid": "someinstanceuuid",
							"instancerw":   true,
							"failover":     "supervised",
							"instances": []any{
								map[any]any{
									"alias": "instance",
									"uuid":  "someinstanceuuid",
									"uri":   "anyuri",
								},
							},
						},
					},
				},
			},
			Expected: replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCentralizedConfig,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:       "somereplicasetuuid",
						LeaderUUID: "someleaderuuid",
						Alias:      "somealias",
						Master:     replicaset.MasterSingle,
						Failover:   replicaset.FailoverSupervised,
						Instances: []replicaset.Instance{
							{
								Alias: "instance",
								UUID:  "someinstanceuuid",
								URI:   "anyuri",
								Mode:  replicaset.ModeRW,
							},
						},
					},
				},
			},
		},
		{
			Name: "single_replica",
			Evaler: &instanceMockEvaler{
				Ret: [][]any{
					{
						map[any]any{
							"uuid":         "somereplicasetuuid",
							"leaderuuid":   "someleaderuuid",
							"alias":        "somealias",
							"instanceuuid": "someinstanceuuid",
							"instancerw":   false,
							"failover":     "manual",
							"instances": []any{
								map[any]any{
									"alias": "instance",
									"uuid":  "someinstanceuuid",
									"uri":   "anyuri",
								},
							},
						},
					},
				},
			},
			Expected: replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCentralizedConfig,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:       "somereplicasetuuid",
						LeaderUUID: "someleaderuuid",
						Alias:      "somealias",
						Master:     replicaset.MasterNo,
						Failover:   replicaset.FailoverManual,
						Instances: []replicaset.Instance{
							{
								Alias: "instance",
								UUID:  "someinstanceuuid",
								URI:   "anyuri",
								Mode:  replicaset.ModeRead,
							},
						},
					},
				},
			},
		},
		{
			Name: "multi_instances",
			Evaler: &instanceMockEvaler{
				Ret: [][]any{
					{
						map[any]any{
							"uuid":         "somereplicasetuuid",
							"leaderuuid":   "someleaderuuid",
							"alias":        "somealias",
							"instanceuuid": "someinstanceuuid",
							"instancerw":   true,
							"failover":     "supervised",
							"instances": []any{
								map[any]any{
									"alias": "instance",
									"uuid":  "someinstanceuuid",
									"uri":   "anyuri",
								},
								map[any]any{
									"alias": "instance",
									"uuid":  "otherinstanceuuid",
									"uri":   "anyuri",
								},
							},
						},
					},
				},
			},
			Expected: replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCentralizedConfig,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:       "somereplicasetuuid",
						LeaderUUID: "someleaderuuid",
						Alias:      "somealias",
						Master:     replicaset.MasterUnknown,
						Failover:   replicaset.FailoverSupervised,
						Instances: []replicaset.Instance{
							{
								Alias: "instance",
								UUID:  "someinstanceuuid",
								URI:   "anyuri",
								Mode:  replicaset.ModeRW,
							},
							{
								Alias: "instance",
								UUID:  "otherinstanceuuid",
								URI:   "anyuri",
								Mode:  replicaset.ModeUnknown,
							},
						},
					},
				},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			instance := replicaset.NewCConfigInstance(tc.Evaler)
			replicasets, err := instance.Discovery(replicaset.SkipCache)
			assert.NoError(t, err)
			assert.Equal(t, tc.Expected, replicasets)
		})
	}
}

func TestCConfigInstance_Discovery_force(t *testing.T) {
	evaler := &instanceMockEvaler{
		Ret: [][]any{
			{
				map[any]any{
					"uuid": "foo1",
				},
			},
			{
				map[any]any{
					"uuid": "foo2",
				},
			},
		},
	}

	getter := replicaset.NewCConfigInstance(evaler)

	replicasets, err := getter.Discovery(replicaset.SkipCache)
	require.NoError(t, err)
	expected := replicaset.Replicasets{
		State:        replicaset.StateBootstrapped,
		Orchestrator: replicaset.OrchestratorCentralizedConfig,
		Replicasets: []replicaset.Replicaset{
			{
				UUID:   "foo1",
				Master: replicaset.MasterNo,
			},
		},
	}
	require.Equal(t, expected, replicasets)

	// Force re-discovery.
	replicasets, err = getter.Discovery(replicaset.SkipCache)
	require.NoError(t, err)
	expected = replicaset.Replicasets{
		State:        replicaset.StateBootstrapped,
		Orchestrator: replicaset.OrchestratorCentralizedConfig,
		Replicasets: []replicaset.Replicaset{
			{
				UUID:   "foo2",
				Master: replicaset.MasterNo,
			},
		},
	}
	require.Equal(t, expected, replicasets)
}

func TestCConfigInstance_Discovery_failover(t *testing.T) {
	cases := []struct {
		Failover string
		Expected replicaset.Failover
	}{
		{"foo", replicaset.FailoverUnknown},
		{"off", replicaset.FailoverOff},
		{"manual", replicaset.FailoverManual},
		{"election", replicaset.FailoverElection},
		{"supervised", replicaset.FailoverSupervised},
	}

	for _, tc := range cases {
		t.Run(tc.Failover, func(t *testing.T) {
			evaler := &instanceMockEvaler{
				Ret: [][]any{
					{
						map[any]any{
							"uuid":     "foo",
							"failover": tc.Failover,
						},
					},
				},
			}
			expected := replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCentralizedConfig,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:     "foo",
						Master:   replicaset.MasterNo,
						Failover: tc.Expected,
					},
				},
			}

			getter := replicaset.NewCConfigInstance(evaler)

			replicasets, err := getter.Discovery(replicaset.SkipCache)
			assert.NoError(t, err)
			assert.Equal(t, expected, replicasets)
		})
	}
}

func TestCConfigInstance_Discovery_errors(t *testing.T) {
	cases := []struct {
		Name     string
		Evaler   *instanceMockEvaler
		Expected string
	}{
		{
			Name:     "error",
			Evaler:   &instanceMockEvaler{Error: []error{errors.New("foo")}},
			Expected: "foo",
		},
		{
			Name:     "nil_response",
			Evaler:   &instanceMockEvaler{Ret: [][]any{nil}},
			Expected: "unexpected response: []",
		},
		{
			Name:     "empty_response",
			Evaler:   &instanceMockEvaler{Ret: [][]any{{}}},
			Expected: "unexpected response: []",
		},
		{
			Name:     "too_big_response",
			Evaler:   &instanceMockEvaler{Ret: [][]any{{"foo", "bar"}}},
			Expected: "unexpected response: [foo bar]",
		},
		{
			Name: "invalid_response",
			Evaler: &instanceMockEvaler{
				Ret: [][]any{
					{
						map[any]any{
							"instances": 123,
						},
					},
				},
			},
			Expected: "failed to parse a response",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			instance := replicaset.NewCConfigInstance(tc.Evaler)
			_, err := instance.Discovery(replicaset.SkipCache)
			assert.ErrorContains(t, err, tc.Expected)
		})
	}
}

func TestCConfigInstance_Promote_error(t *testing.T) {
	err := fmt.Errorf("some error")
	evaler := &instanceMockEvaler{
		Error: []error{err},
	}
	instance := replicaset.NewCConfigInstance(evaler)
	actual := instance.Promote(replicaset.PromoteCtx{})
	require.ErrorIs(t, actual, err)
}

func TestCConfigInstance_Demote(t *testing.T) {
	instance := replicaset.NewCConfigInstance(nil)
	err := instance.Demote(replicaset.DemoteCtx{})
	require.EqualError(t, err,
		`demote is not supported for a single instance by "centralized config" orchestrator`)
}

func TestCConfigInstance_Expel(t *testing.T) {
	instance := replicaset.NewCConfigInstance(nil)
	err := instance.Expel(replicaset.ExpelCtx{})
	assert.EqualError(t, err,
		`expel is not supported for a single instance by "centralized config" orchestrator`)
}

func TestCConfigInstance_RolesChange(t *testing.T) {
	cases := []struct {
		name         string
		changeAction replicaset.RolesChangerAction
		errMsg       string
	}{
		{
			name:         "roles add",
			changeAction: replicaset.RolesAdder{},
			errMsg: "roles add is not supported for a single instance by" +
				` "centralized config" orchestrator`,
		},
		{
			name:         "roles remove",
			changeAction: replicaset.RolesRemover{},
			errMsg: "roles remove is not supported for a single instance by" +
				` "centralized config" orchestrator`,
		},
	}

	instance := replicaset.NewCConfigInstance(nil)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := instance.RolesChange(replicaset.RolesChangeCtx{}, tc.changeAction)
			assert.EqualError(t, err, tc.errMsg)
		})
	}
}
