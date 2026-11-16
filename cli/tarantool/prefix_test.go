package tarantool_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/tarantool"
)

// tempDir is t.TempDir with its symbolic links resolved. ResolvePrefix looks
// along the real path of the executable first, so it returns the real path
// of the prefix: on macOS the temporary directory is under /var, a link to
// /private/var.
func tempDir(t *testing.T) string {
	t.Helper()

	dir, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)

	return dir
}

// stubTarantool writes at path a tarantool that prints the banner of
// tarantool --version of version, built for the install prefix built.
func stubTarantool(t *testing.T, path, version, built string) {
	t.Helper()

	banner := fmt.Sprintf("Tarantool %s\nTarget: Linux-x86_64-RelWithDebInfo\n"+
		"Build options: cmake . -DCMAKE_INSTALL_PREFIX=%s -DENABLE_BACKTRACE=TRUE\n",
		version, built)
	writeScript(t, path, banner)
}

// writeScript writes at path a tarantool that prints banner.
func writeScript(t *testing.T, path, banner string) {
	t.Helper()

	require.NoError(t, os.MkdirAll(filepath.Dir(path), dirMode))
	require.NoError(t, os.WriteFile(path,
		[]byte("#!/bin/sh\ncat <<'EOF'\n"+banner+"EOF\n"), execMode))
}

// writeHeaders writes the headers of Tarantool version, major.minor.patch,
// under prefix, and returns their directory.
func writeHeaders(t *testing.T, prefix, version string) string {
	t.Helper()

	parts := strings.Split(version, ".")
	require.Len(t, parts, 3)

	include := filepath.Join(prefix, "include", "tarantool")
	header := fmt.Sprintf("#ifndef TARANTOOL_MODULE_H_INCLUDED\n"+
		"#define PACKAGE_VERSION_MAJOR %s\n#define PACKAGE_VERSION_MINOR %s\n"+
		"#define PACKAGE_VERSION_PATCH %s\n#define PACKAGE_VERSION \"%s-0-gabcdef\"\n",
		parts[0], parts[1], parts[2], version)

	require.NoError(t, os.MkdirAll(include, dirMode))
	require.NoError(t, os.WriteFile(filepath.Join(include, "module.h"), []byte(header), fileMode))

	return include
}

// resolve resolves the prefix of executable with no prefix named, and an
// empty directory of its own as the prefix of the system packages, so that
// the headers the host has in /usr change nothing.
func resolve(t *testing.T, executable string) tarantool.Prefix {
	t.Helper()

	return resolveWith(t, executable, tempDir(t))
}

// resolveWith resolves the prefix of executable with no prefix named, system
// being the prefix of the system packages.
func resolveWith(t *testing.T, executable, system string) tarantool.Prefix {
	t.Helper()

	prefix, err := tarantool.ResolvePrefix(tarantool.PrefixQuery{
		Project: "", Env: "", Executable: executable, System: system,
	})
	require.NoError(t, err)

	return prefix
}

func TestResolvePrefixStandard(t *testing.T) {
	root := tempDir(t)
	exe := filepath.Join(root, "bin", "tarantool")
	stubTarantool(t, exe, "3.3.5-0-gabcdef", "/nonexistent/prefix")

	include := writeHeaders(t, root, "3.3.5")

	prefix := resolve(t, exe)
	assert.Equal(t, root, prefix.Dir)
	assert.Equal(t, include, prefix.IncludeDir)
	assert.Equal(t, tarantool.PrefixOriginExecutable, prefix.Origin)
	assert.Equal(t, "3.3.5-0-gabcdef", prefix.Version)
	assert.Equal(t, "3.3.5-0-gabcdef", prefix.HeadersVersion)
	assert.Empty(t, prefix.Warnings)
	assert.Empty(t, prefix.MissingHeaders())
}

