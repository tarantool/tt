package integrity_test

import (
	"crypto/rsa"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tarantool/tt/sdk/integrity"
)

// signedEnvironment signs, as one application, an environment with
// tt.yaml, bin/tt and init.lua, and returns its root.
func signedEnvironment(t *testing.T, key *rsa.PrivateKey) string {
	t.Helper()

	base := t.TempDir()

	writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
	writeFile(t, filepath.Join(base, "bin", "tt"), "tt binary\n", 0o755)
	writeFile(t, filepath.Join(base, "init.lua"), "init\n", 0o644)
	require.NoError(t, newSigner(t, key).Sign(base, nil))

	return base
}

// resolve returns path with its symbolic links resolved.
func resolve(t *testing.T, path string) string {
	t.Helper()

	resolved, err := filepath.EvalSymlinks(path)
	require.NoError(t, err)

	return resolved
}

// mismatch returns the text of the error of a file at path whose content
// was expected and is actual.
func mismatch(path, expected, actual string) string {
	return fmt.Sprintf("hash mismatch for %q: expected %q, got %q",
		path, sha256Hex(expected), sha256Hex(actual))
}

func TestRSACheckTamperedFile(t *testing.T) {
	key := signingKey(t)

	t.Run("initialization", func(t *testing.T) {
		base := signedEnvironment(t, key)
		path := filepath.Join(base, "init.lua")
		writeFile(t, path, "tampered\n", 0o644)

		_, err := initializeCheck(t, key, base)
		require.ErrorContains(t, err, mismatch(path, "init\n", "tampered\n"))
	})

	t.Run("read", func(t *testing.T) {
		base := signedEnvironment(t, key)
		path := filepath.Join(base, "init.lua")

		ctx, err := initializeCheck(t, key, base)
		require.NoError(t, err)

		writeFile(t, path, "tampered\n", 0o644)

		_, err = readThrough(t, ctx.Repository, path)
		require.ErrorContains(t, err, mismatch(resolve(t, path), "init\n", "tampered\n"))

		// Every read checks the file again.
		writeFile(t, path, "init\n", 0o644)

		data, err := readThrough(t, ctx.Repository, path)
		require.NoError(t, err)
		assert.Equal(t, "init\n", data)
	})

	t.Run("validate all", func(t *testing.T) {
		base := signedEnvironment(t, key)
		path := filepath.Join(base, "bin", "tt")

		ctx, err := initializeCheck(t, key, base)
		require.NoError(t, err)
		require.NoError(t, ctx.Repository.ValidateAll())

		writeFile(t, path, "tampered binary\n", 0o755)

		err = ctx.Repository.ValidateAll()
		require.ErrorContains(t, err,
			mismatch(resolve(t, path), "tt binary\n", "tampered binary\n"))
	})

	t.Run("removed", func(t *testing.T) {
		base := signedEnvironment(t, key)
		path := filepath.Join(base, "init.lua")

		ctx, err := initializeCheck(t, key, base)
		require.NoError(t, err)

		resolved := resolve(t, path)
		require.NoError(t, os.Remove(path))

		require.ErrorIs(t, ctx.Repository.ValidateAll(), os.ErrNotExist)

		_, err = readThrough(t, ctx.Repository, path)
		require.ErrorIs(t, err, os.ErrNotExist)

		_, err = initializeCheck(t, key, base)
		require.ErrorContains(t, err, filepath.Base(resolved))
	})
}

func TestRSACheckTamperedHashesFiles(t *testing.T) {
	key := signingKey(t)

	for _, name := range []string{"env_hashes.json", "hashes.json"} {
		t.Run(name, func(t *testing.T) {
			base := signedEnvironment(t, key)
			path := filepath.Join(base, name)

			// Trailing whitespace keeps the JSON and its entries as they
			// were: only the signature tells the change.
			writeFile(t, path, readFile(t, path)+" ", 0o644)

			_, err := initializeCheck(t, key, base)
			require.ErrorContains(t, err, verificationError)
			require.ErrorContains(t, err, path)
		})

		t.Run(name+".sig", func(t *testing.T) {
			base := signedEnvironment(t, key)
			path := filepath.Join(base, name+".sig")

			signature := []byte(readFile(t, path))

			signature[0] ^= 0xff
			writeFile(t, path, string(signature), 0o644)

			_, err := initializeCheck(t, key, base)
			require.ErrorContains(t, err, verificationError)
		})
	}
}

