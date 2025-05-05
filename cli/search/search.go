package search

import (
	"fmt"
	"os"
	"path"
	"path/filepath"

	"github.com/apex/log"
	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/util"
	"github.com/tarantool/tt/cli/version"
)

type SearchFlags int64

const (
	SearchRelease SearchFlags = iota
	SearchDebug
	SearchAll
)

// SearchCtx contains information for programs searching.
type SearchCtx struct {
	// Filter out which builds of tarantool-ee must be included in the result of search.
	Filter SearchFlags
	// What package to look for.
	// FIXME: It looks like this is not needed here, as it is then auto filled via [GetApiPackage].
	Package string
	// Release version to look for.
	ReleaseVersion string
	// Program type of program to search for.
	Program Program
	// Search for development builds.
	DevBuilds bool
	// TntIoDoer tarantool.io API handler with interface of TntIoDoer.
	TntIoDoer TntIoDoer

	platformInformer PlatformInformer
}

// NewSearchCtx creates a new SearchCtx with default production values.
func NewSearchCtx(informer PlatformInformer, doer TntIoDoer) SearchCtx {
	return SearchCtx{
		Filter:           SearchRelease,
		TntIoDoer:        doer,
		platformInformer: informer,
	}
}

// printVersion prints the version and labels:
// * if the package is installed: [installed]
// * if the package is installed and in use: [active]
func printVersion(bindir string, program Program, versionStr string) {
	if _, err := os.Stat(filepath.Join(bindir,
		program.String()+version.FsSeparator+versionStr)); err == nil {
		target, _ := util.ResolveSymlink(filepath.Join(bindir, program.Exec()))

		if path.Base(target) == program.String()+version.FsSeparator+versionStr {
			fmt.Printf("%s [active]\n", versionStr)
		} else {
			fmt.Printf("%s [installed]\n", versionStr)
		}
	} else {
		fmt.Println(versionStr)
	}
}

// SearchVersions outputs available versions of program.
func SearchVersions(searchCtx SearchCtx, cliOpts *config.CliOpts) error {
	prg := searchCtx.Program
	log.Infof("Available versions of %s:", prg)

	var err error
	var vers version.VersionSlice
	switch prg {
	case ProgramCe:
		vers, err = searchVersionsGit(cliOpts, GitRepoTarantool)
	case ProgramTt:
		vers, err = searchVersionsGit(cliOpts, GitRepoTT)
	case ProgramEe, ProgramTcm: // Group of API-based searches
		vers, err = searchVersionsTntIo(cliOpts, &searchCtx)
	default:
		return fmt.Errorf("remote search for program '%s' is not implemented", prg)
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
