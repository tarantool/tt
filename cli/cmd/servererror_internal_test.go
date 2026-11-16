package cmd

import (
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/exitcode"
	"github.com/tarantool/tt/v3/cli/manifest/deps"
	manifestrocks "github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// TestShortServerError checks that the failure of a rock server reads as one
// line, the layers above it dropped, with what became of the manifest, and
// still exits as the failure of the network.
func TestShortServerError(t *testing.T) {
	t.Parallel()

	dial := &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}
	serverErr := &manifestrocks.ServerError{
		Op: `resolving "checks"`, Server: "http://127.0.0.1:1/",
		Origin: "TT_REGISTRIES", Reason: "connection refused", Err: dial,
	}
	err := fmt.Errorf("resolving dependencies: resolving product %q: %w; %w",
		"default", serverErr, deps.ErrManifestUnchanged)

	short := shortServerError(err)
	assert.Equal(t, `resolving "checks": cannot reach rock server http://127.0.0.1:1/ `+
		"(from TT_REGISTRIES): connection refused; the manifest was left unchanged", short.Error())
	require.ErrorIs(t, short, deps.ErrManifestUnchanged)
	assert.Equal(t, exitcode.Code(err), exitcode.Code(short))

	other := errors.New("no version of checks matches >=9")
	assert.Equal(t, other, shortServerError(other))
}