func TestRSACheckForeignKey(t *testing.T) {
	base := signedEnvironment(t, signingKey(t))

	_, err := initializeCheck(t, foreignKey(t), base)
	require.ErrorContains(t, err, verificationError)
	require.ErrorContains(t, err, filepath.Join(base, "env_hashes.json"))
}

func TestRSACheckWithoutPublicKey(t *testing.T) {
	base := signedEnvironment(t, signingKey(t))
	path := filepath.Join(base, "init.lua")
	writeFile(t, path, "tampered\n", 0o644)
	writeFile(t, filepath.Join(base, "unlisted.txt"), "unlisted\n", 0o644)

	ctx, err := integrity.NewRSAProvider().InitializeIntegrityCheck("", base)
	require.NoError(t, err)
	require.NoError(t, ctx.Repository.ValidateAll())

	data, err := readThrough(t, ctx.Repository, path)
	require.NoError(t, err)
	assert.Equal(t, "tampered\n", data)

	data, err = readThrough(t, ctx.Repository, filepath.Join(base, "unlisted.txt"))
	require.NoError(t, err)
	assert.Equal(t, "unlisted\n", data)
}

func TestRSACheckUnsignedEnvironment(t *testing.T) {
	key := signingKey(t)

	t.Run("no hashes files", func(t *testing.T) {
		base := t.TempDir()
		writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)

		ctx, err := initializeCheck(t, key, base)
		require.NoError(t, err)
		require.NoError(t, ctx.Repository.ValidateAll())

		_, err = readThrough(t, ctx.Repository, filepath.Join(base, "tt.yaml"))
		require.ErrorContains(t, err, "in repository")
	})

	t.Run("application hashes file only", func(t *testing.T) {
		base := t.TempDir()
		writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
		writeSignedHashes(t, key, filepath.Join(base, "hashes.json"),
			hashesJSON(entry("tt.yaml", "env: {}\n")))

		ctx, err := initializeCheck(t, key, base)
		require.NoError(t, err)

		_, err = readThrough(t, ctx.Repository, filepath.Join(base, "tt.yaml"))
		require.ErrorContains(t, err, "in repository")
	})
}

func TestRSACheckMissingSignatures(t *testing.T) {
	key := signingKey(t)

	for _, name := range []string{"env_hashes.json", "hashes.json"} {
		t.Run(name, func(t *testing.T) {
			base := signedEnvironment(t, key)
			path := filepath.Join(base, name+".sig")
			require.NoError(t, os.Remove(path))

			_, err := initializeCheck(t, key, base)
			require.ErrorIs(t, err, os.ErrNotExist)
			require.ErrorContains(t, err, path)
		})
	}
}

func TestRSACheckRefusesHashesFilesThatDoNotResolve(t *testing.T) {
	key := signingKey(t)

	// A hashes file is missing only when there is no directory entry of
	// its name. A link there that does not resolve, dangling or pointing
	// to itself, is an error and never a reason to check less.
	for _, name := range []string{"env_hashes.json", "hashes.json"} {
		t.Run(name+" dangling", func(t *testing.T) {
			base := signedEnvironment(t, key)
			path := filepath.Join(base, name)

			require.NoError(t, os.Remove(path))
			symlink(t, "missing.json", path)

			_, err := initializeCheck(t, key, base)
			require.ErrorIs(t, err, os.ErrNotExist)
			require.ErrorContains(t, err, path)
		})

		t.Run(name+" to itself", func(t *testing.T) {
			base := signedEnvironment(t, key)
			path := filepath.Join(base, name)

			require.NoError(t, os.Remove(path))
			symlink(t, name, path)

			_, err := initializeCheck(t, key, base)
			require.ErrorContains(t, err, path)
		})
	}
}

