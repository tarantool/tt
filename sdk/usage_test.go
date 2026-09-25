package sdk_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk"
)

func TestUsageError(t *testing.T) {
	t.Parallel()

	t.Run("found through wrapping", func(t *testing.T) {
		t.Parallel()

		err := fmt.Errorf("parsing flags: %w", sdk.Usagef("bad port %q", "x"))

		var usage *sdk.UsageError

		require.ErrorAs(t, err, &usage)
		assert.Equal(t, `bad port "x"`, usage.Error())
		assert.Equal(t, `parsing flags: bad port "x"`, err.Error())
		assert.Equal(t, sdk.ExitFailure, sdk.ExitCode(err))
	})

	t.Run("wraps its cause", func(t *testing.T) {
		t.Parallel()

		err := sdk.WithUsage(errBase)

		require.ErrorIs(t, err, errBase)
		assert.Equal(t, "base", err.Error())
	})

	t.Run("keeps a deeper exit code", func(t *testing.T) {
		t.Parallel()

		err := sdk.WithUsage(sdk.WithCode(sdk.ExitPartial, errBase))

		assert.Equal(t, sdk.ExitPartial, sdk.ExitCode(err))
	})

	t.Run("a plain error is not one", func(t *testing.T) {
		t.Parallel()

		var usage *sdk.UsageError

		assert.NotErrorAs(t, errors.New("plain"), &usage)
	})

	t.Run("nil is refused", func(t *testing.T) {
		t.Parallel()

		assert.PanicsWithValue(t, "sdk.WithUsage: nil error", func() {
			_ = sdk.WithUsage(nil)
		})
	})
}
