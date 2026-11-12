package deps

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// rewriteBase is the lock every case of TestLockRewrites edits a copy of:
// two products sharing a path dependency, a registry rock with digests, and a
// dev closure.
func rewriteBase() *manifest.Lock {
	vendored := manifest.LockDependency{
		Name: "vendored", Version: "scm-1", Source: "path",
		Path: "vendor/vendored", ContentHash: "sha256:a",
	}
	checks := manifest.LockDependency{
		Name: "checks", Version: "3.1.0-1", Source: "registry",
		Artifacts: map[string]string{"all": "sha256:a"},
	}

	return &manifest.Lock{
		LockVersion:     manifest.LockVersion,
		ManifestVersion: manifest.ManifestVersion,
		GeneratedBy:     "tt 3.0.0",
		ManifestHash:    staleHash,
		Products: map[string]manifest.LockProduct{
			"api":     {Dependencies: []manifest.LockDependency{checks, vendored}},
			"storage": {Dependencies: []manifest.LockDependency{checks, vendored}},
		},
		DevDependencies: []manifest.LockDependency{
			{Name: "luatest", Version: "1.0.0-1", Source: "registry"},
		},
	}
}

// editDependency edits the entry of name in the closure of product. A name
// the closure does not hold edits nothing, which the case then fails on.
func editDependency(lock *manifest.Lock, product, name string,
	edit func(*manifest.LockDependency),
) {
	closure := append([]manifest.LockDependency(nil), lock.Products[product].Dependencies...)
	for i := range closure {
		if closure[i].Name == name {
			edit(&closure[i])
		}
	}

	lock.Products[product] = manifest.LockProduct{Dependencies: closure}
}

// cloneLock copies a lock deep enough for a case to edit it.
func cloneLock(lock *manifest.Lock) *manifest.Lock {
	out := *lock

	out.Products = make(map[string]manifest.LockProduct, len(lock.Products))
	for name, product := range lock.Products {
		out.Products[name] = manifest.LockProduct{
			Dependencies: append([]manifest.LockDependency(nil), product.Dependencies...),
		}
	}

	out.DevDependencies = append([]manifest.LockDependency(nil), lock.DevDependencies...)

	return &out
}

// TestLockRewrites pins what a run that resolved the lock again reports
// beyond the moves: every rewrite of the lock that moves no version gets a
// note, so that no rewritten lock reads as one that is up to date.
func TestLockRewrites(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name  string
		edit  func(lock *manifest.Lock)
		moves []Move
		want  []string
	}{
		{
			name: "nothing",
			edit: func(*manifest.Lock) {},
			want: nil,
		},
		{
			name: "the manifest hash",
			edit: func(lock *manifest.Lock) { lock.ManifestHash = "toml1-sha256:1" },
			want: []string{"manifest hash refreshed"},
		},
		{
			name: "the tt that wrote it",
			edit: func(lock *manifest.Lock) { lock.GeneratedBy = "tt 3.1.0" },
			want: []string{`generated_by is "tt 3.1.0", was "tt 3.0.0"`},
		},
		{
			name: "a path dependency of two products",
			edit: func(lock *manifest.Lock) {
				for _, product := range []string{"api", "storage"} {
					editDependency(lock, product, "vendored", func(dep *manifest.LockDependency) {
						dep.ContentHash = "sha256:b"
					})
				}
			},
			want: []string{`path dependency "vendored" re-pinned: its files changed`},
		},
		{
			name: "the digests",
			edit: func(lock *manifest.Lock) {
				editDependency(lock, "api", "checks", func(dep *manifest.LockDependency) {
					dep.Artifacts = map[string]string{"all": "sha256:b"}
				})
			},
			want: []string{`file digests of "checks" pinned anew`},
		},
		{
			name: "the digests of a rock of a server that lists none",
			edit: func(lock *manifest.Lock) {
				editDependency(lock, "api", "checks", func(dep *manifest.LockDependency) {
					dep.Artifacts = nil
				})
			},
			want: []string{`file digests of "checks" dropped: its server lists none`},
		},
		{
			name: "the digests of a rock that had none",
			edit: func(lock *manifest.Lock) {
				lock.DevDependencies[0].Artifacts = map[string]string{"all": "sha256:b"}
			},
			want: []string{`file digests of "luatest" pinned; the lock had none`},
		},
		{
			name: "a source",
			edit: func(lock *manifest.Lock) {
				editDependency(lock, "api", "checks", func(dep *manifest.LockDependency) {
					dep.Source, dep.Path, dep.ContentHash = "path", "vendor/checks", "sha256:c"
					dep.Artifacts = nil
				})
			},
			want: []string{
				`"checks" is from path "vendor/checks", was from registry`,
				`file digests of "checks" dropped: a path dependency is pinned by content_hash`,
			},
		},
		{
			name: "a version moves did not take",
			edit: func(lock *manifest.Lock) {
				editDependency(lock, "storage", "checks", func(dep *manifest.LockDependency) {
					dep.Version = "3.2.0-1"
				})
			},
			want: []string{`"checks" 3.1.0-1 -> 3.2.0-1 in product "storage"`},
		},
		{
			name: "a version moves took",
			edit: func(lock *manifest.Lock) {
				editDependency(lock, "api", "checks", func(dep *manifest.LockDependency) {
					dep.Version = "3.2.0-1"
				})
			},
			moves: []Move{{Name: "checks", From: "3.1.0-1", To: "3.2.0-1"}},
			want:  nil,
		},
		{
			name: "a rock that left a closure and joined another",
			edit: func(lock *manifest.Lock) {
				luatest := lock.DevDependencies[0]

				lock.DevDependencies = nil
				lock.Products["api"] = manifest.LockProduct{Dependencies: append(
					lock.Products["api"].Dependencies, luatest)}
			},
			want: []string{`"luatest" joined product "api"`, `"luatest" left the dev dependencies`},
		},
		{
			name: "a product",
			edit: func(lock *manifest.Lock) {
				lock.Products["cli"] = lock.Products["api"]
				delete(lock.Products, "storage")
			},
			want: []string{`product "cli" added`, `product "storage" dropped`},
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			before := rewriteBase()
			source, err := before.Marshal()
			require.NoError(t, err)

			after := cloneLock(before)
			testCase.edit(after)

			written, err := after.Marshal()
			require.NoError(t, err)

			assert.Equal(t, testCase.want,
				lockRewrites(before, source, after, written, testCase.moves))
		})
	}
}

