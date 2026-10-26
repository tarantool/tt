package tarantool

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"syscall"

	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/scope"
)

var errBrokenLink = errors.New("the project tarantool is not an executable")

// resolveProject chooses the tarantool for the project in dir: its active
// link, else the first tarantool on PATH. With fromPath the link is not
// looked at.
func resolveProject(dir string, fromPath bool) (Executable, error) {
	link := filepath.Join(scope.ProjectRoot(dir), binary.BinDirName, binary.ProgramCe.Exec())

	if !fromPath {
		exe, found, err := fromProjectLink(link)
		if err != nil {
			return Executable{}, err
		}

		if found {
			exe.Warnings = checkLock(dir, exe)

			return exe, nil
		}
	}

	path, err := exec.LookPath(binary.ProgramCe.Exec())
	if err == nil {
		// The version of a tarantool from PATH is only known by running it,
		// and Resolve runs nothing, so the lock is not compared with it.
		return Executable{
			Path:     path,
			Origin:   OriginPath,
			Program:  binary.ProgramUnknown,
			Version:  "",
			Warnings: nil,
		}, nil
	}

	if fromPath {
		return Executable{}, fmt.Errorf("%w: %s is set and there is none on PATH",
			ErrNoTarantool, UseSystemEnv)
	}

	return Executable{}, fmt.Errorf(
		"%w: no %s and none on PATH; install one into the project with: "+
			"tt binary install tarantool",
		ErrNoTarantool, link)
}

// fromProjectLink returns the tarantool the active link of the project
// selects; found is false when there is no link.
//
// The link is resolved here, so Path names the installation it selected and
// Program and Version identify it. A link that selects nothing is an error,
// not a reason to fall back to PATH: the project chose a tarantool, and
// running another one in its place is not what was asked for.
func fromProjectLink(link string) (Executable, bool, error) {
	var none Executable

	info, err := os.Lstat(link)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return none, false, nil
	}

	if err != nil {
		return Executable{}, false, fmt.Errorf("cannot inspect the project tarantool: %w", err)
	}

	// tt binary makes the active selection a symbolic link, but an executable
	// put there by hand runs too; only its version is unknown.
	if info.Mode()&os.ModeSymlink == 0 {
		if !info.Mode().IsRegular() {
			return Executable{}, false, fmt.Errorf("%w: %s", errBrokenLink, link)
		}

		err = checkExecutable(link)
		if err != nil {
			return Executable{}, false, err
		}

		return Executable{
			Path:     link,
			Origin:   OriginProject,
			Program:  binary.ProgramUnknown,
			Version:  "",
			Warnings: nil,
		}, true, nil
	}

	target, err := os.Readlink(link)
	if err != nil {
		return Executable{}, false, fmt.Errorf("cannot read the project tarantool link: %w", err)
	}

	if !filepath.IsAbs(target) {
		target = filepath.Join(filepath.Dir(link), target)
	}

	if !binary.IsRegularFile(target) {
		return Executable{}, false, fmt.Errorf(
			"%w: %s selects %s; select an installed version with: tt binary switch tarantool",
			errBrokenLink, link, target)
	}

	err = checkExecutable(target)
	if err != nil {
		return Executable{}, false, fmt.Errorf("%w; %s selects it", err, link)
	}

	program, selected := installationOf(target)

	return Executable{
		Path:     target,
		Origin:   OriginProject,
		Program:  program,
		Version:  selected,
		Warnings: nil,
	}, true, nil
}

// installationOf identifies the tarantool installation whose executable is
// path, <root>/versions/<program>/<installation>/tarantool. A path of another
// shape, such as a build tt binary install --from-build linked, yields
// binary.ProgramUnknown.
func installationOf(path string) (binary.Program, string) {
	installation := filepath.Dir(path)
	productDir := filepath.Dir(installation)

	if filepath.Base(path) != binary.ProgramCe.Exec() ||
		filepath.Base(filepath.Dir(productDir)) != binary.VersionsDirName {
		return binary.ProgramUnknown, ""
	}

	program, err := binary.ParseProgram(filepath.Base(productDir))
	if err != nil || !program.IsTarantool() {
		return binary.ProgramUnknown, ""
	}

	id, found := binary.InstallationVersion(program, filepath.Base(installation))
	if !found {
		return binary.ProgramUnknown, ""
	}

	return program, id
}

// checkLock compares the tarantool a project runs on with the one its lock
// bundles, and returns a warning when they differ.
//
// Nothing here stops the run: in a project, running on another tarantool is a
// choice its developer makes. For the same reason a lock that cannot be read
// is a warning too; the check is advisory, and the commands that act on the
// lock report it as they need.
func checkLock(dir string, exe Executable) []string {
	if exe.Program == binary.ProgramUnknown {
		return nil
	}

	lock, found, err := readLock(dir)
	if err != nil {
		return []string{fmt.Sprintf("cannot compare the project tarantool with %s: %s",
			lockFileName, err)}
	}

	if !found || lock.BundledTarantool == "" {
		return nil
	}

	// The lock records the version alone, so an installation of that
	// version is taken for the bundled tarantool whichever product it is.
	if slices.Contains(installationIDs(exe.Program, lock.BundledTarantool), exe.Version) {
		return nil
	}

	return []string{fmt.Sprintf("the project runs %s %s (%s), but %s bundles %s",
		exe.Program, exe.Version, exe.Path, lockFileName, lock.BundledTarantool)}
}
