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
	Package string
	// Release version to look for.
	ReleaseVersion string
	// Program name
	ProgramName string
	// Search for development builds.
	DevBuilds bool

	platformInformer platformInformer
	apiDoer          apiDoer
}

// NewSearchCtx creates a new SearchCtx with default production values.
func NewSearchCtx() SearchCtx {
	return SearchCtx{
		Filter: SearchRelease,
	}
}

// NewSearchCtxWithMock creates a new SearchCtx with a mock handlers for testing.
func NewSearchCtxWithMock(informer platformInformer, doer apiDoer) SearchCtx {
	sCtx := NewSearchCtx()
	sCtx.platformInformer = informer
	sCtx.apiDoer = doer
	return sCtx
}

// printVersion prints the version and labels:
// * if the package is installed: [installed]
// * if the package is installed and in use: [active]
func printVersion(bindir string, program string, versionStr string) {
	if _, err := os.Stat(filepath.Join(bindir,
		program+version.FsSeparator+versionStr)); err == nil {
		target := ""
		if program == ProgramEe {
			target, _ = util.ResolveSymlink(filepath.Join(bindir, "tarantool"))
		} else {
			target, _ = util.ResolveSymlink(filepath.Join(bindir, program))
		}

		if path.Base(target) == program+version.FsSeparator+versionStr {
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
	prg := searchCtx.ProgramName
	log.Infof("Available versions of " + prg + ":")

	if searchCtx.platformInformer == nil {
		searchCtx.platformInformer = &platformInfo{program: prg}
	}

	var err error
	var vers version.VersionSlice
	switch prg {
	case ProgramCe:
		vers, err = searchVersionsGit(cliOpts, prg, GitRepoTarantool)
	case ProgramTt:
		vers, err = searchVersionsGit(cliOpts, prg, GitRepoTT)
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
