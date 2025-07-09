package replicaset_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
	"github.com/tarantool/tt/cli/running"
)

// spell-checker:ignore someinstanceuuid someleaderuuid somereplicasetuuid otherinstanceuuid

var (
	_ replicaset.Discoverer         = &replicaset.CartridgeInstance{}
	_ replicaset.Promoter           = &replicaset.CartridgeInstance{}
	_ replicaset.Demoter            = &replicaset.CartridgeInstance{}
	_ replicaset.Expeller           = &replicaset.CartridgeInstance{}
	_ replicaset.VShardBootstrapper = &replicaset.CartridgeInstance{}
	_ replicaset.Bootstrapper       = &replicaset.CartridgeInstance{}
	_ replicaset.RolesChanger       = &replicaset.CartridgeInstance{}
)

var (
	_ replicaset.Discoverer   = &replicaset.CartridgeApplication{}
	_ replicaset.Promoter     = &replicaset.CartridgeApplication{}
	_ replicaset.Demoter      = &replicaset.CartridgeApplication{}
	_ replicaset.Expeller     = &replicaset.CartridgeApplication{}
	_ replicaset.Bootstrapper = &replicaset.CartridgeApplication{}
	_ replicaset.RolesChanger = &replicaset.CartridgeApplication{}
)

func TestCartridgeApplication_Demote(t *testing.T) {
	app := replicaset.NewCartridgeApplication(running.RunningCtx{})
	err := app.Demote(replicaset.DemoteCtx{})
	assert.EqualError(t, err,
		`demote is not supported for an application by "cartridge" orchestrator`)
}

func TestCartridgeApplication_Bootstrap(t *testing.T) {
	app := replicaset.NewCartridgeApplication(running.RunningCtx{})
	err := app.Bootstrap(replicaset.BootstrapCtx{})
	assert.EqualError(t, err,
		`failed to bootstrap: there are no running instances`)
}

func TestCartridgeInstance_Demote(t *testing.T) {
	inst := replicaset.NewCartridgeInstance(nil)
	err := inst.Demote(replicaset.DemoteCtx{})
	assert.EqualError(t, err,
		`demote is not supported for a single instance by "cartridge" orchestrator`)
}

func TestCartridgeInstance_Bootstrap(t *testing.T) {
	inst := replicaset.NewCartridgeInstance(nil)
	err := inst.Bootstrap(replicaset.BootstrapCtx{})
	assert.EqualError(t, err,
		`bootstrap is not supported for a single instance by "cartridge" orchestrator`)
}

