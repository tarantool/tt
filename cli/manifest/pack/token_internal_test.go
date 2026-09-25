package pack

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestPlatformToken(t *testing.T) {
	host := runtime.GOOS + "-" + runtime.GOARCH

	tests := []struct {
		name      string
		withDeps  bool
		hasNative bool
		want      string
	}{
		{
			name:     "with-deps is always platform bound",
			withDeps: true,
			want:     host,
		},
		{
			name:      "with-deps and native is platform bound",
			withDeps:  true,
			hasNative: true,
			want:      host,
		},
		{
			name:      "without-deps but native is platform bound",
			hasNative: true,
			want:      host,
		},
		{
			name: "without-deps and pure lua is universal",
			want: anyToken,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.want, platformToken(tt.withDeps, tt.hasNative))
		})
	}
}

func TestArchiveName(t *testing.T) {
	assert.Equal(t, "my-app-1.0.0-linux-amd64.tt",
		archiveName("my-app", "1.0.0", "linux-amd64"))
	assert.Equal(t, "my-lib-2.1.0-any.tt",
		archiveName("my-lib", "2.1.0", anyToken))
	assert.Equal(t, "my-app-1.0.0-dev.3+gabc1234-linux-arm64.tt",
		archiveName("my-app", "1.0.0-dev.3+gabc1234", "linux-arm64"))
}

func TestIsReservedName(t *testing.T) {
	reserved := []string{
		"app.manifest.toml",
		"app.manifest.lock",
		"VERSION",
		"_runtime",
		"_runtime/tarantool/bin/tarantool",
		".rocks",
		".rocks/share/tarantool/my-app/init.lua",
	}
	for _, name := range reserved {
		assert.True(t, isReservedName(name), "%q must be reserved", name)
	}

	// The staging tree is a real directory, and on a case-insensitive
	// filesystem (APFS, NTFS) "version" opens the staged VERSION and truncates
	// it — so the reserved check is case-insensitive rather than exact.
	caseVariants := []string{"version", "Version", "APP.MANIFEST.TOML", "_Runtime", ".Rocks"}
	for _, name := range caseVariants {
		assert.True(t, isReservedName(name), "%q must be reserved case-insensitively", name)
	}

	// "." is the project root, which contains the staging directory, and
	// _build holds it — copying either would recurse into the copy in progress.
	for _, name := range []string{".", "..", "", "_build", "_build/pack"} {
		assert.True(t, isReservedName(name), "%q must be reserved", name)
	}

	allowed := []string{
		"LICENSE",
		"README.md",
		"doc/index.md",
		"runtime/thing.lua", // No leading underscore.
		"app.manifest.yaml",
		"versions.lua", // Only the exact name is reserved, not a prefix.
	}
	for _, name := range allowed {
		assert.False(t, isReservedName(name), "%q must be allowed", name)
	}
}
