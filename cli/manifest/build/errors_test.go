package build

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
)

// TestExitCode pins the exit-code scheme every manifest command shares: 1 for
// what the user asked, 2 for the system it ran on, and an explicit verdict
// other than 1 standing over both.
func TestExitCode(t *testing.T) {
	t.Parallel()

	refused := &net.OpError{
		Op: "dial", Net: "tcp",
		Err: os.NewSyscallError("connect", syscall.ECONNREFUSED),
	}
	unknownHost := &net.DNSError{Err: "no such host", Name: "rocks.invalid", IsNotFound: true}
	badScheme := &url.Error{
		Op: "Get", URL: "ftp://x/manifest", Err: errors.New("unsupported protocol scheme"),
	}
	timedOut := &url.Error{Op: "Get", URL: "http://x/manifest", Err: context.DeadlineExceeded}
	denied := &fs.PathError{Op: "open", Path: "app.manifest.toml", Err: syscall.EACCES}
	full := &fs.PathError{Op: "write", Path: ".rocks/x", Err: syscall.ENOSPC}
	missing := &fs.PathError{Op: "open", Path: "app.manifest.toml", Err: syscall.ENOENT}

	state := func(err error) error { return &ExitError{Code: exitStateError, Err: err} }

	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"plain error", errors.New("bad manifest"), 1},
		{"state code", state(errors.New("lock is out of date")), 1},
		{"backend code", &ExitError{Code: exitBackendError, Err: errors.New("cc")}, 2},
		{"partial code", &ExitError{Code: 3, Err: errors.New("one of two")}, 3},

		// The generic code 1 gives way to the system failure it wraps: this
		// is the shape every resolve error arrives in.
		{"refused behind state", state(fmt.Errorf("resolving: %w", refused)), 2},
		{"dns behind state", state(fmt.Errorf("resolving: %w", unknownHost)), 2},
		{"timeout behind state", state(timedOut), 2},
		{"permission", fmt.Errorf("reading: %w", denied), 2},
		{"disk full", fmt.Errorf("writing: %w", full), 2},

		// A deliberate verdict stands, even over a system failure, and is
		// found beneath a generic wrapper.
		{"partial over refused", &ExitError{Code: 3, Err: refused}, 3},
		{"partial beneath state", state(&ExitError{Code: 3, Err: errors.New("x")}), 3},

		// What the user can fix stays 1, even when it travels in a type the
		// network code also uses.
		{"malformed registry url", state(badScheme), 1},
		{"missing file", fmt.Errorf("reading: %w", missing), 1},
		{"cancelled", context.Canceled, 1},
		{"deadline alone", context.DeadlineExceeded, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, ExitCode(tc.err))
		})
	}
}
