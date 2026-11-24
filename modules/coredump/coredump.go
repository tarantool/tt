// Package coredump is the tt module of the coredump commands: tt coredump
// pack collects a core dump of Tarantool with the executable, the shared
// libraries and the gdb extensions it needs into a tar.gz archive, unpack
// unpacks such an archive, inspect opens it in gdb, and list lists the
// crashes of the instances of this host and where their cores are.
//
// The module is built on the tt SDK alone. [New] is its constructor; a
// distribution of tt lists it in the table of modules it hands to the core.
package coredump

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/modules/coredump/internal/coredump"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/output"
	"github.com/tarantool/tt/sdk/scope"
)

// New builds the coredump module's commands: the coredump group with its
// pack, unpack, inspect and list commands, mounted at the root of tt.
func New(services sdk.Services) []sdk.Mount {
	group := &cobra.Command{
		Use: "coredump",
		// spell-checker:ignore coredumps
		Short: "Perform manipulations with the tarantool coredumps",
	}

	group.AddCommand(newPackCmd(services), newUnpackCmd(services), newInspectCmd(services),
		newListCmd(services))

	return []sdk.Mount{{Path: "", Cmd: group}}
}

// instanceTargets is the target part of the usage of the commands that take
// instances, as the instance commands of tt write it.
const instanceTargets = "[<APP>:<INSTANCE> | <APP>:*][,...]"

// newListCmd builds `tt coredump list`.
func newListCmd(services sdk.Services) *cobra.Command {
	var (
		lookup sdk.AppLookup
		format *output.FormatFlag
	)

	cmd := &cobra.Command{
		Use:   "list " + instanceTargets,
		Short: "list the crashes of instances and where their cores are",
		Long: "List the crashes of the instances the targets select, as tt instance status " +
			"selects them: every instance of the project the command runs in without a " +
			"target, and with --scope user or system every instance installed there. A " +
			"crash is an exit of the process of an instance by a signal, which its " +
			"watchdog records in crashes.json of its run directory, the last 10, through " +
			"its restarts and stops. Each is listed, the newest first, with when it was, " +
			"the PID of the process, the signal and where its core is now, with its size, " +
			"or why it is not found. Crashes that cannot be read, as those of an instance " +
			"of another user, are warned of, with why.\n\n" +
			"The core is looked for where kernel.core_pattern puts it now: a file of the " +
			"pattern, %p and the other specifiers of core(5) expanded and .PID of " +
			"core_uses_pid appended, relative to the working directory of the process; the " +
			"cores of systemd-coredump, which coredumpctl lists by their PID and dumps; the " +
			"core and the archive tt coredump pack, the pipe of the pattern, made in its " +
			"--dir; and the core apport keeps in /var/lib/apport/coredump, or, of an older " +
			"apport, in the working directory of the process, or its report in /var/crash. " +
			"A core file of another process than the crash, by the PID it records, as of " +
			"another instance that crashed then with a pattern without %p, is not its " +
			"core. A pipe to another program is named, and its cores are not looked for.\n\n" +
			"tt coredump pack <APP>:<INSTANCE> packs the core of the last crash of an " +
			"instance that has one.",
		RunE: func(cmd *cobra.Command, args []string) error {
			instances, err := services.LocalInstances(cmd.Context(), args, lookup)
			if err != nil {
				return err
			}

			loc := coredump.NewLocator()
			list := coredump.List(cmd.Context(), loc, instances)

			printer, err := services.Streams().Printer(format.Format())
			if err != nil {
				return err
			}

			err = printer.Emit(list)
			if err != nil {
				return err
			}

			logListNotes(services.Log(), loc, list)

			return nil
		},
	}

	sdk.AddAppFlags(cmd, &lookup)

	format = output.BindFormat(cmd.Flags(), output.FormatHuman,
		output.FormatHuman, output.FormatJSON, output.FormatYAML)

	return cmd
}

