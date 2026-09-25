package integrity_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/integrity"
)

func TestNewSigner(t *testing.T) {
	for _, path := range []string{"", "private.pem"} {
		signer, err := integrity.NewSigner(path)
		require.Nil(t, signer)
		require.EqualError(t, err, "integrity signer should never be created in ce")
	}
}

func TestInitializeIntegrityCheckWithKey(t *testing.T) {
	_, err := integrity.InitializeIntegrityCheck("public.pem", "app")
	require.EqualError(t, err, "integrity checks should never be initialized in ce")
}

func TestInitializeIntegrityCheckWithoutKey(t *testing.T) {
	ctx, err := integrity.InitializeIntegrityCheck("", "app")
	require.NoError(t, err)
	require.NotNil(t, ctx.Repository)
}

func TestRegisterNoopFlagFuncs(t *testing.T) {
	var (
		text   string
		period int
	)

	flags := &pflag.FlagSet{}

	integrity.RegisterWithIntegrityFlag(flags, &text)
	integrity.RegisterIntegrityCheckFlag(flags, &text)
	integrity.RegisterIntegrityCheckPeriodFlag(flags, &period)

	require.False(t, flags.HasFlags(), "noop flag registrars must not modify the flag set")
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

func TestGetStorageVerifiers(t *testing.T) {
	hashers, verifiers, err := integrity.GetStorageVerifiers(integrity.IntegrityCtx{})
	require.Nil(t, hashers)
	require.Nil(t, verifiers)
	require.ErrorIs(t, err, integrity.ErrNotConfigured)
}

func TestGetStorageSigners(t *testing.T) {
	hashers, signers, err := integrity.GetStorageSigners("private.pem")
	require.Nil(t, hashers)
	require.Nil(t, signers)
	require.ErrorIs(t, err, integrity.ErrNoSignerInCE)
}

func TestDisabledProvider(t *testing.T) {
	provider := integrity.NewDisabledProvider()

	var (
		flagValue  string
		flagPeriod int
	)

	fs := &pflag.FlagSet{}
	provider.RegisterWithIntegrityFlag(fs, &flagValue)
	provider.RegisterIntegrityCheckFlag(fs, &flagValue)
	provider.RegisterIntegrityCheckPeriodFlag(fs, &flagPeriod)
	assert.False(t, fs.HasFlags(), "the disabled provider must not register flags")

	assert.Equal(t, integrity.HashesFileName, provider.HashesFileName())

	_, err := provider.InitializeIntegrityCheck("public.pem", "app")
	require.ErrorIs(t, err, integrity.ErrNoVerifierInCE)

	ctx, err := provider.InitializeIntegrityCheck("", t.TempDir())
	require.NoError(t, err)
	require.NoError(t, ctx.Repository.ValidateAll())

	path := filepath.Join(t.TempDir(), "file")
	require.NoError(t, os.WriteFile(path, []byte("content"), 0o600))

	f, err := ctx.Repository.Read(path)
	require.NoError(t, err)

	data, err := io.ReadAll(f)
	require.NoError(t, f.Close())
	require.NoError(t, err)
	assert.Equal(t, "content", string(data))

	signer, err := provider.NewSigner("private.pem")
	assert.Nil(t, signer)
	require.ErrorIs(t, err, integrity.ErrNoSignerInCE)

	check, err := provider.GetCheckFunction(ctx)
	assert.Nil(t, check)
	require.ErrorIs(t, err, integrity.ErrNotConfigured)

	sign, err := provider.GetSignFunction("private.pem")
	assert.Nil(t, sign)
	require.ErrorIs(t, err, integrity.ErrNoSignerInCE)

	hashers, verifiers, err := provider.GetStorageVerifiers(ctx)
	assert.Nil(t, hashers)
	assert.Nil(t, verifiers)
	require.ErrorIs(t, err, integrity.ErrNotConfigured)

	hashers, signers, err := provider.GetStorageSigners("private.pem")
	assert.Nil(t, hashers)
	assert.Nil(t, signers)
	require.ErrorIs(t, err, integrity.ErrNoSignerInCE)
}
