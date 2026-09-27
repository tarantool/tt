package integrity_test

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/pflag"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/integrity"
)

// assertOnlyFlag checks that flags holds exactly the flag name, of the type
// typeName, with the default defValue.
func assertOnlyFlag(t *testing.T, flags *pflag.FlagSet, name, typeName, defValue string) {
	t.Helper()

	var names []string

	flags.VisitAll(func(flag *pflag.Flag) { names = append(names, flag.Name) })
	assert.Equal(t, []string{name}, names)

	flag := flags.Lookup(name)
	require.NotNil(t, flag)
	assert.Equal(t, typeName, flag.Value.Type())
	assert.Equal(t, defValue, flag.DefValue)
	assert.NotEmpty(t, flag.Usage)
}

func TestRSAProviderRegistersFlags(t *testing.T) {
	provider := integrity.NewRSAProvider()

	t.Run("with-integrity-check", func(t *testing.T) {
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		privateKey := "default.pem"

		provider.RegisterWithIntegrityFlag(flags, &privateKey)
		assertOnlyFlag(t, flags, "with-integrity-check", "string", "default.pem")

		require.NoError(t, flags.Parse([]string{"--with-integrity-check=private.pem"}))
		assert.Equal(t, "private.pem", privateKey)
	})

	t.Run("integrity-check", func(t *testing.T) {
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		publicKey := "default.pem"

		provider.RegisterIntegrityCheckFlag(flags, &publicKey)
		assertOnlyFlag(t, flags, "integrity-check", "string", "default.pem")

		require.NoError(t, flags.Parse([]string{"--integrity-check=public.pem"}))
		assert.Equal(t, "public.pem", publicKey)
	})

	t.Run("integrity-check-period", func(t *testing.T) {
		flags := pflag.NewFlagSet("test", pflag.ContinueOnError)
		period := 42

		provider.RegisterIntegrityCheckPeriodFlag(flags, &period)
		assertOnlyFlag(t, flags, "integrity-check-period", "int", "42")

		// No range is checked.
		require.NoError(t, flags.Parse([]string{"--integrity-check-period=-5"}))
		assert.Equal(t, -5, period)
	})
}

func TestRSAProviderHashesFileName(t *testing.T) {
	assert.Equal(t, "hashes.json", integrity.NewRSAProvider().HashesFileName())
}

// requirePrivateKeyRefused checks that every function of the provider that
// loads a private key refuses the one at path with an error that contains
// text, and returns nothing else.
func requirePrivateKeyRefused(t *testing.T, path, text string) {
	t.Helper()

	provider := integrity.NewRSAProvider()

	signer, err := provider.NewSigner(path)
	require.ErrorContains(t, err, text)
	assert.Nil(t, signer)

	sign, err := provider.GetSignFunction(path)
	require.ErrorContains(t, err, text)
	assert.Nil(t, sign)

	hashers, signers, err := provider.GetStorageSigners(path)
	require.ErrorContains(t, err, text)
	assert.Nil(t, hashers)
	assert.Nil(t, signers)
}

// requirePrivateKeyAccepted checks that every function of the provider
// that loads a private key accepts the one at path.
func requirePrivateKeyAccepted(t *testing.T, path string) {
	t.Helper()

	provider := integrity.NewRSAProvider()

	signer, err := provider.NewSigner(path)
	require.NoError(t, err)
	assert.NotNil(t, signer)

	sign, err := provider.GetSignFunction(path)
	require.NoError(t, err)
	assert.NotNil(t, sign)

	hashers, signers, err := provider.GetStorageSigners(path)
	require.NoError(t, err)
	assert.Len(t, hashers, 1)
	assert.Len(t, signers, 1)
}

