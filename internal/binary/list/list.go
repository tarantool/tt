package list

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"

	"github.com/fatih/color"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/version"
	"github.com/tarantool/tt/v3/internal/binary"
)

var (
	// ErrNoBinariesInstalled reports a scope without installed binaries.
	ErrNoBinariesInstalled = errors.New(
		"there are no binaries installed in the selected scope",
	)
)

// ListCtx contains the options for listing installed binaries.
type ListCtx struct {
	binary.BinaryContext

	// Color styles the active version: the caller sets it when stdout is a
	// terminal that takes colour.
	Color bool
}

// displayVersion parses ordering information without changing installation identity.
func displayVersion(raw string) (version.Version, error) {
	name := strings.TrimPrefix(raw, binary.SDKVersionPrefix)

	name = strings.TrimPrefix(name, binary.DevVersionPrefix)
	name = strings.TrimPrefix(name, "v")

	isCommit, _ := binary.IsValidCommitHash(name)

	var parsed version.Version

	switch {
	case name == "master":
		parsed.Major = math.MaxUint // Keep master before release versions.
	case isCommit:
		// Commit hashes keep their stable directory order after release versions.
	default:
		var err error

		parsed, err = version.Parse(name)
		if err != nil {
			return parsed, err
		}
	}

	parsed.Str = raw

	return parsed, nil
}

// regularTarantoolVersion preserves support for an unversioned executable in this scope.
func regularTarantoolVersion(root string) (version.Version, bool, error) {
	var parsed version.Version

	programPath := filepath.Join(root, binary.BinDirName, binary.ProgramCe.Exec())
	info, err := os.Lstat(programPath)

	switch {
	case errors.Is(err, os.ErrNotExist):
		return parsed, false, nil
	case err != nil:
		return parsed, false, fmt.Errorf("failed to access %q: %w", programPath, err)
	case !info.Mode().IsRegular():
		return parsed, false, nil
	}

	tntCli := cmdcontext.TarantoolCli{Executable: programPath}

	parsed, err = tntCli.GetVersion()
	if err != nil {
		return parsed, false, err
	}

	parsed.Str += " [active]"

	return parsed, true, nil
}

// ParseBinaries reads installed executables from the selected product's version entries.
// Str preserves the exact installation suffix, followed by an optional active marker.
func ParseBinaries(fileList []fs.DirEntry, program binary.Program,
	root string,
) ([]version.Version, error) {
	active, err := binary.ResolveSymlink(filepath.Join(root, binary.BinDirName, program.Exec()))
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	var versions []version.Version

	for _, entry := range fileList {
		selected, found := binary.InstallationVersion(program, entry.Name())
		if !entry.IsDir() || !found {
			continue
		}

		var isActive bool

		switch program {
		case binary.ProgramSDK:
			sdk, err := binary.ReadSDK(root, selected)
			if errors.Is(err, os.ErrNotExist) {
				continue
			}

			if err != nil {
				return nil, err
			}

			err = binary.ValidateSDKManifest(sdk, selected)
			if err != nil {
				return nil, err
			}

			err = binary.ValidateSDKFiles(root, sdk)
			if err == nil {
				isActive = binary.SDKActive(root, sdk)
			}
		default:
			binPath := filepath.Join(root, binary.VersionsDirName, program.String(),
				entry.Name(), program.Exec())
			info, err := os.Stat(binPath)

			switch {
			case errors.Is(err, os.ErrNotExist):
				continue
			case err != nil:
				return nil, fmt.Errorf("failed to access %q: %w", binPath, err)
			case !info.Mode().IsRegular():
				continue
			}

			resolved, err := binary.ResolveSymlink(binPath)
			if err != nil {
				return nil, err
			}

			isActive = active == resolved
		}

		parsed, err := displayVersion(selected)
		if err != nil {
			return nil, err
		}

		if isActive {
			parsed.Str += " [active]"
		}

		versions = append(versions, parsed)
	}

	return versions, nil
}

// installedVersions prepares the selected product's versions for sorted display.
func installedVersions(root string, program binary.Program) ([]version.Version, error) {
	dir := filepath.Join(root, binary.VersionsDirName, program.String())
	entries, err := os.ReadDir(dir)

	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("error reading directory %q: %w", dir, err)
	}

	versions, err := ParseBinaries(entries, program, root)
	if err != nil {
		return nil, err
	}

	if program == binary.ProgramCe {
		active, found, err := regularTarantoolVersion(root)
		if err != nil {
			return nil, err
		}

		if found {
			versions = append(versions, active)
		}
	}

	sort.Stable(sort.Reverse(version.VersionSlice(versions)))

	return versions, nil
}

