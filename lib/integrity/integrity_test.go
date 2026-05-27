package integrity_test

import (
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/lib/integrity"
)

func TestNewSigner(t *testing.T) {
	for _, path := range []string{"", "private.pem"} {
		signer, err := integrity.NewSigner(path)
		require.Nil(t, signer)
		require.EqualError(t, err, "integrity signer should never be created in ce")
	}
}

func TestInitializeIntegrityCheckWithKey(t *testing.T) {
	_, err := integrity.InitializeIntegrityCheck("public.pem", "app", "instances.enabled")
	require.EqualError(t, err, "integrity checks should never be initialized in ce")
}

func TestInitializeIntegrityCheckWithoutKey(t *testing.T) {
	ctx, err := integrity.InitializeIntegrityCheck("", "app", "instances.enabled")
	require.NoError(t, err)
	require.NotNil(t, ctx.Repository)
}

func TestRegisterNoopFlagFuncs(t *testing.T) {
	var s string
	var i int
	fs := &pflag.FlagSet{}

	integrity.RegisterWithIntegrityFlag(fs, &s)
	integrity.RegisterIntegrityCheckFlag(fs, &s)
	integrity.RegisterIntegrityCheckPeriodFlag(fs, &i)

	require.False(t, fs.HasFlags(), "noop flag registrars must not modify the flag set")
}

func TestGetCheckFunction(t *testing.T) {
	fn, err := integrity.GetCheckFunction(integrity.IntegrityCtx{})
	require.Nil(t, fn)
	require.ErrorIs(t, err, integrity.ErrNotConfigured)
}

func TestGetSignFunction(t *testing.T) {
	fn, err := integrity.GetSignFunction("")
	require.Nil(t, fn)
	require.EqualError(t, err, "integrity signer should never be created in ce")
}