func TestRSACheckStatErrors(t *testing.T) {
	// A stat error other than not-found is an error, not a reason to skip
	// the checks.
	path := filepath.Join(t.TempDir(), "file")
	writeFile(t, path, "file\n", 0o644)

	_, err := initializeCheck(t, signingKey(t), path)
	require.ErrorIs(t, err, syscall.ENOTDIR)
	require.ErrorContains(t, err, path)
}

func TestRSACheckAcceptsHashesFilesOfTheFormat(t *testing.T) {
	key := signingKey(t)
	root := t.TempDir()
	base := filepath.Join(root, "env")

	files := map[string]string{
		"tt.yaml":         "env: {}\n",
		"bin/tt":          "tt binary\n",
		"init.lua":        "init\n",
		"app/role.lua":    "role\n",
		"../outside.lua":  "outside\n",
		"space name.lua":  "space\n",
		"quote\"name.lua": "quote\n",
	}
	for name, content := range files {
		writeFile(t, filepath.Join(base, filepath.FromSlash(name)), content, 0o644)
	}

	// Written by hand: whitespace, members in any order, entries in any
	// order, uppercase hex, a member besides files, a path listed twice
	// with one digest and a path that leaves the directory.
	writeSignedHashes(t, key, filepath.Join(base, "env_hashes.json"), fmt.Sprintf(`{
  "version": 1,
  "files": [
    {"sha256": %q, "path": "tt.yaml"},
    {"path": "bin/tt", "sha256": %q},
    {"path": "bin/tt", "sha256": %q}
  ]
}
`, strings.ToUpper(sha256Hex(files["tt.yaml"])), sha256Hex(files["bin/tt"]),
		strings.ToUpper(sha256Hex(files["bin/tt"]))))
	writeSignedHashes(t, key, filepath.Join(base, "hashes.json"), fmt.Sprintf(
		`{"files":[{"path":"init.lua","sha256":%q},{"path":"app/role.lua","sha256":%q},`+
			`{"path":"../outside.lua","sha256":%q},{"path":"space name.lua","sha256":%q},`+
			`{"path":"quote\"name.lua","sha256":%q}]}`,
		sha256Hex(files["init.lua"]), sha256Hex(files["app/role.lua"]),
		sha256Hex(files["../outside.lua"]), sha256Hex(files["space name.lua"]),
		sha256Hex(files["quote\"name.lua"])))

	ctx, err := initializeCheck(t, key, base)
	require.NoError(t, err)
	require.NoError(t, ctx.Repository.ValidateAll())

	for name, content := range files {
		data, err := readThrough(t, ctx.Repository, filepath.Join(base, filepath.FromSlash(name)))
		require.NoError(t, err, name)
		assert.Equal(t, content, data, name)
	}
}