func TestRSAProviderRefusesPrivateKeys(t *testing.T) {
	dir := t.TempDir()
	key := signingKey(t)

	missing := filepath.Join(dir, "missing.pem")

	notPEM := filepath.Join(dir, "not-pem.pem")
	writeFile(t, notPEM, "not a key\n", 0o600)

	garbage := filepath.Join(dir, "garbage.pem")
	writeFile(t, garbage, string(pem.EncodeToMemory(
		&pem.Block{Type: "RSA PRIVATE KEY", Bytes: []byte("garbage")})), 0o600)

	pkcs8Der, err := x509.MarshalPKCS8PrivateKey(key)
	require.NoError(t, err)

	pkcs8 := filepath.Join(dir, "pkcs8.pem")
	writeFile(t, pkcs8, string(pem.EncodeToMemory(
		&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8Der})), 0o600)

	cases := []struct {
		name string
		path string
		text string
	}{
		{name: "missing", path: missing, text: "stat " + missing + ": no such file or directory"},
		{name: "not PEM", path: notPEM, text: "no pem block found"},
		{name: "not a key", path: garbage, text: garbage},
		{name: "not PKCS #1", path: pkcs8, text: pkcs8},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			requirePrivateKeyRefused(t, testCase.path, testCase.text)
		})
	}

	for _, mode := range []os.FileMode{0o640, 0o604, 0o700, 0o660, 0o644, 0o777} {
		t.Run(fmt.Sprintf("mode %#o", mode), func(t *testing.T) {
			path := writePrivateKey(t, t.TempDir(), key)
			require.NoError(t, os.Chmod(path, mode))

			requirePrivateKeyRefused(t, path,
				fmt.Sprintf("permissions for private key file %q are too open", path))
		})
	}
}

func TestRSAProviderAcceptsPrivateKeys(t *testing.T) {
	key := signingKey(t)

	for _, mode := range []os.FileMode{0o600, 0o400} {
		t.Run(fmt.Sprintf("mode %#o", mode), func(t *testing.T) {
			path := writePrivateKey(t, t.TempDir(), key)
			require.NoError(t, os.Chmod(path, mode))

			requirePrivateKeyAccepted(t, path)
		})
	}

	t.Run("symbolic link", func(t *testing.T) {
		// The permissions checked are those of the key, not of the link.
		path := filepath.Join(t.TempDir(), "link.pem")
		symlink(t, writePrivateKey(t, t.TempDir(), key), path)

		requirePrivateKeyAccepted(t, path)
	})
}

func TestRSAProviderRefusesPublicKeys(t *testing.T) {
	dir := t.TempDir()
	key := signingKey(t)

	edPublic, _, err := ed25519.GenerateKey(rand.Reader)
	require.NoError(t, err)

	edDer, err := x509.MarshalPKIXPublicKey(edPublic)
	require.NoError(t, err)

	cases := []struct {
		name    string
		content string
		text    string
	}{
		{name: "not PEM", content: "not a key\n", text: "no pem block found"},
		{
			name: "not a key",
			content: string(pem.EncodeToMemory(
				&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("garbage")})),
			text: "failed to parse public key",
		},
		{
			name: "PKCS #1",
			content: string(pem.EncodeToMemory(&pem.Block{
				Type: "RSA PUBLIC KEY", Bytes: x509.MarshalPKCS1PublicKey(&key.PublicKey),
			})),
			text: "failed to parse public key",
		},
		{
			name:    "not RSA",
			content: string(pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: edDer})),
			text:    "not an RSA key",
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			path := filepath.Join(dir, "public.pem")
			writeFile(t, path, testCase.content, 0o644)

			_, err := integrity.NewRSAProvider().InitializeIntegrityCheck(path, t.TempDir())
			require.ErrorContains(t, err, testCase.text)
			require.ErrorContains(t, err, path)
		})
	}

	t.Run("missing", func(t *testing.T) {
		path := filepath.Join(dir, "missing.pem")

		_, err := integrity.NewRSAProvider().InitializeIntegrityCheck(path, t.TempDir())
		require.ErrorIs(t, err, os.ErrNotExist)
		require.ErrorContains(t, err, path)
	})
}