func TestResolvePrefixSDK(t *testing.T) {
	sdk := tempDir(t)
	exe := filepath.Join(sdk, "tarantool")
	stubTarantool(t, exe, "Enterprise 3.7.0-0-g1f1ec9fdf", "/usr")

	include := writeHeaders(t, sdk, "3.7.0")

	prefix := resolve(t, exe)
	assert.Equal(t, sdk, prefix.Dir)
	assert.Equal(t, include, prefix.IncludeDir)
	assert.Equal(t, tarantool.PrefixOriginExecutable, prefix.Origin)
	assert.Equal(t, []string{filepath.Join(filepath.Dir(sdk), "include", "tarantool"), include},
		prefix.Tried, "the directory above the executable comes first")
	assert.Empty(t, prefix.Warnings)
}

// ttScope lays out a scope of tt binary install with tarantool 3.3.5 active,
// and returns its root, the version directory and the active link.
func ttScope(t *testing.T) (string, string, string) {
	t.Helper()

	root := tempDir(t)
	version := filepath.Join(root, "versions", "tarantool", "tarantool_3.3.5")
	stubTarantool(t, filepath.Join(version, "tarantool"), "3.3.5-0-gabcdef", "/usr")

	link := filepath.Join(root, "bin", "tarantool")
	require.NoError(t, os.MkdirAll(filepath.Dir(link), dirMode))
	require.NoError(t, os.Symlink(filepath.Join("..", "versions", "tarantool",
		"tarantool_3.3.5", "tarantool"), link))

	return root, version, link
}

func TestResolvePrefixScope(t *testing.T) {
	root, version, link := ttScope(t)
	include := writeHeaders(t, version, "3.3.5")

	require.NoError(t, os.MkdirAll(filepath.Join(root, "include"), dirMode))
	require.NoError(t, os.Symlink(filepath.Join("..", "versions", "tarantool",
		"tarantool_3.3.5", "include", "tarantool"), filepath.Join(root, "include", "tarantool")))

	// The headers of the very version the link points at come first.
	prefix := resolve(t, link)
	assert.Equal(t, version, prefix.Dir)
	assert.Equal(t, include, prefix.IncludeDir)
	assert.Equal(t, tarantool.PrefixOriginExecutable, prefix.Origin)
}

func TestResolvePrefixScopeActiveHeaders(t *testing.T) {
	root, _, link := ttScope(t)
	include := writeHeaders(t, root, "3.3.5")

	// With none in the version directory, those of the scope are taken.
	prefix := resolve(t, link)
	assert.Equal(t, root, prefix.Dir)
	assert.Equal(t, include, prefix.IncludeDir)
	assert.Equal(t, tarantool.PrefixOriginExecutable, prefix.Origin)
}

func TestResolvePrefixSymlinkedDir(t *testing.T) {
	sdk := tempDir(t)
	stubTarantool(t, filepath.Join(sdk, "tarantool"), "3.7.0-0-g1f1ec9fdf", "/usr")

	include := writeHeaders(t, sdk, "3.7.0")

	link := filepath.Join(tempDir(t), "sdk")
	require.NoError(t, os.Symlink(sdk, link))

	prefix := resolve(t, filepath.Join(link, "tarantool"))
	assert.Equal(t, sdk, prefix.Dir)
	assert.Equal(t, include, prefix.IncludeDir)

	// A bin directory that is a link: the prefix is the one it is found in.
	root := tempDir(t)
	elsewhere := tempDir(t)
	stubTarantool(t, filepath.Join(elsewhere, "tarantool"), "3.7.0-0-g1f1ec9fdf", "/usr")
	require.NoError(t, os.Symlink(elsewhere, filepath.Join(root, "bin")))

	include = writeHeaders(t, root, "3.7.0")

	prefix = resolve(t, filepath.Join(root, "bin", "tarantool"))
	assert.Equal(t, root, prefix.Dir)
	assert.Equal(t, include, prefix.IncludeDir)
}

