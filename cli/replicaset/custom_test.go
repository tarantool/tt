package replicaset_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
)

// spell-checker:ignore somealias someinstanceuuid someleaderuuid somereplicasetuuid anyuri
// spell-checker:ignore otherinstanceuuid instanceuuid instancerw

var (
	_ replicaset.Discoverer         = &replicaset.CustomInstance{}
	_ replicaset.Promoter           = &replicaset.CustomInstance{}
	_ replicaset.Demoter            = &replicaset.CustomInstance{}
	_ replicaset.Expeller           = &replicaset.CustomInstance{}
	_ replicaset.VShardBootstrapper = &replicaset.CustomInstance{}
	_ replicaset.Bootstrapper       = &replicaset.CustomInstance{}
	_ replicaset.RolesChanger       = &replicaset.CustomInstance{}
)

var (
	_ replicaset.Discoverer         = &replicaset.CustomApplication{}
	_ replicaset.Promoter           = &replicaset.CustomApplication{}
	_ replicaset.Demoter            = &replicaset.CustomApplication{}
	_ replicaset.Expeller           = &replicaset.CustomApplication{}
	_ replicaset.VShardBootstrapper = &replicaset.CustomApplication{}
	_ replicaset.Bootstrapper       = &replicaset.CustomApplication{}
	_ replicaset.RolesChanger       = &replicaset.CustomApplication{}
)

func TestCustomApplication_Promote(t *testing.T) {
	app := replicaset.NewCustomApplication(running.RunningCtx{})
	err := app.Promote(replicaset.PromoteCtx{})
	assert.EqualError(t, err,
		`promote is not supported for an application by "custom" orchestrator`)
}

func TestCustomApplication_Demote(t *testing.T) {
	app := replicaset.NewCustomApplication(running.RunningCtx{})
	err := app.Demote(replicaset.DemoteCtx{})
	assert.EqualError(t, err,
		`demote is not supported for an application by "custom" orchestrator`)
}

func TestCustomApplication_Expel(t *testing.T) {
	instance := replicaset.NewCustomApplication(running.RunningCtx{})
	err := instance.Expel(replicaset.ExpelCtx{})
	assert.EqualError(t, err,
		`expel is not supported for an application by "custom" orchestrator`)
}

func TestCustomApplication_BootstrapVShard(t *testing.T) {
	instance := replicaset.NewCustomApplication(running.RunningCtx{})
	err := instance.BootstrapVShard(replicaset.VShardBootstrapCtx{})
	assert.EqualError(t, err,
		`bootstrap vshard is not supported for an application by "custom" orchestrator`)
}

func TestCustomApplication_Bootstrap(t *testing.T) {
	instance := replicaset.NewCustomApplication(running.RunningCtx{})
	err := instance.Bootstrap(replicaset.BootstrapCtx{})
	assert.EqualError(t, err,
		`bootstrap is not supported for an application by "custom" orchestrator`)
}

func TestCustomApplication_RolesAdd(t *testing.T) {
	cases := []struct {
		name         string
		changeAction replicaset.RolesChangerAction
		errMsg       string
	}{
		{
			name:         "roles add",
			changeAction: replicaset.RolesAdder{},
			errMsg:       `roles add is not supported for an application by "custom" orchestrator`,
		},
		{
			name:         "roles remove",
			changeAction: replicaset.RolesRemover{},
			errMsg: `roles remove is not supported for an application by "custom"` +
				" orchestrator",
		},
	}

	instance := replicaset.NewCustomApplication(running.RunningCtx{})

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := instance.RolesChange(replicaset.RolesChangeCtx{}, tc.changeAction)
			assert.EqualError(t, err, tc.errMsg)
		})
	}
}