// logListNotes logs what tt coredump list says of list beside the table:
// the instances whose crashes cannot be read, and why, the instances with no
// crash, the pattern the cores were looked for by, and how to pack one that
// was found.
func logListNotes(log *slog.Logger, loc *coredump.Locator, list coredump.CrashList) {
	for _, inst := range list.Unreadable() {
		log.Warn(fmt.Sprintf("The crashes of %s cannot be read: %s.", inst.Instance,
			inst.CrashesError))
	}

	quiet := list.NotCrashed()
	if len(quiet) > 0 {
		log.Info("No crash is recorded of " + strings.Join(quiet, ", ") + ".")
	}

	if len(list.Crashed()) == 0 {
		return
	}

	pattern, err := loc.Pattern()
	if err == nil {
		log.Info(fmt.Sprintf("The cores are looked for where kernel.core_pattern %q puts "+
			"them now.", pattern))
	}

	if list.HasCore() {
		log.Info("tt coredump pack <APP>:<INSTANCE> packs the core of the last crash of an " +
			"instance.")
	}
}

// packCmd is tt coredump pack: the values of its flags.
type packCmd struct {
	services   sdk.Services
	executable string
	directory  string
	time       string
	pid        uint
	// noLogs leaves the logs of the instance of a target out of the
	// archive.
	noLogs bool
	// lookup finds the application of an instance target.
	lookup sdk.AppLookup
	// notify and stopNotify are signal.Notify and signal.Stop, which a test
	// replaces.
	notify     func(c chan<- os.Signal, sig ...os.Signal)
	stopNotify func(c chan<- os.Signal)
}