func TestResolvePrefixBuild(t *testing.T) {
	built := tempDir(t)
	include := writeHeaders(t, built, "3.3.5")

	exe := filepath.Join(tempDir(t), "bin", "tarantool")
	stubTarantool(t, exe, "3.3.5-0-gabcdef", built)

	prefix := resolve(t, exe)
	assert.Equal(t, built, prefix.Dir)
	assert.Equal(t, include, prefix.IncludeDir)
	assert.Equal(t, tarantool.PrefixOriginBuild, prefix.Origin)
}

func TestResolvePrefixBuildOtherVersion(t *testing.T) {
	// Another Tarantool installed into the prefix the build names, or into
	// /usr, is not the tarantool run: its headers are passed over.
	built := tempDir(t)
	include := writeHeaders(t, built, "3.9.0")

	dir := tempDir(t)
	exe := filepath.Join(dir, "bin", "tarantool")
	stubTarantool(t, exe, "3.3.5-0-gabcdef", built)

	prefix := resolve(t, exe)
	assert.NotEqual(t, built, prefix.Dir, "the guess is no prefix of the headers passed over")
	assert.NoFileExists(t, filepath.Join(prefix.Dir, "include", "tarantool", "module.h"))
	assert.Empty(t, prefix.IncludeDir)
	assert.Equal(t, tarantool.PrefixOriginGuess, prefix.Origin)
	assert.Empty(t, prefix.Warnings)
	assert.Equal(t, "headers of 3.9.0-0-gabcdef, the tarantool is 3.3.5-0-gabcdef",
		prefix.Rejected[include])
	assert.Contains(t, prefix.MissingHeaders(),
		include+": headers of 3.9.0-0-gabcdef, the tarantool is 3.3.5-0-gabcdef")
	assert.Contains(t, prefix.MissingHeaders(),
		"no module.h in "+filepath.Join(dir, "include", "tarantool")+", ")

	// Of the version of the executable, major.minor.patch, they are taken.
	writeHeaders(t, built, "3.3.5")

	prefix = resolve(t, exe)
	assert.Equal(t, include, prefix.IncludeDir)
	assert.Equal(t, tarantool.PrefixOriginBuild, prefix.Origin)
	assert.Empty(t, prefix.Rejected)

	// A version that is not known is not the same.
	writeScript(t, exe, "Tarantool dev\nTarget: Linux\nBuild options: cmake . "+
		"-DCMAKE_INSTALL_PREFIX="+built+" -DA=1\n")

	prefix = resolve(t, exe)
	assert.Empty(t, prefix.IncludeDir)
	assert.Equal(t, "headers of 3.3.5-0-gabcdef, the tarantool is dev",
		prefix.Rejected[include])
}

func TestResolvePrefixBuildWithoutHeaders(t *testing.T) {
	built := tempDir(t)
	dir := tempDir(t)
	system := tempDir(t)
	exe := filepath.Join(dir, "bin", "tarantool")
	stubTarantool(t, exe, "3.3.5-0-gabcdef", built)

	// The prefix the build names exists, but has no headers: it is only the
	// guess, which builds what compiles no C.
	prefix := resolveWith(t, exe, system)
	assert.Equal(t, built, prefix.Dir)
	assert.Empty(t, prefix.IncludeDir)
	assert.Equal(t, tarantool.PrefixOriginGuess, prefix.Origin)
	assert.Equal(t, []string{
		filepath.Join(dir, "include", "tarantool"),
		filepath.Join(dir, "bin", "include", "tarantool"),
		filepath.Join(built, "include", "tarantool"),
		filepath.Join(system, "include", "tarantool"),
	}, prefix.Tried)
}

