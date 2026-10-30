package install

import (
	"archive/tar"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/klauspost/compress/zstd"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/pack"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/internal/binary"
)

// testChecks are the Integrity of an install with the RSA provider, as tt
// package install --integrity-check wires them.
type testChecks struct {
	publicKey string
}

func (checks testChecks) VerifyTree(dir string) error {
	return integrity.NewRSAProvider().VerifyTree(checks.publicKey, dir)
}

func (checks testChecks) VerifyFile(root, file string) error {
	ctx, err := integrity.NewRSAProvider().InitializeIntegrityCheck(checks.publicKey, root)
	if err != nil {
		return err
	}

	reader, err := ctx.Repository.Read(file)
	if err != nil {
		return err
	}

	return reader.Close()
}

// signingKeys writes an RSA key pair and returns the signer of its private
// half and the checks of its public half.
func signingKeys(t *testing.T) (integrity.Signer, testChecks) {
	t.Helper()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)

	public, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)

	dir := t.TempDir()
	privatePath := filepath.Join(dir, "private.pem")
	publicPath := filepath.Join(dir, "public.pem")

	require.NoError(t, os.WriteFile(privatePath, pem.EncodeToMemory(&pem.Block{
		Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key),
	}), 0o600))
	require.NoError(t, os.WriteFile(publicPath, pem.EncodeToMemory(&pem.Block{
		Type: "PUBLIC KEY", Bytes: public,
	}), 0o644))

	signer, err := integrity.NewRSAProvider().NewSigner(privatePath)
	require.NoError(t, err)

	return signer, testChecks{publicKey: publicPath}
}

// runtimeCache writes a runtime cache with tarantool 3.1.0, headers
// included, and tt 3.1.5.
func runtimeCache(t *testing.T) string {
	t.Helper()

	cache := t.TempDir()
	writeSource(t, cache, map[string]string{
		"tarantool/ce/3.1.0/bin/tarantool":              "#!/bin/sh\necho tarantool\n",
		"tarantool/ce/3.1.0/LICENSE":                    "BSD-2-Clause",
		"tarantool/ce/3.1.0/include/tarantool/module.h": "header\n",
		"tt/ce/3.1.5/bin/tt":                            "#!/bin/sh\necho tt\n",
	})

	return cache
}

// packRoundtrip packs the dependency-free roundtrip package with deps and
// its runtime, signed by signer when it is set.
func packRoundtrip(t *testing.T, signer integrity.Signer) string {
	t.Helper()

	src := t.TempDir()
	writeSource(t, src, map[string]string{
		"app.manifest.toml":                 roundtripManifest,
		"VERSION":                           "1.0.0\n",
		"init.lua":                          "return 1\n",
		"README.md":                         "round-app\n",
		".templates/default/instances.yaml": "\"*\":\n",
		".templates/default/config.yaml":    "groups: {}\n",
	})

	packed, err := pack.Run(context.Background(), pack.Options{
		ProjectDir: src,
		OutputDir:  filepath.Join(src, "_out"),
		Signer:     signer,
		Runtime:    pack.RuntimeOptions{CacheDir: runtimeCache(t)},
		Build: build.Options{
			TtVersion: "tt test",
			Tarantool: rocks.TarantoolInfo{Prefix: "/nonexistent", Version: "3.1.0"},
		},
	})
	require.NoError(t, err)

	return packed.Path
}

// readEntries returns the regular files of an archive by name.
func readEntries(t *testing.T, path string) map[string]string {
	t.Helper()

	file, err := os.Open(path)
	require.NoError(t, err)

	defer func() { _ = file.Close() }()

	decoder, err := zstd.NewReader(file)
	require.NoError(t, err)

	defer decoder.Close()

	entries := map[string]string{}
	reader := tar.NewReader(decoder)

	for {
		header, err := reader.Next()
		if errors.Is(err, io.EOF) {
			return entries
		}

		require.NoError(t, err)

		if header.Typeflag != tar.TypeReg {
			continue
		}

		data, err := io.ReadAll(reader)
		require.NoError(t, err)

		entries[header.Name] = string(data)
	}
}

// installSigned installs archive into the user scope with checks.
func installSigned(t *testing.T, archive string, checks Integrity) error {
	t.Helper()

	_, err := Run(context.Background(), Options{
		ProjectDir: t.TempDir(),
		Scope:      ScopeUser,
		Archives:   []string{archive},
		Yes:        true,
		Integrity:  checks,
	})

	return err
}

func TestInstallSignedArchive(t *testing.T) {
	root := userRoot(t)
	signer, checks := signingKeys(t)

	require.NoError(t, installSigned(t, packRoundtrip(t, signer), checks))

	version := filepath.Join(root, "versions", "round-app", "round-app_1.0.0")
	require.NoError(t, checks.VerifyTree(version))

	// The runtime is installed with its signature, and its executable checks.
	for _, component := range []struct {
		program binary.Program
		version string
	}{
		{binary.ProgramCe, "3.1.0"},
		{binary.ProgramTt, "3.1.5"},
	} {
		dir := binary.InstallationPath(root, component.program, component.version)
		require.FileExists(t, filepath.Join(dir, "env_hashes.json"))
		require.NoError(t, checks.VerifyTree(dir))
		require.NoError(t, checks.VerifyFile(dir,
			filepath.Join(dir, component.program.Exec())))
	}
}