// newPackCmd builds `tt coredump pack`.
func newPackCmd(services sdk.Services) *cobra.Command {
	pack := &packCmd{
		services: services, executable: "", directory: "", time: "", pid: 0, noLogs: false,
		lookup: sdk.AppLookup{Scope: "", Template: ""}, notify: signal.Notify,
		stopNotify: signal.Stop,
	}

	cmd := &cobra.Command{
		Use:   "pack [COREDUMP | <APP>:<INSTANCE>]",
		Short: "pack tarantool coredump into tar.gz archive",
		Long: "Pack COREDUMP, a core dump of Tarantool, into a tar.gz archive with the " +
			"tarantool that dumped it, the shared libraries it had loaded and the gdb " +
			"extensions, for `tt coredump inspect`. The archive is " +
			"tarantool-core-PID-TIME-HOST.tar.gz in --dir, the working directory by " +
			"default, with the PID of --pid, or of the process of the core, and the time " +
			"of --time, or now; pack says its path. SIGINT or SIGTERM, as Ctrl-C or the " +
			"stop of a service sends, stops pack, which removes what it was making, and tt " +
			"exits 130 or 143.\n\n" +
			"<APP>:<INSTANCE>, which is no file, is an instance of this host, found as " +
			"tt instance status finds it with --scope and --template: pack packs the " +
			"core of the last crash of the instance that dumped one, its watchdog " +
			"recorded, found as tt coredump list finds it, a core of systemd-coredump " +
			"dumped by coredumpctl, the one of the entry of the crash, into a file of " +
			"--dir that pack removes after, with its holes, and names the archive by the " +
			"PID and the time of the crash; a core file of another process is refused. " +
			"pack says which " +
			"crash it packs and where the core came from; a crash with no core, or one " +
			"whose core is not found, is an error that says why. The archive has the last " +
			"MiB of the logs of the instance in logs/: tt.log, with the standard error " +
			"of tarantool, and the file tarantool logs into of its own, if any; the " +
			"config of the instance, which may hold passwords, is not packed. With " +
			"--no-logs the archive has no logs.\n\n" +
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
		RunE: func(cmd *cobra.Command, args []string) error {
			core := ""
			if len(args) > 0 {
				core = args[0]
			}

			ctx, stop := pack.signalContext(cmd.Context())
			defer stop()

			var err error

			// The pipe of core_pattern packs the core it is given alone.
			if pack.pid == 0 && coredump.IsInstanceTarget(core) {
				err = pack.runInstance(ctx, core)
			} else {
				err = pack.run(ctx, core)
			}

			return interruptedError(ctx, err)
		},
	}

	sdk.AddAppFlags(cmd, &pack.lookup)

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
	cmd.Flags().BoolVar(&pack.noLogs, "no-logs", false,
		"Pack no logs of the instance of <APP>:<INSTANCE>, which may tell what it worked with")

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

// interruptSignals are the signals that interrupt pack, which then removes
// what it was making: Ctrl-C of a terminal, and the stop of a service.
var interruptSignals = []os.Signal{syscall.SIGINT, syscall.SIGTERM}

// signalExitBase is what the exit code of a process a signal ended adds the
// number of the signal to, as a shell tells it: 130 of SIGINT, 143 of
// SIGTERM.
const signalExitBase = 128

// errInterrupted reports a pack a signal interrupted.
var errInterrupted = errors.New("pack is interrupted")

// interruption is the cause of the context of pack a signal ended.
type interruption struct {
	signal syscall.Signal
}

// Error says which signal interrupted pack.
func (i interruption) Error() string {
	return fmt.Sprintf("%v by %s", errInterrupted, coredump.SignalName(i.signal))
}

// Unwrap returns errInterrupted.
func (i interruption) Unwrap() error {
	return errInterrupted
}

// signalContext returns a context of parent that the first of
// interruptSignals tt gets ends, with an interruption of it as its cause,
// and the function that releases it. pack then stops what it runs, the
// coredumpctl dump and the pack script, and removes the temporary core,
// the temporary directory and the archive begun, which a signal of its
// default handling left. After the first signal the default handling is
// back, so a second one ends tt at once, as for tt package.
func (p *packCmd) signalContext(parent context.Context) (context.Context, func()) {
	ctx, cancel := context.WithCancelCause(parent)
	signals := make(chan os.Signal, 1)
	released := make(chan struct{})

	p.notify(signals, interruptSignals...)

	go func() {
		select {
		case received := <-signals:
			p.stopNotify(signals)

			number, _ := received.(syscall.Signal)
			cancel(interruption{signal: number})
		case <-released:
		}
	}()

	return ctx, func() {
		close(released)
		p.stopNotify(signals)
		cancel(nil)
	}
}

// interruptedError returns err, of a pack of ctx, or, of a pack a signal
// interrupted, the interruption, which tt exits with 128 and the number of
// the signal for (signalExitBase): what pack was making is removed by then.
func interruptedError(ctx context.Context, err error) error {
	cause, interrupted := errors.AsType[interruption](context.Cause(ctx))
	if err == nil || !interrupted {
		return err
	}

	return sdk.WithCode(signalExitBase+int(cause.signal),
		fmt.Errorf("%w: what it was making is removed", cause))
}

// run packs the core at corePath, or the core on the standard input with
// corePath empty or "-".
func (p *packCmd) run(ctx context.Context, corePath string) error {
	streams := p.services.Streams().IO()
	opts := coredump.PackOptions{
		Core: corePath, Executable: p.executable, Tarantools: nil, Dir: p.directory, PID: p.pid,
		Time: p.time, Logs: nil,
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

	return p.pack(ctx, opts)
}

// pack packs the core of opts, with the tarantool pack finds for it when
// opts name none, and says where the archive is.
func (p *packCmd) pack(ctx context.Context, opts coredump.PackOptions) error {
	if opts.Executable == "" {
		opts.Tarantools = p.tarantools
	}

	archive, taken, err := coredump.Pack(ctx, p.services.Streams().IO(), p.services.Log(),
		opts)
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

// errNotOneInstance reports an instance target of pack that selects other
// than one instance.
var errNotOneInstance = errors.New("pack takes the core of one instance")

// runInstance packs the core of the last crash of the instance target that
// dumped one (Locator.LastCore), the archive named by the PID and the time
// of the crash, unless --time names another. It says which crash it packs
// and where the core came from; one coredumpctl dumps goes into a file of
// --dir it removes after. The core of a crash the pipe of core_pattern
// packed already is not packed again: pack says where its archive is.
func (p *packCmd) runInstance(ctx context.Context, target string) error {
	log := p.services.Log()

	instances, err := p.services.LocalInstances(ctx, []string{target}, p.lookup)
	if err != nil {
		return err
	}

	if len(instances) != 1 {
		return sdk.WithUsage(fmt.Errorf("%s selects %d instances: %w, <APP>:<INSTANCE>", target,
			len(instances), errNotOneInstance))
	}

	inst := instances[0]
	loc := coredump.NewLocator()

	found, err := loc.LastCore(ctx, inst)
	if errors.Is(err, coredump.ErrPackedByPipe) {
		log.Info(err.Error())
		log.Info("The archive of the pipe has no logs of the instance: they are " +
			strings.Join(logPaths(inst), " and ") + ".")

		return nil
	}

	if err != nil {
		return fmt.Errorf("%w; tt coredump list %s lists the crashes of the instance and where "+
			"their cores are", err, inst.Name)
	}

	if len(found.Later) > 0 {
		log.Warn(fmt.Sprintf("The last crash of %s, %s, dumped no core: the core packed is "+
			"of the last crash that dumped one.", inst.Name,
			coredump.DescribeCrash(found.Later[0])))
	}

	log.Info(fmt.Sprintf("Packing the core of the crash of %s, %s, from %s.", inst.Name,
		coredump.DescribeCrash(found.Crash), coredump.DescribeSource(found.Core)))

	core, remove, err := loc.Fetch(ctx, found, p.directory)
	if err != nil {
		return err
	}

	defer remove()

	if p.noLogs {
		log.Info("The logs of " + inst.Name + " are not packed: --no-logs.")
	}

	return p.pack(ctx, p.instancePackOptions(inst, found.Crash, core))
}

// instancePackOptions returns what pack packs of the core at core of crash
// of inst: the archive named by the PID and the time of the crash, unless
// --time names another, with the tails of the logs of inst, unless
// --no-logs leaves them out.
func (p *packCmd) instancePackOptions(inst sdk.LocalInstance, crash sdk.Crash, core string,
) coredump.PackOptions {
	opts := coredump.PackOptions{
		Core: core, Executable: p.executable, Tarantools: nil, Dir: p.directory,
		PID: uint(crash.PID), Time: p.time, Logs: instanceLogs(inst),
	}

	if p.noLogs {
		opts.Logs = nil
	}

	if opts.Time == "" {
		opts.Time = strconv.FormatInt(crash.At.Unix(), 10)
	}

	return opts
}

// The names of the tails of the logs of an instance in logs/ of the archive.
const (
	ttLogName        = "tt.log"
	tarantoolLogName = "tarantool.log"
)

// instanceLogs are the logs of inst whose tails pack puts into the archive:
// tt.log, which has the standard error of tarantool, where it logs by
// default, and the file it logs into of its own, if any. The config of the
// instance is not packed: it may hold passwords.
func instanceLogs(inst sdk.LocalInstance) []coredump.PackLog {
	var logs []coredump.PackLog

	if inst.Log != "" {
		logs = append(logs, coredump.PackLog{Name: ttLogName, Path: inst.Log})
	}

	if inst.TarantoolLog != "" && inst.TarantoolLog != inst.Log {
		logs = append(logs, coredump.PackLog{Name: tarantoolLogName, Path: inst.TarantoolLog})
	}

	return logs
}

// logPaths returns the paths of the logs of inst.
func logPaths(inst sdk.LocalInstance) []string {
	logs := instanceLogs(inst)
	paths := make([]string, 0, len(logs))

	for _, packed := range logs {
		paths = append(paths, packed.Path)
	}

	return paths
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
