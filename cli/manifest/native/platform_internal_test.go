package native

import (
	"testing"

	"github.com/stretchr/testify/assert"
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