func TestCartridgeInstance_Discovery(t *testing.T) {
	cases := []struct {
		Name     string
		Evaler   *instanceMockEvaler
		Expected replicaset.Replicasets
	}{
		{
			Name: "no_replicasets",
			Evaler: &instanceMockEvaler{
				Ret: [][]any{
					{
						map[any]any{},
					},
				},
			},
			Expected: replicaset.Replicasets{
				State:        replicaset.StateUninitialized,
				Orchestrator: replicaset.OrchestratorCartridge,
				Replicasets:  nil,
			},
		},
		{
			Name: "empty_replicasets",
			Evaler: &instanceMockEvaler{
				Ret: [][]any{
					{
						map[any]any{
							"replicasets": []any{},
						},
					},
				},
			},
			Expected: replicaset.Replicasets{
				State:        replicaset.StateUninitialized,
				Orchestrator: replicaset.OrchestratorCartridge,
				Replicasets:  []replicaset.Replicaset{},
			},
		},
		{
			Name: "simplest",
			Evaler: &instanceMockEvaler{
				Ret: [][]any{
					{
						map[any]any{
							"replicasets": []any{
								map[any]any{
									"uuid": "foo",
								},
							},
						},
					},
					{
						map[any]any{
							"uuid": "bar",
							"rw":   false,
						},
					},
				},
			},
			Expected: replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCartridge,
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
							"failover": "disabled",
							"provider": "none",
							"replicasets": []any{
								map[any]any{
									"uuid":       "somereplicasetuuid",
									"leaderuuid": "someleaderuuid",
									"alias":      "somealias",
								},
							},
						},
					},
					{
						map[any]any{
							"uuid": "someinstanceuuid",
							"rw":   false,
						},
					},
				},
			},
			Expected: replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCartridge,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:          "somereplicasetuuid",
						LeaderUUID:    "someleaderuuid",
						Alias:         "somealias",
						Master:        replicaset.MasterNo,
						Failover:      replicaset.FailoverOff,
						StateProvider: replicaset.StateProviderNone,
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
							"failover": "disabled",
							"provider": "tarantool",
							"replicasets": []any{
								map[any]any{
									"uuid":       "somereplicasetuuid",
									"leaderuuid": "someleaderuuid",
									"alias":      "somealias",
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
					{
						map[any]any{
							"uuid": "someinstanceuuid",
							"rw":   false,
						},
					},
				},
			},
			Expected: replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCartridge,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:          "somereplicasetuuid",
						LeaderUUID:    "someleaderuuid",
						Alias:         "somealias",
						Master:        replicaset.MasterUnknown,
						Failover:      replicaset.FailoverOff,
						StateProvider: replicaset.StateProviderTarantool,
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
							"failover": "disabled",
							"provider": "tarantool",
							"replicasets": []any{
								map[any]any{
									"uuid":       "somereplicasetuuid",
									"leaderuuid": "someleaderuuid",
									"alias":      "somealias",
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
					{
						map[any]any{
							"uuid": "someinstanceuuid",
							"rw":   true,
						},
					},
				},
			},
			Expected: replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCartridge,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:          "somereplicasetuuid",
						LeaderUUID:    "someleaderuuid",
						Alias:         "somealias",
						Master:        replicaset.MasterSingle,
						Failover:      replicaset.FailoverOff,
						StateProvider: replicaset.StateProviderTarantool,
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
							"failover": "disabled",
							"provider": "tarantool",
							"replicasets": []any{
								map[any]any{
									"uuid":       "somereplicasetuuid",
									"leaderuuid": "someleaderuuid",
									"alias":      "somealias",
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
					{
						map[any]any{
							"uuid": "someinstanceuuid",
							"rw":   false,
						},
					},
				},
			},
			Expected: replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCartridge,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:          "somereplicasetuuid",
						LeaderUUID:    "someleaderuuid",
						Alias:         "somealias",
						Master:        replicaset.MasterNo,
						Failover:      replicaset.FailoverOff,
						StateProvider: replicaset.StateProviderTarantool,
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
							"failover": "disabled",
							"provider": "tarantool",
							"replicasets": []any{
								map[any]any{
									"uuid":       "somereplicasetuuid",
									"leaderuuid": "someleaderuuid",
									"alias":      "somealias",
									"instances": []any{
										map[any]any{
											"alias": "instance1",
											"uuid":  "someinstanceuuid1",
											"uri":   "anyuri1",
										},
										map[any]any{
											"alias": "instance2",
											"uuid":  "someinstanceuuid2",
											"uri":   "anyuri2",
										},
									},
								},
							},
						},
					},
					{
						map[any]any{
							"uuid": "someinstanceuuid1",
							"rw":   true,
						},
					},
				},
			},
			Expected: replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCartridge,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:          "somereplicasetuuid",
						LeaderUUID:    "someleaderuuid",
						Alias:         "somealias",
						Master:        replicaset.MasterUnknown,
						Failover:      replicaset.FailoverOff,
						StateProvider: replicaset.StateProviderTarantool,
						Instances: []replicaset.Instance{
							{
								Alias: "instance1",
								UUID:  "someinstanceuuid1",
								URI:   "anyuri1",
								Mode:  replicaset.ModeRW,
							},
							{
								Alias: "instance2",
								UUID:  "someinstanceuuid2",
								URI:   "anyuri2",
								Mode:  replicaset.ModeUnknown,
							},
						},
					},
				},
			},
		},
		{
			Name: "multi_replicasets",
			Evaler: &instanceMockEvaler{
				Ret: [][]any{
					{
						map[any]any{
							"failover": "disabled",
							"provider": "tarantool",
							"replicasets": []any{
								map[any]any{
									"uuid":       "somereplicasetuuid1",
									"leaderuuid": "someleaderuuid1",
									"alias":      "somealias1",
									"instances": []any{
										map[any]any{
											"alias": "instance1",
											"uuid":  "someinstanceuuid1",
											"uri":   "anyuri1",
										},
										map[any]any{
											"alias": "instance2",
											"uuid":  "someinstanceuuid2",
											"uri":   "anyuri2",
										},
									},
								},
								map[any]any{
									"uuid":       "somereplicasetuuid2",
									"leaderuuid": "someleaderuuid2",
									"alias":      "somealias2",
									"instances": []any{
										map[any]any{
											"alias": "instance3",
											"uuid":  "someinstanceuuid3",
											"uri":   "anyuri3",
										},
										map[any]any{
											"alias": "instance4",
											"uuid":  "someinstanceuuid4",
											"uri":   "anyuri4",
										},
									},
								},
							},
						},
					},
					{
						map[any]any{
							"uuid": "someinstanceuuid1",
							"rw":   true,
						},
					},
				},
			},
			Expected: replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCartridge,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:          "somereplicasetuuid1",
						LeaderUUID:    "someleaderuuid1",
						Alias:         "somealias1",
						Master:        replicaset.MasterUnknown,
						Failover:      replicaset.FailoverOff,
						StateProvider: replicaset.StateProviderTarantool,
						Instances: []replicaset.Instance{
							{
								Alias: "instance1",
								UUID:  "someinstanceuuid1",
								URI:   "anyuri1",
								Mode:  replicaset.ModeRW,
							},
							{
								Alias: "instance2",
								UUID:  "someinstanceuuid2",
								URI:   "anyuri2",
								Mode:  replicaset.ModeUnknown,
							},
						},
					},
					{
						UUID:          "somereplicasetuuid2",
						LeaderUUID:    "someleaderuuid2",
						Alias:         "somealias2",
						Master:        replicaset.MasterUnknown,
						Failover:      replicaset.FailoverOff,
						StateProvider: replicaset.StateProviderTarantool,
						Instances: []replicaset.Instance{
							{
								Alias: "instance3",
								UUID:  "someinstanceuuid3",
								URI:   "anyuri3",
								Mode:  replicaset.ModeUnknown,
							},
							{
								Alias: "instance4",
								UUID:  "someinstanceuuid4",
								URI:   "anyuri4",
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
			instance := replicaset.NewCartridgeInstance(tc.Evaler)
			replicasets, err := instance.Discovery(replicaset.SkipCache)
			assert.NoError(t, err)
			assert.Equal(t, tc.Expected, replicasets)
		})
	}
}