func TestRSACheckRefusesMalformedHashesFiles(t *testing.T) {
	key := signingKey(t)
	content := "content\n"
	digest := sha256Hex(content)
	sha256Member := `"sha256":"` + digest + `"`

	// Apart from the violation, every entry names an existing file with its
	// digest, so that accepting the violation would pass the check.
	cases := map[string]string{
		"not JSON":           `not JSON`,
		"empty":              ``,
		"trailing data":      `{"files":[]} []`,
		"array":              `[]`,
		"null":               `null`,
		"string":             `"files"`,
		"no files":           `{}`,
		"files misspelled":   `{"Files":[]}`,
		"files null":         `{"files":null}`,
		"files object":       `{"files":{}}`,
		"files string":       `{"files":"file.lua"}`,
		"entry number":       `{"files":[1]}`,
		"entry null":         `{"files":[null]}`,
		"entry array":        `{"files":[["file.lua"]]}`,
		"entry string":       `{"files":["file.lua"]}`,
		"no path":            `{"files":[{"sha256":"` + digest + `"}]}`,
		"path misspelled":    `{"files":[{"Path":"file.lua","sha256":"` + digest + `"}]}`,
		"path number":        `{"files":[{"path":1,"sha256":"` + digest + `"}]}`,
		"path null":          `{"files":[{"path":null,"sha256":"` + digest + `"}]}`,
		"path array":         `{"files":[{"path":["file.lua"],"sha256":"` + digest + `"}]}`,
		"no hash":            `{"files":[{"path":"file.lua"}]}`,
		"two hashes":         `{"files":[{"path":"file.lua",` + sha256Member + `,"md5":"00"}]}`,
		"hash number":        `{"files":[{"path":"file.lua","sha256":1}]}`,
		"hash null":          `{"files":[{"path":"file.lua","sha256":null}]}`,
		"hash not hex":       `{"files":[{"path":"file.lua","sha256":"zz` + digest[2:] + `"}]}`,
		"hash odd length":    `{"files":[{"path":"file.lua","sha256":"` + digest + `0"}]}`,
		"31-byte digest":     `{"files":[{"path":"file.lua","sha256":"` + digest[:62] + `"}]}`,
		"33-byte digest":     `{"files":[{"path":"file.lua","sha256":"` + digest + `00"}]}`,
		"empty digest":       `{"files":[{"path":"file.lua","sha256":""}]}`,
		"other hash not hex": `{"files":[{"path":"file.lua","md5":"zz"}]}`,
		"other hash number":  `{"files":[{"path":"file.lua","md5":5}]}`,
	}

	// The violation each error reports, where the provider describes it
	// rather than encoding/json.
	reported := map[string]string{
		"null":               "not an object",
		"no files":           `missing member "files"`,
		"files misspelled":   `missing member "files"`,
		"files null":         `"files": null`,
		"entry number":       "entry 0: not an object",
		"entry null":         "entry 0: not an object",
		"entry array":        "entry 0: not an object",
		"entry string":       "entry 0: not an object",
		"no path":            `missing member "path"`,
		"path misspelled":    `missing member "path"`,
		"path number":        `"path": not a string`,
		"path null":          `"path": not a string`,
		"path array":         `"path": not a string`,
		"no hash":            "exactly one hash besides the path is expected, found 0",
		"two hashes":         "exactly one hash besides the path is expected, found 2",
		"hash number":        `hash "sha256" of "file.lua": not a string`,
		"hash null":          `hash "sha256" of "file.lua": not a string`,
		"hash not hex":       `hash "sha256" of "file.lua": not hex`,
		"hash odd length":    `hash "sha256" of "file.lua": not hex`,
		"31-byte digest":     "31 bytes, 32 expected",
		"33-byte digest":     "33 bytes, 32 expected",
		"empty digest":       "0 bytes, 32 expected",
		"other hash not hex": `hash "md5" of "file.lua": not hex`,
		"other hash number":  `hash "md5" of "file.lua": not a string`,
	}

	for name, hashes := range cases {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			path := filepath.Join(base, "env_hashes.json")

			writeFile(t, filepath.Join(base, "file.lua"), content, 0o644)
			writeSignedHashes(t, key, path, hashes)

			var err error

			require.NotPanics(t, func() { _, err = initializeCheck(t, key, base) })
			require.ErrorContains(t, err, "malformed hashes file")
			require.ErrorContains(t, err, path)

			if violation, ok := reported[name]; ok {
				require.ErrorContains(t, err, violation)
			}
		})
	}

	t.Run("application hashes file", func(t *testing.T) {
		base := t.TempDir()
		path := filepath.Join(base, "hashes.json")

		writeFile(t, filepath.Join(base, "file.lua"), content, 0o644)
		writeSignedHashes(t, key, filepath.Join(base, "env_hashes.json"),
			hashesJSON(entry("file.lua", content)))
		writeSignedHashes(t, key, path, `{"files":[{"path":"file.lua"}]}`)

		_, err := initializeCheck(t, key, base)
		require.ErrorContains(t, err, "malformed hashes file")
		require.ErrorContains(t, err, path)
	})
}

