package search

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/config"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	errRemoteSearchForProgramIsNotImplemented = errors.New("remote search for program '")
)

type SearchFlags int64

const (
	SearchRelease SearchFlags = iota
	SearchDebug
	SearchAll
)

// SearchCtx contains information for programs searching.
type SearchCtx struct {
	binary.BinaryContext

	// Local searches the configured local repository.
	Local bool
	// Debug includes debug builds instead of release builds.
	Debug bool
	// Filter out which builds of tarantool-ee must be included in the result of search.
	Filter SearchFlags
	// What package to look for.
	// FIXME: It looks like this is not needed here, as it is then auto filled via [GetApiPackage].
	Package string
	// Release version to look for.
	ReleaseVersion string
	// Search for development builds.
	DevBuilds bool
	// TntIoDoer tarantool.io API handler with interface of TntIoDoer.
	TntIoDoer binary.TntIoDoer

	platformInformer PlatformInformer
}

// NewSearchCtx creates a new SearchCtx with default production values.
func NewSearchCtx(informer PlatformInformer, doer binary.TntIoDoer) SearchCtx {
	var searchCtx SearchCtx

	searchCtx.Filter = SearchRelease
	searchCtx.TntIoDoer = doer
	searchCtx.platformInformer = informer

	return searchCtx
}

// printVersion prints the version and labels:
// * if the package is installed: [installed]
// * if the package is installed and in use: [active].
func printVersion(bindir string, program binary.Program, versionStr string) {
	_, err := os.Stat(filepath.Join(bindir, program.String()+version.FsSeparator+versionStr))
	if err == nil {
		target, _ := binary.ResolveSymlink(filepath.Join(bindir, program.Exec()))

		if path.Base(target) == program.String()+version.FsSeparator+versionStr {
			_, _ = fmt.Fprintf(os.Stdout, "%s [active]\n", versionStr)
		} else {
			_, _ = fmt.Fprintf(os.Stdout, "%s [installed]\n", versionStr)
		}
	} else {
		_, _ = fmt.Fprintln(os.Stdout, versionStr)
	}
}

// SearchVersions outputs available versions of program.
func SearchVersions(searchCtx SearchCtx, cliOpts *config.CliOpts) error {
	prg := searchCtx.Program
	log.Infof("Available versions of %s:", prg)

	var (
		err  error
		vers version.VersionSlice
	)

	switch prg {
	case binary.ProgramCe:
		vers, err = searchVersionsGit(GitRepoTarantool)
	case binary.ProgramTt:
		vers, err = searchVersionsGit(GitRepoTT)
	case binary.ProgramEe, binary.ProgramTcm: // Group of API-based searches.
		vers, err = searchVersionsTntIo(cliOpts, &searchCtx)
	default:
		return fmt.Errorf("%w%s' is not implemented",
			errRemoteSearchForProgramIsNotImplemented, prg)
	}

	if err != nil {
		return err
	}

	if vers.Len() == 0 {
		log.Infof("No versions found for %s.", prg)

		return nil // It's not an error if nothing is found.
	}

	for _, v := range vers {
		printVersion(cliOpts.Env.BinDir, prg, v.Str)
	}

	return nil
}