func TestCartridgeInstance_Discovery_force(t *testing.T) {
	evaler := &instanceMockEvaler{
		Ret: [][]any{
			{
				map[any]any{
					"replicasets": []any{
						map[any]any{
							"uuid": "foo1",
						},
					},
				},
			},
			{
				map[any]any{
					"uuid": "foo1",
					"rw":   false,
				},
			},
			{
				map[any]any{
					"replicasets": []any{
						map[any]any{
							"uuid": "foo2",
						},
					},
				},
			},
			{
				map[any]any{
					"uuid": "foo2",
					"rw":   false,
				},
			},
		},
	}

	getter := replicaset.NewCartridgeInstance(evaler)

	replicasets, err := getter.Discovery(replicaset.SkipCache)
	require.NoError(t, err)
	expected := replicaset.Replicasets{
		State:        replicaset.StateBootstrapped,
		Orchestrator: replicaset.OrchestratorCartridge,
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
		Orchestrator: replicaset.OrchestratorCartridge,
		Replicasets: []replicaset.Replicaset{
			{
				UUID:   "foo2",
				Master: replicaset.MasterNo,
			},
		},
	}
	require.Equal(t, expected, replicasets)
}

func TestCartridgeInstance_Discovery_failover(t *testing.T) {
	cases := []struct {
		Failover string
		Expected replicaset.Failover
	}{
		{"foo", replicaset.FailoverUnknown},
		{"disabled", replicaset.FailoverOff},
		{"eventual", replicaset.FailoverEventual},
		{"raft", replicaset.FailoverElection},
	}

	for _, tc := range cases {
		t.Run(tc.Failover, func(t *testing.T) {
			evaler := &instanceMockEvaler{
				Ret: [][]any{
					{
						map[any]any{
							"failover": tc.Failover,
							"replicasets": []any{
								map[any]any{
									"uuid": "foo1",
								},
								map[any]any{
									"uuid": "foo2",
								},
							},
						},
					},
					{
						map[any]any{
							"uuid": "bar",
							"rw":   false,
						},
					},
				},
			}
			expected := replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCartridge,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:     "foo1",
						Master:   replicaset.MasterNo,
						Failover: tc.Expected,
					},
					{
						UUID:     "foo2",
						Master:   replicaset.MasterNo,
						Failover: tc.Expected,
					},
				},
			}

			getter := replicaset.NewCartridgeInstance(evaler)

			replicasets, err := getter.Discovery(replicaset.SkipCache)
			assert.NoError(t, err)
			assert.Equal(t, expected, replicasets)
		})
	}
}