func TestResolvePrefixSystemOtherVersion(t *testing.T) {
	// The headers of the system packages, as a CI runner has in /usr, are of
	// another Tarantool than the one run: they are passed over, and so is
	// their prefix as the guess, which a build would compile against.
	system := tempDir(t)
	include := writeHeaders(t, system, "3.9.0")

	dir := tempDir(t)
	exe := filepath.Join(dir, "tarantool")
	stubTarantool(t, exe, "3.8.1-0-ge643f88a5e0c", "/tarantool/static-build/tarantool-prefix")

	prefix := resolveWith(t, exe, system)
	assert.Empty(t, prefix.IncludeDir)
	assert.Equal(t, tarantool.PrefixOriginGuess, prefix.Origin)
	assert.Equal(t, filepath.Dir(dir), prefix.Dir,
		"the guess is the directory above the executable, not the system prefix")
	assert.Equal(t, "headers of 3.9.0-0-gabcdef, the tarantool is 3.8.1-0-ge643f88a5e0c",
		prefix.Rejected[include])
	assert.Contains(t, prefix.MissingHeaders(),
		include+": headers of 3.9.0-0-gabcdef, the tarantool is 3.8.1-0-ge643f88a5e0c")

	// Of the version of the executable, they are taken.
	writeHeaders(t, system, "3.8.1")

	prefix = resolveWith(t, exe, system)
	assert.Equal(t, system, prefix.Dir)
	assert.Equal(t, include, prefix.IncludeDir)
	assert.Equal(t, tarantool.PrefixOriginDefault, prefix.Origin)
}

func TestResolvePrefixRelocated(t *testing.T) {
	root := tempDir(t)
	exe := filepath.Join(root, "bin", "tarantool")
	stubTarantool(t, exe, "3.8.1-0-ge643f88a5e0c", "/tarantool/static-build/tarantool-prefix")

	include := writeHeaders(t, root, "3.8.1")

	prefix := resolve(t, exe)
	assert.Equal(t, root, prefix.Dir)
	assert.Equal(t, include, prefix.IncludeDir)
}

func TestResolvePrefixNothingFound(t *testing.T) {
	dir := tempDir(t)
	system := tempDir(t)
	exe := filepath.Join(dir, "tarantool")
	stubTarantool(t, exe, "3.8.1-0-ge643f88a5e0c", "/tarantool/static-build/tarantool-prefix")

	prefix := resolveWith(t, exe, system)
	assert.Equal(t, system, prefix.Dir,
		"a prefix that does not exist is replaced with the prefix of the system packages")
	assert.Empty(t, prefix.IncludeDir)
	assert.Equal(t, tarantool.PrefixOriginGuess, prefix.Origin)

	tried := []string{
		filepath.Join(filepath.Dir(dir), "include", "tarantool"),
		filepath.Join(dir, "include", "tarantool"),
		"/tarantool/static-build/tarantool-prefix/include/tarantool",
		filepath.Join(system, "include", "tarantool"),
	}
	assert.Equal(t, tried, prefix.Tried)

	missing := prefix.MissingHeaders()
	for _, path := range tried {
		assert.Contains(t, missing, path)
	}

	assert.Contains(t, missing, exe)
	assert.Contains(t, missing, "set "+tarantool.PrefixEnv+" to the install prefix")
}

