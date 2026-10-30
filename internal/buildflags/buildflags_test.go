package buildflags_test

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/internal/buildflags"
)

func TestParse(t *testing.T) {
	t.Parallel()

	for name, want := range map[string]buildflags.SSL{
		"":               buildflags.Default,
		"default":        buildflags.Default,
		"openssl":        buildflags.OpenSSL,
		"openssl_static": buildflags.OpenSSLStatic,
	} {
		got, err := buildflags.Parse(name)
		require.NoError(t, err, name)
		assert.Equal(t, want, got, name)
	}

	_, err := buildflags.Parse("static")
	require.ErrorIs(t, err, buildflags.ErrUnsupported)
}

func TestFlags(t *testing.T) {
	t.Parallel()

	assert.Equal(t, []string{"netgo", "osusergo"}, buildflags.Default.Tags())
	assert.Equal(t, []string{"netgo", "osusergo", "openssl", "go_storage_ssl"},
		buildflags.OpenSSL.Tags())
	assert.Equal(t, []string{"netgo", "osusergo", "openssl", "go_storage_ssl", "openssl_static"},
		buildflags.OpenSSLStatic.Tags())

	assert.Empty(t, buildflags.Default.Ldflags("linux"))
	assert.Empty(t, buildflags.OpenSSL.Ldflags("linux"))
	assert.Equal(t, []string{"-linkmode=external", "-extldflags", "-static"},
		buildflags.OpenSSLStatic.Ldflags("linux"))
	assert.Empty(t, buildflags.OpenSSLStatic.Ldflags("darwin"))

	assert.False(t, buildflags.Default.Cgo())
	assert.True(t, buildflags.OpenSSL.Cgo())
	assert.True(t, buildflags.OpenSSLStatic.Cgo())
}
