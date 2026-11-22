package pack

import (
	"errors"
	"fmt"
	"path"
	"slices"
	"strings"

	sdkbuild "github.com/tarantool/tt/sdk/build"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/rockscript"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
)

// The names the rock of a package reserves at the root of its sources, where
// LuaRocks builds it: the project must hold none of them (checkRockReserved).
const (
	// rockVersionFile is _version.lua of the package in the sources: the
	// rockspec carries it in build.extra_files, and build.install installs
	// it where the binary rock has it.
	rockVersionFile = "tt-rock-version.lua"
	// rockScriptFile and rockStateDir are the script that builds a package
	// with build blocks without tt, which the rockspec carries in
	// build.extra_files (rockscript), and the directory it builds in.
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
	rockScriptFile: "the script that builds the package without tt",
	rockStateDir:   "the directory that script builds in",
	luaRocksPatchesMarker: "the mark of LuaRocks, which writes no file of the rockspec " +
		"into sources that hold it",
}

// hookBuilds are the hooks a build runs, which the script of the rock runs
// too.
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
	// ttOnly are the builds of the product that build with tt alone, why
	// each, a sentence each; a product with any builds with tt alone.
	ttOnly []string
	// leftOut are the files the layout takes that the sources of the rock do
	// not hold, by path in the project, but those the output of a build
	// lists: build.install leaves them out, and the script lays them out
	// when a build makes them.
	leftOut []string
	// scripted is set for a package built by the script, tt-rock.sh.
	scripted bool
	// lost are what the builds of the script lack against those of tt, a
	// sentence each, prefixed with what each builds.
	lost []string
}

// rockSources are the sources of the rock of a package, and how they are
// built.
type rockSources struct {
	// dir is the root of the project.
	dir string
	// carried are the files the sources hold, slash-separated paths in the
	// project (sourceFiles): those of the source rock, or of the repository
	// of the package at the version.
	carried []string
	// backends are the backends of the build of the package.
	backends *buildreg.Registry
}