// TestLockRewritesOfBytes pins the case the parsed locks cannot tell: a lock
// on disk that says the same as the one written in other bytes.
func TestLockRewritesOfBytes(t *testing.T) {
	t.Parallel()

	lock := rewriteBase()

	written, err := lock.Marshal()
	require.NoError(t, err)

	assert.Empty(t, lockRewrites(lock, written, lock, written, nil))
	assert.Equal(t, []string{"lock rewritten in the form tt writes it"},
		lockRewrites(lock, append([]byte("# by hand\n"), written...), lock, written, nil))
}

// withChecksums renders lock as an older tt wrote it: the entries of the
// rocks named carry the md5 of their source under checksum.
func withChecksums(t *testing.T, lock *manifest.Lock, names ...string) []byte {
	t.Helper()

	data, err := lock.Marshal()
	require.NoError(t, err)

	text := string(data)

	for _, name := range names {
		entry := fmt.Sprintf("name = '%s'\n", name)

		require.Contains(t, text, entry)

		text = strings.ReplaceAll(text, entry,
			entry+"checksum = 'md5:0cc175b9c0f1b6a831c399e269772661'\n")
	}

	return []byte(text)
}

// TestLockRewritesOfChecksums pins what a resolve over a lock of an older tt
// says of the checksum keys it drops: the rocks they were of, which the
// parsed locks cannot tell, beside whatever else changed, and nothing of a
// rock that left the lock.
func TestLockRewritesOfChecksums(t *testing.T) {
	t.Parallel()

	const why = " dropped: tt no longer locks the md5 of the source of a rock"

	before := rewriteBase()

	written, err := before.Marshal()
	require.NoError(t, err)

	// The very lock but for the checksums: not a rewrite of the form alone.
	assert.Equal(t, []string{`checksum of "checks"` + why},
		lockRewrites(before, withChecksums(t, before, "checks"), before, written, nil))

	stamped := cloneLock(before)

	stamped.GeneratedBy = "tt 3.1.0"

	stampedBytes, err := stamped.Marshal()
	require.NoError(t, err)

	assert.Equal(t, []string{
		`generated_by is "tt 3.1.0", was "tt 3.0.0"`,
		`checksums of "checks", "luatest"` + why,
	}, lockRewrites(before, withChecksums(t, before, "luatest", "checks"),
		stamped, stampedBytes, nil))

	// luatest left: the move says so, and its checksum goes unnamed.
	gone := cloneLock(before)

	gone.DevDependencies = nil

	goneBytes, err := gone.Marshal()
	require.NoError(t, err)

	moves := []Move{{Name: "luatest", From: "1.0.0-1"}}
	assert.Equal(t, []string{`checksum of "checks"` + why},
		lockRewrites(before, withChecksums(t, before, "luatest", "checks"),
			gone, goneBytes, moves))
}

// TestLockRewritesOfAFirstLock pins what the first lock of a project reports:
// nothing beyond the arrivals of the moves, and that the lock was written
// when there are none - nothing to pin, or only a path dependency without a
// version, which moves do not report.
func TestLockRewritesOfAFirstLock(t *testing.T) {
	t.Parallel()

	lock := rewriteBase()
	assert.Empty(t, lockRewrites(nil, nil, lock, nil, movesBetween(nil, lock)))

	empty := &manifest.Lock{Products: map[string]manifest.LockProduct{"default": {}}}
	assert.Equal(t, []string{"lock written; there was none"},
		lockRewrites(nil, nil, empty, nil, nil))

	files := &manifest.Lock{Products: map[string]manifest.LockProduct{
		"default": {Dependencies: []manifest.LockDependency{{
			Name: "helper", Source: "path", Path: "vendor/helper", ContentHash: "sha256:a",
		}}},
	}}
	moves := movesBetween(nil, files)
	assert.Empty(t, moves)
	assert.Equal(t, []string{"lock written; there was none", `"helper" joined product "default"`},
		lockRewrites(nil, nil, files, nil, moves))
}
