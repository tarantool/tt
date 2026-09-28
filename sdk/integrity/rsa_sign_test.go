package integrity_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// assertNoFile checks that there is no directory entry at path.
func assertNoFile(t *testing.T, path string) {
	t.Helper()

	_, err := os.Lstat(path)
	require.ErrorIs(t, err, os.ErrNotExist, "%s must not exist", path)
}

func TestRSASignerWritesRootApplication(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()

	writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
	writeFile(t, filepath.Join(base, "config.yaml"), "config\n", 0o644)
	// A configuration file that is also executable is listed once, first.
	writeFile(t, filepath.Join(base, "instances.yml"), "instances\n", 0o755)
	writeFile(t, filepath.Join(base, "README.md"), "readme\n", 0o644)
	writeFile(t, filepath.Join(base, "B.so"), "B\n", 0o644)
	writeFile(t, filepath.Join(base, "a", "x.lua"), "a/x\n", 0o644)
	writeFile(t, filepath.Join(base, "a-b.lua"), "a-b\n", 0o644)
	writeFile(t, filepath.Join(base, "a.dylib"), "a\n", 0o644)
	writeFile(t, filepath.Join(base, "init.lua"), "init\n", 0o644)
	writeFile(t, filepath.Join(base, "run.sh"), "#!/bin/sh\n", 0o755)
	writeFile(t, filepath.Join(base, "data", "notes.txt"), "notes\n", 0o644)
	writeFile(t, filepath.Join(base, "bin", "tt"), "tt binary\n", 0o755)
	writeFile(t, filepath.Join(base, "bin", "tools", "helper.lua"), "helper\n", 0o644)
	writeFile(t, filepath.Join(base, "modules", "m.lua"), "module\n", 0o644)
	writeFile(t, filepath.Join(base, "modules", "README"), "module readme\n", 0o644)

	require.NoError(t, newSigner(t, key).Sign(base, nil))

	// Configuration files first, then executable files depth-first with
	// the names of each directory in byte order: "a" is walked before
	// "a-b.lua", uppercase before lowercase. bin and modules of the
	// environment are not part of the application.
	assert.Equal(t, hashesJSON(
		entry("config.yaml", "config\n"),
		entry("instances.yml", "instances\n"),
		entry("B.so", "B\n"),
		entry("a/x.lua", "a/x\n"),
		entry("a-b.lua", "a-b\n"),
		entry("a.dylib", "a\n"),
		entry("init.lua", "init\n"),
		entry("run.sh", "#!/bin/sh\n"),
	), readFile(t, filepath.Join(base, "hashes.json")))

	// Every file under bin, then under modules, then tt.yaml.
	assert.Equal(t, hashesJSON(
		entry("bin/tools/helper.lua", "helper\n"),
		entry("bin/tt", "tt binary\n"),
		entry("modules/README", "module readme\n"),
		entry("modules/m.lua", "module\n"),
		entry("tt.yaml", "env: {}\n"),
	), readFile(t, filepath.Join(base, "env_hashes.json")))

	for _, name := range []string{"hashes.json", "env_hashes.json"} {
		entries := readSignedHashes(t, key, filepath.Join(base, name))
		assertDigests(t, base, entries)
	}
}

