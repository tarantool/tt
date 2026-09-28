//go:build linux || darwin

package integrity_test

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/integrity"
)

// promptTimeout is how long a call given a FIFO may take: it fails at once
// or it waits for a writer that never comes.
const promptTimeout = 5 * time.Second

// promptly runs call and returns its error, failing the test when call
// has not returned within promptTimeout, so that a call blocked on a FIFO
// fails the test instead of hanging the suite. call runs in a goroutine of
// its own and must not use the test.
func promptly(t *testing.T, call func() error) error {
	t.Helper()

	done := make(chan error, 1)

	go func() { done <- call() }()

	select {
	case err := <-done:
		return err
	case <-time.After(promptTimeout):
		t.Fatalf("the call has not returned in %s: it waits on a FIFO", promptTimeout)

		return nil
	}
}

// requireFIFORefused checks that err refuses the FIFO at path by its name
// and type.
func requireFIFORefused(t *testing.T, err error, path string) {
	t.Helper()

	require.ErrorContains(t, err, path)
	require.ErrorContains(t, err, "not a regular file: a named pipe")
}

// replaceWithFIFO replaces the file at path with a FIFO anyone may read.
func replaceWithFIFO(t *testing.T, path string) {
	t.Helper()

	require.NoError(t, os.Remove(path))
	mkfifo(t, path, 0o644)
}

// initializePromptly checks the environment in configDir with the public
// key at publicKeyPath, failing the test when the check blocks.
func initializePromptly(t *testing.T, publicKeyPath, configDir string) error {
	t.Helper()

	return promptly(t, func() error {
		_, err := integrity.NewRSAProvider().InitializeIntegrityCheck(publicKeyPath, configDir)

		return err
	})
}

func TestRSACheckRefusesFIFOs(t *testing.T) {
	key := signingKey(t)
	publicKey := writePublicKey(t, t.TempDir(), key)

	for _, name := range []string{
		"env_hashes.json", "env_hashes.json.sig", "hashes.json", "hashes.json.sig",
	} {
		t.Run(name, func(t *testing.T) {
			base := signedEnvironment(t, key)
			path := filepath.Join(base, name)
			replaceWithFIFO(t, path)

			requireFIFORefused(t, initializePromptly(t, publicKey, base), path)
		})
	}

	t.Run("listed file", func(t *testing.T) {
		base := signedEnvironment(t, key)
		path := filepath.Join(base, "init.lua")
		replaceWithFIFO(t, path)

		requireFIFORefused(t, initializePromptly(t, publicKey, base), resolve(t, path))
	})

	t.Run("public key", func(t *testing.T) {
		base := signedEnvironment(t, key)
		path := filepath.Join(t.TempDir(), "public.pem")
		mkfifo(t, path, 0o644)

		requireFIFORefused(t, initializePromptly(t, path, base), path)
	})
}

func TestRSASignerRefusesFIFOs(t *testing.T) {
	key := signingKey(t)

	t.Run("private key", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "private.pem")
		mkfifo(t, path, 0o600)

		provider := integrity.NewRSAProvider()

		err := promptly(t, func() error {
			_, err := provider.NewSigner(path)

			return err
		})
		requireFIFORefused(t, err, path)

		err = promptly(t, func() error {
			_, err := provider.GetSignFunction(path)

			return err
		})
		requireFIFORefused(t, err, path)

		err = promptly(t, func() error {
			_, _, err := provider.GetStorageSigners(path)

			return err
		})
		requireFIFORefused(t, err, path)
	})

	for _, name := range []string{"tt.yaml", "config.yaml"} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			path := filepath.Join(base, name)

			writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
			writeFile(t, filepath.Join(base, "config.yaml"), "app: {}\n", 0o644)
			writeFile(t, filepath.Join(base, "init.lua"), "init\n", 0o644)
			replaceWithFIFO(t, path)

			signer := newSigner(t, key)

			err := promptly(t, func() error { return signer.Sign(base, nil) })
			requireFIFORefused(t, err, path)
		})
	}
}

func TestRSARepositoryRefusesFIFOs(t *testing.T) {
	key := signingKey(t)

	// A FIFO put in place of a signed file after the check: it is refused
	// before anything is read from it.
	t.Run("checked", func(t *testing.T) {
		base := signedEnvironment(t, key)
		path := filepath.Join(base, "init.lua")

		ctx, err := initializeCheck(t, key, base)
		require.NoError(t, err)

		replaceWithFIFO(t, path)
		requireRepositoryRefusesFIFO(t, ctx.Repository, path, resolve(t, path))
	})

	t.Run("unchecked", func(t *testing.T) {
		base := signedEnvironment(t, key)
		path := filepath.Join(base, "init.lua")

		ctx, err := integrity.NewRSAProvider().InitializeIntegrityCheck("", base)
		require.NoError(t, err)

		replaceWithFIFO(t, path)
		requireRepositoryRefusesFIFO(t, ctx.Repository, path, path)
	})
}

// requireRepositoryRefusesFIFO checks that Read and ReadFile of repository
// refuse the FIFO at path at once, naming it as named.
func requireRepositoryRefusesFIFO(
	t *testing.T, repository integrity.Repository, path, named string,
) {
	t.Helper()

	err := promptly(t, func() error {
		reader, err := repository.Read(path)
		if err != nil {
			return err
		}

		_, err = io.ReadAll(reader)

		return err
	})
	requireFIFORefused(t, err, named)

	err = promptly(t, func() error {
		_, err := repository.ReadFile(path)

		return err
	})
	requireFIFORefused(t, err, named)
}
