package search

import (
	"errors"
	"fmt"
	"os"
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

	// LocalRepo is the explicit local repository directory.
	LocalRepo string
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

// printVersion annotates a version using only paths in the selected scope.
func printVersion(root string, program binary.Program, versionStr string) {
	installedPath := filepath.Join(root, binary.VersionsDirName, program.String(),
		program.String()+version.FsSeparator+versionStr, program.Exec())
	info, err := os.Stat(installedPath)

	if err != nil || !info.Mode().IsRegular() {
		_, _ = fmt.Fprintln(os.Stdout, versionStr)

		return
	}

	label := "installed"
	activePath, _ := binary.ResolveSymlink(filepath.Join(root, binary.BinDirName, program.Exec()))
	resolvedInstalledPath, err := binary.ResolveSymlink(installedPath)

	if err == nil && resolvedInstalledPath == activePath {
		label = "active"
	}

	_, _ = fmt.Fprintf(os.Stdout, "%s [%s]\n", versionStr, label)
}

// SearchVersions outputs available versions of program.
func SearchVersions(searchCtx SearchCtx) error {
	root, err := searchCtx.Scope.ArtifactPath()
	if err != nil {
		return err
	}

	prg := searchCtx.Program
	log.Infof("Available versions of %s:", prg)

	var vers version.VersionSlice

	switch prg {
	case binary.ProgramCe, binary.ProgramTt:
		vers, err = searchVersionsGit(prg.GitRepo())
	case binary.ProgramEe, binary.ProgramTcm: // Group of API-based searches.
		var opts config.CliOpts

		vers, err = searchVersionsTntIo(&opts, &searchCtx)
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
		printVersion(root, prg, v.Str)
	}

	return nil
}
