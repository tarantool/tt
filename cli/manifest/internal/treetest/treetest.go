// Package treetest lays rocks into a rocks tree for the tests of the packages
// that read and change one, through the tree package of go-luarocks, so that a
// fixture tree has the tree manifest and the rock_manifests a real install
// writes rather than directories that only look like rocks.
package treetest

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/rockspec"
	"github.com/tarantool/go-luarocks/tree"
)

// Modes the staged files are written with.
const (
	dirPerm  os.FileMode = 0o750
	filePerm os.FileMode = 0o644
	execPerm os.FileMode = 0o755
)

// Rock is one rock version to install into a fixture tree.
type Rock struct {
	// Name is the rock name.
	Name string
	// Version is its version; a version without a revision gets "-1".
	Version string
	// Files are its files, as paths in an unpacked binary rock (lua/...,
	// lib/..., bin/...) to their contents. Nil installs lua/<name>/init.lua and
	// lib/<name>/<name>.so.
	Files map[string]string
	// Dependencies are the dependencies its rockspec declares, as LuaRocks
	// dependency strings ("checks", "metrics >= 1.0").
	Dependencies []string
}

// RockVersion is version with a revision: version itself when it ends in
// -<number>, version-1 otherwise.
func RockVersion(version string) string {
	if i := strings.LastIndexByte(version, '-'); i > 0 && isDigits(version[i+1:]) {
		return version
	}

	return version + "-1"
}

// isDigits reports whether text is a non-empty run of decimal digits.
func isDigits(text string) bool {
	if text == "" {
		return false
	}

	for _, r := range text {
		if r < '0' || r > '9' {
			return false
		}
	}

	return true
}

// Install installs every rock of rocks into the tree rooted at root, in one
// transaction, as the binary rocks they would be.
func Install(tb testing.TB, root string, rocks ...Rock) {
	tb.Helper()

	rocksTree, err := tree.Open(luarocks.Config{Tree: root})
	if err != nil {
		tb.Fatalf("opening %s: %s", root, err)
	}

	err = rocksTree.Update(context.Background(), func(change *tree.Tx) error {
		for _, rock := range rocks {
			spec, stage := stageRock(tb, rock)

			_, err := change.InstallStaged(spec, stage, tree.StagedOpts{
				RequirePlain: false, RemoveOtherVersions: false,
			})
			if err != nil {
				return fmt.Errorf("installing %s %s: %w", rock.Name, rock.Version, err)
			}
		}

		return nil
	})
	if err != nil {
		tb.Fatalf("installing into %s: %s", root, err)
	}
}

// stageRock lays rock out as an unpacked binary rock in a temporary directory
// and returns its spec and the directory.
func stageRock(tb testing.TB, rock Rock) (*luarocks.Rockspec, string) {
	tb.Helper()

	version := RockVersion(rock.Version)

	var src strings.Builder

	fmt.Fprintf(&src, "package = %q\nversion = %q\nsource = { url = \"file://.\" }\n",
		rock.Name, version)

	if len(rock.Dependencies) > 0 {
		src.WriteString("dependencies = {\n")

		for _, dep := range rock.Dependencies {
			fmt.Fprintf(&src, "   %q,\n", dep)
		}

		src.WriteString("}\n")
	}

	src.WriteString("build = { type = \"none\" }\n")

	spec, err := rockspec.EvalSource(rock.Name+"-"+version+".rockspec", []byte(src.String()),
		luarocks.RockspecConfig{})
	if err != nil {
		tb.Fatalf("the rockspec of %s %s: %s", rock.Name, version, err)
	}

	files := rock.Files
	if files == nil {
		files = map[string]string{
			"lua/" + rock.Name + "/init.lua":             "-- " + rock.Name + "\n",
			"lib/" + rock.Name + "/" + rock.Name + ".so": "binary\n",
		}
	}

	stage := tb.TempDir()

	for rel, body := range files {
		path := filepath.Join(stage, filepath.FromSlash(rel))

		mode := filePerm
		if strings.HasPrefix(rel, "bin/") {
			mode = execPerm
		}

		err := os.MkdirAll(filepath.Dir(path), dirPerm)
		if err == nil {
			err = os.WriteFile(path, []byte(body), mode)
		}

		if err != nil {
			tb.Fatalf("staging %s: %s", path, err)
		}
	}

	return spec, stage
}

// Remove removes the version of the rock name from the tree rooted at root.
func Remove(tb testing.TB, root, name, version string) {
	tb.Helper()

	rocksTree, err := tree.Open(luarocks.Config{Tree: root})
	if err == nil {
		err = rocksTree.Update(context.Background(), func(change *tree.Tx) error {
			return change.Remove(tree.RockRef{Name: name, Version: version})
		})
	}

	if err != nil {
		tb.Fatalf("removing %s %s from %s: %s", name, version, root, err)
	}
}

// Versions lists the versions of the rock name the tree rooted at root has,
// newest first.
func Versions(tb testing.TB, root, name string) []string {
	tb.Helper()

	view, err := tree.Inspect(context.Background(), root)
	if err != nil {
		tb.Fatalf("reading %s: %s", root, err)
	}

	return view.Versions(name)
}
