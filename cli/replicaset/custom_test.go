package replicaset_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
)

var _ replicaset.Discoverer = &replicaset.CustomInstance{}
var _ replicaset.Promoter = &replicaset.CustomInstance{}
var _ replicaset.Demoter = &replicaset.CustomInstance{}

var _ replicaset.Discoverer = &replicaset.CustomApplication{}
var _ replicaset.Promoter = &replicaset.CustomApplication{}
var _ replicaset.Demoter = &replicaset.CustomApplication{}

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
					[]any{
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
					replicaset.Replicaset{
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
					[]any{
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
					replicaset.Replicaset{
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
					[]any{
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
					replicaset.Replicaset{
						UUID:       "somereplicasetuuid",
						LeaderUUID: "someleaderuuid",
						Alias:      "somealias",
						Master:     replicaset.MasterUnknown,
						Instances: []replicaset.Instance{
							replicaset.Instance{
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
					[]any{
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
					replicaset.Replicaset{
						UUID:       "somereplicasetuuid",
						LeaderUUID: "someleaderuuid",
						Alias:      "somealias",
						Master:     replicaset.MasterSingle,
						Instances: []replicaset.Instance{
							replicaset.Instance{
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
					[]any{
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
					replicaset.Replicaset{
						UUID:       "somereplicasetuuid",
						LeaderUUID: "someleaderuuid",
						Alias:      "somealias",
						Master:     replicaset.MasterNo,
						Instances: []replicaset.Instance{
							replicaset.Instance{
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
					[]any{
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
					replicaset.Replicaset{
						UUID:       "somereplicasetuuid",
						LeaderUUID: "someleaderuuid",
						Alias:      "somealias",
						Master:     replicaset.MasterUnknown,
						Instances: []replicaset.Instance{
							replicaset.Instance{
								Alias: "instance",
								UUID:  "someinstanceuuid",
								URI:   "anyuri",
								Mode:  replicaset.ModeRW,
							},
							replicaset.Instance{
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
			replicasets, err := instance.Discovery()
			assert.NoError(t, err)
			assert.Equal(t, tc.Expected, replicasets)
		})
	}
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
			Evaler:   &instanceMockEvaler{Ret: [][]any{[]any{}}},
			Expected: "unexpected response: []",
		},
		{
			Name:     "too_big_response",
			Evaler:   &instanceMockEvaler{Ret: [][]any{[]any{"foo", "bar"}}},
			Expected: "unexpected response: [foo bar]",
		},
		{
			Name: "invalid_response",
			Evaler: &instanceMockEvaler{
				Ret: [][]any{
					[]any{
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
			_, err := instance.Discovery()
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