func TestRSACheckReadsHashesFilesAsJSON(t *testing.T) {
	key := signingKey(t)
	content := "content\n"
	digest := sha256Hex(content)
	other := sha256Hex("other\n")

	cases := map[string]string{
		// The last occurrence of a member counts.
		"repeated entry members": `{"files":[{"path":"missing.lua","path":"file.lua",` +
			`"sha256":"` + other + `","sha256":"` + digest + `"}]}`,
		"repeated files": `{"files":[{"path":"missing.lua","sha256":"` + other + `"}],` +
			`"files":[{"path":"file.lua","sha256":"` + digest + `"}]}`,
		"escaped path": `{"files":[{"path":"file.lua","sha256":"` + digest + `"}]}`,
	}

	for name, hashes := range cases {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			writeFile(t, filepath.Join(base, "file.lua"), content, 0o644)
			writeSignedHashes(t, key, filepath.Join(base, "env_hashes.json"), hashes)

			ctx, err := initializeCheck(t, key, base)
			require.NoError(t, err)

			_, err = readThrough(t, ctx.Repository, filepath.Join(base, "file.lua"))
			require.NoError(t, err)
		})
	}

	t.Run("no entries", func(t *testing.T) {
		base := t.TempDir()
		writeFile(t, filepath.Join(base, "file.lua"), content, 0o644)
		writeSignedHashes(t, key, filepath.Join(base, "env_hashes.json"), hashesJSON())

		ctx, err := initializeCheck(t, key, base)
		require.NoError(t, err)

		_, err = readThrough(t, ctx.Repository, filepath.Join(base, "file.lua"))
		require.ErrorContains(t, err, "in repository")
	})
}

func TestRSACheckRefusesOtherAlgorithms(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()

	writeFile(t, filepath.Join(base, "file.lua"), "content\n", 0o644)
	writeSignedHashes(t, key, filepath.Join(base, "env_hashes.json"),
		`{"files":[{"path":"file.lua","md5":"f75b8179e4bbe7e2b4a074dcef62de95"}]}`)

	_, err := initializeCheck(t, key, base)
	require.ErrorContains(t, err, `"md5"`)
	require.ErrorContains(t, err, "file.lua")
	assert.NotContains(t, err.Error(), "malformed")
}

func TestRSACheckConflictingDigests(t *testing.T) {
	key := signingKey(t)
	good := entry("file.lua", "content\n")
	bad := entry("file.lua", "other\n")

	cases := map[string][]hashesEntry{
		"good first": {good, bad},
		"bad first":  {bad, good},
	}

	for name, entries := range cases {
		t.Run(name, func(t *testing.T) {
			base := t.TempDir()
			writeFile(t, filepath.Join(base, "file.lua"), "content\n", 0o644)
			writeSignedHashes(t, key, filepath.Join(base, "env_hashes.json"),
				hashesJSON(entries...))

			_, err := initializeCheck(t, key, base)
			require.ErrorContains(t, err, "hash mismatch")
		})
	}

	t.Run("two names for one file", func(t *testing.T) {
		base := t.TempDir()
		writeFile(t, filepath.Join(base, "file.lua"), "content\n", 0o644)
		symlink(t, "file.lua", filepath.Join(base, "link.lua"))
		writeSignedHashes(t, key, filepath.Join(base, "env_hashes.json"), hashesJSON(
			entry("file.lua", "content\n"), entry("link.lua", "other\n")))

		_, err := initializeCheck(t, key, base)
		require.ErrorContains(t, err, "hash mismatch")
	})
}

