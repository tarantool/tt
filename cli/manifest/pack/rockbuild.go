package pack

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

// The names the rock of a package reserves at the root of its sources, where
// LuaRocks builds it: the project must hold none of them (checkRockReserved).
const (
	// rockVersionFile is _version.lua of the package in the sources: the
	// rockspec carries it in build.extra_files, and build.install installs
	// it where the binary rock has it.
	rockVersionFile = "tt-rock-version.lua"
	// rockScriptFile and rockStateDir are the script that is to build a
	// package with build blocks without tt, and the directory it is to build
	// in.
	rockScriptFile = "tt-rock.sh"
	rockStateDir   = ".tt-rock"
	// luaRocksPatchesMarker is the file LuaRocks leaves in sources it has
	// written the extra files of a rockspec into, and writes none into
	// sources holding it.
	luaRocksPatchesMarker = ".luarocks.patches.applied"
)

// rockReserved says what each name at the root of the sources the rock of a
// package reserves is for.
//
//nolint:gochecknoglobals // A constant table.
var rockReserved = map[string]string{
	rockVersionFile: "_version.lua of the package, which LuaRocks writes there from " +
		"the rockspec",
	rockScriptFile: "the script that is to build the package without tt",
	rockStateDir:   "the directory that script is to build in",
	luaRocksPatchesMarker: "the mark of LuaRocks, which writes no file of the rockspec " +
		"into sources that hold it",
}

// hookBuilds are the hooks a build runs, which LuaRocks would have to run too.
//
//nolint:gochecknoglobals // A constant list.
var hookBuilds = []string{"pre_build", "post_build"}

// errRockReserved reports a file of the sources of the rock of a package at
// a name the rock reserves (rockReserved).
var errRockReserved = errors.New("the name is reserved for the build of the rock by LuaRocks")

// rockBuild is how LuaRocks builds the rock of a package without tt
// (releaseBuild), and what the pack warns of it.
type rockBuild struct {
	// build is the build table of the rockspec.
	build rocktree.ReleaseBuild
	// ttOnly are the build blocks of the product, a sentence each; a product
	// with any builds with tt alone.
	ttOnly []string
	// leftOut are the files the layout takes that the sources of the rock do
	// not hold, by path in the project: build.install leaves them out.
	leftOut []string
}

// releaseBuild plans how LuaRocks builds rel, the rock built is released as,
// from its sources, which hold the files carried, slash-separated paths in the
// project (sourceFiles): the source rock, or the repository of the package
// at the version.
//
// A product with no build block - no component of it has one, and the
// package has no hook a build runs - installs what its layout took
// (build.Result.Layout) and the _version.lua its build generated, which the
// rockspec carries in build.extra_files: the files of the binary rock, but
// _rocks.lua, which no rock has, and those of its layout the sources do not
// hold (rockBuild.leftOut). A path of the layout build.install cannot say
// fails the pack (rocktree.CheckInstallDest), the hint naming the component.
//
// A product with a build block builds with tt alone: the build of the
// rockspec fails, saying which blocks need tt (rockBuild.ttOnly).
//
// A file of the sources at a name the rock reserves fails either
// (checkRockReserved).
func releaseBuild(built *build.Result, rel rocktree.Release, carried []string) (rockBuild, error) {
	err := checkRockReserved(carried)
	if err != nil {
		return rockBuild{}, err
	}

	ttOnly := buildBlocks(built.Manifest, built.Product)
	if len(ttOnly) > 0 {
		return rockBuild{
			build: rocktree.ReleaseBuild{
				Install: nil, ExtraFiles: nil,
				TtOnly: fmt.Sprintf("%s %s builds with tt only: %s. Install its binary rock, or "+
					"depend on it in app.manifest.toml of a project tt 3 builds.", rel.Name,
					rel.Version, strings.Join(ttOnly, "; ")),
			},
			ttOnly: ttOnly, leftOut: nil,
		}, nil
	}

	plan := rockBuild{
		build:  rocktree.ReleaseBuild{Install: nil, ExtraFiles: nil, TtOnly: ""},
		ttOnly: nil, leftOut: nil,
	}

	inSources := make(map[string]bool, len(carried))
	for _, file := range carried {
		inSources[file] = true
	}

	// A file two components lay out at one path is the one copied last.
	last := map[string]int{}
	for index, file := range built.Layout {
		last[file.Dest] = index
	}

	for index, file := range built.Layout {
		if last[file.Dest] != index {
			continue
		}

		err := rocktree.CheckInstallDest(file.Dest)
		if err != nil {
			return rockBuild{}, stateErrorf("%w; the layout of the component %q takes %s "+
				"there: rename it, or leave it out of the layout with the exclude of "+
				"[components.%s]", err, file.Component, file.Source, file.Component)
		}

		if !inSources[file.Source] {
			plan.leftOut = append(plan.leftOut, file.Source)

			continue
		}

		plan.build.Install = append(plan.build.Install,
			rocktree.ReleaseFile{Source: file.Source, Dest: file.Dest})
	}

	if versionLua := built.VersionLua; versionLua.Dest != "" {
		plan.build.Install = append(plan.build.Install,
			rocktree.ReleaseFile{Source: rockVersionFile, Dest: versionLua.Dest})
		plan.build.ExtraFiles = map[string]string{rockVersionFile: versionLua.Content}
	}

	return plan, nil
}

