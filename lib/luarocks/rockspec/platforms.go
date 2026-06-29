package rockspec

import (
	"maps"
	"runtime"

	rocks "github.com/tarantool/tt/lib/luarocks"
)

// RuntimePlatforms returns the platform-name slice to feed to MergePlatforms
// for the current runtime.GOOS, ordered least-specific to most-specific. The
// Tarantool-relevant set is {unix, linux, macosx, macos, bsd}. macosx and
// macos are both emitted on darwin because rockspecs in the wild use either
// spelling.
func RuntimePlatforms() []string {
	switch runtime.GOOS {
	case "linux":
		return []string{"unix", "linux"}
	case "darwin":
		return []string{"unix", "macosx", "macos"}
	case "freebsd", "openbsd", "netbsd", "dragonfly":
		return []string{"unix", "bsd"}
	default:
		return []string{"unix"}
	}
}

// MergePlatforms folds spec.Build.Platforms[name] overlays into spec.Build
// for each name in plats, in order. Scalars overwrite; maps merge recursively.
// After all overlays are applied, spec.Build.Platforms is cleared (matching
// upstream rockspecs.lua: `tbl.platforms = nil`).
//
// Platform names not present in spec.Build.Platforms are ignored — undeclared
// platforms in the rockspec are valid; they are inert for our target (the
// fail-loud rule applies to unknown build types, not unknown platforms).
func MergePlatforms(spec *rocks.Rockspec, plats []string) {
	if spec == nil || len(spec.Build.Platforms) == 0 {
		if spec != nil {
			spec.Build.Platforms = nil
		}

		return
	}

	for _, name := range plats {
		overlay, ok := spec.Build.Platforms[name]
		if !ok {
			continue
		}

		mergeBuild(&spec.Build, &overlay)
	}

	spec.Build.Platforms = nil
}

// mergeBuild applies src's fields onto dst.
//
// Mirrors util.deep_merge: for tables, merge in place; for scalars (non-zero
// in src), overwrite dst. We adopt the convention that the zero value of a
// scalar means "not set" by the overlay, so it does not clobber the base.
// This is conservative — rockspecs in practice declare a field only when
// they want to override it.
func mergeBuild(dst, src *rocks.Build) {
	if src.Type != "" {
		dst.Type = src.Type
	}

	if src.BuildTarget != "" {
		dst.BuildTarget = src.BuildTarget
	}

	if src.InstallTarget != "" {
		dst.InstallTarget = src.InstallTarget
	}

	if src.BuildCommand != "" {
		dst.BuildCommand = src.BuildCommand
	}

	if src.InstallCommand != "" {
		dst.InstallCommand = src.InstallCommand
	}

	if len(src.CopyDirectories) > 0 {
		dst.CopyDirectories = append(dst.CopyDirectories, src.CopyDirectories...)
	}

	dst.Modules = mergeModules(dst.Modules, src.Modules)
	dst.Variables = mergeStringMap(dst.Variables, src.Variables)
	dst.BuildVariables = mergeStringMap(dst.BuildVariables, src.BuildVariables)
	dst.InstallVariables = mergeStringMap(dst.InstallVariables, src.InstallVariables)
	dst.Install = mergeInstall(dst.Install, src.Install)
}

func mergeModules(dst, src map[string]rocks.Module) map[string]rocks.Module {
	if len(src) == 0 {
		return dst
	}

	if dst == nil {
		dst = make(map[string]rocks.Module, len(src))
	}

	maps.Copy(dst, src)

	return dst
}

func mergeStringMap(dst, src map[string]string) map[string]string {
	if len(src) == 0 {
		return dst
	}

	if dst == nil {
		dst = make(map[string]string, len(src))
	}

	maps.Copy(dst, src)

	return dst
}

func mergeInstall(dst, src rocks.BuildInstall) rocks.BuildInstall {
	dst.Lua = mergeStringMap(dst.Lua, src.Lua)
	dst.Lib = mergeStringMap(dst.Lib, src.Lib)
	dst.Bin = mergeStringMap(dst.Bin, src.Bin)
	dst.Conf = mergeStringMap(dst.Conf, src.Conf)

	return dst
}