// Installed is one installed version of a product in the scope, or the
// local build its active tarantool links to.
type Installed struct {
	// Product is the product: tt, tt-ee, tarantool, tarantool-ee, tcm or
	// tarantool-sdk.
	Product string `json:"product" yaml:"product"`
	// Version is the installed version as tt binary switch takes it. A local
	// build has none.
	Version string `json:"version,omitempty" yaml:"version,omitempty"`
	// Active tells the version the active links of the scope select.
	Active bool `json:"active" yaml:"active"`
	// Path is the executable of a local build linked into the scope.
	Path string `json:"path,omitempty" yaml:"path,omitempty"`
	// Missing tells a local build whose executable is gone.
	Missing bool `json:"missing,omitempty" yaml:"missing,omitempty"`
}

// Listing is what tt binary list prints: every installed version of every
// product in one scope.
type Listing struct {
	// Scope is the scope listed: project, user or system.
	Scope string `json:"scope" yaml:"scope"`
	// Root is the directory of the scope.
	Root string `json:"root" yaml:"root"`
	// Binaries are the installed versions, product by product, the newest
	// first.
	Binaries []Installed `json:"binaries" yaml:"binaries"`

	// color styles the active version in the human form.
	color bool
}

// tabColumnPadding is the gap between the columns of the human form.
const tabColumnPadding = 2

// Human writes one line per installed version: the product and the
// version, the active one marked [active]. It is meant for both reading
// and scripts, so the product is on every line and nothing but the data
// goes to the writer; -o json gives the same data as JSON.
func (l *Listing) Human(w io.Writer) error {
	table := tabwriter.NewWriter(w, 0, 0, tabColumnPadding, ' ', 0)
	active := color.New(color.FgGreen, color.Bold)

	if l.color {
		active.EnableColor()
	} else {
		active.DisableColor()
	}

	for _, installed := range l.Binaries {
		label := installed.Version
		if installed.Path != "" {
			label = installed.Path
		}

		switch {
		case installed.Active && installed.Missing:
			label = active.Sprint(label + " [active, missing]")
		case installed.Active:
			label = active.Sprint(label + " [active]")
		case installed.Missing:
			label += " [missing]"
		}

		_, err := fmt.Fprintf(table, "%s\t%s\n", installed.Product, label)
		if err != nil {
			return fmt.Errorf("rendering the list: %w", err)
		}
	}

	err := table.Flush()
	if err != nil {
		return fmt.Errorf("rendering the list: %w", err)
	}

	return nil
}

// List reads the installed versions from exactly the selected scope. A
// scope with none is ErrNoBinariesInstalled.
func List(listCtx ListCtx) (*Listing, error) {
	root, err := listCtx.Scope.ArtifactPath()
	if err != nil {
		return nil, err
	}

	buildPath, err := binary.TarantoolBuildPath(filepath.Join(root, binary.BinDirName))
	if err != nil {
		return nil, err
	}

	programs := [...]binary.Program{
		binary.ProgramTt,
		binary.ProgramTtEe,
		binary.ProgramCe,
		binary.ProgramEe,
		binary.ProgramTcm,
		binary.ProgramSDK,
	}

	listing := &Listing{
		Scope:    listCtx.Scope.String(),
		Root:     root,
		Binaries: []Installed{},
		color:    listCtx.Color,
	}

	for _, program := range programs {
		versions, err := installedVersions(root, program)
		if err != nil {
			return nil, err
		}

		for _, installed := range versions {
			name, active := strings.CutSuffix(installed.Str, " [active]")

			listing.Binaries = append(listing.Binaries, Installed{
				Product: program.String(),
				Version: strings.TrimPrefix(name, "v"),
				Active:  active,
				Path:    "",
				Missing: false,
			})
		}

		if program == binary.ProgramCe && buildPath != "" {
			listing.Binaries = append(listing.Binaries, Installed{
				Product: program.String(),
				Version: "",
				Active:  true,
				Path:    buildPath,
				Missing: !binary.IsRegularFile(buildPath),
			})
		}
	}

	if len(listing.Binaries) == 0 {
		return nil, ErrNoBinariesInstalled
	}

	return listing, nil
}
