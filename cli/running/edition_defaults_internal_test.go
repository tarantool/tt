package running

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/internal/binary"
)

func TestEditionDefaults(t *testing.T) {
	inst := &InstanceCtx{VinylDir: "/root/var/lib/app/inst"}
	quiver := []string{"TT_QUIVER_DIR_DEFAULT=/root/var/lib/app/inst"}

	enterprise := bannerTarantool(t, "Tarantool Enterprise 3.7.0-0-g1f1ec9fdf")
	community := bannerTarantool(t, "Tarantool 3.8.1-0-ge643f88a5e0c")

	tests := []struct {
		name string
		exe  tarantool.Executable
		want []string
	}{
		{
			name: "Enterprise",
			exe:  tarantool.Executable{Path: enterprise},
			want: quiver,
		},
		{
			name: "Community",
			exe:  tarantool.Executable{Path: community},
		},
		{
			// The banner decides: an Enterprise build installed as the
			// Community product still takes the option.
			name: "Enterprise installed as Community",
			exe:  tarantool.Executable{Path: enterprise, Program: binary.ProgramCe},
			want: quiver,
		},
		{
			name: "Community installed as Enterprise",
			exe:  tarantool.Executable{Path: community, Program: binary.ProgramEe},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assert.Equal(t, test.want, editionDefaults(t.Context(), test.exe, inst))
		})
	}
}