func TestRSASignerListsAllConfigurationFiles(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()
	app := filepath.Join(base, "app")

	writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
	writeFile(t, filepath.Join(base, "shared", "instances.yaml"), "shared instances\n", 0o644)

	// The four configuration files, one of them executable and one a link,
	// among scripts whose names sort before, between and after theirs.
	writeFile(t, filepath.Join(app, "a.lua"), "a\n", 0o644)
	writeFile(t, filepath.Join(app, "config.yaml"), "config yaml\n", 0o644)
	writeFile(t, filepath.Join(app, "config.yml"), "config yml\n", 0o755)
	writeFile(t, filepath.Join(app, "d.lua"), "d\n", 0o644)
	symlink(t, filepath.Join("..", "shared", "instances.yaml"),
		filepath.Join(app, "instances.yaml"))
	writeFile(t, filepath.Join(app, "instances.yml"), "instances yml\n", 0o644)
	writeFile(t, filepath.Join(app, "z.lua"), "z\n", 0o644)
	// A configuration name below the application directory does not make
	// a configuration file.
	writeFile(t, filepath.Join(app, "roles", "config.yaml"), "role config\n", 0o644)
	writeFile(t, filepath.Join(app, "roles", "role.lua"), "role\n", 0o644)

	require.NoError(t, newSigner(t, key).Sign(base, []string{"app"}))

	hashes := filepath.Join(app, "hashes.json")
	assert.Equal(t, hashesJSON(
		entry("config.yaml", "config yaml\n"),
		entry("config.yml", "config yml\n"),
		entry("instances.yaml", "shared instances\n"),
		entry("instances.yml", "instances yml\n"),
		entry("a.lua", "a\n"),
		entry("d.lua", "d\n"),
		entry("roles/role.lua", "role\n"),
		entry("z.lua", "z\n"),
	), readFile(t, hashes))
	assertDigests(t, app, readSignedHashes(t, key, hashes))
}

func TestRSASignerRootApplicationIsChecked(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()

	writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
	writeFile(t, filepath.Join(base, "config.yaml"), "config\n", 0o644)
	writeFile(t, filepath.Join(base, "init.lua"), "init\n", 0o644)
	writeFile(t, filepath.Join(base, "bin", "tt"), "tt binary\n", 0o755)
	writeFile(t, filepath.Join(base, "README.md"), "readme\n", 0o644)

	require.NoError(t, newSigner(t, key).Sign(base, nil))

	ctx, err := initializeCheck(t, key, base)
	require.NoError(t, err)
	require.NoError(t, ctx.Repository.ValidateAll())

	// Files of both hashes files are known.
	for name, content := range map[string]string{
		"tt.yaml":     "env: {}\n",
		"config.yaml": "config\n",
		"init.lua":    "init\n",
		"bin/tt":      "tt binary\n",
	} {
		data, err := readThrough(t, ctx.Repository, filepath.Join(base, name))
		require.NoError(t, err, name)
		assert.Equal(t, content, data, name)
	}

	_, err = readThrough(t, ctx.Repository, filepath.Join(base, "README.md"))
	require.ErrorContains(t, err, "in repository")
}

func TestRSASignerNamedApplications(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()

	writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
	writeFile(t, filepath.Join(base, "app", "config.yaml"), "app config\n", 0o644)
	writeFile(t, filepath.Join(base, "app", "init.lua"), "app init\n", 0o644)
	writeFile(t, filepath.Join(base, "app", "roles", "role.lua"), "role\n", 0o644)
	writeFile(t, filepath.Join(base, "app", "notes.txt"), "notes\n", 0o644)
	// bin and modules are skipped at the root of the environment only.
	writeFile(t, filepath.Join(base, "app", "bin", "tool.lua"), "tool\n", 0o644)
	writeFile(t, filepath.Join(base, "app", "modules", "m.lua"), "module\n", 0o644)
	writeFile(t, filepath.Join(base, "single.lua"), "single\n", 0o644)
	symlink(t, filepath.Join("..", "single.lua"),
		filepath.Join(base, "instances.enabled", "single.lua"))

	require.NoError(t, newSigner(t, key).Sign(base, []string{"app", "single"}))

	appHashes := filepath.Join(base, "app", "hashes.json")
	assert.Equal(t, hashesJSON(
		entry("config.yaml", "app config\n"),
		entry("bin/tool.lua", "tool\n"),
		entry("init.lua", "app init\n"),
		entry("modules/m.lua", "module\n"),
		entry("roles/role.lua", "role\n"),
	), readFile(t, appHashes))
	assertDigests(t, filepath.Dir(appHashes), readSignedHashes(t, key, appHashes))

	singleHashes := filepath.Join(base, "instances.enabled", "single", "hashes.json")
	assert.Equal(t, hashesJSON(entry("../single.lua", "single\n")), readFile(t, singleHashes))
	assertDigests(t, filepath.Dir(singleHashes), readSignedHashes(t, key, singleHashes))

	envHashes := filepath.Join(base, "env_hashes.json")
	assert.Equal(t, hashesJSON(entry("tt.yaml", "env: {}\n")), readFile(t, envHashes))
	assertDigests(t, base, readSignedHashes(t, key, envHashes))

	// Named applications do not make the environment an application.
	assertNoFile(t, filepath.Join(base, "hashes.json"))

	// The check reads the hashes files of configDir only: the files of the
	// applications are not known to it, and their tampering goes unnoticed.
	writeFile(t, filepath.Join(base, "app", "init.lua"), "tampered\n", 0o644)
	writeFile(t, filepath.Join(base, "single.lua"), "tampered\n", 0o644)

	ctx, err := initializeCheck(t, key, base)
	require.NoError(t, err)
	require.NoError(t, ctx.Repository.ValidateAll())

	for _, path := range []string{
		filepath.Join(base, "app", "init.lua"),
		filepath.Join(base, "instances.enabled", "single.lua"),
	} {
		_, err = readThrough(t, ctx.Repository, path)
		require.ErrorContains(t, err, "in repository")
	}
}