func TestCartridgeInstance_Discovery_provider(t *testing.T) {
	cases := []struct {
		Provider string
		Expected replicaset.StateProvider
	}{
		{"foo", replicaset.StateProviderUnknown},
		{"none", replicaset.StateProviderNone},
		{"tarantool", replicaset.StateProviderTarantool},
		{"etcd2", replicaset.StateProviderEtcd2},
	}

	for _, tc := range cases {
		t.Run(tc.Provider, func(t *testing.T) {
			evaler := &instanceMockEvaler{
				Ret: [][]any{
					{
						map[any]any{
							"provider": tc.Provider,
							"replicasets": []any{
								map[any]any{
									"uuid": "foo1",
								},
								map[any]any{
									"uuid": "foo2",
								},
							},
						},
					},
					{
						map[any]any{
							"uuid": "bar",
							"rw":   false,
						},
					},
				},
			}
			expected := replicaset.Replicasets{
				State:        replicaset.StateBootstrapped,
				Orchestrator: replicaset.OrchestratorCartridge,
				Replicasets: []replicaset.Replicaset{
					{
						UUID:          "foo1",
						Master:        replicaset.MasterNo,
						StateProvider: tc.Expected,
					},
					{
						UUID:          "foo2",
						Master:        replicaset.MasterNo,
						StateProvider: tc.Expected,
					},
				},
			}

			getter := replicaset.NewCartridgeInstance(evaler)

			replicasets, err := getter.Discovery(replicaset.SkipCache)
			assert.NoError(t, err)
			assert.Equal(t, expected, replicasets)
		})
	}
}

func TestCartridgeInstance_Discovery_errors(t *testing.T) {
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
							"replicasets": 123,
						},
					},
				},
			},
			Expected: "failed to parse a response",
		},
		{
			Name: "invalid_instance_info_response",
			Evaler: &instanceMockEvaler{
				Ret: [][]any{
					{
						map[any]any{
							"replicasets": []any{
								map[any]any{
									"uuid": "foo",
								},
							},
						},
					},
					{
						map[any]any{
							"rw": "foo",
						},
					},
				},
			},
			Expected: "failed to parse a response",
		},
		{
			Name: "instance_info_error",
			Evaler: &instanceMockEvaler{
				Ret: [][]any{
					{
						map[any]any{
							"replicasets": []any{
								map[any]any{
									"uuid": "foo",
								},
							},
						},
					},
					{},
				},
				Error: []error{nil, errors.New("foo")},
			},
			Expected: "foo",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			instance := replicaset.NewCartridgeInstance(tc.Evaler)
			_, err := instance.Discovery(replicaset.SkipCache)
			assert.ErrorContains(t, err, tc.Expected)
		})
	}
}

