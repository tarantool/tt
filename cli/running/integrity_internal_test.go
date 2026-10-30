package running

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"maps"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/internal/integrityprovider"
)

// signingFixture signs applications and runtimes with an RSA key, with the
// RSA provider made the provider of the process for the test.
type signingFixture struct {
	signer    integrity.Signer
	publicKey string
}

func newSigningFixture(t *testing.T) signingFixture {
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

	integrityprovider.Set(integrity.NewRSAProvider())
	t.Cleanup(func() { integrityprovider.Set(nil) })

	signer, err := integrityprovider.Get().NewSigner(privatePath)
	require.NoError(t, err)

	return signingFixture{signer: signer, publicKey: publicPath}
}

// signApp signs the application directory dir as a package.
func (fixture signingFixture) signApp(t *testing.T, dir string) {
	t.Helper()

	require.NoError(t, fixture.signer.Sign(dir, integrity.SignOptions{}))
}

// signRuntime signs the runtime installed with its executable at path.
func (fixture signingFixture) signRuntime(t *testing.T, path string) {
	t.Helper()

	require.NoError(t, fixture.signer.SignFiles(filepath.Dir(path), []integrity.SignedFile{
		{Path: filepath.Base(path), Source: path},
	}))
}

// collect resolves app:* with the integrity checks of the fixture on, as
// the instance commands do.
func (fixture signingFixture) collect(t *testing.T, appScope app.Scope, start string) (
	[]InstanceCtx, error,
) {
	t.Helper()

	targets := []app.Target{{App: fixtureApp, Instance: app.AllInstances}}
	cmdCtx := &cmdcontext.CmdCtx{}

	cmdCtx.Cli.IntegrityCheck = fixture.publicKey

	return Collect(context.Background(), targets,
		app.Options{
			Scope:            appScope,
			Template:         "",
			Start:            start,
			Dir:              "",
			Integrity:        integrity.IntegrityCtx{Repository: nil},
			IntegrityFor:     AppIntegrity(cmdCtx),
			BestEffortConfig: false,
		})
}

func TestIntegrityChecksTheInstalledApplication(t *testing.T) {
	signing := newSigningFixture(t)
	fixture := newAppFixture(t, app.ScopeUser, appFiles("{}", ""))
	tarantoolPath := fixture.installTarantool(t, fixtureTarantool)

	signing.signApp(t, fixture.dir)
	signing.signRuntime(t, tarantoolPath)

	// The checks are rooted at the version, wherever tt runs.
	insts, err := signing.collect(t, app.ScopeUser, t.TempDir())
	require.NoError(t, err)
	require.Len(t, insts, 1)
	require.NotNil(t, insts[0].Integrity)

	ok, err := IsAbleToStartInstances(insts)
	require.NoError(t, err)
	assert.True(t, ok)

	spec, err := instanceSpec(&insts[0], specOptions{integrity: insts[0].Integrity})
	require.NoError(t, err)
	assert.Contains(t, spec.Args, filepath.Join(fixture.dir, "hashes.json"))

	// The runtime the application runs on is a root of its checks.
	reader, err := insts[0].Integrity.Repository.Read(tarantoolPath)
	require.NoError(t, err)
	require.NoError(t, reader.Close())

	// The watchdog is run by a tt the checks know only when it is signed.
	_, err = watchdogCommandLine(&cmdcontext.CmdCtx{}, filepath.Join(t.TempDir(), "tt"),
		&insts[0], nil, false)
	require.ErrorIs(t, err, errNotSigned)

	t.Run("tarantool not signed", func(t *testing.T) {
		require.NoError(t, os.Remove(filepath.Join(filepath.Dir(tarantoolPath),
			"env_hashes.json")))

		insts, err := signing.collect(t, app.ScopeUser, t.TempDir())
		require.NoError(t, err)

		_, err = IsAbleToStartInstances(insts)
		require.ErrorIs(t, err, errNotSigned)
		require.ErrorContains(t, err, tarantoolPath)
	})

	t.Run("tampered", func(t *testing.T) {
		writeFiles(t, fixture.dir, map[string]string{
			".templates/default/config.yaml": clusterConfig("credentials: {}\n"),
		})

		_, err := signing.collect(t, app.ScopeUser, t.TempDir())
		require.ErrorContains(t, err, "integrity check failed")
		require.ErrorContains(t, err, "hash mismatch")
	})
}

