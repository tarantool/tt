package xlog

import (
	"errors"
	"math"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/tarantool/go-xlog/format"

	"github.com/tarantool/tt/cli/backup"
)

func TestToFormatVClock_RoundTrip(t *testing.T) {
	src := backup.Vclock{0: 0, 1: 42, 2: 9000}

	got, err := toFormatVClock(src)
	require.NoError(t, err)
	require.Equal(t, format.VClock{0: 0, 1: 42, 2: 9000}, got)

	back, err := fromFormatVClock(got)
	require.NoError(t, err)
	require.Equal(t, src, back)
}

func TestToFormatVClock_Empty(t *testing.T) {
	got, err := toFormatVClock(backup.Vclock{})
	require.NoError(t, err)
	require.Equal(t, format.VClock{}, got)
}

func TestToFormatVClock_Nil(t *testing.T) {
	got, err := toFormatVClock(nil)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestToFormatVClock_Overflow(t *testing.T) {
	src := backup.Vclock{1: math.MaxInt64 + 1}

	_, err := toFormatVClock(src)
	require.ErrorIs(t, err, ErrLSNOverflow)
}

func TestToFormatVClock_MaxInt64OK(t *testing.T) {
	src := backup.Vclock{1: math.MaxInt64}

	got, err := toFormatVClock(src)
	require.NoError(t, err)
	require.Equal(t, int64(math.MaxInt64), got[1])
}

func TestFromFormatVClock_Negative(t *testing.T) {
	src := format.VClock{1: -1}

	_, err := fromFormatVClock(src)
	require.ErrorIs(t, err, ErrLSNOverflow)
}

func TestFromFormatVClock_Nil(t *testing.T) {
	got, err := fromFormatVClock(nil)
	require.NoError(t, err)
	require.Empty(t, got)
}

func TestVClock_RoundTripBoundaries(t *testing.T) {
	src := backup.Vclock{
		0:  0,
		1:  1,
		7:  math.MaxInt64,
		13: 1 << 40,
	}

	fv, err := toFormatVClock(src)
	require.NoError(t, err)

	back, err := fromFormatVClock(fv)
	require.NoError(t, err)
	require.Equal(t, src, back)
}

func TestVClock_OverflowIsSentinel(t *testing.T) {
	_, err := toFormatVClock(backup.Vclock{1: math.MaxUint64})
	require.True(t, errors.Is(err, ErrLSNOverflow))
}