func TestRSASignerSingleFileApplicationCopy(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()

	writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
	writeFile(t, filepath.Join(base, "single.lua"), "packed\n", 0o644)
	writeFile(t, filepath.Join(base, "instances.enabled", "single.lua"), "enabled\n", 0o644)

	require.NoError(t, newSigner(t, key).Sign(base, []string{"single"}))

	// The file signed is the one in instances.enabled, not the probe.
	hashes := filepath.Join(base, "instances.enabled", "single", "hashes.json")
	assert.Equal(t, hashesJSON(entry("../single.lua", "enabled\n")), readFile(t, hashes))
}

func TestRSASignerProbesThroughLinks(t *testing.T) {
	key := signingKey(t)

	t.Run("link to a file", func(t *testing.T) {
		base := t.TempDir()

		writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
		writeFile(t, filepath.Join(base, "scripts", "app.lua"), "script\n", 0o644)
		symlink(t, filepath.Join("scripts", "app.lua"), filepath.Join(base, "app.lua"))
		symlink(t, filepath.Join("..", "app.lua"),
			filepath.Join(base, "instances.enabled", "app.lua"))

		require.NoError(t, newSigner(t, key).Sign(base, []string{"app"}))
		assert.Equal(t, hashesJSON(entry("../app.lua", "script\n")),
			readFile(t, filepath.Join(base, "instances.enabled", "app", "hashes.json")))
	})

	t.Run("link to a directory", func(t *testing.T) {
		base := t.TempDir()
		probe := filepath.Join(base, "app.lua")

		writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
		writeFile(t, filepath.Join(base, "scripts", "init.lua"), "script\n", 0o644)
		writeFile(t, filepath.Join(base, "app", "init.lua"), "init\n", 0o644)
		symlink(t, "scripts", probe)

		err := newSigner(t, key).Sign(base, []string{"app"})
		require.ErrorContains(t, err, probe)
	})

	t.Run("link that does not resolve", func(t *testing.T) {
		// Not found through the link: a directory application.
		base := t.TempDir()

		writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
		writeFile(t, filepath.Join(base, "app", "init.lua"), "init\n", 0o644)
		symlink(t, "missing.lua", filepath.Join(base, "app.lua"))

		require.NoError(t, newSigner(t, key).Sign(base, []string{"app"}))
		assert.Equal(t, hashesJSON(entry("init.lua", "init\n")),
			readFile(t, filepath.Join(base, "app", "hashes.json")))
	})
}