// warnings are the warnings of a pack of the source rock of plan: that it
// builds with tt only, and the files its layout takes that it leaves out but
// those untracked lists, the files git neither tracks nor ignores, which
// untrackedWarning warns of already.
func (plan rockBuild) warnings(untracked []string) []string {
	var warnings []string

	if len(plan.ttOnly) > 0 {
		warnings = append(warnings, fmt.Sprintf("the source rock builds with tt only, which "+
			"builds it from its manifest: %s; LuaRocks, tt rocks included, fails to build "+
			"it, saying so", strings.Join(plan.ttOnly, "; ")))
	}

	var ignored []string

	for _, file := range plan.leftOut {
		if !slices.Contains(untracked, file) {
			ignored = append(ignored, file)
		}
	}

	if len(ignored) > 0 {
		warnings = append(warnings, fmt.Sprintf("the source rock leaves out the files of the "+
			"layout that git ignores, or that are out of the project: %s; a build of the "+
			"source rock, by tt or by LuaRocks, lacks them, so commit them, or leave them "+
			"out of the layout", fileList(ignored)))
	}

	return warnings
}

// buildBlocks lists the build blocks a build of product of man runs, a
// sentence each: those of its components and of the hooks a build runs.
func buildBlocks(man *manifest.Manifest, product string) []string {
	var blocks []string

	for _, name := range man.EffectiveProducts()[product].Components {
		if block := man.Components[name].Build; block != nil {
			blocks = append(blocks, fmt.Sprintf("the component %q is built by the tt backend %q",
				name, block.Backend))
		}
	}

	for _, name := range hookBuilds {
		if hook, ok := man.Hooks[name]; ok {
			blocks = append(blocks, fmt.Sprintf("the hook %s is run by the tt backend %q", name,
				hook.Backend))
		}
	}

	return blocks
}

// checkRockReserved reports a file of carried, the files of the sources of
// the rock of a package, at a name the rock reserves at their root
// (rockReserved): LuaRocks would write over it, or the build without tt
// would.
func checkRockReserved(carried []string) error {
	for _, file := range carried {
		top, _, _ := strings.Cut(file, "/")

		if what, ok := rockReserved[top]; ok {
			return stateErrorf("%w: the project has %s at its root, which the rock of the "+
				"package reserves for %s; rename it, or have git not track it",
				errRockReserved, top, what)
		}
	}

	return nil
}