func TestResolvePrefixOverrides(t *testing.T) {
	root := tempDir(t)
	exe := filepath.Join(root, "bin", "tarantool")
	stubTarantool(t, exe, "3.3.5-0-gabcdef", "/usr")
	writeHeaders(t, root, "3.3.5")

	env := tempDir(t)
	project := tempDir(t)

	// The prefix named wins, headers there or not.
	prefix, err := tarantool.ResolvePrefix(tarantool.PrefixQuery{Env: env, Executable: exe})
	require.NoError(t, err)
	assert.Equal(t, env, prefix.Dir)
	assert.Equal(t, tarantool.PrefixOriginEnv, prefix.Origin)
	assert.Empty(t, prefix.IncludeDir)
	assert.Equal(t, []string{filepath.Join(env, "include", "tarantool")}, prefix.Tried)
	assert.Contains(t, prefix.MissingHeaders(), tarantool.PrefixEnv+"="+env)

	prefix, err = tarantool.ResolvePrefix(tarantool.PrefixQuery{
		Project: project, Env: env, Executable: exe,
	})
	require.NoError(t, err)
	assert.Equal(t, project, prefix.Dir)
	assert.Equal(t, tarantool.PrefixOriginProject, prefix.Origin)
	assert.Contains(t, prefix.MissingHeaders(), "tt binary install tarantool")

	include := writeHeaders(t, env, "3.3.5")

	prefix, err = tarantool.ResolvePrefix(tarantool.PrefixQuery{Env: env, Executable: exe})
	require.NoError(t, err)
	assert.Equal(t, include, prefix.IncludeDir)
	assert.Empty(t, prefix.MissingHeaders())

	// A prefix named needs no executable that runs.
	prefix, err = tarantool.ResolvePrefix(tarantool.PrefixQuery{Env: env})
	require.NoError(t, err)
	assert.Equal(t, env, prefix.Dir)
}

func TestResolvePrefixVersionMismatch(t *testing.T) {
	sdk := tempDir(t)
	exe := filepath.Join(sdk, "tarantool")
	stubTarantool(t, exe, "3.3.5-0-gabcdef", "/usr")

	include := writeHeaders(t, sdk, "3.3.4")

	prefix := resolve(t, exe)
	assert.Equal(t, include, prefix.IncludeDir, "headers of another version are taken")
	require.Len(t, prefix.Warnings, 1)
	assert.Contains(t, prefix.Warnings[0], "3.3.4-0-gabcdef")
	assert.Contains(t, prefix.Warnings[0], "3.3.5-0-gabcdef")
	assert.Contains(t, prefix.Warnings[0], include)

	// Those of a prefix named are checked as well.
	prefix, err := tarantool.ResolvePrefix(tarantool.PrefixQuery{Env: sdk, Executable: exe})
	require.NoError(t, err)
	assert.Len(t, prefix.Warnings, 1)
}

func TestResolvePrefixBanner(t *testing.T) {
	dir := tempDir(t)
	exe := filepath.Join(dir, "bin", "tarantool")

	writeScript(t, exe, "Tarantool 3.3.5\nTarget: Linux-x86_64-RelWithDebInfo\n")

	_, err := tarantool.ResolvePrefix(tarantool.PrefixQuery{Executable: exe})
	require.ErrorIs(t, err, tarantool.ErrBannerShort)

	writeScript(t, exe, "Tarantool 3.3.5\nTarget: Linux-x86_64-RelWithDebInfo\n"+
		"Build options: cmake . -DENABLE_BACKTRACE=TRUE\n")

	_, err = tarantool.ResolvePrefix(tarantool.PrefixQuery{Executable: exe})
	require.ErrorIs(t, err, tarantool.ErrBannerNoPrefix)

	// The headers beside the executable need no banner.
	include := writeHeaders(t, dir, "3.3.5")

	prefix := resolve(t, exe)
	assert.Equal(t, include, prefix.IncludeDir)

	// The prefix ends at the first blank, whatever options follow it.
	built := tempDir(t)
	writeHeaders(t, built, "3.3.5")

	other := filepath.Join(tempDir(t), "tarantool")
	writeScript(t, other, "Tarantool 3.3.5\nTarget: Linux\nBuild options: cmake . "+
		"-DCMAKE_INSTALL_PREFIX="+built+" -DA=1 -DB=2\n")

	prefix = resolve(t, other)
	assert.Equal(t, built, prefix.Dir)
	assert.Equal(t, tarantool.PrefixOriginBuild, prefix.Origin)

	// An executable that does not run is an error when the prefix is to be
	// taken from its banner.
	_, err = tarantool.ResolvePrefix(tarantool.PrefixQuery{
		Executable: filepath.Join(tempDir(t), "missing"),
	})
	require.Error(t, err)
}