func TestInstallSignedRefusals(t *testing.T) {
	signer, checks := signingKeys(t)
	signed := packRoundtrip(t, signer)

	t.Run("unsigned archive", func(t *testing.T) {
		userRoot(t)

		err := installSigned(t, packRoundtrip(t, nil), checks)
		require.ErrorContains(t, err, "integrity check failed")
		require.ErrorContains(t, err, "not signed")
	})

	t.Run("foreign key", func(t *testing.T) {
		userRoot(t)

		_, other := signingKeys(t)

		err := installSigned(t, signed, other)
		require.ErrorContains(t, err, "integrity check failed")
	})

	tamper := func(t *testing.T, name, content string) string {
		t.Helper()

		entries := readEntries(t, signed)

		entries[name] = content

		path := filepath.Join(t.TempDir(), filepath.Base(signed))
		writeTarZst(t, path, entries)

		return path
	}

	t.Run("tampered package", func(t *testing.T) {
		root := userRoot(t)

		err := installSigned(t, tamper(t, ".rocks/share/tarantool/round-app/init.lua",
			"os.exit(1)\n"), checks)
		require.ErrorContains(t, err, "hash mismatch")
		assert.NoDirExists(t,
			filepath.Join(root, "versions", "round-app", "round-app_1.0.0"))
	})

	t.Run("file added to the package", func(t *testing.T) {
		userRoot(t)

		err := installSigned(t, tamper(t, ".rocks/share/tarantool/round-app/evil.lua",
			"os.exit(1)\n"), checks)
		require.ErrorContains(t, err, "not listed in the hashes files")
	})

	t.Run("tampered runtime", func(t *testing.T) {
		root := userRoot(t)

		err := installSigned(t, tamper(t, "_runtime/tt/bin/tt", "#!/bin/sh\nevil\n"), checks)
		require.ErrorContains(t, err, "integrity check failed")
		assert.NoDirExists(t, binary.InstallationPath(root, binary.ProgramTt, "3.1.5"))
	})

	t.Run("project scope", func(t *testing.T) {
		_, err := Run(context.Background(), Options{
			ProjectDir: t.TempDir(),
			Scope:      ScopeProject,
			Archives:   []string{signed},
			Yes:        true,
			Integrity:  checks,
		})
		require.ErrorIs(t, err, errSignedIntoProject)
	})

	t.Run("without deps", func(t *testing.T) {
		userRoot(t)

		src := t.TempDir()
		writeSource(t, src, map[string]string{
			"app.manifest.toml": roundtripManifest,
			"VERSION":           "1.0.0\n",
			"init.lua":          "return 1\n",
			"README.md":         "round-app\n",
		})

		packed, err := pack.Run(context.Background(), pack.Options{
			ProjectDir:  src,
			WithoutDeps: true,
			OutputDir:   filepath.Join(src, "_out"),
			Build: build.Options{
				TtVersion: "tt test",
				Tarantool: rocks.TarantoolInfo{Prefix: "/nonexistent", Version: "3.0.0"},
			},
		})
		require.NoError(t, err)

		require.ErrorIs(t, installSigned(t, packed.Path, checks), errSignedWithoutDeps)
	})
}

func TestInstallSignedOnInstalledRuntime(t *testing.T) {
	signer, checks := signingKeys(t)
	signed := packRoundtrip(t, signer)

	// An installation tt binary laid before, without hashes files: the same
	// executable gets the signature of the archive.
	t.Run("same", func(t *testing.T) {
		root := userRoot(t)
		dir := binary.InstallationPath(root, binary.ProgramTt, "3.1.5")
		writeSource(t, dir, map[string]string{"tt": "#!/bin/sh\necho tt\n"})

		require.NoError(t, installSigned(t, signed, checks))
		require.NoError(t, checks.VerifyFile(dir, filepath.Join(dir, "tt")))
	})

	// Another executable is refused, and the signature is not left there.
	t.Run("other", func(t *testing.T) {
		root := userRoot(t)
		dir := binary.InstallationPath(root, binary.ProgramTt, "3.1.5")
		writeSource(t, dir, map[string]string{"tt": "#!/bin/sh\necho other\n"})

		err := installSigned(t, signed, checks)
		require.ErrorIs(t, err, errRuntimeNotSigned)
		assert.NoFileExists(t, filepath.Join(dir, "env_hashes.json"))
		assert.NoFileExists(t, filepath.Join(dir, "env_hashes.json.sig"))
	})

	// Without checks an installed runtime is left as it is, as always.
	t.Run("unchecked", func(t *testing.T) {
		root := userRoot(t)
		dir := binary.InstallationPath(root, binary.ProgramTt, "3.1.5")
		writeSource(t, dir, map[string]string{"tt": "#!/bin/sh\necho other\n"})

		require.NoError(t, installSigned(t, signed, nil))
		assert.NoFileExists(t, filepath.Join(dir, "env_hashes.json"))
	})
}
