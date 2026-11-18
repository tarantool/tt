package native

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSupported pins the platforms tt builds for, in the spelling of the
// platform tokens, and the OSes of them, each once.
func TestSupported(t *testing.T) {
	t.Parallel()

	tokens := make([]string, 0, len(Supported()))
	for _, platform := range Supported() {
		tokens = append(tokens, platform.String())
	}

	assert.Equal(t, []string{"linux-amd64", "linux-arm64", "darwin-amd64", "darwin-arm64"}, tokens)
	assert.Equal(t, []string{"linux", "darwin"}, SupportedOS())
}

// TestParsePlatform pins the spellings of a platform --target takes: GOOS/
// GOARCH, the token, the names of LuaRocks and uname, in any case; and
// refuses another, naming what it takes.
func TestParsePlatform(t *testing.T) {
	t.Parallel()

	for text, want := range map[string]Platform{
		"linux/amd64":    {OS: "linux", Arch: "amd64"},
		"linux/arm64":    {OS: "linux", Arch: "arm64"},
		"darwin/amd64":   {OS: "darwin", Arch: "amd64"},
		"darwin/arm64":   {OS: "darwin", Arch: "arm64"},
		"linux-arm64":    {OS: "linux", Arch: "arm64"},
		"linux/aarch64":  {OS: "linux", Arch: "arm64"},
		"linux-x86_64":   {OS: "linux", Arch: "amd64"},
		"macos/arm64":    {OS: "darwin", Arch: "arm64"},
		"macosx-aarch64": {OS: "darwin", Arch: "arm64"},
		"Linux/ARM64":    {OS: "linux", Arch: "arm64"},
	} {
		platform, err := ParsePlatform(text)
		require.NoError(t, err, text)
		assert.Equal(t, want, platform, text)
	}

	for _, text := range []string{
		"", "linux", "arm64", "linux/", "/arm64", "linux/riscv64", "windows/amd64",
		"linux/arm64/v8", "linux_arm64", "freebsd-amd64",
	} {
		_, err := ParsePlatform(text)
		require.ErrorIs(t, err, ErrUnknownPlatform, text)
		assert.Equal(t, `unknown platform "`+text+`": want linux/amd64, linux/arm64, `+
			`darwin/amd64 or darwin/arm64`, err.Error())
	}
}