func TestCartridgeInstancePromote_errs(t *testing.T) {
	getReplicasetInfo := func(failover string) map[any]any {
		return map[any]any{
			"failover": failover,
			"replicasets": []any{
				map[any]any{
					"uuid": "foo1",
					"instances": []any{
						map[any]any{
							"uuid":  "i1",
							"alias": "inst-01",
						},
					},
				},
			},
		}
	}

	var (
		instInfoBar = map[any]any{
			"uuid": "bar",
			"rw":   false,
		}

		instInfoI1 = map[any]any{
			"uuid": "i1",
			"rw":   true,
		}

		replicasetInfo                 = getReplicasetInfo("disabled")
		replicasetInfoFailoverUnknown  = getReplicasetInfo("lol")
		replicasetInfoFailoverStateful = getReplicasetInfo("stateful")
	)

	mockErr := fmt.Errorf("mocked error")
	cases := []struct {
		evaler   connector.Evaler
		expected string
		name     string
		ctx      replicaset.PromoteCtx
	}{
		{
			evaler: &instanceMockEvaler{
				Ret: [][]any{
					{replicasetInfo},
					{instInfoBar},
					{instInfoBar},
				},
			},
			expected: `instance with uuid "bar" not found in a configured replicaset`,
			name:     "no instance",
			ctx:      replicaset.PromoteCtx{InstName: "inst-02"},
		},
		{
			evaler: &instanceMockEvaler{
				Ret: [][]any{
					{replicasetInfo},
					{instInfoI1},
					{instInfoI1},
					nil,
				},
				Error: []error{nil, nil, nil, mockErr},
			},
			expected: "failed to edit replicasets: " + mockErr.Error(),
			ctx:      replicaset.PromoteCtx{InstName: "inst-01"},
			name:     "edit replicasets err",
		},
		{
			evaler: &instanceMockEvaler{
				Ret: [][]any{
					{replicasetInfoFailoverUnknown},
					{instInfoI1},
					{instInfoI1},
				},
			},
			expected: "unexpected failover",
			name:     "unexpected failover",
		},
		{
			evaler: &instanceMockEvaler{
				Ret: [][]any{
					{replicasetInfo},
					{instInfoI1},
					{instInfoI1},
					nil,
					nil,
				},
				Error: []error{nil, nil, nil, nil, mockErr},
			},
			expected: "failed to get cartridge version: " + mockErr.Error(),
			ctx:      replicaset.PromoteCtx{InstName: "inst-01"},
			name:     "version error",
		},
		{
			evaler: &instanceMockEvaler{
				Ret: [][]any{
					{replicasetInfo},
					{instInfoI1},
					{instInfoI1},
					nil,
					{"lol"},
				},
			},
			expected: `failed to parse version "lol": format is not valid`,
			ctx:      replicaset.PromoteCtx{InstName: "inst-01"},
			name:     "version parse err",
		},
		{
			evaler: &instanceMockEvaler{
				Ret: [][]any{
					{replicasetInfo},
					{instInfoI1},
					{instInfoI1},
					nil,
					{"unknown"},
				},
			},
			expected: "cartridge version is unknown",
			ctx:      replicaset.PromoteCtx{InstName: "inst-01"},
			name:     "unknown version",
		},
		{
			evaler: &instanceMockEvaler{
				Ret: [][]any{
					{replicasetInfo},
					{instInfoI1},
					{instInfoI1},
					nil,
					{"1.0.0"},
					nil,
				},
				Error: []error{nil, nil, nil, nil, nil, mockErr},
			},
			expected: "failed to wait healthy: " + mockErr.Error(),
			ctx:      replicaset.PromoteCtx{InstName: "inst-01"},
			name:     "healthy waiting error",
		},
		{
			evaler: &instanceMockEvaler{
				Ret: [][]any{
					{replicasetInfo},
					{instInfoI1},
					{instInfoI1},
					nil,
					{"2.8.0"},
					nil,
				},
				Error: []error{nil, nil, nil, nil, nil, mockErr},
			},
			expected: "failed to wait rw: " + mockErr.Error(),
			ctx:      replicaset.PromoteCtx{InstName: "inst-01"},
			name:     "rw waiting error",
		},
		{
			evaler: &instanceMockEvaler{
				Ret: [][]any{
					{replicasetInfoFailoverStateful},
					{instInfoI1},
					{instInfoI1},
					nil,
				},
				Error: []error{nil, nil, nil, mockErr},
			},
			expected: "failed to failover promote: " + mockErr.Error(),
			ctx:      replicaset.PromoteCtx{InstName: "inst-01"},
			name:     "promoting via failover error",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			instance := replicaset.NewCartridgeInstance(tc.evaler)
			err := instance.Promote(tc.ctx)
			require.EqualError(t, err, tc.expected)
		})
	}
}

func TestCartridgeInstance_Expel(t *testing.T) {
	instance := replicaset.NewCartridgeInstance(nil)
	err := instance.Expel(replicaset.ExpelCtx{})
	assert.EqualError(t, err,
		`expel is not supported for a single instance by "cartridge" orchestrator`)
}

func TestCartridgeInstance_RolesChange(t *testing.T) {
	cases := []struct {
		name         string
		changeAction replicaset.RolesChangerAction
		errMsg       string
	}{
		{
			name:         "roles add",
			changeAction: replicaset.RolesAdder{},
			errMsg: "roles add is not supported for a single instance by" +
				` "cartridge" orchestrator`,
		},
		{
			name:         "roles remove",
			changeAction: replicaset.RolesRemover{},
			errMsg: "roles remove is not supported for a single instance by" +
				` "cartridge" orchestrator`,
		},
	}

	inst := replicaset.NewCartridgeInstance(nil)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := inst.RolesChange(replicaset.RolesChangeCtx{}, tc.changeAction)
			assert.EqualError(t, err, tc.errMsg)
		})
	}
}
