package scaffold

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/cli/manifest"
)

// projectDir makes a temp directory with the given name under it, so the name
// derivation can be exercised: the package name comes from the directory, and
// t.TempDir()'s own name is not one a test can choose.
func projectDir(t *testing.T, name string) string {
	t.Helper()

	dir := filepath.Join(t.TempDir(), name)
	require.NoError(t, os.MkdirAll(dir, 0o750))

	return dir
}

// readManifest reads back the file Create wrote.
func readManifest(t *testing.T, dir string) string {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(dir, manifestFileName)) //nolint:gosec // temp path
	require.NoError(t, err)

	return string(data)
}

// TestCreate_writesAValidManifest is the acceptance criterion: what tt new
// writes has to survive the same parse and validation every later command runs
// it through.
func TestCreate_writesAValidManifest(t *testing.T) {
	t.Parallel()

	dir := projectDir(t, "my-app")

	result, err := Create(Options{ProjectDir: dir})
	require.NoError(t, err)

	assert.Equal(t, filepath.Join(dir, manifestFileName), result.Path)
	assert.Equal(t, "my-app", result.Package)

	man, warnings, err := manifest.ParseManifest([]byte(readManifest(t, dir)))
	require.NoError(t, err)
	require.Empty(t, warnings)

	validationWarnings, err := man.Validate()
	require.NoError(t, err)
	assert.Empty(t, validationWarnings)

	assert.Equal(t, manifest.ManifestVersion, man.ManifestVersion)
	assert.Equal(t, "my-app", man.Package.Name)
	assert.Equal(t, defaultTarantoolConstraint, man.Platform.Tarantool.Version)
	assert.Equal(t, defaultTtConstraint, man.Platform.Tt.Version)
	// The table is there and empty: tt package add splices into it, and an
	// absent table is a different edit from an empty one.
	assert.Contains(t, readManifest(t, dir), "[dependencies]")
	assert.Empty(t, man.Dependencies)
}

// TestCreate_secondRunRefuses: a manifest is hand-authored and may hold a whole
// project's dependencies, so "create a skeleton" is never consent to lose it.
func TestCreate_secondRunRefuses(t *testing.T) {
	t.Parallel()

	dir := projectDir(t, "my-app")

	_, err := Create(Options{ProjectDir: dir})
	require.NoError(t, err)

	before := readManifest(t, dir)
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, manifestFileName), []byte(before+"\n# edited by hand\n"), 0o600))

	_, err = Create(Options{ProjectDir: dir})
	require.ErrorIs(t, err, ErrManifestExists)
	assert.Equal(t, exitStateError, ExitCode(err))
	// The hand edit is still there: the refusal happened before any write.
	assert.Contains(t, readManifest(t, dir), "# edited by hand")
}

// TestCreate_derivesThePackageNameFromTheDirectory covers the names a directory
// can carry, including the ones that need normalizing.
func TestCreate_derivesThePackageNameFromTheDirectory(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"my-app":     "my-app",
		"MyApp":      "myapp",
		"My App v2":  "my-app-v2",
		"my_app":     "my-app",
		"app.tt":     "app-tt",
		"--weird--":  "weird",
		"2do":        fallbackPackageName, // Must start with a letter.
		"bin":        fallbackPackageName, // Reserved.
		"...":        fallbackPackageName, // Nothing left after normalizing.
		"tt-project": "tt-project",
	}

	for dirName, want := range cases {
		t.Run(dirName, func(t *testing.T) {
			t.Parallel()

			dir := projectDir(t, dirName)

			result, err := Create(Options{ProjectDir: dir})
			require.NoError(t, err)
			assert.Equal(t, want, result.Package)

			// Whatever the name came out as, the manifest still validates.
			man, _, err := manifest.ParseManifest([]byte(readManifest(t, dir)))
			require.NoError(t, err)
			_, err = man.Validate()
			require.NoError(t, err)
		})
	}
}

