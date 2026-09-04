package manifest_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/cli/manifest"
)

func TestConstraintExpr(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		declared string
		want     string
	}{
		{name: "any version", declared: "*", want: ""},
		{name: "any version padded", declared: "  *  ", want: ""},
		{name: "omitted", declared: "", want: ""},
		{name: "single bound", declared: ">=3.0.0", want: ">=3.0.0"},
		{name: "range", declared: ">=3.0.0,<4.0.0", want: ">=3.0.0,<4.0.0"},
		{name: "bare version", declared: "1.2.3", want: "1.2.3"},
		{name: "star is not a wildcard character", declared: "1.*", want: "1.*"},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, testCase.want, manifest.ConstraintExpr(testCase.declared))
		})
	}
}
