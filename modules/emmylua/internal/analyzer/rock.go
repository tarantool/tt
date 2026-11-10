package analyzer

import (
	"fmt"
	"os"
	"path/filepath"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/go-luarocks/rockspec"
)

const (
	// rockBinDir is the directory of a binary rock its programs are in; it is
	// deployed to the bin/ of the tree, .rocks/bin.
	rockBinDir = "bin"
	// rockSuffix ends the file name of a rock.
	rockSuffix = ".rock"
	// dirPerm is the mode the registry and the stage are written with.
	dirPerm os.FileMode = 0o755
	// programPerm is the mode of a program in the stage of the rock.
	programPerm os.FileMode = 0o755
	// filePerm is the mode of the configuration tt writes.
	filePerm os.FileMode = 0o644
)

// RockFileName is the file name of the binary rock of arch.
func (r Release) RockFileName(arch string) string {
	return RockName + "-" + r.RockVersion() + "." + arch + rockSuffix
}

// rockspec renders the rockspec the rock is packed with. It is the same for
// every platform - what differs is the programs, which a binary rock carries
// built - so the build table says there is nothing left to build, and the
// programs are not Lua: they are never wrapped.
func (r Release) rockspec() string {
	return fmt.Sprintf(`rockspec_format = "3.0"
package = %q
version = %q
source = {
   url = %q,
   md5 = %q,
}
description = {
   summary = "EmmyLua: the checker, the language server, the doc generator and the formatter",
   detailed = "The programs of EmmyLua at %s, packed by tt: " ..
      "emmylua_check, emmylua_ls, emmylua_doc_cli and luafmt.",
   homepage = %q,
   license = "MIT",
}
supported_platforms = { "linux", "macosx" }
build = {
   type = "none",
}
deploy = {
   wrap_bin_scripts = false,
}
`, RockName, r.RockVersion(), r.SourceURL(), r.SourceMD5, r.Ref, sourceRepo)
}

// BuildRock packs programs, the build of arch, as the binary rock of the
// release in dir, and returns the rock's path. Every program of Programs
// must be there.
//
// The rock is the one go-luarocks packs of the release's rock installed from
// a stage holding bin/<program> (client.PackStaged): the rockspec, bin/ and
// the rock_manifest upstream LuaRocks writes for it, with no modification
// times and no permissions, so the same programs are the same rock. It is
// written to a temporary file and renamed into place, so the registry never
// serves half a rock.
func BuildRock(
	dir string, release Release, arch string, programs map[Program][]byte,
) (string, error) {
	for _, program := range Programs {
		if _, ok := programs[program]; !ok {
			return "", fmt.Errorf("%w: %s is missing", ErrBuild, program)
		}
	}

	specName := RockName + "-" + release.RockVersion() + ".rockspec"

	spec, err := rockspec.EvalSource(specName, []byte(release.rockspec()),
		luarocks.RockspecConfig{})
	if err != nil {
		return "", fmt.Errorf("the rockspec of the rock: %w", err)
	}

	stage, err := os.MkdirTemp("", "tt-emmylua-rock-*")
	if err != nil {
		return "", fmt.Errorf("staging the rock: %w", err)
	}

	defer func() { _ = os.RemoveAll(stage) }()

	binDir := filepath.Join(stage, rockBinDir)

	err = os.MkdirAll(binDir, dirPerm)
	if err != nil {
		return "", fmt.Errorf("staging the rock: %w", err)
	}

	for _, program := range Programs {
		err = os.WriteFile(filepath.Join(binDir, string(program)), programs[program], programPerm)
		if err != nil {
			return "", fmt.Errorf("staging the rock: %w", err)
		}
	}

	err = os.MkdirAll(dir, dirPerm)
	if err != nil {
		return "", fmt.Errorf("creating the registry: %w", err)
	}

	path := filepath.Join(dir, release.RockFileName(arch))

	err = client.PackStaged(spec, stage, path, client.PackStagedOpts{})
	if err != nil {
		return "", fmt.Errorf("packing the rock: %w", err)
	}

	return path, nil
}

// ReadBuild reads the programs of a local build of EmmyLua from dir: a
// cargo target directory such as target/release, or wherever the programs
// were put. Every program of Programs must be there, a regular executable.
func ReadBuild(dir string) (map[Program][]byte, error) {
	programs := make(map[Program][]byte, len(Programs))

	for _, program := range Programs {
		path := filepath.Join(dir, string(program))

		info, err := os.Stat(path)
		if err != nil {
			return nil, fmt.Errorf("%w: %s: %w", ErrBuild, dir, err)
		}

		if !info.Mode().IsRegular() || info.Mode().Perm()&0o100 == 0 {
			return nil, fmt.Errorf("%w: %s is not an executable", ErrBuild, path)
		}

		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("reading %s: %w", path, err)
		}

		programs[program] = data
	}

	return programs, nil
}