func TestRSASignerNilAndEmptyApplications(t *testing.T) {
	key := signingKey(t)

	for name, appNames := range map[string][]string{"nil": nil, "empty": {}} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
			writeFile(t, filepath.Join(base, "init.lua"), "init\n", 0o644)

			require.NoError(t, newSigner(t, key).Sign(base, appNames))

			assert.Equal(t, hashesJSON(entry("tt.yaml", "env: {}\n")),
				readFile(t, filepath.Join(base, "env_hashes.json")))

			if appNames == nil {
				assert.Equal(t, hashesJSON(entry("init.lua", "init\n")),
					readFile(t, filepath.Join(base, "hashes.json")))
			} else {
				assertNoFile(t, filepath.Join(base, "hashes.json"))
			}
		})
	}
}

func TestRSASignerNeverListsItsOutputs(t *testing.T) {
	key := signingKey(t)
	outputs := []string{"hashes.json", "hashes.json.sig", "env_hashes.json", "env_hashes.json.sig"}

	// Executable files named as the outputs, below the application
	// directory, are left out as well; an executable file next to them is
	// listed.
	for _, name := range outputs {
		t.Run("nested "+name, func(t *testing.T) {
			base := t.TempDir()

			writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
			writeFile(t, filepath.Join(base, "init.lua"), "init\n", 0o644)
			writeFile(t, filepath.Join(base, "sub", name), "nested\n", 0o755)
			writeFile(t, filepath.Join(base, "sub", "tool"), "tool\n", 0o755)

			require.NoError(t, newSigner(t, key).Sign(base, nil))
			assert.Equal(t, hashesJSON(entry("init.lua", "init\n"), entry("sub/tool", "tool\n")),
				readFile(t, filepath.Join(base, "hashes.json")))
		})
	}

	// A signed tree whose outputs were made executable signs again into
	// the same bytes, and the result checks.
	cases := map[string]struct {
		appNames []string
		appDir   string
	}{
		"root application":  {appNames: nil, appDir: ""},
		"named application": {appNames: []string{"app"}, appDir: "app"},
	}

	for name, testCase := range cases {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			appDir := filepath.Join(base, testCase.appDir)

			writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
			writeFile(t, filepath.Join(appDir, "init.lua"), "init\n", 0o644)

			require.NoError(t, newSigner(t, key).Sign(base, testCase.appNames))

			appHashes := filepath.Join(appDir, "hashes.json")
			envHashes := filepath.Join(base, "env_hashes.json")
			expectedApp := hashesJSON(entry("init.lua", "init\n"))
			expectedEnv := hashesJSON(entry("tt.yaml", "env: {}\n"))

			require.Equal(t, expectedApp, readFile(t, appHashes))
			require.Equal(t, expectedEnv, readFile(t, envHashes))

			written := []string{appHashes, appHashes + ".sig", envHashes, envHashes + ".sig"}
			for _, path := range written {
				require.NoError(t, os.Chmod(path, 0o755))
			}

			require.NoError(t, newSigner(t, key).Sign(base, testCase.appNames))

			assert.Equal(t, expectedApp, readFile(t, appHashes))
			assert.Equal(t, expectedEnv, readFile(t, envHashes))
			assertDigests(t, appDir, readSignedHashes(t, key, appHashes))

			ctx, err := initializeCheck(t, key, base)
			require.NoError(t, err)
			require.NoError(t, ctx.Repository.ValidateAll())
		})
	}
}

func TestRSASignerApplicationWithoutFiles(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()

	writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
	writeFile(t, filepath.Join(base, "app", "notes.txt"), "notes\n", 0o644)

	require.NoError(t, newSigner(t, key).Sign(base, nil))
	require.NoError(t, newSigner(t, key).Sign(base, []string{"app"}))

	// Exactly {"files":[]}, what hashesJSON returns without entries: an
	// empty array, never null.
	assert.Equal(t, hashesJSON(), readFile(t, filepath.Join(base, "hashes.json")))
	assert.Equal(t, hashesJSON(), readFile(t, filepath.Join(base, "app", "hashes.json")))
	assert.Empty(t, readSignedHashes(t, key, filepath.Join(base, "app", "hashes.json")))
}

