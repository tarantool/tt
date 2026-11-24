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
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/modules/coredump/internal/coredump"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/scope"
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
		Use:   "pack [COREDUMP]",
		Short: "pack tarantool coredump into tar.gz archive",
		Long: "Pack COREDUMP, a core dump of Tarantool, into a tar.gz archive with the " +
			"tarantool that dumped it, the shared libraries it had loaded and the gdb " +
			"extensions, for `tt coredump inspect`. The archive is " +
			"tarantool-core-PID-TIME-HOST.tar.gz in --dir, the working directory by " +
			"default, with the PID of --pid, or of the process of the core, and the time " +
			"of --time, or now; pack says its path.\n\n" +
			"The tarantool is the one --executable names, and the core is refused when " +
			"that one did not dump it. Without --executable, it is the executable the core " +
			"names, when the file is still there and dumped the core, and otherwise the " +
			"first that dumped it, by its build ID, of the tarantool of PATH, the one tt " +
			"runs here and those tt binary installed into the project, user and system " +
			"scopes; pack says which it takes.\n\n" +
			"With --pid, tt is the pipe of kernel.core_pattern, as in /etc/sysctl.conf:\n\n" +
			"  kernel.core_pattern = |/usr/bin/tt coredump pack -d /var/core -p %p -t %t\n\n" +
			"COREDUMP is omitted then, or -, and tt reads the core from its standard input. " +
			"While /proc/PID has the process, the core is stored as DIR/COMM-core.PID.TIME, " +
			"COMM of /proc/PID/comm, and packed with /proc/PID/exe, unless --executable " +
			"names a tarantool; the core of a process whose comm is not tarantool is " +
			"stored and not packed. A COREDUMP file is packed as it is, with the tarantool " +
			"found as without --pid, and the PID names the archive.",
		Args: pack.args,
		RunE: func(_ *cobra.Command, args []string) error {
			core := ""
			if len(args) > 0 {
				core = args[0]
			}

			return pack.run(core)
		},
	}

	cmd.Flags().StringVarP(&pack.executable, "executable", "e", "",
		"Tarantool executable that dumped the core, instead of the one pack finds for it")
	// tt reserves --directory, -C, for the project directory of a command.
	cmd.Flags().StringVarP(&pack.directory, "dir", "d", "",
		"Directory the resulting archive is created in")
	cmd.Flags().StringVarP(&pack.time, "time", "t", "",
		"Time of dump, expressed as seconds since the Epoch (1970-01-01 00:00 UTC)")
	cmd.Flags().UintVarP(&pack.pid, "pid", "p", 0,
		"PID of the dumped process, as seen in the PID namespace in which "+
			"the given process resides (see %p in core(5) for more info). This flag "+
			"is to be used when tt is used as kernel.core_pattern pipeline script: "+
			"without COREDUMP, or with -, the core is read from the standard input")

	return cmd
}

// errStdinWithoutPID reports the core of the standard input given without
// --pid, the PID of the process of the core.
var errStdinWithoutPID = errors.New("the core on the standard input needs --pid")

// args checks the arguments of pack: COREDUMP, which --pid makes optional,
// for the core on the standard input, as a kernel.core_pattern pipe gives
// it, as COREDUMP - does.
func (p *packCmd) args(cmd *cobra.Command, args []string) error {
	if p.pid != 0 {
		return cobra.MaximumNArgs(1)(cmd, args)
	}

	err := cobra.ExactArgs(1)(cmd, args)
	if err != nil {
		return err
	}

	if args[0] == coredump.StdinCore {
		return fmt.Errorf("COREDUMP %s: %w", args[0], errStdinWithoutPID)
	}

	return nil
}

// run packs the core at corePath, or the core on the standard input with
// corePath empty or "-".
func (p *packCmd) run(corePath string) error {
	streams := p.services.Streams().IO()
	opts := coredump.PackOptions{
		Core: corePath, Executable: p.executable, Tarantools: nil, Dir: p.directory, PID: p.pid,
		Time: p.time,
	}

	if p.pid != 0 {
		var (
			tarantool bool
			err       error
		)

		opts, tarantool, err = coredump.ReceiveCore(streams.In, opts)
		if err != nil {
			return err
		}

		if !tarantool {
			p.services.Log().Info(fmt.Sprintf("The process %d is not a tarantool, by its "+
				"comm: its core is stored and not packed: %s", p.pid, opts.Core))

			return nil
		}
	}

	if opts.Executable == "" {
		opts.Tarantools = p.tarantools
	}

	archive, taken, err := coredump.Pack(streams, p.services.Log(), opts)
	if err != nil {
		return err
	}

	// An archive of the name is kept, and the archive is named apart.
	packed := "Core was successfully packed: " + archive
	if taken != "" {
		packed += ", since " + filepath.Base(taken) + " is there already"
	}

	p.services.Log().Info(packed)

	return nil
}

// runsOrigin is what pack calls the Tarantool tt runs, Services.Tarantool.
const runsOrigin = "the tarantool tt runs here"

// tarantools returns the tarantools tt knows of, for pack to look the one
// that dumped a core up among when it is given none and neither the
// executable the core names nor the tarantool of PATH dumped it, in their
// order: the Tarantool tt runs here, of the project or of PATH, then those
// tt binary installed into the project, the user and the system scopes, the
// active one of each first. One that cannot be found is a candidate with
// the error, which pack lists should none of the others be the one.
func (p *packCmd) tarantools() []coredump.Candidate {
	var candidates []coredump.Candidate

	tarantool, err := p.services.Tarantool()

	switch {
	case err == nil:
		candidates = append(candidates,
			coredump.Candidate{Path: tarantool.Path(), Origin: runsOrigin, Err: nil})
	case !errors.Is(err, sdk.ErrNotFound):
		candidates = append(candidates, coredump.Candidate{Path: "", Origin: runsOrigin,
			Err: fmt.Errorf("cannot find the tarantool executable: %w", err)})
	}

	scopes := []struct {
		name string
		root func() (string, error)
	}{
		{name: "project", root: p.services.Project().ScopeDir},
		{name: "user", root: scope.UserRoot},
		{name: "system", root: scope.SystemRoot},
	}

	for _, installed := range scopes {
		root, err := installed.root()
		if err != nil {
			candidates = append(candidates, coredump.Candidate{
				Path: "", Origin: "the tarantools of the " + installed.name + " scope", Err: err,
			})

			continue
		}

		candidates = append(candidates, coredump.ScopeTarantools(installed.name, root)...)
	}

	return candidates
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
