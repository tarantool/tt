package distbuild

import (
	"context"
	"path/filepath"
	"strconv"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/v3/cli/manifest"
)

// replacement is an entry of [replace], resolved: the Go module that is
// replaced and what it is replaced with, exactly.
type replacement struct {
	// Key is the key of the entry in the manifest: the module path, with
	// @version when the entry is limited to a version.
	Key string
	// Module is the module path that is replaced.
	Module string
	// Version is the version that is replaced; empty for every version.
	Version string
	// Dir is the local directory, absolute; empty for a module.
	Dir string
	// Path is the local directory as the manifest names it.
	Path string
	// Target is the module the module is replaced with; empty for a
	// directory.
	Target string
	// TargetVersion is the exact version of Target.
	TargetVersion string
}

// resolveReplaces resolves the entries of [replace] of the manifest, in the
// order of their keys. A directory must hold the module it replaces; a
// version query is resolved to an exact version, or taken from the lock.
//
// The core, the modules of its repository and the tt modules of
// [dependencies] have a place in the manifest of their own, so replacing
// them here is refused.
func (r *resolver) resolveReplaces(
	ctx context.Context, dist *manifest.Distribution, resolved core, modules []module,
) ([]replacement, error) {
	var out []replacement

	for _, rep := range dist.Replacements() {
		field := "replace." + strconv.Quote(rep.Key)

		err := checkReplaceable(rep, resolved, modules)
		if err != nil {
			return nil, err
		}

		res := replacement{
			Key: rep.Key, Module: rep.Module, Version: rep.Version, Dir: "", Path: rep.Path,
			Target: rep.Target, TargetVersion: "",
		}

		if rep.IsPath() {
			res.Dir = r.abs(rep.Path)

			err = r.checkReplaceDir(ctx, res)
		} else {
			err = r.resolveReplaceTarget(ctx, &res, rep)
		}

		if err != nil {
			return nil, sdk.Errorf(sdk.ExitFailure, "%s: %w", field, err)
		}

		out = append(out, res)
	}

	return out, nil
}

// checkReplaceable refuses to replace a module the manifest has another way
// to pick.
func checkReplaceable(
	rep manifest.Replacement, resolved core, modules []module,
) error {
	field := "replace." + strconv.Quote(rep.Key)

	if resolved.inRepository(rep.Module) {
		return sdk.Errorf(sdk.ExitFailure, "%s: is a module of the core's repository "+
			"and follows the core: use [core].path or --core to build on another one", field)
	}

	for _, mod := range modules {
		if mod.Module == rep.Module {
			return sdk.Errorf(sdk.ExitFailure, "%s: is the tt module %q of [dependencies]: "+
				"give that module a path or a version there instead", field, mod.Name)
		}
	}

	return nil
}

// checkReplaceDir checks that the directory of rep holds the module it
// replaces: the go command refuses a directory of another module.
func (r *resolver) checkReplaceDir(ctx context.Context, rep replacement) error {
	gomod, err := readGoMod(ctx, r.gocmd, r.work, r.env, filepath.Join(rep.Dir, "go.mod"))
	if err != nil {
		return err
	}

	if gomod.Module.Path != rep.Module {
		return sdk.Errorf(sdk.ExitFailure, "%s holds module %s, not %s",
			rep.Path, gomod.Module.Path, rep.Module)
	}

	return nil
}

// resolveReplaceTarget finds the exact version of the module rep is replaced
// with, in the lock or by the go command.
func (r *resolver) resolveReplaceTarget(
	ctx context.Context, res *replacement, rep manifest.Replacement,
) error {
	if r.pinned != nil {
		locked, ok := r.pinned.Replace[rep.Key]
		if ok && locked.Module == rep.Target && locked.Version != "" {
			res.TargetVersion = locked.Version

			return nil
		}
	}

	info, err := listModule(ctx, r.gocmd, r.work, r.env, rep.Target, rep.TargetVersion)
	if err != nil {
		return err
	}

	res.TargetVersion = info.Version

	return nil
}
