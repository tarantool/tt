package distbuild

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/manifest"
)

// standardSource is a package standard that lists aeon, emmylua and wasm.
const standardSource = `package standard

import "github.com/tarantool/tt/sdk"

// Modules is a method elsewhere, which is not the list.
func (x) Modules() map[string]sdk.Constructor { return nil }

func Modules() map[string]sdk.Constructor {
	return map[string]sdk.Constructor{
		"wasm":    wasm.New,
		"aeon":    aeon.New,
		"emmylua": emmylua.New,
	}
}
`

// standardCore writes a core with a package standard into a new directory.
func standardCore(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "standard", "standard.go"), standardSource)
	writeFile(t, filepath.Join(dir, "standard", "standard_test.go"),
		"package standard\n\nfunc Modules() {}\n")

	return dir
}

func TestReadStandardOfThisCore(t *testing.T) {
	t.Parallel()

	// The list tt's own main package is built from is one the build reads.
	names, found, err := readStandard(filepath.Join("..", "..", "..", standardPackage))
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, []string{"aeon", "emmylua"}, names)
}

func TestReadStandard(t *testing.T) {
	t.Parallel()

	names, found, err := readStandard(filepath.Join(standardCore(t), standardPackage))
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, []string{"aeon", "emmylua", "wasm"}, names, "in order")

	_, found, err = readStandard(filepath.Join(t.TempDir(), standardPackage))
	require.NoError(t, err)
	assert.False(t, found, "a core without the package")

	for name, src := range map[string]string{
		"no func": "package standard\n",
		"computed key": "package standard\n\nfunc Modules() map[string]any {\n" +
			"\treturn map[string]any{name: x}\n}\n",
		"no literal": "package standard\n\nfunc Modules() map[string]any {\n\treturn list()\n}\n",
		"two statements": "package standard\n\nfunc Modules() map[string]any {\n" +
			"\tm := map[string]any{}\n\treturn m\n}\n",
		"not a map": "package standard\n\nfunc Modules() []any {\n\treturn []any{x}\n}\n",
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			dir := t.TempDir()
			writeFile(t, filepath.Join(dir, "standard.go"), src)

			_, found, err := readStandard(dir)
			require.ErrorIs(t, err, errNoStandardList)
			assert.True(t, found)
		})
	}
}

func TestResolveStandard(t *testing.T) {
	t.Parallel()

	dir := standardCore(t)
	local := core{Module: coreModule, Dir: dir, Path: "../tt"}
	res := resolver{gocmd: newFakeGo(), dir: "/src/ee"}

	set, err := res.resolveStandard(t.Context(), parse(t, ""), local)
	require.NoError(t, err)
	assert.Equal(t, standardSet{Enabled: true, Modules: []string{"aeon", "emmylua", "wasm"}}, set)

	// The excluded ones and those [dependencies] takes the place of are not
	// the core's in the binary; builtin is no module of the list.
	set, err = res.resolveStandard(t.Context(), parse(t, `[distribution]
exclude = ["emmylua", "builtin"]

[dependencies]
wasm = "github.com/acme/tt-wasm@v1.0.0"
`), local)
	require.NoError(t, err)
	assert.Equal(t, standardSet{
		Enabled: true, Modules: []string{"aeon"}, Excluded: []string{"emmylua"},
	}, set)

	set, err = res.resolveStandard(t.Context(), parse(t, "[distribution]\nstandard = false\n"),
		core{Module: coreModule, Dir: t.TempDir(), Path: "../old"})
	require.NoError(t, err)
	assert.Equal(t, standardSet{}, set, "standard = false reads no core")

	_, err = res.resolveStandard(t.Context(),
		parse(t, "[distribution]\nexclude = [\"tcm\"]\n"), local)
	require.ErrorContains(t, err, `distribution.exclude: "tcm" is not a standard module `+
		`of the core ../tt: they are aeon, emmylua, wasm, builtin`)

	_, err = res.resolveStandard(t.Context(), parse(t, ""),
		core{Module: coreModule, Dir: t.TempDir(), Path: "../old"})
	require.ErrorContains(t, err, "distribution.standard: the core ../old has no package "+
		"github.com/tarantool/tt/v3/standard that lists the standard modules: standard "+
		"needs core "+standardSince+" or later; set standard = false and list the modules "+
		"in [dependencies]")
}

func TestResolveStandardOfVersionedCore(t *testing.T) {
	t.Parallel()

	gocmd := newFakeGo()

	gocmd.downloads = map[string]string{coreModule + "@" + corePseudo: standardCore(t)}

	res := resolver{gocmd: gocmd, dir: "/src/ee"}
	versioned := core{Module: coreModule, Version: corePseudo, Revision: coreHash}

	set, err := res.resolveStandard(t.Context(), parse(t, ""), versioned)
	require.NoError(t, err)
	assert.Equal(t, []string{"aeon", "emmylua", "wasm"}, set.Modules)
	assert.Contains(t, gocmd.calls, "mod download -json "+coreModule+"@"+corePseudo)

	old := core{Module: coreModule, Version: "v3.0.0-alpha.2"}

	gocmd.downloads[coreModule+"@v3.0.0-alpha.2"] = t.TempDir()

	_, err = res.resolveStandard(t.Context(), parse(t, ""), old)
	require.ErrorContains(t, err, "the core "+coreModule+"@v3.0.0-alpha.2 has no package")
}

func TestCheckLockedStandard(t *testing.T) {
	t.Parallel()

	lock := &manifest.DistributionLock{Core: manifest.LockedCore{Standard: []string{"aeon"}}}

	require.NoError(t, checkLockedStandard(lock, standardSet{Enabled: true,
		Modules: []string{"aeon"}}))

	err := checkLockedStandard(lock, standardSet{Enabled: true,
		Modules: []string{"aeon", "emmylua"}})
	require.ErrorIs(t, err, errStaleLock)
	require.ErrorContains(t, err, "[aeon], the build takes [aeon, emmylua]")

	// A lock without standard modules is that of a distribution with none.
	require.NoError(t, checkLockedStandard(&manifest.DistributionLock{}, standardSet{}))
}