func TestRSASignerEnvironmentWithoutBinAndModules(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()

	writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)

	require.NoError(t, newSigner(t, key).Sign(base, []string{}))

	assert.Equal(t, hashesJSON(entry("tt.yaml", "env: {}\n")),
		readFile(t, filepath.Join(base, "env_hashes.json")))

	ctx, err := initializeCheck(t, key, base)
	require.NoError(t, err)

	data, err := readThrough(t, ctx.Repository, filepath.Join(base, "tt.yaml"))
	require.NoError(t, err)
	assert.Equal(t, "env: {}\n", data)
}

func TestRSASignerWithoutTTYaml(t *testing.T) {
	base := t.TempDir()
	writeFile(t, filepath.Join(base, "bin", "tt"), "tt binary\n", 0o755)
	writeFile(t, filepath.Join(base, "app", "init.lua"), "init\n", 0o644)

	err := newSigner(t, signingKey(t)).Sign(base, []string{"app"})
	require.ErrorIs(t, err, os.ErrNotExist)
	require.ErrorContains(t, err, "tt.yaml: no such file or directory")
	assertNoFile(t, filepath.Join(base, "env_hashes.json"))

	// The application signed before stays written.
	assert.Equal(t, hashesJSON(entry("init.lua", "init\n")),
		readFile(t, filepath.Join(base, "app", "hashes.json")))
}

func TestRSASignerRefusesEnvironmentFilesForDirectories(t *testing.T) {
	key := signingKey(t)

	// A bin or a modules that exists is walked: a file there is not a
	// tree to list.
	for _, name := range []string{"bin", "modules"} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			path := filepath.Join(base, name)

			writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
			writeFile(t, path, "not a directory\n", 0o644)

			err := newSigner(t, key).Sign(base, []string{})
			require.ErrorContains(t, err, path)
			assertNoFile(t, filepath.Join(base, "env_hashes.json"))
		})
	}
}

func TestRSASignerSymbolicLinks(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()

	writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
	writeFile(t, filepath.Join(base, "data", "notes.txt"), "notes\n", 0o644)
	writeFile(t, filepath.Join(base, "data", "lib.txt"), "library\n", 0o644)
	writeFile(t, filepath.Join(base, "data", "tool"), "tool\n", 0o755)
	// The execute bits are those of the target, not of the link.
	symlink(t, filepath.Join("data", "notes.txt"), filepath.Join(base, "notes"))
	symlink(t, filepath.Join("data", "tool"), filepath.Join(base, "tool"))
	// A link is listed under its own name, with the content of its target.
	symlink(t, filepath.Join("data", "lib.txt"), filepath.Join(base, "lib.lua"))
	// Links to directories are neither descended into nor listed.
	symlink(t, "data", filepath.Join(base, "linked"))
	symlink(t, "data", filepath.Join(base, "linked.lua"))

	require.NoError(t, newSigner(t, key).Sign(base, nil))

	hashes := filepath.Join(base, "hashes.json")
	assert.Equal(t, hashesJSON(
		entry("data/tool", "tool\n"),
		entry("lib.lua", "library\n"),
		entry("tool", "tool\n"),
	), readFile(t, hashes))
	assertDigests(t, base, readSignedHashes(t, key, hashes))

	ctx, err := initializeCheck(t, key, base)
	require.NoError(t, err)

	data, err := readThrough(t, ctx.Repository, filepath.Join(base, "lib.lua"))
	require.NoError(t, err)
	assert.Equal(t, "library\n", data)
}

