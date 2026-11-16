package core

import (
	"errors"
	"flag"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync/atomic"
	"testing"
	"unicode"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/v3/cli/cmd/clidoc"
)

// clidocDir is where TestCLIReference writes the command reference of the
// documentation, a temporary directory when empty:
//
//	go test ./core -run TestCLIReference -args -clidoc DIR
//
// mage docs:reference runs it for doc/reference/commands.
var clidocDir = flag.String("clidoc", "", "write the command reference into DIR")

// TestCLIReference renders the command reference from tt as its main package
// builds it, the tree TestHelpGoldenThroughModules pins the help of.
//
//nolint:paralleltest // Replaces the process's root, logger and directory.
func TestCLIReference(t *testing.T) {
	dir := *clidocDir
	if dir == "" {
		dir = t.TempDir()
	}

	pages := writeReference(t, dir)
	require.Contains(t, pages, "instance.md")
	require.Contains(t, pages, "errors.md")
}

// writeReference writes the command reference into dir, as TestCLIReference
// does, and returns the names of its pages.
func writeReference(t *testing.T, dir string) []string {
	t.Helper()

	// The directory is given from where go test runs, before the test
	// changes it.
	dir, err := filepath.Abs(dir)
	require.NoError(t, err)

	restoreProcess(t)

	// Configuration is looked for from the working directory: there is none
	// in an empty one.
	t.Chdir(t.TempDir())
	t.Setenv("TT_CLI_MODULES_PATH", "")
	t.Setenv("TT_CLI_CFG", "")
	require.NoError(t, os.Unsetenv("TT_CLI_CFG"))

	root, err := build(nil, mainModules(), &atomic.Bool{}, options{})
	require.NoError(t, err)

	// cobra adds the help and completion commands when it executes.
	root.SetOut(io.Discard)
	root.SetArgs([]string{"--help"})
	require.NoError(t, root.Execute())

	pages, err := clidoc.Write(root, dir)
	require.NoError(t, err)

	return pages
}

// docDir is the documentation, from the directory of the package.
const docDir = "../doc"

// referenceLink is a link of a page of the documentation to a page of the
// command reference, with the anchor it may have.
var referenceLink = regexp.MustCompile(
	`\]\(([^()\s#]*reference/commands/[^()\s#]+\.md)(#[^()\s]*)?\)`)

// navPage is a page of the command reference the nav of zensical.toml lists.
var navPage = regexp.MustCompile(`"reference/commands/([^"]+\.md)"`)

// anchorOf returns the anchor Zensical gives a heading: its text in lower
// case, the spaces made hyphens and the punctuation dropped.
func anchorOf(heading string) string {
	var anchor strings.Builder

	for _, char := range strings.ToLower(heading) {
		switch {
		case char == ' ' || char == '-':
			anchor.WriteRune('-')
		case char == '_' || unicode.IsLetter(char) || unicode.IsDigit(char):
			anchor.WriteRune(char)
		}
	}

	return anchor.String()
}

// anchorsOf returns the anchors of the headings of a Markdown page, those
// outside its code blocks.
func anchorsOf(page string) map[string]bool {
	anchors := map[string]bool{}
	inCode := false

	for line := range strings.SplitSeq(page, "\n") {
		if strings.HasPrefix(line, "```") {
			inCode = !inCode
		}

		if !inCode && strings.HasPrefix(line, "#") {
			anchors[anchorOf(strings.TrimLeft(line, "# "))] = true
		}
	}

	return anchors
}

// TestCLIReferenceMatchesDocs checks the documentation against the command
// reference tt generates, which is not in git: the nav of zensical.toml
// lists exactly its pages, and every link of a page to it, the anchor
// included, names what it has. A copy of the reference in doc/, left by an
// earlier mage docs:reference, must be the reference of this tt: a stale one
// shows commands tt no longer has.
//
//nolint:paralleltest // Replaces the process's root, logger and directory.
func TestCLIReferenceMatchesDocs(t *testing.T) {
	dir := t.TempDir()
	pages := writeReference(t, dir)

	generated := map[string]string{}

	for _, page := range pages {
		content, err := os.ReadFile(filepath.Join(dir, page))
		require.NoError(t, err)

		generated[page] = string(content)
	}

	// The test runs in a directory of its own: the paths are from the
	// package's.
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)

	docs := filepath.Join(filepath.Dir(file), docDir)

	config, err := os.ReadFile(filepath.Join(docs, "..", "zensical.toml"))
	require.NoError(t, err)

	matches := navPage.FindAllStringSubmatch(string(config), -1)
	listed := make([]string, 0, len(matches))

	for _, match := range matches {
		listed = append(listed, match[1])
	}

	assert.ElementsMatch(t, pages, listed,
		"the nav of zensical.toml must list the pages of the command reference")

	reference := filepath.Join(docs, "reference", "commands")

	err = filepath.WalkDir(docs, func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".md") ||
			filepath.Dir(path) == reference {
			return err
		}

		content, err := os.ReadFile(path) //nolint:gosec // The checkout's own pages.
		if err != nil {
			return err
		}

		for _, match := range referenceLink.FindAllStringSubmatch(string(content), -1) {
			target := filepath.Join(filepath.Dir(path), match[1])
			name := filepath.Base(target)
			page, found := generated[name]

			if !assert.True(t, found && filepath.Dir(target) == reference,
				"%s links to %s, which the command reference has not", path, match[1]) {
				continue
			}

			if anchor := strings.TrimPrefix(match[2], "#"); anchor != "" {
				assert.True(t, anchorsOf(page)[anchor],
					"%s links to %s%s, a heading %s has not", path, match[1], match[2], name)
			}
		}

		return nil
	})
	require.NoError(t, err)

	entries, err := os.ReadDir(reference)
	if errors.Is(err, fs.ErrNotExist) {
		return
	}

	require.NoError(t, err)

	for _, entry := range entries {
		content, err := os.ReadFile(filepath.Join(reference, entry.Name()))
		require.NoError(t, err)

		page, found := generated[entry.Name()]
		assert.True(t, found && page == string(content),
			"doc/reference/commands/%s is stale: run mage docs:reference", entry.Name())
	}
}