func TestIntegrityRefusesTheHost(t *testing.T) {
	signing := newSigningFixture(t)

	t.Run("tarantool from PATH", func(t *testing.T) {
		fixture := newAppFixture(t, app.ScopeProject, appFiles("{}", ""))

		bin := t.TempDir()
		fakeTarantool(t, filepath.Join(bin, "tarantool"))
		t.Setenv("PATH", bin)

		signing.signApp(t, fixture.dir)

		insts, err := signing.collect(t, app.ScopeProject, fixture.dir)
		require.NoError(t, err)

		_, err = IsAbleToStartInstances(insts)
		require.ErrorIs(t, err, errNotSigned)
		require.ErrorContains(t, err, "from PATH")
	})

	external := func(t *testing.T, record string, files map[string]string) appFixture {
		t.Helper()

		all := map[string]string{
			"app.manifest.toml":                 fixtureManifest,
			"app.manifest.lock":                 fixtureLock,
			".templates/default/instances.yaml": "gw: " + record + "\n",
		}

		maps.Copy(all, files)

		fixture := newAppFixture(t, app.ScopeUser, all)
		signing.signApp(t, fixture.dir)

		return fixture
	}

	t.Run("program from PATH", func(t *testing.T) {
		external(t, "{binary: gw}", nil)

		bin := t.TempDir()
		writeExecutable(t, filepath.Join(bin, "gw"))
		t.Setenv("PATH", bin)

		insts, err := signing.collect(t, app.ScopeUser, t.TempDir())
		require.NoError(t, err)

		require.ErrorContains(t, checkStart(&insts[0]), "not looked up in PATH")
	})

	t.Run("program of the package", func(t *testing.T) {
		all := map[string]string{
			"app.manifest.toml":                 fixtureManifest,
			"app.manifest.lock":                 fixtureLock,
			".templates/default/instances.yaml": "gw: {binary: gw}\n",
		}
		fixture := newAppFixture(t, app.ScopeUser, all)
		program := filepath.Join(fixture.dir, ".rocks", "bin", "gw")
		writeExecutable(t, program)
		signing.signApp(t, fixture.dir)

		insts, err := signing.collect(t, app.ScopeUser, t.TempDir())
		require.NoError(t, err)
		require.Len(t, insts, 1)

		path, err := ResolveBinary(&insts[0])
		require.NoError(t, err)
		assert.Equal(t, program, path)

		require.NoError(t, os.WriteFile(program, []byte("#!/bin/sh\nexit 1\n"), 0o755))

		_, err = ResolveBinary(&insts[0])
		require.ErrorIs(t, err, errNotSigned)
		require.ErrorContains(t, err, "hash mismatch")
	})

	t.Run("binary of the host", func(t *testing.T) {
		host := filepath.Join(t.TempDir(), "gw")
		writeExecutable(t, host)

		external(t, "{binary: "+host+"}", nil)

		insts, err := signing.collect(t, app.ScopeUser, t.TempDir())
		require.NoError(t, err)

		err = checkStart(&insts[0])
		require.ErrorIs(t, err, errNotSigned)
		require.ErrorContains(t, err, host)
	})

	t.Run("script of the host", func(t *testing.T) {
		script := filepath.Join(t.TempDir(), "init.lua")
		writeFiles(t, filepath.Dir(script), map[string]string{"init.lua": "return\n"})

		fixture := newAppFixture(t, app.ScopeUser, map[string]string{
			"app.manifest.toml":                 fixtureManifest,
			"app.manifest.lock":                 fixtureLock,
			".templates/default/instances.yaml": "worker: {script: " + script + "}\n",
		})
		signing.signApp(t, fixture.dir)

		insts, err := signing.collect(t, app.ScopeUser, t.TempDir())
		require.NoError(t, err)

		require.ErrorIs(t, checkStart(&insts[0]), errNotSigned)
	})
}

func TestIntegrityRefusesThePairOfTheHost(t *testing.T) {
	signing := newSigningFixture(t)
	fixture := newAppFixture(t, app.ScopeUser, appFiles("{}", ""))

	signing.signApp(t, fixture.dir)

	// The pair of the host is not part of the package: its files are not
	// signed, and an application that runs from it is refused.
	writeFiles(t, app.HostDir(fixture.root, fixtureApp), map[string]string{
		"instances.yaml": fixtureInst + ":\n",
		"config.yaml":    clusterConfig(""),
	})

	_, err := signing.collect(t, app.ScopeUser, t.TempDir())
	require.ErrorContains(t, err, "file is not in repository")
	require.ErrorContains(t, err, app.HostDir(fixture.root, fixtureApp))
}

func TestIntegrityOffChangesNothing(t *testing.T) {
	fixture := newAppFixture(t, app.ScopeUser, appFiles("{}", ""))
	fixture.installTarantool(t, fixtureTarantool)

	targets := []app.Target{{App: fixtureApp, Instance: app.AllInstances}}

	insts, err := Collect(context.Background(), targets,
		app.Options{
			Scope:            app.ScopeUser,
			Template:         "",
			Start:            t.TempDir(),
			Dir:              "",
			Integrity:        integrity.IntegrityCtx{Repository: nil},
			IntegrityFor:     AppIntegrity(&cmdcontext.CmdCtx{}),
			BestEffortConfig: false,
		})
	require.NoError(t, err)
	require.Len(t, insts, 1)
	assert.Nil(t, insts[0].Integrity)

	ok, err := IsAbleToStartInstances(insts)
	require.NoError(t, err)
	assert.True(t, ok)
}