func TestCustomInstance_Discovery(t *testing.T) {
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
				Orchestrator: replicaset.OrchestratorCustom,
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
						},
					},
				},
			},
			Expected: replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCustom,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:       "somereplicasetuuid",
						LeaderUUID: "someleaderuuid",
						Alias:      "somealias",
						Master:     replicaset.MasterNo,
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
				Orchestrator: replicaset.OrchestratorCustom,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:       "somereplicasetuuid",
						LeaderUUID: "someleaderuuid",
						Alias:      "somealias",
						Master:     replicaset.MasterUnknown,
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
				Orchestrator: replicaset.OrchestratorCustom,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:       "somereplicasetuuid",
						LeaderUUID: "someleaderuuid",
						Alias:      "somealias",
						Master:     replicaset.MasterSingle,
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
				Orchestrator: replicaset.OrchestratorCustom,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:       "somereplicasetuuid",
						LeaderUUID: "someleaderuuid",
						Alias:      "somealias",
						Master:     replicaset.MasterNo,
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
				Orchestrator: replicaset.OrchestratorCustom,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:       "somereplicasetuuid",
						LeaderUUID: "someleaderuuid",
						Alias:      "somealias",
						Master:     replicaset.MasterUnknown,
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
			instance := replicaset.NewCustomInstance(tc.Evaler)
			replicasets, err := instance.Discovery(replicaset.SkipCache)
			assert.NoError(t, err)
			assert.Equal(t, tc.Expected, replicasets)
		})
	}
}

func TestCustomInstance_Discovery_force(t *testing.T) {
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

	getter := replicaset.NewCustomInstance(evaler)

	replicasets, err := getter.Discovery(replicaset.SkipCache)
	require.NoError(t, err)
	expected := replicaset.Replicasets{
		State:        replicaset.StateBootstrapped,
		Orchestrator: replicaset.OrchestratorCustom,
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
		Orchestrator: replicaset.OrchestratorCustom,
		Replicasets: []replicaset.Replicaset{
			{
				UUID:   "foo2",
				Master: replicaset.MasterNo,
			},
		},
	}
	require.Equal(t, expected, replicasets)
}

func TestCustomInstance_Discovery_errors(t *testing.T) {
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
			instance := replicaset.NewCustomInstance(tc.Evaler)
			_, err := instance.Discovery(replicaset.SkipCache)
			assert.ErrorContains(t, err, tc.Expected)
		})
	}
}

func TestCustomInstance_Promote(t *testing.T) {
	inst := replicaset.NewCustomInstance(nil)
	err := inst.Promote(replicaset.PromoteCtx{})
	assert.EqualError(t, err,
		`promote is not supported for a single instance by "custom" orchestrator`)
}

func TestCustomInstance_Demote(t *testing.T) {
	inst := replicaset.NewCustomInstance(nil)
	err := inst.Demote(replicaset.DemoteCtx{})
	assert.EqualError(t, err,
		`demote is not supported for a single instance by "custom" orchestrator`)
}

func TestCustomInstance_Expel(t *testing.T) {
	instance := replicaset.NewCustomInstance(nil)
	err := instance.Expel(replicaset.ExpelCtx{})
	assert.EqualError(t, err,
		`expel is not supported for a single instance by "custom" orchestrator`)
}

func TestCustomInstance_BootstrapVShard(t *testing.T) {
	instance := replicaset.NewCustomInstance(nil)
	err := instance.BootstrapVShard(replicaset.VShardBootstrapCtx{})
	assert.EqualError(t, err,
		`bootstrap vshard is not supported for a single instance by "custom" orchestrator`)
}

func TestCustomInstance_Bootstrap(t *testing.T) {
	instance := replicaset.NewCustomInstance(nil)
	err := instance.Bootstrap(replicaset.BootstrapCtx{})
	assert.EqualError(t, err,
		`bootstrap is not supported for a single instance by "custom" orchestrator`)
}

func TestCustomInstance_RolesChange(t *testing.T) {
	cases := []struct {
		name         string
		changeAction replicaset.RolesChangerAction
		errMsg       string
	}{
		{
			name:         "roles add",
			changeAction: replicaset.RolesAdder{},
			errMsg: `roles add is not supported for a single instance by "custom"` +
				" orchestrator",
		},
		{
			name:         "roles remove",
			changeAction: replicaset.RolesRemover{},
			errMsg: `roles remove is not supported for a single instance by "custom"` +
				" orchestrator",
		},
	}

	instance := replicaset.NewCustomInstance(nil)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := instance.RolesChange(replicaset.RolesChangeCtx{}, tc.changeAction)
			assert.EqualError(t, err, tc.errMsg)
		})
	}
}

// unmigrationgoog
