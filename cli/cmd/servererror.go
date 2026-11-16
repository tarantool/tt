package cmd

import (
	"errors"
	"strings"

	"github.com/tarantool/tt/sdk/log"

	"github.com/tarantool/tt/v3/cli/manifest/deps"
	manifestrocks "github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// shortError is an error that reads as msg and unwraps to err, which it
// stands for: the exit code and errors.Is see the whole chain.
type shortError struct {
	msg string
	err error
}

func (e *shortError) Error() string { return e.msg }

func (e *shortError) Unwrap() error { return e.err }

// originLabels name the layers of the rock servers as a user configures them.
var originLabels = map[manifestrocks.Source]string{
	manifestrocks.SourceFlag:     "--registry",
	manifestrocks.SourceEnv:      manifestrocks.EnvRegistries,
	manifestrocks.SourceManifest: "[platform].registries",
	manifestrocks.SourceDefault:  "",
}

// shortServerError returns err, when a rock server could not be reached or
// read in it (manifestrocks.ServerError), as what was being done, the
// server, where it is configured and why, then what became of the manifest:
//
//	resolving "checks": cannot reach rock server http://example.invalid/
//	(from TT_REGISTRIES): no such host; the manifest was left unchanged
//
// The layers of wrapping in between - the product, the engine, the index
// and the request of go-luarocks - are left to -V, which logs err whole.
// Any other err is returned as it is.
func shortServerError(err error) error {
	serverErr, ok := errors.AsType[*manifestrocks.ServerError](err)
	if !ok {
		return err
	}

	log.Debugf("The error in full: %s", err)
	log.Debugf("The failure of the rock server: %s", serverErr.Err)

	if serverErr.Origin == "" {
		serverErr.Origin = serverOrigin(serverErr.Server)
	}

	msg := serverErr.Error()

	switch {
	case errors.Is(err, deps.ErrManifestEdited):
		msg += "; " + deps.ErrManifestEdited.Error()
	case errors.Is(err, deps.ErrManifestUnchanged):
		msg += "; " + deps.ErrManifestUnchanged.Error()
	}

	return &shortError{msg: msg, err: err}
}

// serverOrigin says where the rock server named server is configured, as the
// command's servers are: "" when it is not one of them, a registry of a
// declaration say, or when they cannot be told.
func serverOrigin(server string) string {
	registries, err := effectiveRegistries()
	if err != nil {
		return ""
	}

	for _, registry := range registries {
		if strings.TrimSuffix(registry.URL, "/") == strings.TrimSuffix(server, "/") {
			return originLabels[registry.Source]
		}
	}

	return ""
}