// releaseBuild plans how LuaRocks builds rel, the rock built is released as,
// from its sources.
//
// A product with no build block - no component of it has one, and the
// package has no hook a build runs - installs what its layout took
// (build.Result.Layout) and the _version.lua its build generated, which the
// rockspec carries in build.extra_files: the files of the binary rock, but
// _rocks.lua, which no rock has, and those of its layout the sources do not
// hold (rockBuild.leftOut). A path of the layout build.install cannot say
// fails the pack (rocktree.CheckInstallDest), the hint naming the component.
//
// A product whose builds all export (exportBuilds) is built by tt-rock.sh,
// the script the rockspec carries in build.extra_files too
// (scriptedBuild). A product with a build that does not, or whose script
// would hold a NUL byte, which no script of sh holds, builds with tt alone:
// the build of the rockspec fails, saying which builds need tt and why
// (rockBuild.ttOnly).
//
// A file of the sources at a name the rock reserves fails either
// (checkRockReserved).
func releaseBuild(built *build.Result, rel rocktree.Release, sources rockSources) (
	rockBuild, error,
) {
	err := checkRockReserved(sources.carried)
	if err != nil {
		return rockBuild{}, err
	}

	if hasBuildBlocks(built.Manifest, built.Product) {
		builds, err := exportBuilds(built, sources.backends)
		if err != nil {
			return rockBuild{}, err
		}

		if len(builds.ttOnly) > 0 {
			return ttOnlyBuild(rel, builds.ttOnly), nil
		}

		plan := scriptedBuild(built, sources, builds.script)

		// No value of a build exportBuilds takes has a NUL byte, and no other
		// value of the script has one - the names and the version of the
		// manifest, the paths of files; this is for what slips by them.
		if strings.Contains(plan.build.ExtraFiles[rockScriptFile], "\x00") {
			return ttOnlyBuild(rel, []string{fmt.Sprintf("%s, the script that builds the "+
				"package without tt, %v", rockScriptFile, rockscript.ErrNUL)}), nil
		}

		return plan, nil
	}

	plan := rockBuild{
		build:  rocktree.ReleaseBuild{Install: nil, ExtraFiles: nil, Script: "", TtOnly: ""},
		ttOnly: nil, leftOut: nil, scripted: false, lost: nil,
	}

	inSources := sources.holds()

	for _, file := range lastLaidOut(built.Layout) {
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

	addVersionLua(&plan.build, built)

	return plan, nil
}

// ttOnlyBuild is the build of rel, the rock of a package that builds with tt
// alone for reasons, a sentence each: the build of the rockspec fails,
// saying so.
func ttOnlyBuild(rel rocktree.Release, reasons []string) rockBuild {
	return rockBuild{
		build: rocktree.ReleaseBuild{
			Install: nil, ExtraFiles: nil, Script: "",
			TtOnly: fmt.Sprintf("%s %s builds with tt only: %s. Install its binary rock, or "+
				"depend on it in app.manifest.toml of a project tt 3 builds.", rel.Name,
				rel.Version, strings.Join(reasons, "; ")),
		},
		ttOnly: reasons, leftOut: nil, scripted: false, lost: nil,
	}
}

// lastLaidOut are the files of layout but those another file is laid out
// over after them: a file two components lay out at one path is the one
// copied last.
func lastLaidOut(layout []build.LaidOut) []build.LaidOut {
	last := map[string]int{}
	for index, file := range layout {
		last[file.Dest] = index
	}

	var files []build.LaidOut

	for index, file := range layout {
		if last[file.Dest] == index {
			files = append(files, file)
		}
	}

	return files
}

// addVersionLua adds the _version.lua of built, when its build generated one,
// to build.install of release, from the extra file the rockspec carries it
// as.
func addVersionLua(release *rocktree.ReleaseBuild, built *build.Result) {
	versionLua := built.VersionLua
	if versionLua.Dest == "" {
		return
	}

	release.Install = append(release.Install,
		rocktree.ReleaseFile{Source: rockVersionFile, Dest: versionLua.Dest})

	if release.ExtraFiles == nil {
		release.ExtraFiles = map[string]string{}
	}

	release.ExtraFiles[rockVersionFile] = versionLua.Content
}

// holds returns the files the sources hold, as a set.
func (sources rockSources) holds() map[string]bool {
	held := make(map[string]bool, len(sources.carried))
	for _, file := range sources.carried {
		held[file] = true
	}

	return held
}

// scriptedBuild plans the build of built by script, the script of its
// builds (exportBuilds), from sources: build type command, running
// tt-rock.sh, which the rockspec carries with _version.lua in
// build.extra_files.
//
// The layout of tt runs after the builds of the components, and takes the
// files of the components that are there then: those of the sources, and
// those a build made - the layout of the files the output of a build lists
// is planned whether the project has them or not (build.LayoutOf). Each file
// it takes is put in the rock once. build.install installs one the sources
// hold, at a path it can say (rocktree.CheckInstallDest), unless a file of
// the output of a build goes there too, which the layout writes over in tt:
// such a file, and one of another path, is laid out by the script after the
// builds. So is a file the sources do not hold, when a build made it - one
// the default include takes of what a make target left in the directory of
// its component; a file of the layout that is a file of the output of a
// build, copied where the script copies the output, is left to that copy.
func scriptedBuild(built *build.Result, sources rockSources, script rockscript.Script) rockBuild {
	plan := rockBuild{
		build: rocktree.ReleaseBuild{
			Install: nil, ExtraFiles: nil, Script: rockScriptFile, TtOnly: "",
		},
		ttOnly: nil, leftOut: nil, scripted: true, lost: nil,
	}

	inSources := sources.holds()

	// The output each build copies into the stage, by its path there, from
	// its path in the project, and the files of the output in the project.
	outputs := map[string]string{}
	made := map[string]bool{}

	var madeFiles []string

	for _, step := range script.Components {
		for _, file := range step.Outputs {
			source := path.Join(step.Cwd, file.Path)

			if !made[source] {
				made[source] = true
				madeFiles = append(madeFiles, source)
			}

			if dest := outputStageDest(step.Namespace, file); dest != "" {
				outputs[dest] = source
			}
		}
	}

	layout := slices.Clone(built.Layout)

	for _, file := range build.LayoutOf(sources.dir, built.Manifest, built.Product,
		sources.backends, madeFiles) {
		if !slices.Contains(layout, file) {
			layout = append(layout, file)
		}
	}

	for _, file := range lastLaidOut(layout) {
		source, toOutput := outputs[file.Dest]

		switch {
		case toOutput && source == file.Source:
			// The copy of the output is this file.
		case inSources[file.Source] && !toOutput && rocktree.CheckInstallDest(file.Dest) == nil:
			plan.build.Install = append(plan.build.Install,
				rocktree.ReleaseFile{Source: file.Source, Dest: file.Dest})
		default:
			script.Lay = append(script.Lay, rockscript.Lay{Source: file.Source, Dest: file.Dest})

			if !inSources[file.Source] && !made[file.Source] {
				plan.leftOut = append(plan.leftOut, file.Source)
			}
		}
	}

	addVersionLua(&plan.build, built)

	if plan.build.ExtraFiles == nil {
		plan.build.ExtraFiles = map[string]string{}
	}

	plan.build.ExtraFiles[rockScriptFile] = rockscript.Render(script)

	for _, step := range scriptSteps(script) {
		for _, lost := range step.Export.Lost {
			plan.lost = append(plan.lost, step.What()+": "+lost)
		}
	}

	return plan
}

// scriptSteps are the builds of script in the order it runs them.
func scriptSteps(script rockscript.Script) []rockscript.Step {
	var steps []rockscript.Step

	if script.PreBuild != nil {
		steps = append(steps, *script.PreBuild)
	}

	steps = append(steps, script.Components...)

	if script.PostBuild != nil {
		steps = append(steps, *script.PostBuild)
	}

	return steps
}

// outputStageDest is the path in the stage, slash-separated, the copy of
// file, a file of the output of a build of a component of the namespace
// namespace, goes to: lib/<namespace>/ for a module, lua/<namespace>/ for a
// data file, bin/ for a program, under its base name; empty for a library
// to link, which is not in the stage.
func outputStageDest(namespace string, file sdkbuild.OutputFile) string {
	name := path.Base(file.Path)

	switch file.To {
	case sdkbuild.Module:
		return path.Join(rocktree.StageLib, namespace, name)
	case sdkbuild.Data:
		return path.Join(rocktree.StageLua, namespace, name)
	case sdkbuild.Program:
		return path.Join(rocktree.StageBin, name)
	default:
		return ""
	}
}

// warnings are the warnings of a pack of the source rock of plan: that it
// builds with tt only, what its script lacks against a build of tt, and the
// files its layout takes that it leaves out but those untracked lists, the
// files git neither tracks nor ignores, which untrackedWarning warns of
// already.
func (plan rockBuild) warnings(untracked []string) []string {
	var warnings []string

	if len(plan.ttOnly) > 0 {
		warnings = append(warnings, fmt.Sprintf("the source rock builds with tt only, which "+
			"builds it from its manifest: %s; LuaRocks, tt rocks included, fails to build "+
			"it, saying so", strings.Join(plan.ttOnly, "; ")))
	}

	if len(plan.lost) > 0 {
		warnings = append(warnings, fmt.Sprintf("LuaRocks builds the source rock by %s of "+
			"its rockspec, which lacks what a build of tt has: %s", rockScriptFile,
			strings.Join(plan.lost, "; ")))
	}

	var ignored []string

	for _, file := range plan.leftOut {
		if !slices.Contains(untracked, file) {
			ignored = append(ignored, file)
		}
	}

	lacks := "a build of the source rock, by tt or by LuaRocks, lacks them"
	if plan.scripted {
		lacks += " unless a build of the package makes them"
	}

	if len(ignored) > 0 {
		warnings = append(warnings, fmt.Sprintf("the source rock leaves out the files of the "+
			"layout that git ignores, or that are out of the project: %s; %s, so commit "+
			"them, or leave them out of the layout", fileList(ignored), lacks))
	}

	return warnings
}

// hasBuildBlocks reports whether a build of product of man runs a build
// block: a component of it has one, or the package a hook a build runs.
func hasBuildBlocks(man *manifest.Manifest, product string) bool {
	for _, name := range man.EffectiveProducts()[product].Components {
		if man.Components[name].Build != nil {
			return true
		}
	}

	for _, name := range hookBuilds {
		if _, ok := man.Hooks[name]; ok {
			return true
		}
	}

	return false
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
