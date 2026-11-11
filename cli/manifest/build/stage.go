package build

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/tree"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/rocktree"
	"github.com/tarantool/tt/v3/cli/manifest/version"
)

// stageDirName is the directory of _build/ the packages' stages are in, one
// per product.
const stageDirName = "rock"

// packageStage is the stage of the product productName: the directory under
// _build/ its components are laid out in, as an unpacked binary rock, before
// the rock is installed into .rocks/.
//
// It is tt's own state, as _build/ is, and it stays between builds: an
// incremental component whose build is skipped leaves what it wrote in the
// stage there, and a build of one component (--component) lays out that one
// alone, the stage keeping the others. What no component owns any more goes
// (stageRecord). Each product has its own, so the rock a build installs holds
// the components of the product it built and no other.
func packageStage(projectDir, productName string) string {
	return filepath.Join(projectDir, buildDirName, stageDirName, productName)
}

// stageRecordPerm is the mode the record of a stage is written with.
const stageRecordPerm os.FileMode = 0o644

// stageRecord is what the builds of a product put into its stage, by
// component. A build reads it to tell the files of the stage a component still
// owns from the stale ones - a Lua file deleted from the sources, the files of
// a component no longer in the product, a file a backend no longer writes -
// which the install of the stage would put into .rocks/ again. The stage is
// not cleared instead: an incremental component whose build is skipped, and
// a component a build of another one (--component) does not build, keep
// there what they put there before.
//
// The record is kept beside the stage, in <stage>.json, rather than in it: the
// stage is installed as a rock whole. A product name has no dot, so the file
// is no stage of another product.
type stageRecord struct {
	Components map[string]stageFiles `json:"components"`
}

// stageFiles is what the last build of a component put into the stage, by
// slash-separated paths relative to it.
type stageFiles struct {
	// Written is what its backend left in TT_OUTPUT_DIR (runBackends).
	Written []string `json:"written,omitempty"`
	// LaidOut is what the layout copied from the files of the component.
	LaidOut []string `json:"laid_out,omitempty"`
}

// stageRecordPath is where the record of stage is.
func stageRecordPath(stage string) string {
	return stage + ".json"
}

// readStageRecord reads the record of stage; false when there is none, or one
// that cannot be read.
func readStageRecord(stage string) (stageRecord, bool) {
	var record stageRecord

	data, err := os.ReadFile(stageRecordPath(stage))
	if err != nil {
		return record, false
	}

	err = json.Unmarshal(data, &record)
	if err != nil || record.Components == nil {
		return stageRecord{Components: nil}, false
	}

	return record, true
}

// writeStageRecord writes the record of stage.
func writeStageRecord(stage string, record stageRecord) error {
	path := stageRecordPath(stage)

	data, err := json.MarshalIndent(record, "", "  ")
	if err != nil {
		return fmt.Errorf("encoding the record of the stage: %w", err)
	}

	err = os.MkdirAll(filepath.Dir(path), dirPerm)
	if err == nil {
		err = os.WriteFile(path, append(data, '\n'), stageRecordPerm)
	}

	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

// openStage reads the record of stage before a build of the components of
// proj, and reports whether the build is to settle the stage after it
// (settleStage).
//
// A build of every component of the product starts from an empty stage when
// the stage has no record - what is in it cannot be told apart, an older tt
// having left it - and with --rebuild, which builds from scratch: so does a
// make target that writes into TT_OUTPUT_DIR, and a file only an earlier
// build wrote goes. A build of some of the components (--component) of a
// stage with no record leaves it as it is, unsettled: it does not know the
// files of the others.
func openStage(
	opts Options, proj project, stage string, components []string,
) (stageRecord, bool, error) {
	record, found := readStageRecord(stage)

	if !proj.coversProduct(components) || (found && !opts.Rebuild) {
		return record, found, nil
	}

	err := os.RemoveAll(stage)
	if err != nil {
		return record, false, fmt.Errorf("clearing the stage %s: %w", stage, err)
	}

	return stageRecord{Components: map[string]stageFiles{}}, true, nil
}

// coversProduct reports whether components are every component of the
// product of proj.
func (proj project) coversProduct(components []string) bool {
	for _, name := range proj.man.EffectiveProducts()[proj.product].Components {
		if !slices.Contains(components, name) {
			return false
		}
	}

	return true
}

// stageBuild is what a build put into its stage, by absolute paths.
type stageBuild struct {
	// components are the components it built.
	components []string
	// written maps a component to what its backend left in the stage
	// (runBackends), and laidOut to what its layout copied there
	// (layoutComponents).
	written map[string][]string
	laidOut map[string][]string
	// generated is what the build generated itself: version.lua.
	generated []string
}

// settleStage records what build put into stage over last, the record of the
// builds before it, and removes from the stage every file that neither a
// component of the product owns by the new record nor the build generated,
// and the directories that leaves empty. A component the build did not build
// (--component) owns what last records of it; one whose incremental build was
// skipped, what its last build wrote (runModuleBackend).
func settleStage(stage string, proj project, last stageRecord, build stageBuild) error {
	record := stageRecord{Components: map[string]stageFiles{}}
	keep := map[string]bool{}

	for _, file := range inDir(stage, build.generated) {
		keep[file] = true
	}

	for _, name := range proj.man.EffectiveProducts()[proj.product].Components {
		files, ok := last.Components[name]
		if slices.Contains(build.components, name) {
			files = stageFiles{
				Written: inDir(stage, build.written[name]),
				LaidOut: inDir(stage, build.laidOut[name]),
			}
			ok = true
		}

		if !ok {
			continue
		}

		record.Components[name] = files

		for _, file := range slices.Concat(files.Written, files.LaidOut) {
			keep[file] = true
		}
	}

	err := pruneStage(stage, keep)
	if err != nil {
		return err
	}

	return writeStageRecord(stage, record)
}

// pruneStage removes from stage every file whose slash-separated path relative
// to it keep does not hold, then the directories left empty, but the stage
// itself.
func pruneStage(stage string, keep map[string]bool) error {
	root, err := os.OpenRoot(stage)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("removing what is stale from %s: %w", stage, err)
	}

	defer func() { _ = root.Close() }()

	var dirs []string

	err = fs.WalkDir(root.FS(), ".", func(path string, entry fs.DirEntry, err error) error {
		switch {
		case err != nil || path == ".":
			return err
		case entry.IsDir():
			dirs = append(dirs, path)

			return nil
		case keep[path]:
			return nil
		default:
			return root.Remove(path)
		}
	})

	// A directory is walked before what is in it, so the deepest come first
	// backwards.
	for _, dir := range slices.Backward(dirs) {
		if err != nil {
			break
		}

		var entries []fs.DirEntry

		entries, err = fs.ReadDir(root.FS(), dir)
		if err == nil && len(entries) == 0 {
			err = root.Remove(dir)
		}
	}

	if err != nil {
		return fmt.Errorf("removing what is stale from %s: %w", stage, err)
	}

	return nil
}

