package exitcode_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/exitcode"
)

// Failures of each kind the classification tells apart.
var (
	refused = &net.OpError{
		Op: "dial", Net: "tcp",
		Err: os.NewSyscallError("connect", syscall.ECONNREFUSED),
	}
	unknownHost = &net.DNSError{Err: "no such host", Name: "rocks.invalid", IsNotFound: true}
	badScheme   = &url.Error{
		Op: "Get", URL: "ftp://x/manifest", Err: errors.New("unsupported protocol scheme"),
	}
	timedOut = &url.Error{Op: "Get", URL: "http://x/manifest", Err: context.DeadlineExceeded}
	denied   = &fs.PathError{Op: "open", Path: "app.manifest.toml", Err: syscall.EACCES}
	full     = &fs.PathError{Op: "write", Path: ".rocks/x", Err: syscall.ENOSPC}
	readOnly = &fs.PathError{Op: "write", Path: ".rocks/x", Err: syscall.EROFS}
	missing  = &fs.PathError{Op: "open", Path: "app.manifest.toml", Err: syscall.ENOENT}
)

// state wraps err in the generic code every command wraps its errors in.
func state(err error) error { return sdk.WithCode(sdk.ExitFailure, err) }

// TestCode pins the exit-code scheme every manifest command shares: 1 for
// what the user asked, 2 for the system it ran on, and an explicit verdict
// other than 1 standing over both.
func TestCode(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"nil", nil, 0},
		{"plain error", errors.New("bad manifest"), 1},
		{"state code", state(errors.New("lock is out of date")), 1},
		{"backend code", sdk.WithCode(sdk.ExitSystem, errors.New("cc")), 2},
		{"partial code", sdk.WithCode(sdk.ExitPartial, errors.New("one of two")), 3},

		// The generic code 1 gives way to the system failure it wraps: this
		// is the shape every resolve error arrives in.
		{"refused behind state", state(fmt.Errorf("resolving: %w", refused)), 2},
		{"dns behind state", state(fmt.Errorf("resolving: %w", unknownHost)), 2},
		{"timeout behind state", state(timedOut), 2},
		{"permission", fmt.Errorf("reading: %w", denied), 2},
		{"disk full", fmt.Errorf("writing: %w", full), 2},
		{"read-only disk", fmt.Errorf("writing: %w", readOnly), 2},

		// A deliberate verdict stands, even over a system failure, and is
		// found beneath a generic wrapper.
		{"partial over refused", sdk.WithCode(sdk.ExitPartial, refused), 3},
		{"partial beneath state", state(sdk.WithCode(sdk.ExitPartial, errors.New("x"))), 3},

		// What the user can fix stays 1, even when it travels in a type the
		// network code also uses.
		{"malformed registry url", state(badScheme), 1},
		{"missing file", fmt.Errorf("reading: %w", missing), 1},
		{"cancelled", context.Canceled, 1},
		{"deadline alone", context.DeadlineExceeded, 1},

		// An external module's own status is its verdict, not tt's to
		// reclassify.
		{"module status 7", sdk.WithCode(7, exitcode.Silent(errors.New("module"))), 7},
		{"module status 1", sdk.WithCode(1, exitcode.Silent(errors.New("module"))), 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, exitcode.Code(tc.err))
		})
	}
}

// TestSilent pins that a silenced error keeps its code and its identity.
func TestSilent(t *testing.T) {
	t.Parallel()

	assert.NoError(t, exitcode.Silent(nil))
	assert.False(t, exitcode.IsSilent(nil))

	plain := errors.New("aborted by user")
	assert.False(t, exitcode.IsSilent(plain))

	silent := exitcode.Silent(plain)
	assert.True(t, exitcode.IsSilent(silent))
	assert.True(t, exitcode.IsSilent(fmt.Errorf("installing: %w", silent)))
	assert.ErrorIs(t, silent, plain)
	assert.Equal(t, plain.Error(), silent.Error())

	coded := sdk.WithCode(7, exitcode.Silent(errors.New("module exited with status 7")))
	assert.True(t, exitcode.IsSilent(coded))
	assert.Equal(t, 7, sdk.ExitCode(coded))
}

// TestReport pins that an error is logged as one error record and that a
// silent or nil one is not logged at all. It swaps the process logger, so
// it does not run in parallel.
//
//nolint:paralleltest // Replaces slog.Default for the duration of the test.
func TestReport(t *testing.T) {
	var buf bytes.Buffer

	previous := slog.Default()

	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	exitcode.Report(nil)
	exitcode.Report(exitcode.Silent(errors.New("already said")))
	require.Empty(t, buf.String())

	exitcode.Report(state(errors.New("lock is out of date")))
	assert.Equal(t, 1, bytes.Count(buf.Bytes(), []byte("\n")), buf.String())
	assert.Contains(t, buf.String(), "level=ERROR")
	assert.Contains(t, buf.String(), `msg="lock is out of date"`)
}
