package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest/native"
)

// TestTargetFlag: tt package build and pack take --target, bound to the
// target of their build, the machine by default, in every spelling of a
// platform tt builds for, and refuse another before anything runs.
func TestTargetFlag(t *testing.T) {
	old := packageTarget

	t.Cleanup(func() { packageTarget = old })

	for name, newCommand := range map[string]func() *cobra.Command{
		"package build": newPackageBuildCmd,
		"package pack":  newPackagePackCmd,
	} {
		t.Run(name, func(t *testing.T) {
			packageTarget = native.Platform{OS: "", Arch: ""}

			command := newCommand()

			flag := command.Flags().Lookup("target")
			require.NotNil(t, flag)
			assert.Empty(t, flag.DefValue)
			assert.Equal(t, "OS/ARCH", flag.Value.Type())

			for text, want := range map[string]native.Platform{
				"linux/arm64":  {OS: "linux", Arch: "arm64"},
				"linux-x86_64": {OS: "linux", Arch: "amd64"},
				"darwin/arm64": {OS: "darwin", Arch: "arm64"},
			} {
				require.NoError(t, command.Flags().Parse([]string{"--target", text}))
				assert.Equal(t, want, packageTarget, text)
				assert.Equal(t, want.OS+"/"+want.Arch, flag.Value.String(), text)
			}

			err := newCommand().Flags().Parse([]string{"--target", "linux/riscv64"})
			require.ErrorIs(t, err, native.ErrUnknownPlatform)
			assert.Contains(t, err.Error(), `invalid argument "linux/riscv64" for "--target" `+
				`flag: unknown platform "linux/riscv64": want linux/amd64, linux/arm64, `+
				`darwin/amd64 or darwin/arm64`)
		})
	}
}

// TestTargetFlagNotOnFetch: tt package fetch and tt test build for the
// machine alone, and have no --target.
func TestTargetFlagNotOnFetch(t *testing.T) {
	assert.Nil(t, newPackageFetchCmd().Flags().Lookup("target"))
	assert.Nil(t, NewTestCmd().Flags().Lookup("target"))
}
