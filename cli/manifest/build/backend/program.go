package backend

import (
	"errors"
	"fmt"
	"os/exec"

	"github.com/tarantool/tt/v3/cli/manifest"
)

var (
	// ErrNoTool reports that the program a backend runs, go or cargo, is not
	// in PATH.
	ErrNoTool = errors.New("build tool not found")
	// ErrStaticDarwin reports a static build for darwin: macOS has no static
	// libSystem to link against, so a static program cannot be made there.
	ErrStaticDarwin = errors.New("a static program cannot be built for darwin")
	// errUnquotableFlag reports a linker flag go build cannot be given.
	errUnquotableFlag = errors.New(
		"a linker flag with a space and both kinds of quotes cannot be passed to go build")
	// errUnsupportedTarget reports a platform cargo is not mapped to a target
	// for.
	errUnsupportedTarget = errors.New("no cargo target for the platform")
	// errNoArtifact reports a cargo build that reported no program for the
	// binary it was asked to build.
	errNoArtifact = errors.New("cargo reported no executable")
)

// darwinOS is the GOOS spelling of macOS.
const darwinOS = "darwin"

// Preflight checks what a go or cargo build needs before it runs: a static
// program is not asked for darwin, the tool is in PATH and, for cargo, the
// platform has a target. The build orchestration calls it for every
// component it is about to build before it builds any, so that a missing
// tool fails the build before the first long compile rather than after it;
// the backends call it again. Any other backend passes.
func Preflight(build manifest.Build, env Env) error {
	var tool string

	switch build.Backend {
	case BackendGo:
		tool = goTool
	case BackendCargo:
		tool = cargoTool
	default:
		return nil
	}

	if build.StaticValue() && env.platformOS() == darwinOS {
		return fmt.Errorf("%w: static = true needs a linux build", ErrStaticDarwin)
	}

	if build.Backend == BackendCargo {
		_, err := cargoTarget(build, env)
		if err != nil {
			return err
		}
	}

	_, err := exec.LookPath(tool)
	if err != nil {
		return fmt.Errorf("%w: the %s backend runs %q, which is not in PATH",
			ErrNoTool, build.Backend, tool)
	}

	return nil
}
