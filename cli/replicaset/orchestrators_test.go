package replicaset_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/cli/connector"
	"github.com/tarantool/tt/cli/replicaset"
)

type instanceMockEvaler struct {
	Ret    [][]any
	Error  []error
	Called int
}

func (e *instanceMockEvaler) Eval(expr string,
	args []interface{}, opts connector.RequestOpts) ([]interface{}, error) {
	defer func() { e.Called++ }()

	var ret []any
	if e.Ret != nil {
		ret = e.Ret[e.Called]
	}

	var err error
	if e.Error != nil {
		err = e.Error[e.Called]
	}
	return ret, err
}

var orchestratorCases = []struct {
	Name string
	New  func(evaler connector.Evaler) replicaset.Discoverer
}{
	{
		Name: "cartridge",
		New: func(evaler connector.Evaler) replicaset.Discoverer {
			return replicaset.NewCartridgeInstance(evaler)
		},
	},
	{
		Name: "cconfig",
		New: func(evaler connector.Evaler) replicaset.Discoverer {
			return replicaset.NewCConfigInstance(evaler)
		},
	},
	{
		Name: "custom",
		New: func(evaler connector.Evaler) replicaset.Discoverer {
			return replicaset.NewCustomInstance(evaler)
		},
	},
}

type dummyEvaler struct {
	connector.Evaler
}

func TestNewReplicasetGetter(t *testing.T) {
	cases := []struct {
		Name   string
		Evaler connector.Evaler
	}{
		{"nil", nil},
		{"dummy", dummyEvaler{}},
	}

	for _, oc := range orchestratorCases {
		for _, tc := range cases {
			t.Run(oc.Name+"_"+tc.Name, func(t *testing.T) {
				var getter replicaset.Discoverer
				getter = oc.New(tc.Evaler)
				assert.NotNil(t, getter)
			})
		}
	}
}

func TestReplicasetGetter_Discovery_panics_with_invalid_evaler(t *testing.T) {
	cases := []struct {
		Name   string
		Evaler connector.Evaler
	}{
		{"nil", nil},
		{"dummy", dummyEvaler{}},
	}

	for _, oc := range orchestratorCases {
		for _, tc := range cases {
			t.Run(oc.Name+"_"+tc.Name, func(t *testing.T) {
				getter := oc.New(tc.Evaler)
				assert.Panics(t, func() {
					getter.Discovery(replicaset.SkipCache)
				})
			})
		}
	}
}
