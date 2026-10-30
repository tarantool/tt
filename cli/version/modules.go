package version

import (
	"fmt"
	"runtime/debug"
	"strings"
	"text/tabwriter"
)

// develVersion is the version Go records for a module built from a
// directory: the main module of a working tree, or a dependency replaced by
// a directory.
const develVersion = "(devel)"

// columnGap is the number of spaces between the columns of FormatModules.
const columnGap = 2

// Module is a module tt is built from, as the table of the core names it.
type Module struct {
	// Name is the name the table gives the module.
	Name string
	// Package is the import path of the package of the module's
	// constructor.
	Package string
}

// ModuleVersion is a module with the Go module its constructor comes from,
// as the build info of the binary records it.
type ModuleVersion struct {
	// Name is the name the table of the core gives the module.
	Name string
	// Path is the path of the Go module, empty when the build info does not
	// record the module of the constructor.
	Path string
	// Version is the version of the Go module: its own, or the version of
	// its replacement when it is replaced by another module, (devel) when it
	// is built from a directory, <unknown> when it is not recorded.
	Version string
	// Replace is the path of the module that replaces it, when that is
	// another module; empty otherwise.
	Replace string
}

// ModuleVersions returns the Go modules the constructors of modules come
// from, in the order of modules, as the build info of the binary records
// them.
func ModuleVersions(modules []Module) []ModuleVersion {
	return moduleVersions(modules, debug.ReadBuildInfo)
}

// moduleVersions is ModuleVersions with the build info read by
// readBuildInfo.
func moduleVersions(
	modules []Module, readBuildInfo func() (*debug.BuildInfo, bool),
) []ModuleVersion {
	info, ok := readBuildInfo()
	if !ok {
		info = nil
	}

	versions := make([]ModuleVersion, 0, len(modules))

	for _, module := range modules {
		versions = append(versions, moduleVersion(module, info))
	}

	return versions
}

// moduleVersion returns the Go module of info that holds the package of
// module: the one whose path is the longest prefix of the package path.
func moduleVersion(module Module, info *debug.BuildInfo) ModuleVersion {
	found := ModuleVersion{Name: module.Name, Path: "", Version: unknownVersion, Replace: ""}

	if info == nil {
		return found
	}

	candidates := make([]*debug.Module, 0, len(info.Deps)+1)

	candidates = append(candidates, &info.Main)
	candidates = append(candidates, info.Deps...)

	var best *debug.Module

	for _, candidate := range candidates {
		if candidate == nil || !holdsPackage(candidate.Path, module.Package) {
			continue
		}

		if best == nil || len(candidate.Path) > len(best.Path) {
			best = candidate
		}
	}

	if best == nil {
		return found
	}

	found.Path = best.Path
	found.Version = recordedVersion(best.Version)

	if best.Replace != nil {
		switch best.Replace.Version {
		case "", develVersion:
			// Replaced by a directory, whose path is the build machine's.
			found.Version = develVersion
		default:
			found.Version = best.Replace.Version

			if best.Replace.Path != best.Path {
				found.Replace = best.Replace.Path
			}
		}
	}

	return found
}

// holdsPackage reports whether the Go module path holds the package pkg.
func holdsPackage(path, pkg string) bool {
	return path != "" && (pkg == path || strings.HasPrefix(pkg, path+"/"))
}

// recordedVersion returns version, or <unknown> when Go recorded none.
func recordedVersion(version string) string {
	if version == "" {
		return unknownVersion
	}

	return version
}

// FormatModules renders versions one module per line - its name, its
// version and its Go module - in aligned columns, each line indented by two
// spaces and ended with a newline.
func FormatModules(versions []ModuleVersion) string {
	var out strings.Builder

	writer := tabwriter.NewWriter(&out, 0, 0, columnGap, ' ', 0)

	for _, module := range versions {
		path := module.Path
		if path == "" {
			path = unknownVersion
		}

		if module.Replace != "" {
			path = fmt.Sprintf("%s => %s", path, module.Replace)
		}

		_, _ = fmt.Fprintf(writer, "  %s\t%s\t%s\n", module.Name, module.Version, path)
	}

	_ = writer.Flush()

	return out.String()
}