func TestRSASignerWalksLinkedRoots(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()
	outside := t.TempDir()

	writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
	writeFile(t, filepath.Join(outside, "app", "init.lua"), "app init\n", 0o644)
	writeFile(t, filepath.Join(outside, "bin", "tt"), "tt binary\n", 0o755)
	symlink(t, filepath.Join(outside, "app"), filepath.Join(base, "app"))
	symlink(t, filepath.Join(outside, "bin"), filepath.Join(base, "bin"))

	require.NoError(t, newSigner(t, key).Sign(base, []string{"app"}))

	assert.Equal(t, hashesJSON(entry("init.lua", "app init\n")),
		readFile(t, filepath.Join(outside, "app", "hashes.json")))
	assert.Equal(t, hashesJSON(
		entry("bin/tt", "tt binary\n"),
		entry("tt.yaml", "env: {}\n"),
	), readFile(t, filepath.Join(base, "env_hashes.json")))
}

func TestRSASignerDanglingSymbolicLinks(t *testing.T) {
	key := signingKey(t)

	// A link that does not resolve fails whether or not it would be
	// listed, and no hashes file is written.
	for _, name := range []string{"broken.lua", "broken"} {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			link := filepath.Join(base, "app", "sub", name)

			writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
			writeFile(t, filepath.Join(base, "app", "init.lua"), "init\n", 0o644)
			symlink(t, filepath.Join(base, "missing"), link)

			err := newSigner(t, key).Sign(base, []string{"app"})
			require.ErrorContains(t, err, link)
			assertNoFile(t, filepath.Join(base, "app", "hashes.json"))
			assertNoFile(t, filepath.Join(base, "app", "hashes.json.sig"))
			assertNoFile(t, filepath.Join(base, "env_hashes.json"))
		})
	}

	t.Run("existing hashes file", func(t *testing.T) {
		base := t.TempDir()
		hashes := filepath.Join(base, "app", "hashes.json")

		writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
		writeFile(t, filepath.Join(base, "app", "init.lua"), "init\n", 0o644)
		writeFile(t, hashes, "previous hashes", 0o644)
		writeFile(t, hashes+".sig", "previous signature", 0o644)
		symlink(t, filepath.Join(base, "missing"), filepath.Join(base, "app", "broken.lua"))

		require.Error(t, newSigner(t, key).Sign(base, []string{"app"}))
		assert.Equal(t, "previous hashes", readFile(t, hashes))
		assert.Equal(t, "previous signature", readFile(t, hashes+".sig"))
	})

	for name, appNames := range map[string][]string{"root application": nil, "no": {}} {
		t.Run("bin with "+name, func(t *testing.T) {
			base := t.TempDir()
			bin := filepath.Join(base, "bin")

			writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
			symlink(t, filepath.Join(base, "missing"), bin)

			err := newSigner(t, key).Sign(base, appNames)
			require.ErrorContains(t, err, bin)
			assertNoFile(t, filepath.Join(base, "env_hashes.json"))
		})
	}
}

