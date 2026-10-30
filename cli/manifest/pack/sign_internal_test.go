package pack

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/rocks"
)

// testKeys writes an RSA key pair into a temporary directory and returns the
// paths of its private and public halves, in the formats the RSA provider
// reads.
func testKeys(t *testing.T) (string, string) {
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

	return privatePath, publicPath
}

// testSigner returns a signer of the RSA provider and the path of the public
// key that checks what it signs.
func testSigner(t *testing.T) (integrity.Signer, string) {
	t.Helper()

	privatePath, publicPath := testKeys(t)

	signer, err := integrity.NewRSAProvider().NewSigner(privatePath)
	require.NoError(t, err)

	return signer, publicPath
}

// signedManifest is a dependency-free package with a Lua component and a
// payload of scripts, whose template runs one of them.
const signedManifest = `manifest_version = '0.1'

[package]
name = 'signed-app'
include = ['scripts']

[platform]
tarantool = '>=3.0.0,<4.0.0'
tt = '>=2.0.0,<3.0.0'

[products.default]
components = ['lua']
default = true

[components.lua]
path = '.'
include = ['*.lua']
`

// packSigned packs a project made of signedManifest and files with deps and
// a runtime from a fake cache, signed by signer.
func packSigned(t *testing.T, signer integrity.Signer, files map[string]string) (
	*Result, error,
) {
	t.Helper()

	cache := fakeCache(t, map[string][]string{
		runtimeTarantool: {"3.1.0"},
		runtimeTt:        {"2.5.0"},
	})
	writeTree(t, filepath.Join(cache, runtimeTarantool, flavorCE, "3.1.0"), map[string]string{
		"include/tarantool/module.h": "header\n",
	})

	src := t.TempDir()
	writeTree(t, src, map[string]string{
		manifestFileName:                    signedManifest,
		versionFileName:                     "1.0.0\n",
		"init.lua":                          "return 1\n",
		"scripts/run":                       "dofile('init.lua')\n",
		".templates/default/instances.yaml": "worker: {script: scripts/run}\n",
	})
	writeTree(t, src, files)

	return Run(context.Background(), Options{
		ProjectDir: src,
		OutputDir:  filepath.Join(src, "_out"),
		Signer:     signer,
		Runtime:    RuntimeOptions{CacheDir: cache},
		Build: build.Options{
			TtVersion: "tt test",
			Tarantool: rocks.TarantoolInfo{Prefix: "/nonexistent", Version: "3.1.0"},
		},
	})
}

// extract writes the files among the entries of an archive into a new
// directory: an entry that other entries are under is a directory.
func extract(t *testing.T, entries map[string]string) string {
	t.Helper()

	files := map[string]string{}

	for name, content := range entries {
		isDir := strings.HasSuffix(name, "/")

		for other := range entries {
			if strings.HasPrefix(other, name+"/") {
				isDir = true

				break
			}
		}

		if !isDir {
			files[name] = content
		}
	}

	dir := t.TempDir()
	writeTree(t, dir, files)

	return dir
}

func TestPackSignsArchive(t *testing.T) {
	signer, publicKey := testSigner(t)

	result, err := packSigned(t, signer, nil)
	require.NoError(t, err)

	entries := readArchive(t, result.Path)

	for _, name := range []string{
		"hashes.json", "hashes.json.sig", "env_hashes.json", "env_hashes.json.sig",
		"_runtime/tarantool/env_hashes.json", "_runtime/tarantool/env_hashes.json.sig",
		"_runtime/tt/env_hashes.json", "_runtime/tt/env_hashes.json.sig",
	} {
		assert.Contains(t, entries, name)
	}

	// The script the template runs is listed for Tarantool, whatever its
	// name; the template's description is listed for tt.
	assert.Contains(t, entries["hashes.json"], `"scripts/run"`)
	assert.Contains(t, entries["hashes.json"], `".rocks/share/tarantool/signed-app/init.lua"`)
	assert.Contains(t, entries["env_hashes.json"], `".templates/default/instances.yaml"`)
	assert.Contains(t, entries["env_hashes.json"], `"app.manifest.toml"`)
	assert.NotContains(t, entries["env_hashes.json"], "_runtime")

	// The runtime is listed as it is installed: the executable at the top.
	assert.Contains(t, entries["_runtime/tarantool/env_hashes.json"], `"tarantool"`)
	assert.Contains(t, entries["_runtime/tarantool/env_hashes.json"],
		`"include/tarantool/module.h"`)
	assert.NotContains(t, entries["_runtime/tarantool/env_hashes.json"], "LICENSE")

	// The package without its runtime is what an install lays out, and it
	// checks as a whole.
	version := map[string]string{}
	runtime := map[string]string{}

	for name, content := range entries {
		rest, inRuntime := strings.CutPrefix(name, runtimeDirName+"/"+runtimeTarantool+"/")
		switch {
		case inRuntime && rest == "bin/tarantool":
			runtime["tarantool"] = content
		case inRuntime && rest != "" && rest != "LICENSE" && rest != "bin/":
			runtime[rest] = content
		case !inRuntime && !strings.HasPrefix(name, runtimeDirName+"/"):
			version[name] = content
		}
	}

	provider := integrity.NewRSAProvider()
	require.NoError(t, provider.VerifyTree(publicKey, extract(t, version)))
	require.NoError(t, provider.VerifyTree(publicKey, extract(t, runtime)))
}

func TestPackSignedRefusals(t *testing.T) {
	signer, _ := testSigner(t)

	t.Run("without deps", func(t *testing.T) {
		_, err := Run(context.Background(), Options{
			ProjectDir: t.TempDir(), WithoutDeps: true, Signer: signer,
		})
		require.ErrorIs(t, err, errSignedWithoutDeps)
	})

	cases := map[string]struct {
		description string
		message     string
	}{
		"script outside": {
			description: "worker: {script: /srv/init.lua}\n",
			message:     "/srv/init.lua",
		},
		"binary path outside": {
			description: "gw: {binary: ../gw}\n",
			message:     "runs a file outside the package",
		},
		"program of the host": {
			description: "gw: {binary: nginx}\n",
			message:     `"nginx" is not in .rocks/bin of the package`,
		},
		"script not packed": {
			description: "worker: {script: missing.lua}\n",
			message:     `"missing.lua" is not a file of the tree`,
		},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := packSigned(t, signer, map[string]string{
				".templates/default/instances.yaml": testCase.description,
			})
			require.ErrorContains(t, err, testCase.message)
		})
	}
}

func TestSignStageReservedNames(t *testing.T) {
	signer, _ := testSigner(t)

	for _, name := range []string{"hashes.json", "env_hashes.json.sig"} {
		stage := t.TempDir()
		writeTree(t, stage, map[string]string{name: "{}"})

		err := signStage(stage, t.TempDir(), signer)
		require.ErrorIs(t, err, errReservedName)
		require.ErrorContains(t, err, name)
	}
}

func TestSignRuntimeAmbiguous(t *testing.T) {
	signer, _ := testSigner(t)
	stage := t.TempDir()

	writeTree(t, stage, map[string]string{
		"_runtime/tt/bin/tt": "one",
		"_runtime/tt/tt":     "two",
	})

	require.ErrorIs(t, signRuntime(stage, runtimeTt, signer), errAmbiguousRuntime)
	require.NoError(t, signRuntime(stage, runtimeTcm, signer), "an absent component is no error")
}