// TestCreate_warnsWhenTheDirectoryNameCannotBeUsed: the fallback is silent
// otherwise, and a package quietly named "app" is exactly the kind of thing
// nobody notices until it is packed.
func TestCreate_warnsWhenTheDirectoryNameCannotBeUsed(t *testing.T) {
	t.Parallel()

	var warnings []string

	dir := projectDir(t, "2do")

	result, err := Create(Options{
		ProjectDir: dir,
		Warn:       func(msg string) { warnings = append(warnings, msg) },
	})
	require.NoError(t, err)

	assert.Equal(t, fallbackPackageName, result.Package)
	require.Len(t, warnings, 1)
	assert.Contains(t, warnings[0], "2do")
	assert.Contains(t, warnings[0], "[package].name")
}

// TestCreate_usableNameWarnsAboutNothing is the other half of the rule above: a
// directory whose name works produces no diagnostics at all.
func TestCreate_usableNameWarnsAboutNothing(t *testing.T) {
	t.Parallel()

	var warnings []string

	dir := projectDir(t, "my-app")

	_, err := Create(Options{
		ProjectDir: dir,
		Warn:       func(msg string) { warnings = append(warnings, msg) },
	})
	require.NoError(t, err)
	assert.Empty(t, warnings)
}

// TestCreate_quotesLikeTheDependencyEditor: tt package add writes double-quoted
// values, so a skeleton quoted otherwise would make the first add read as a
// reformat of the file.
func TestCreate_quotesLikeTheDependencyEditor(t *testing.T) {
	t.Parallel()

	dir := projectDir(t, "my-app")

	_, err := Create(Options{ProjectDir: dir})
	require.NoError(t, err)

	source := readManifest(t, dir)
	assert.Contains(t, source, `name = "my-app"`)
	assert.NotContains(t, source, "'")

	// And the editor accepts the file: an add against the skeleton is the very
	// next thing a user does.
	editor, err := manifest.NewEditor([]byte(source))
	require.NoError(t, err)

	_, err = editor.SetDependency(manifest.TableDependencies, "checks", ">=3.0.0")
	require.NoError(t, err)

	edited, _, err := manifest.ParseManifest(editor.Bytes())
	require.NoError(t, err)
	assert.Equal(t, ">=3.0.0", edited.Dependencies["checks"].Version)
}

// TestSkeleton_commentedExamplesAreValid: the commented component and product
// block is the next step a user takes, by uncommenting it. If it does not parse
// and validate, it is worse than absent.
func TestSkeleton_commentedExamplesAreValid(t *testing.T) {
	t.Parallel()

	uncommented := uncomment(render("my-app"))

	man, warnings, err := manifest.ParseManifest([]byte(uncommented))
	require.NoError(t, err, uncommented)
	require.Empty(t, warnings)

	_, err = man.Validate()
	require.NoError(t, err)

	require.Contains(t, man.Components, "app")
	require.Contains(t, man.Products, "default")
	assert.True(t, man.Products["default"].Default)
	assert.Equal(t, "*", man.DevDependencies["luatest"].Version)
}

// TestRender_isTOML guards the template itself: a stray unquoted value would
// otherwise only be caught by the validation inside Create.
func TestRender_isTOML(t *testing.T) {
	t.Parallel()

	var decoded map[string]any
	require.NoError(t, toml.Unmarshal([]byte(render("my-app")), &decoded))
	assert.Contains(t, decoded, "dependencies")
}

// tomlLine matches a commented line that is TOML rather than prose: a table
// header, or a key assignment. Matching on "contains a bracket" instead would
// uncomment the sentence documenting `tt package add <name> [<constraint>]`,
// which is how this helper got written wrong the first time.
var tomlLine = regexp.MustCompile(`^(\[[\w.]+\]|[\w-]+ = )`)

// uncomment strips the leading "# " from every commented line that carries TOML,
// which is what a user does by hand to the skeleton's examples.
func uncomment(source string) string {
	var out strings.Builder

	for _, line := range strings.Split(source, "\n") {
		trimmed := strings.TrimPrefix(line, "# ")
		if trimmed != line && tomlLine.MatchString(trimmed) {
			line = trimmed
		}

		out.WriteString(line)
		out.WriteString("\n")
	}

	return out.String()
}
