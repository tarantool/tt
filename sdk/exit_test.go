package sdk_test

import (
	"errors"
	"fmt"
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
)

var errBase = errors.New("base")

// multiCodeError is an ExitCoder that wraps several errors, the shape of a
// command that aggregates per-target failures.
type multiCodeError struct {
	code  int
	inner []error
}

func (m *multiCodeError) Error() string   { return "multi" }
func (m *multiCodeError) ExitStatus() int { return m.code }
func (m *multiCodeError) Unwrap() []error { return m.inner }

func TestExitCode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		err  error
		want int
	}{
		{name: "nil", err: nil, want: sdk.ExitOK},
		{name: "plain error", err: errBase, want: sdk.ExitFailure},
		{
			name: "exit error",
			err:  sdk.WithCode(sdk.ExitPartial, errBase),
			want: sdk.ExitPartial,
		},
		{
			name: "wrapped exit error",
			err:  fmt.Errorf("outer: %w", sdk.WithCode(sdk.ExitSystem, errBase)),
			want: sdk.ExitSystem,
		},
		{
			name: "code 1 does not hide a code beneath it",
			err: sdk.Errorf(sdk.ExitFailure, "building: %w",
				fmt.Errorf("backend: %w", sdk.WithCode(sdk.ExitSystem, errBase))),
			want: sdk.ExitSystem,
		},
		{
			name: "outer non-generic code wins over an inner one",
			err: sdk.Errorf(sdk.ExitPartial, "some failed: %w",
				sdk.WithCode(sdk.ExitSystem, errBase)),
			want: sdk.ExitPartial,
		},
		{
			name: "code 1 over a plain error",
			err:  sdk.Errorf(sdk.ExitFailure, "resolving: %w", errBase),
			want: sdk.ExitFailure,
		},
		{
			name: "child process status is not an exit code",
			err: fmt.Errorf("running luatest: %w",
				&exec.ExitError{ProcessState: nil, Stderr: nil}),
			want: sdk.ExitFailure,
		},
		{
			name: "code 1 carrier with several wrapped errors",
			err: &multiCodeError{code: sdk.ExitFailure, inner: []error{
				errBase, sdk.WithCode(sdk.ExitSystem, errBase),
			}},
			want: sdk.ExitSystem,
		},
		{
			name: "joined errors: siblings of a code 1 carrier are not searched",
			err: errors.Join(
				sdk.WithCode(sdk.ExitFailure, errBase),
				sdk.WithCode(sdk.ExitPartial, errBase),
			),
			want: sdk.ExitFailure,
		},
		{
			name: "joined errors: first non-generic carrier",
			err: errors.Join(errBase,
				sdk.WithCode(sdk.ExitPartial, errBase),
				sdk.WithCode(sdk.ExitSystem, errBase)),
			want: sdk.ExitPartial,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, sdk.ExitCode(tc.err))
		})
	}
}

func TestExitError(t *testing.T) {
	t.Parallel()

	err := sdk.Errorf(sdk.ExitSystem, "dialing %s: %w", "localhost", errBase)

	assert.Equal(t, "dialing localhost: base", err.Error())
	require.ErrorIs(t, err, errBase)
	assert.Equal(t, sdk.ExitSystem, err.ExitStatus())

	var coder sdk.ExitCoder

	require.ErrorAs(t, fmt.Errorf("wrapped: %w", err), &coder)
	assert.Equal(t, sdk.ExitSystem, coder.ExitStatus())
}

func TestWithCodeNilPanics(t *testing.T) {
	t.Parallel()

	assert.PanicsWithValue(t, "sdk.WithCode: nil error", func() {
		_ = sdk.WithCode(sdk.ExitSystem, nil)
	})
}
