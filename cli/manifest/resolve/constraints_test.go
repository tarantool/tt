//nolint:testpackage // white-box: exercises the unexported satisfiable directly.
package resolve

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/lib/luarocks/deps"
)

func TestSatisfiable(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		expr string
		want bool
	}{
		{name: "single range", expr: ">=1.0.0,<2.0.0", want: true},
		{name: "conflicting equals", expr: "==1.0.0,==2.0.0", want: false},
		{name: "equal inside range", expr: "==1.5.0,>=1.0.0,<2.0.0", want: true},
		{name: "equal below range", expr: "==0.9.0,>=1.0.0", want: false},
		{name: "equal above range", expr: "==2.0.0,<2.0.0", want: false},
		{name: "disjoint ranges", expr: ">=2.0.0,<1.0.0", want: false},
		{name: "touching exclusive", expr: ">2.0.0,<=2.0.0", want: false},
		{name: "touching inclusive", expr: ">=2.0.0,<=2.0.0", want: true},
		{name: "point excluded", expr: ">=2.0.0,<=2.0.0,~=2.0.0", want: false},
		{name: "exclude in open range", expr: ">=1.0.0,~=1.5.0", want: true},
		{name: "pessimistic compatible", expr: "~>1.2,>=1.2.5", want: true},
		{name: "pessimistic excludes upper", expr: "~>1.2,>=1.3.0", want: false},
		{name: "pessimistic with equal outside", expr: "~>1.2,==1.3.0", want: false},
		// A revision-pinned ~> must not over-reject: 1.2.3.4-2 matches ~>1.2.3-2
		// and is >1.2.3-2, so the set is non-empty.
		{name: "pessimistic revision alone", expr: "~>1.2.3-2", want: true},
		{name: "pessimistic revision with strict greater", expr: "~>1.2.3-2,>1.2.3-2", want: true},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			constraints, err := deps.ParseConstraints(testCase.expr)
			require.NoError(t, err)

			assert.Equal(t, testCase.want, satisfiable(constraints))
		})
	}
}
