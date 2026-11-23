// Package coredump is the tt module of the coredump commands: tt coredump
// pack collects a core dump of Tarantool with the executable, the shared
// libraries and the gdb extensions it needs into a tar.gz archive, unpack
// unpacks such an archive, and inspect opens it in gdb.
//
// The module is built on the tt SDK alone. [New] is its constructor; a
// distribution of tt lists it in the table of modules it hands to the core.
package coredump

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/modules/coredump/internal/coredump"
	"github.com/tarantool/tt/sdk"
)

// New builds the coredump module's commands: the coredump group with its
// pack, unpack and inspect commands, mounted at the root of tt.
func New(services sdk.Services) []sdk.Mount {
	group := &cobra.Command{
		Use: "coredump",
		// spell-checker:ignore coredumps
		Short: "Perform manipulations with the tarantool coredumps",
	}

	group.AddCommand(newPackCmd(services), newUnpackCmd(services), newInspectCmd(services))

	return []sdk.Mount{{Path: "", Cmd: group}}
}

// packCmd is tt coredump pack: the values of its flags.
type packCmd struct {
	services   sdk.Services
	executable string
	directory  string
	time       string
	pid        uint
}

// newPackCmd builds `tt coredump pack`.
func newPackCmd(services sdk.Services) *cobra.Command {
	pack := &packCmd{services: services, executable: "", directory: "", time: "", pid: 0}

	cmd := &cobra.Command{
		Use:   "pack COREDUMP",
		Short: "pack tarantool coredump into tar.gz archive",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			return pack.run(args[0])
		},
	}

	cmd.Flags().StringVarP(&pack.executable, "executable", "e", "",
		"Tarantool executable path")
	// tt reserves --directory, -C, for the project directory of a command.
	cmd.Flags().StringVarP(&pack.directory, "dir", "d", "",
		"Directory the resulting archive is created in")
	cmd.Flags().StringVarP(&pack.time, "time", "t", "",
		"Time of dump, expressed as seconds since the Epoch (1970-01-01 00:00 UTC)")
	cmd.Flags().UintVarP(&pack.pid, "pid", "p", 0,
		"PID of the dumped process, as seen in the PID namespace in which "+
			"the given process resides (see %p in core(5) for more info). This flag "+
			"is to be used when tt is used as kernel.core_pattern pipeline script")

	return cmd
}

// run packs the core at corePath.
func (p *packCmd) run(corePath string) error {
	executable, err := p.tarantool()
	if err != nil {
		return err
	}

	err = coredump.Pack(p.services.Streams().IO(), coredump.PackOptions{
		Core: corePath, Executable: executable, Dir: p.directory, PID: p.pid, Time: p.time,
	})
	if err != nil {
		return err
	}

	p.services.Log().Info("Core was successfully packed.")

	return nil
}

// tarantool returns the executable to pack the core with: the one --executable
// names, or the Tarantool tt runs. With neither it is empty, and the pack
// script looks tarantool up in PATH.
func (p *packCmd) tarantool() (string, error) {
	if p.executable != "" {
		return p.executable, nil
	}

	tarantool, err := p.services.Tarantool()
	if errors.Is(err, sdk.ErrNotFound) {
		return "", nil
	}

	if err != nil {
		return "", fmt.Errorf("cannot find the tarantool executable: %w", err)
	}

	return tarantool.Path(), nil
}

// newUnpackCmd builds `tt coredump unpack`.
func newUnpackCmd(services sdk.Services) *cobra.Command {
	return &cobra.Command{
		Use:   "unpack ARCHIVE",
		Short: "unpack tarantool coredump tar.gz archive",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			err := coredump.Unpack(args[0])
			if err != nil {
				return err
			}

			services.Log().Info("Archive was successfully unpacked.")

			return nil
		},
	}
}

// experimentalFlag is the flag of tt coredump inspect that loads the
// experimental gdb extensions of tarantool-gdb-tools tt embeds, one version
// for many releases of Tarantool, instead of those of the Tarantool of the
// core.
const experimentalFlag = "experimental"

// newInspectCmd builds `tt coredump inspect`.
func newInspectCmd(services sdk.Services) *cobra.Command {
	opts := coredump.InspectOptions{Path: "", SourceDir: "", GDBTools: false}

	cmd := &cobra.Command{
		Use:   "inspect {ARCHIVE|DIRECTORY}",
		Short: "inspect tarantool coredump",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			opts.Path = args[0]

			return coredump.Inspect(cmd.Context(), services.Streams().IO(), services.Log(), opts)
		},
	}

	cmd.Flags().StringVarP(&opts.SourceDir, "sourcedir", "s", "", "Source directory")
	// The help names no version of Tarantool: those the extensions are
	// checked on are the ones of their manifest, which a pin of another
	// commit changes with the files alone.
	cmd.Flags().BoolVar(&opts.GDBTools, experimentalFlag, false, fmt.Sprintf(
		"Load the experimental gdb extensions of tarantool-gdb-tools (vendored by tt at %s), "+
			"with lj-bt, instead of those of the Tarantool of the core: they are checked on "+
			"the versions of Tarantool their manifest names, and inspect warns of a core "+
			"outside them; -s then only maps the sources and picks no extensions",
		coredump.GDBToolsCommit()))

	return cmd
}
