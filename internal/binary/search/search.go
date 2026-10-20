package search

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/sdk/log"
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

	// LocalRepo is the source directory: --local-repo for Git repositories,
	// or --local-dir for SDK or standalone archives.
	LocalRepo string
	// CredentialsFile contains customer-zone credentials for remote EE and TCM searches.
	CredentialsFile string
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
func printVersion(root string, program binary.Program, available version.Version, dev bool) {
	versionStr := available.Str
	if dev {
		versionStr = binary.DevVersionPrefix + versionStr
	}

	selected := binary.BundleInstallationID(program, available, dev)
	if program == binary.ProgramSDK {
		printSDKVersion(root, selected)

		return
	}

	installedPath := filepath.Join(binary.InstallationPath(root, program, selected), program.Exec())
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

func printSDKVersion(root, selected string) {
	sdk, err := binary.ReadSDK(root, selected)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stdout, selected)

		return
	}

	err = binary.ValidateSDKManifest(sdk, selected)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stdout, selected)

		return
	}

	err = binary.ValidateSDKFiles(root, sdk)
	if err != nil {
		_, _ = fmt.Fprintln(os.Stdout, selected)

		return
	}

	label := "installed"
	if binary.SDKActive(root, sdk) {
		label = "active"
	}

	_, _ = fmt.Fprintf(os.Stdout, "%s [%s]\n", selected, label)
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

	switch {
	case prg.GitRepo() != "":
		vers, err = searchVersionsGit(prg.GitRepo())
	case prg.BundleSource() != binary.ProgramUnknown:
		vers, err = searchVersionsTntIo(&searchCtx)
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
		printVersion(root, prg, v, searchCtx.DevBuilds)
	}

	return nil
}