// inDir lists the files of files, absolute paths, that are under dir, by their
// slash-separated paths relative to it.
func inDir(dir string, files []string) []string {
	var rel []string

	prefix := dir + string(filepath.Separator)

	for _, file := range files {
		if rest, ok := strings.CutPrefix(file, prefix); ok {
			rel = append(rel, filepath.ToSlash(rest))
		}
	}

	return rel
}

// joinDir returns the absolute paths of files, slash-separated paths relative
// to dir.
func joinDir(dir string, files []string) []string {
	paths := make([]string, 0, len(files))

	for _, file := range files {
		paths = append(paths, filepath.Join(dir, filepath.FromSlash(file)))
	}

	return paths
}

// installPackageRock installs the stage of the product prod into the project's
// rocks tree as the rock of the package, replacing its other versions
// (rocktree.InstallPackage).
//
// The rock is the package's name at the LuaRocks form of its version
// (rocktree.RockVersion), and declares the package's dependencies - those of
// the manifest and of the product's components - at the versions the lock
// pins. A file of the package another rock holds the plain path of is an
// error (exit code 1): require would load that rock's.
func installPackageRock(
	ctx context.Context, cfg luarocks.Config, man *manifest.Manifest, productName string,
	prod manifest.LockProduct, ver version.Version, stage string,
) error {
	name := man.Package.Name

	for _, dep := range prod.Dependencies {
		if dep.Name == name {
			return sdk.Errorf(sdk.ExitFailure,
				"the package %s is installed as a rock of that name, and its lock pins a "+
					"rock %s %s too", name, dep.Name, dep.Version)
		}
	}

	return installStage(ctx, cfg, rocktree.Package{
		Name:         name,
		Version:      ver.SemVer,
		Summary:      man.Package.Description,
		Dependencies: rocktree.PackageDependencies(man, productName, prod),
	}, stage)
}

// installStage installs stage into the rocks tree of cfg as the rock of pkg,
// replacing its other versions (rocktree.InstallPackage): the rock of the
// package itself, or that of a path dependency holding a manifest.
func installStage(
	ctx context.Context, cfg luarocks.Config, pkg rocktree.Package, stage string,
) error {
	spec, err := rocktree.Rockspec(pkg)
	if err != nil {
		return sdk.Errorf(sdk.ExitFailure, "%w", err)
	}

	// A product that lays nothing out is a rock of its rockspec alone.
	err = os.MkdirAll(stage, dirPerm)
	if err != nil {
		return fmt.Errorf("creating the stage %s: %w", stage, err)
	}

	rocksTree, err := openTree(cfg)
	if err != nil {
		return err
	}

	err = rocksTree.Update(ctx, func(change *tree.Tx) error {
		return rocktree.InstallPackage(change, rocksTree.Tree, spec, stage)
	})
	if err != nil {
		return sdk.Errorf(sdk.ExitFailure, "installing %s into %s: %w", pkg.Name, cfg.Tree, err)
	}

	return nil
}
