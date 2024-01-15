package replicaset_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/cli/replicaset"
)

var _ replicaset.Discoverer = &replicaset.CartridgeInstance{}
var _ replicaset.Discoverer = &replicaset.CartridgeApplication{}

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
					[]any{
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
					[]any{
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
					[]any{
						map[any]any{
							"replicasets": []any{
								map[any]any{
									"uuid": "foo",
								},
							},
						},
					},
					[]any{
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
					[]any{
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
					replicaset.Replicaset{
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
					[]any{
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
					[]any{
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
					replicaset.Replicaset{
						UUID:          "somereplicasetuuid",
						LeaderUUID:    "someleaderuuid",
						Alias:         "somealias",
						Master:        replicaset.MasterUnknown,
						Failover:      replicaset.FailoverOff,
						StateProvider: replicaset.StateProviderTarantool,
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
					[]any{
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
					replicaset.Replicaset{
						UUID:          "somereplicasetuuid",
						LeaderUUID:    "someleaderuuid",
						Alias:         "somealias",
						Master:        replicaset.MasterSingle,
						Failover:      replicaset.FailoverOff,
						StateProvider: replicaset.StateProviderTarantool,
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
					[]any{
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
					replicaset.Replicaset{
						UUID:          "somereplicasetuuid",
						LeaderUUID:    "someleaderuuid",
						Alias:         "somealias",
						Master:        replicaset.MasterNo,
						Failover:      replicaset.FailoverOff,
						StateProvider: replicaset.StateProviderTarantool,
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
					[]any{
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
					replicaset.Replicaset{
						UUID:          "somereplicasetuuid",
						LeaderUUID:    "someleaderuuid",
						Alias:         "somealias",
						Master:        replicaset.MasterUnknown,
						Failover:      replicaset.FailoverOff,
						StateProvider: replicaset.StateProviderTarantool,
						Instances: []replicaset.Instance{
							replicaset.Instance{
								Alias: "instance1",
								UUID:  "someinstanceuuid1",
								URI:   "anyuri1",
								Mode:  replicaset.ModeRW,
							},
							replicaset.Instance{
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
					[]any{
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
					[]any{
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
					replicaset.Replicaset{
						UUID:          "somereplicasetuuid1",
						LeaderUUID:    "someleaderuuid1",
						Alias:         "somealias1",
						Master:        replicaset.MasterUnknown,
						Failover:      replicaset.FailoverOff,
						StateProvider: replicaset.StateProviderTarantool,
						Instances: []replicaset.Instance{
							replicaset.Instance{
								Alias: "instance1",
								UUID:  "someinstanceuuid1",
								URI:   "anyuri1",
								Mode:  replicaset.ModeRW,
							},
							replicaset.Instance{
								Alias: "instance2",
								UUID:  "someinstanceuuid2",
								URI:   "anyuri2",
								Mode:  replicaset.ModeUnknown,
							},
						},
					},
					replicaset.Replicaset{
						UUID:          "somereplicasetuuid2",
						LeaderUUID:    "someleaderuuid2",
						Alias:         "somealias2",
						Master:        replicaset.MasterUnknown,
						Failover:      replicaset.FailoverOff,
						StateProvider: replicaset.StateProviderTarantool,
						Instances: []replicaset.Instance{
							replicaset.Instance{
								Alias: "instance3",
								UUID:  "someinstanceuuid3",
								URI:   "anyuri3",
								Mode:  replicaset.ModeUnknown,
							},
							replicaset.Instance{
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
			replicasets, err := instance.Discovery()
			assert.NoError(t, err)
			assert.Equal(t, tc.Expected, replicasets)
		})
	}
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
					[]any{
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
					[]any{
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
					replicaset.Replicaset{
						UUID:     "foo1",
						Master:   replicaset.MasterNo,
						Failover: tc.Expected,
					},
					replicaset.Replicaset{
						UUID:     "foo2",
						Master:   replicaset.MasterNo,
						Failover: tc.Expected,
					},
				},
			}

			getter := replicaset.NewCartridgeInstance(evaler)

			replicasets, err := getter.Discovery()
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
					[]any{
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
					[]any{
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
					replicaset.Replicaset{
						UUID:          "foo1",
						Master:        replicaset.MasterNo,
						StateProvider: tc.Expected,
					},
					replicaset.Replicaset{
						UUID:          "foo2",
						Master:        replicaset.MasterNo,
						StateProvider: tc.Expected,
					},
				},
			}

			getter := replicaset.NewCartridgeInstance(evaler)

			replicasets, err := getter.Discovery()
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
					[]any{map[any]any{
						"replicasets": 123,
					},
					}},
			},
			Expected: "failed to parse a response",
		},
		{
			Name: "invalid_instance_info_response",
			Evaler: &instanceMockEvaler{
				Ret: [][]any{
					[]any{
						map[any]any{
							"replicasets": []any{
								map[any]any{
									"uuid": "foo",
								},
							},
						},
					},
					[]any{
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
					[]any{
						map[any]any{
							"replicasets": []any{
								map[any]any{
									"uuid": "foo",
								},
							},
						},
					},
					[]any{},
				},
				Error: []error{nil, errors.New("foo")},
			},
			Expected: "foo",
		},
	}

	for _, tc := range cases {
		t.Run(tc.Name, func(t *testing.T) {
			instance := replicaset.NewCartridgeInstance(tc.Evaler)
			_, err := instance.Discovery()
			assert.ErrorContains(t, err, tc.Expected)
		})
	}
}
