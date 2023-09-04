package pack

import (
	_ "embed"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/cli/version"
)

func Test_convertVersionToStringForInstall(t *testing.T) {
	var err error
	var ver version.Version

	ver, err = version.Parse("2.11.0")
	require.NoError(t, err)
	assert.Equal(t, "2.11.0", getVersionStringForInstall(ver))

	ver, err = version.Parse("2.11.0-alpha")
	require.NoError(t, err)
	assert.Equal(t, "2.11.0-alpha", getVersionStringForInstall(ver))

	ver, err = version.Parse("2.11.0-alpha2")
	require.NoError(t, err)
	assert.Equal(t, "2.11.0-alpha2", getVersionStringForInstall(ver))

	ver, err = version.Parse("2.10.7-0-g60f7e1858")
	require.NoError(t, err)
	assert.Equal(t, "2.10.7", getVersionStringForInstall(ver))

	ver, err = version.Parse("3.0.0-alpha2-24-g4916389a3")
	require.NoError(t, err)
	assert.Equal(t, "3.0.0-alpha2", getVersionStringForInstall(ver))

}
