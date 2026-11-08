package build

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

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
// alone, the stage keeping the others. Each product has its own, so the rock a
// build installs holds the components of the product it built and no other.
func packageStage(projectDir, productName string) string {
	return filepath.Join(projectDir, buildDirName, stageDirName, productName)
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

	spec, err := rocktree.Rockspec(rocktree.Package{
		Name:         name,
		Version:      ver.SemVer,
		Summary:      man.Package.Description,
		Dependencies: rocktree.PackageDependencies(man, productName, prod),
	})
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
		return sdk.Errorf(sdk.ExitFailure, "installing the package into %s: %w", cfg.Tree, err)
	}

	return nil
}
