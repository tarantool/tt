package coredump

import (
	"debug/elf"
	"fmt"
	"log/slog"
	"path/filepath"
	"slices"
	"strings"
)

// archNames are the architectures the arch of a manifest lists, as uname -m
// names them, by the machine of an ELF file.
var archNames = map[elf.Machine]string{
	elf.EM_X86_64:  "x86_64",
	elf.EM_AARCH64: "aarch64",
}

// coreTarget is what a core is of, in the terms the manifest of an
// extension says what the extension was checked on.
type coreTarget struct {
	// Version is the version of the Tarantool of the core; its Release is
	// empty when it cannot be told.
	Version tarantoolVersion
	// Arch is the architecture of the core, as uname -m names it, or the
	// name debug/elf gives the machine of another; empty when it cannot be
	// told.
	Arch string
}

// readCoreTarget returns what the core of the artefacts in dir is of: the
// version its version file names, and the architecture of the core or, when
// the core is no ELF file tt reads, of the tarantool. What cannot be told
// is logged once, at the debug level: the extensions are loaded without
// being checked against it.
func readCoreTarget(log *slog.Logger, dir string) coreTarget {
	target := coreTarget{
		Version: tarantoolVersion{Line: "", Enterprise: false, Tag: "", Release: "", Commit: ""},
		Arch:    "",
	}

	line, err := readVersion(filepath.Join(dir, "version"))
	if err == nil {
		version, ok := parseVersion(line)
		if ok {
			target.Version = version
		} else {
			err = fmt.Errorf("%w from %q", errNoVersion, line)
		}
	}

	if err != nil {
		log.Debug("cannot tell the version of the core, the extensions are not checked " +
			"against it: " + err.Error())
	}

	target.Arch, err = coreArch(dir)
	if err != nil {
		log.Debug("cannot tell the architecture of the core, the extensions are not checked " +
			"against it: " + err.Error())
	}

	return target
}

// coreArch returns the architecture of the core of the artefacts in dir,
// or of their tarantool when the core is no ELF file tt reads.
func coreArch(dir string) (string, error) {
	arch, coreErr := elfArch(filepath.Join(dir, "coredump"))
	if coreErr == nil {
		return arch, nil
	}

	arch, tarantoolErr := elfArch(filepath.Join(dir, "tarantool"))
	if tarantoolErr == nil {
		return arch, nil
	}

	return "", fmt.Errorf("%w; %w", coreErr, tarantoolErr)
}

// elfArch returns the architecture of the ELF file at path: the name of its
// machine in archNames, or the one debug/elf gives another, EM_RISCV.
func elfArch(path string) (string, error) {
	file, err := elf.Open(path)
	if err != nil {
		return "", fmt.Errorf("cannot read %s: %w", path, err)
	}

	defer func() { _ = file.Close() }()

	name, ok := archNames[file.Machine]
	if !ok {
		return file.Machine.String(), nil
	}

	return name, nil
}

// warnUnchecked warns of each of exts its manifest does not say it was
// checked on the target of the core: its Tarantool of a minor out of the
// range of the extension (see minorRange), its architecture none of those
// of the extension. One warning says both. The extension is loaded all the
// same: it reads the structures of the core, which may be those of a
// version it was checked on, or not. What cannot be told of the core is
// not checked, and neither is the version against a range whose ends are
// not releases, which is said in a line at the debug level: a test of the
// manifest tt embeds rules such a range out, so that it hides no warning.
// An upstream extension has no range, and nothing is said of it.
func warnUnchecked(log *slog.Logger, target coreTarget, exts []extension) {
	for _, ext := range exts {
		var checked, core []string

		minors, ok := ext.Tarantool.minors()

		switch {
		case ok && minors.excludes(target.Version.Release):
			checked = append(checked, "Tarantool "+minors.String())
			core = append(core, target.Version.Tag)
		case !ok && (ext.Tarantool.Min != "" || ext.Tarantool.Max != ""):
			log.Debug(fmt.Sprintf("cannot tell the versions of Tarantool %s is checked on from "+
				"its manifest, %q to %q, not releases x.y.z: the core is not checked against them",
				ext.Name, ext.Tarantool.Min, ext.Tarantool.Max))
		}

		if target.Arch != "" && len(ext.Arch) > 0 && !slices.Contains(ext.Arch, target.Arch) {
			checked = append(checked, joinAnd(ext.Arch))
			core = append(core, target.Arch)
		}

		if len(checked) > 0 {
			log.Warn(fmt.Sprintf("%s is checked on %s, the core is of %s: it may not work",
				ext.Name, strings.Join(checked, " on "), strings.Join(core, " on ")))
		}
	}
}

// joinAnd joins names as a sentence lists them: "a", "a and b", "a, b and
// c".
func joinAnd(names []string) string {
	if len(names) < 2 { //nolint:mnd // One name or none has no "and".
		return strings.Join(names, "")
	}

	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}