func TestRSACheckRefusesOtherSaltLengths(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()
	path := filepath.Join(base, "env_hashes.json")
	content := hashesJSON(entry("file.lua", "content\n"))

	writeFile(t, filepath.Join(base, "file.lua"), "content\n", 0o644)
	writeFile(t, path, content, 0o644)
	writeFile(t, path+".sig", string(pssSign(t, key, []byte(content), 2*sha256.Size)), 0o644)

	_, err := initializeCheck(t, key, base)
	require.ErrorContains(t, err, verificationError)
}

func TestRSACheckJoinsPathsLexically(t *testing.T) {
	key := signingKey(t)
	root := t.TempDir()
	env := filepath.Join(root, "real", "env")
	link := filepath.Join(root, "link")

	// The configuration directory is a link: ".." leaves the link, not the
	// directory it points to.
	writeFile(t, filepath.Join(root, "outside.lua"), "lexical\n", 0o644)
	writeFile(t, filepath.Join(root, "real", "outside.lua"), "physical\n", 0o644)
	writeSignedHashes(t, key, filepath.Join(env, "env_hashes.json"),
		hashesJSON(entry("../outside.lua", "lexical\n")))
	symlink(t, env, link)

	ctx, err := initializeCheck(t, key, link)
	require.NoError(t, err)

	data, err := readThrough(t, ctx.Repository, filepath.Join(root, "outside.lua"))
	require.NoError(t, err)
	assert.Equal(t, "lexical\n", data)

	_, err = initializeCheck(t, key, env)
	require.ErrorContains(t, err, "hash mismatch")
}

func TestRSACheckResolvesLinksOnce(t *testing.T) {
	key := signingKey(t)
	base := t.TempDir()
	one := filepath.Join(base, "versions", "one")
	link := filepath.Join(base, "bin", "tt")

	writeFile(t, filepath.Join(base, "tt.yaml"), "env: {}\n", 0o644)
	writeFile(t, one, "one\n", 0o755)
	writeFile(t, filepath.Join(base, "versions", "two"), "two\n", 0o755)
	writeFile(t, filepath.Join(base, "bin", "other"), "other\n", 0o755)
	symlink(t, one, link)

	require.NoError(t, newSigner(t, key).Sign(base, []string{}))
	assert.Equal(t, hashesJSON(
		entry("bin/other", "other\n"),
		entry("bin/tt", "one\n"),
		entry("tt.yaml", "env: {}\n"),
	), readFile(t, filepath.Join(base, "env_hashes.json")))

	ctx, err := initializeCheck(t, key, base)
	require.NoError(t, err)

	// Redirected to a file the repository does not know: Read refuses it,
	// ValidateAll still checks the target the link had.
	require.NoError(t, os.Remove(link))
	symlink(t, filepath.Join(base, "versions", "two"), link)

	require.NoError(t, ctx.Repository.ValidateAll())

	_, err = readThrough(t, ctx.Repository, link)
	require.ErrorContains(t, err, "in repository")

	// Redirected to a file the repository knows: it is checked against the
	// digest of that file.
	require.NoError(t, os.Remove(link))
	symlink(t, filepath.Join(base, "bin", "other"), link)

	data, err := readThrough(t, ctx.Repository, link)
	require.NoError(t, err)
	assert.Equal(t, "other\n", data)

	writeFile(t, one, "tampered\n", 0o755)
	require.ErrorContains(t, ctx.Repository.ValidateAll(),
		mismatch(resolve(t, one), "one\n", "tampered\n"))
}

func TestRSACheckReadsRelativePaths(t *testing.T) {
	key := signingKey(t)
	base := signedEnvironment(t, key)

	ctx, err := initializeCheck(t, key, base)
	require.NoError(t, err)

	t.Chdir(filepath.Join(base, "bin"))

	for _, path := range []string{"../init.lua", "tt"} {
		_, err := readThrough(t, ctx.Repository, path)
		require.NoError(t, err, path)
	}

	_, err = readThrough(t, ctx.Repository, "missing")
	require.ErrorIs(t, err, os.ErrNotExist)
}