func TestRSASignerRefusesApplications(t *testing.T) {
	key := signingKey(t)

	t.Run("missing directory", func(t *testing.T) {
		base := t.TempDir()
		app := filepath.Join(base, "app")

		writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)

		// The directory itself is reported missing, not a file to write in
		// it.
		err := newSigner(t, key).Sign(base, []string{"app"})
		require.ErrorContains(t, err, "stat "+app+": no such file or directory")
		assertNoFile(t, filepath.Join(base, "env_hashes.json"))
	})

	t.Run("directory named like a script", func(t *testing.T) {
		base := t.TempDir()
		probe := filepath.Join(base, "app.lua")

		writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
		writeFile(t, filepath.Join(probe, "init.lua"), "init\n", 0o644)
		writeFile(t, filepath.Join(base, "app", "init.lua"), "init\n", 0o644)

		err := newSigner(t, key).Sign(base, []string{"app"})
		require.ErrorContains(t, err, probe)
		assertNoFile(t, filepath.Join(base, "app", "hashes.json"))
	})

	t.Run("probe that does not resolve", func(t *testing.T) {
		base := t.TempDir()
		probe := filepath.Join(base, "app.lua")

		writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
		writeFile(t, filepath.Join(base, "app", "init.lua"), "init\n", 0o644)
		// A link to itself is a stat error other than not-found.
		symlink(t, "app.lua", probe)

		err := newSigner(t, key).Sign(base, []string{"app"})
		require.ErrorContains(t, err, probe)
		assertNoFile(t, filepath.Join(base, "app", "hashes.json"))
	})

	t.Run("single file not enabled", func(t *testing.T) {
		base := t.TempDir()
		enabled := filepath.Join(base, "instances.enabled", "single.lua")

		writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
		writeFile(t, filepath.Join(base, "single.lua"), "single\n", 0o644)

		err := newSigner(t, key).Sign(base, []string{"single"})
		require.ErrorContains(t, err, enabled)
		assertNoFile(t, filepath.Join(base, "instances.enabled", "single"))
	})

	t.Run("application that is a file", func(t *testing.T) {
		base := t.TempDir()
		app := filepath.Join(base, "app")

		writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
		writeFile(t, app, "not a directory\n", 0o755)

		err := newSigner(t, key).Sign(base, []string{"app"})
		require.ErrorContains(t, err, app)
		assertNoFile(t, filepath.Join(base, "env_hashes.json"))
	})

	t.Run("configuration directory", func(t *testing.T) {
		base := t.TempDir()
		config := filepath.Join(base, "app", "config.yaml")

		writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
		writeFile(t, filepath.Join(config, "nested.yaml"), "nested\n", 0o644)

		err := newSigner(t, key).Sign(base, []string{"app"})
		require.ErrorContains(t, err, config)
		assertNoFile(t, filepath.Join(base, "app", "hashes.json"))
	})
}

func TestRSASignerReportsWriteFailures(t *testing.T) {
	key := signingKey(t)

	// A directory in the place of a file makes writing it fail whoever
	// runs the test.
	t.Run("hashes file", func(t *testing.T) {
		base := t.TempDir()
		hashes := filepath.Join(base, "app", "hashes.json")

		writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
		writeFile(t, filepath.Join(base, "app", "init.lua"), "init\n", 0o644)
		require.NoError(t, os.MkdirAll(hashes, 0o755))

		err := newSigner(t, key).Sign(base, []string{"app"})
		require.ErrorContains(t, err, hashes)
		assertNoFile(t, hashes+".sig")
		assertNoFile(t, filepath.Join(base, "env_hashes.json"))
	})

	t.Run("signature", func(t *testing.T) {
		base := t.TempDir()
		hashes := filepath.Join(base, "app", "hashes.json")

		writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
		writeFile(t, filepath.Join(base, "app", "init.lua"), "init\n", 0o644)
		require.NoError(t, os.MkdirAll(hashes+".sig", 0o755))

		err := newSigner(t, key).Sign(base, []string{"app"})
		require.ErrorContains(t, err, hashes+".sig")

		// The hashes file and its signature are not replaced atomically.
		assert.Equal(t, hashesJSON(entry("init.lua", "init\n")), readFile(t, hashes))
		assertNoFile(t, filepath.Join(base, "env_hashes.json"))
	})
}

func TestRSASignerKeepsWrittenApplications(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()

	writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
	writeFile(t, filepath.Join(base, "first", "init.lua"), "first\n", 0o644)

	err := newSigner(t, key).Sign(base, []string{"first", "second"})
	require.ErrorContains(t, err, filepath.Join(base, "second"))

	// Applications are signed in order: the first one stays signed.
	assert.Equal(t, hashesJSON(entry("init.lua", "first\n")),
		readFile(t, filepath.Join(base, "first", "hashes.json")))
	assertNoFile(t, filepath.Join(base, "env_hashes.json"))
}
