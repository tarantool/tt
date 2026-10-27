package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/user"
	"path/filepath"
	"slices"
	"strings"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/process_utils"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/systemd"
)

const (
	// serviceCmdName is the name of the tt service group.
	serviceCmdName = "service"
	// systemUnitDir is where tt service systemd install puts the units
	// without --unit-dir: the directory of the units of the administrator,
	// which no package writes to.
	systemUnitDir = "/etc/systemd/system"
	// unitDirFlag is the flag of the directory the units go to.
	unitDirFlag = "unit-dir"
	// runAsFlag is the flag of the user and the group the units run the
	// instances as.
	runAsFlag = "run-as"
)

var (
	// errScopeNotSupported is wrapped by the error for an application of a
	// scope other than system: units are made for the system scope only
	// (decision 39).
	errScopeNotSupported = errors.New("systemd units are made for the system scope only")
	// errUnitsElsewhere is wrapped by the error for an install into a unit
	// directory other than the one the units of the application are in.
	errUnitsElsewhere = errors.New("the units are installed elsewhere")
	// errInstancesRunning is wrapped by the error for an install or an
	// uninstall that would leave running instances out of step with the
	// units.
	errInstancesRunning = errors.New("instances run")
)

var (
	// serviceScope is --scope of tt service systemd.
	serviceScope string
	// generateUnitDir is --unit-dir of tt service systemd generate: the
	// directory the units are written into instead of the standard output.
	generateUnitDir string
	// installUnitDir is --unit-dir of tt service systemd install.
	installUnitDir string
	// generateRunAs and installRunAs are --run-as of tt service systemd
	// generate and install.
	generateRunAs = newRunAsValue()
	installRunAs  = newRunAsValue()
)

// runAsValue is the value of --run-as, <user>[:<group>]
// (systemd.ParseRunAs), and whether the flag was given: install keeps the
// user and the group of the units installed before when it was not.
type runAsValue struct {
	runAs systemd.RunAs
	given bool
}

// newRunAsValue returns --run-as as it is without the flag: tarantool for
// the user and the group.
func newRunAsValue() *runAsValue {
	return &runAsValue{
		runAs: systemd.RunAs{User: systemd.DefaultRunAs, Group: systemd.DefaultRunAs},
		given: false,
	}
}

// String returns the user and the group, <user>:<group>.
func (value *runAsValue) String() string {
	return value.runAs.String()
}

// Set parses the flag.
func (value *runAsValue) Set(text string) error {
	runAs, err := systemd.ParseRunAs(text)
	if err != nil {
		return err
	}

	value.runAs = runAs
	value.given = true

	return nil
}

// Type names the value in the help.
func (*runAsValue) Type() string {
	return "user[:group]"
}

// addRunAsFlag declares --run-as of cmd, bound to value.
func addRunAsFlag(cmd *cobra.Command, value *runAsValue) {
	cmd.Flags().Var(value, runAsFlag, "the user and the group the units run the instances "+
		"as; the group is the user's name when left out")
}

// NewServiceCmd creates the tt service group: what ties the instances of
// applications to the service manager of the system.
func NewServiceCmd() *cobra.Command {
	serviceCmd := &cobra.Command{
		Use:   serviceCmdName,
		Short: "Integrate with the service manager of the system",
	}

	serviceCmd.AddCommand(newSystemdCmd())

	return serviceCmd
}

// newSystemdCmd creates tt service systemd.
func newSystemdCmd() *cobra.Command {
	systemdCmd := &cobra.Command{
		Use:   "systemd",
		Short: "Run the instances of an application under systemd units",
		Long: "Make the systemd units that run the instances of an application " +
			"installed in the system scope.\n\n" +
			"The template unit <APP>@.service runs one instance, " +
			"tt instance start --foreground <APP>:%i, with TT_SYSTEM_ROOT set to the " +
			"root of the system scope, as the user and the group --" + runAsFlag +
			" names, " + systemd.DefaultRunAs + " by default. " +
			"tt does not restart the instance: systemd does, as Restart= says. " +
			"The drop-in <APP>@<INSTANCE>.service.d/" + systemd.DropInName + " of " +
			"every instance sets Restart= from the restart field of its record and " +
			"TimeoutStopSec= from its stop_timeout, with a margin for tt, which " +
			"stops the instance itself. ExecStart= runs <root>/bin/tt, or the tt that " +
			"makes the units when the system scope has none.\n\n" +
			"install records the instances in <root>/etc/<APP>/" + systemd.MarkFileName +
			": tt instance start, stop, restart and kill refuse them and point at " +
			"systemctl. tt does not run systemctl itself.\n\n" +
			"<APP> names an application as the instance commands name it, and the " +
			"units are made for the system scope only: the user scope and projects " +
			"are refused; inside a project of the same name, --scope system picks the " +
			"installed application.",
	}

	systemdCmd.AddCommand(
		newSystemdGenerateCmd(),
		newSystemdInstallCmd(),
		newSystemdUninstallCmd(),
	)

	return systemdCmd
}

// newSystemdGenerateCmd creates tt service systemd generate.
func newSystemdGenerateCmd() *cobra.Command {
	generateCmd := &cobra.Command{
		Use:   "generate <APP>",
		Short: "Print the systemd units of an application",
		Long: "Print the systemd units of the application: the template unit and the " +
			"drop-in of every instance, each after a line ==> <file> <==. With --" +
			unitDirFlag + " the units are written into the directory instead, which " +
			"is created if need be. Nothing is installed and nothing is recorded: " +
			"tt instance commands do not leave the instances to the units.",
		Args:              cobra.ExactArgs(1),
		RunE:              internalRunE(internalSystemdGenerateModule),
		ValidArgsFunction: completeSystemdApp,
	}

	addServiceScopeFlag(generateCmd)
	generateCmd.Flags().StringVar(&generateUnitDir, unitDirFlag, "",
		"write the units into this directory instead of printing them")
	addRunAsFlag(generateCmd, generateRunAs)

	return generateCmd
}

// newSystemdInstallCmd creates tt service systemd install.
func newSystemdInstallCmd() *cobra.Command {
	installCmd := &cobra.Command{
		Use:   "install <APP>",
		Short: "Install the systemd units of an application",
		Long: "Write the systemd units of the application into the unit directory, " +
			systemUnitDir + " unless --" + unitDirFlag + " names another, and record " +
			"in <root>/etc/<APP>/" + systemd.MarkFileName + " that the units manage its " +
			"instances. Run it again after the instances of the application change: it " +
			"rewrites the units and removes the drop-ins of the instances that are gone. " +
			"A unit file of the same name that tt did not write is an error, and so is " +
			"an instance that runs without a unit: stop it with tt instance stop first.\n\n" +
			"--" + runAsFlag + " is recorded with the units: without it, an install " +
			"over installed units keeps the user and the group they run as.\n\n" +
			"systemctl is not run: the command prints what to run.",
		Args:              cobra.ExactArgs(1),
		RunE:              internalRunE(internalSystemdInstallModule),
		ValidArgsFunction: completeSystemdApp,
	}

	addServiceScopeFlag(installCmd)
	installCmd.Flags().StringVar(&installUnitDir, unitDirFlag, systemUnitDir,
		"the directory the units are written into")
	addRunAsFlag(installCmd, installRunAs)

	return installCmd
}

// newSystemdUninstallCmd creates tt service systemd uninstall.
func newSystemdUninstallCmd() *cobra.Command {
	uninstallCmd := &cobra.Command{
		Use:   "uninstall <APP>",
		Short: "Remove the systemd units of an application",
		Long: "Remove the systemd units tt service systemd install wrote for the " +
			"application, from the directory it wrote them into, and the record in " +
			"<root>/etc/<APP>/" + systemd.MarkFileName + ". The application itself is " +
			"not needed. Units that are not installed are no failure: there is nothing " +
			"to remove. An instance that runs under its unit is an error: stop the " +
			"units first, with systemctl disable --now. A file tt did not write is left " +
			"in place.",
		Args:              cobra.ExactArgs(1),
		RunE:              internalRunE(internalSystemdUninstallModule),
		ValidArgsFunction: completeSystemdApp,
	}

	addServiceScopeFlag(uninstallCmd)

	return uninstallCmd
}

// addServiceScopeFlag declares --scope of tt service systemd, which takes
// system only, and is there so that a target names the scope it names in
// the instance commands.
func addServiceScopeFlag(cmd *cobra.Command) {
	cmd.Flags().StringVar(&serviceScope, running.ScopeFlag, "",
		"scope the application is looked for in: only system is supported")

	_ = cmd.RegisterFlagCompletionFunc(running.ScopeFlag, cobra.FixedCompletions(
		[]string{app.ScopeSystem.String()}, cobra.ShellCompDirectiveNoFileComp))
}

// completeSystemdApp completes the application of tt service systemd: the
// applications installed in the system scope.
func completeSystemdApp(
	_ *cobra.Command, args []string, _ string,
) ([]string, cobra.ShellCompDirective) {
	if len(args) > 0 {
		return nil, cobra.ShellCompDirectiveNoFileComp
	}

	return app.Names(app.Options{
		Scope:            app.ScopeSystem,
		Template:         "",
		Start:            "",
		Dir:              "",
		Integrity:        cmdCtx.Integrity,
		BestEffortConfig: true,
	}), cobra.ShellCompDirectiveNoFileComp
}

// systemdScope finds the scope of the application name for tt service
// systemd as the instance commands find it (app.ScopeOf), and returns the
// options to load it with and the root of the scope. A scope other than
// system is an error: units are made for it only (decision 39).
func systemdScope(cmdCtx *cmdcontext.CmdCtx, name string) (app.Options, string, error) {
	opts, err := instanceOptions(cmdCtx)
	if err != nil {
		return app.Options{}, "", err
	}

	opts.Scope, err = app.ParseScope(serviceScope)
	if err != nil {
		return app.Options{}, "", err
	}

	scp, root, err := app.ScopeOf(name, opts)
	if err != nil {
		return app.Options{}, "", err
	}

	switch {
	case scp == app.ScopeSystem:
	case opts.Scope != "":
		return app.Options{}, "", fmt.Errorf("%w: --scope %s is not supported",
			errScopeNotSupported, opts.Scope)
	default:
		return app.Options{}, "", fmt.Errorf("%w: %q is the project in %s, which is not "+
			"supported; pass --scope system for the application %q of the system scope",
			errScopeNotSupported, name, filepath.Dir(root), name)
	}

	opts.Scope = app.ScopeSystem

	return opts, root, nil
}

// loadSystemdApp loads the application name of the system scope for
// tt service systemd generate and install, with the contexts of its
// instances.
//
// An application the system scope does not have is an error that points at
// tt package install: the hint of the resolver to look in another scope
// does not apply, as units are made for the system scope only.
func loadSystemdApp(cmdCtx *cmdcontext.CmdCtx, name string) (
	*app.App, []running.InstanceCtx, error,
) {
	opts, root, err := systemdScope(cmdCtx, name)
	if err != nil {
		return nil, nil, err
	}

	application, err := app.Load(context.Background(), name, opts)
	if errors.Is(err, app.ErrAppNotFound) {
		return nil, nil, fmt.Errorf("%w: %q is not installed in the system scope in %s; "+
			"install it with tt package install --scope system <ARCHIVE>", app.ErrAppNotFound,
			name, root)
	}

	if err != nil {
		return nil, nil, err
	}

	insts, err := running.InstancesOf(application, application.Instances)
	if err != nil {
		return nil, nil, err
	}

	return application, insts, nil
}

// unitsOf returns what the units of application, with the contexts insts of
// its instances, run as runAs, are made of.
//
// TimeoutStopSec= of an instance is its stop timeout, after which its
// watchdog kills it, and the margin tt instance stop allows the watchdog over
// it: systemd must not kill the instance before tt does.
func unitsOf(application *app.App, insts []running.InstanceCtx, runAs systemd.RunAs) (
	systemd.Units, error,
) {
	ttPath, err := unitTT(application.Root)
	if err != nil {
		return systemd.Units{}, err
	}

	units := systemd.Units{
		App:         application.Name,
		RunAs:       runAs,
		Root:        application.Root,
		TT:          ttPath,
		StopTimeout: running.DefaultStopTimeout + running.StopMargin,
		Instances:   make([]systemd.Instance, 0, len(insts)),
	}

	for i := range insts {
		units.Instances = append(units.Instances, systemd.Instance{
			Name:        insts[i].InstName,
			Restart:     insts[i].Restart,
			StopTimeout: running.StopTimeoutOf(&insts[i]) + running.StopMargin,
		})
	}

	return units, nil
}

// unitTT returns the tt the units of an application of the system scope
// under root run: <root>/bin/tt, which tt binary install tt --scope system
// installs and tt binary switch switches, so that the units run the tt of
// the scope; or else the tt that runs the command, with a warning, as a host
// may have tt from a package rather than in the scope.
func unitTT(root string) (string, error) {
	scoped := filepath.Join(root, binary.BinDirName, binary.ProgramTt.Exec())

	info, err := os.Stat(scoped)
	if err == nil && info.Mode().IsRegular() {
		return scoped, nil
	}

	self, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("failed to get the tt executable path: %w", err)
	}

	log.Warnf("There is no %s: the units run %s, the tt that makes them. Install tt into "+
		"the system scope with tt binary install tt --scope system for the units to run "+
		"the tt of the scope.", scoped, self)

	return self, nil
}

// internalSystemdGenerateModule is tt service systemd generate.
func internalSystemdGenerateModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	application, insts, err := loadSystemdApp(cmdCtx, args[0])
	if err != nil {
		return err
	}

	units, err := unitsOf(application, insts, generateRunAs.runAs)
	if err != nil {
		return err
	}

	files, err := systemd.Generate(units)
	if err != nil {
		return err
	}

	if generateUnitDir == "" {
		return printUnits(os.Stdout, files)
	}

	err = systemd.WriteFiles(generateUnitDir, files)
	if err != nil {
		return err
	}

	log.Infof("Wrote the systemd units of %s into %s.", application.Name, generateUnitDir)

	return nil
}

// printUnits writes files to out, each after a line naming it, as tail
// names the files it prints.
func printUnits(out io.Writer, files []systemd.File) error {
	for i, file := range files {
		separator := ""
		if i > 0 {
			separator = "\n"
		}

		_, err := fmt.Fprintf(out, "%s==> %s <==\n%s", separator, file.Name, file.Content)
		if err != nil {
			return fmt.Errorf("cannot print the units: %w", err)
		}
	}

	return nil
}

// internalSystemdInstallModule is tt service systemd install.
//
// The instances the units are to manage that run without a unit are an
// error: once they are recorded, tt instance stop would refuse them, and
// their units would fail to start next to them. An instance the units
// managed before may run: it runs under its unit.
//
// The units run as --run-as says, and without the flag as the units
// installed before ran, as their mark records: an install that only follows
// a changed description must not move the instances to another user.
func internalSystemdInstallModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	application, insts, err := loadSystemdApp(cmdCtx, args[0])
	if err != nil {
		return err
	}

	unitDir, err := filepath.Abs(installUnitDir)
	if err != nil {
		return fmt.Errorf("cannot resolve the unit directory %q: %w", installUnitDir, err)
	}

	hostDir := app.HostDir(application.Root, application.Name)

	previous, found, err := systemd.ReadMark(hostDir)
	if err != nil {
		return err
	}

	if found && filepath.Clean(previous.UnitDir) != unitDir {
		return fmt.Errorf("%w: the units of %s are in %s; uninstall them with "+
			"tt service systemd uninstall %s first", errUnitsElsewhere, application.Name,
			previous.UnitDir, application.Name)
	}

	err = refuseRunningWithoutUnits(insts, previous)
	if err != nil {
		return err
	}

	runAs := installRunAs.runAs
	if !installRunAs.given && previous.RunAs != "" {
		// ReadMark has checked it.
		runAs, err = systemd.ParseRunAs(previous.RunAs)
		if err != nil {
			return fmt.Errorf("the units of %s: %w", application.Name, err)
		}
	}

	units, err := unitsOf(application, insts, runAs)
	if err != nil {
		return err
	}

	files, err := systemd.Generate(units)
	if err != nil {
		return err
	}

	mark := systemd.Mark{
		UnitDir:   unitDir,
		Template:  systemd.TemplateName(application.Name),
		RunAs:     runAs.String(),
		Instances: make(map[string]string, len(insts)),
	}

	unitNames := make([]string, 0, len(insts))

	for _, inst := range insts {
		unit := systemd.UnitName(application.Name, inst.InstName)

		mark.Instances[inst.InstName] = unit
		unitNames = append(unitNames, unit)
	}

	var gone []string

	for name, unit := range previous.Instances {
		if _, kept := mark.Instances[name]; !kept {
			gone = append(gone, unit)
		}
	}

	slices.Sort(gone)

	stale := make([]string, 0, len(gone))
	for _, unit := range gone {
		stale = append(stale, systemd.DropInPath(unit))
	}

	err = systemd.Install(systemd.Installation{
		UnitDir: unitDir,
		HostDir: hostDir,
		Files:   files,
		Mark:    mark,
		Stale:   stale,
	})
	if err != nil {
		return err
	}

	log.Infof("Installed the systemd units of %s into %s, run as %s; tt instance start, "+
		"stop, restart and kill leave its instances to systemctl.", application.Name, unitDir,
		runAs)

	warnNoRunAs(runAs)

	if len(gone) > 0 {
		log.Warnf("Removed the drop-ins of the units of instances gone from %s: %s. Stop "+
			"them with systemctl disable --now %s", application.Pair.DescriptionFile(),
			strings.Join(gone, ", "), strings.Join(gone, " "))
	}

	_, err = fmt.Fprintf(os.Stdout, "To start the instances under the units, run:\n"+
		"  systemctl daemon-reload\n  systemctl enable --now %s\n", strings.Join(unitNames, " "))
	if err != nil {
		return fmt.Errorf("cannot print the next steps: %w", err)
	}

	return nil
}

// refuseRunningWithoutUnits fails for the instances of insts that run while
// the mark previous does not record them: they run without a unit.
func refuseRunningWithoutUnits(insts []running.InstanceCtx, previous systemd.Mark) error {
	var runningNames []string

	for idx := range insts {
		inst := &insts[idx]
		if previous.Instances[inst.InstName] != "" {
			continue
		}

		if running.Status(inst).Code == process_utils.ProcessRunningCode {
			runningNames = append(runningNames, running.GetAppInstanceName(*inst))
		}
	}

	if len(runningNames) == 0 {
		return nil
	}

	return fmt.Errorf("%w without systemd units: %s; stop them with tt instance stop %s "+
		"before their units manage them", errInstancesRunning, strings.Join(runningNames, ", "),
		strings.Join(runningNames, ","))
}

// warnNoRunAs warns when the host has no user or no group the units run the
// instances as, runAs: systemd would fail to start them. A name of digits
// alone is a numeric id, as systemd takes it.
func warnNoRunAs(runAs systemd.RunAs) {
	numeric := func(name string) bool { return strings.Trim(name, "0123456789") == "" }

	var err error

	if numeric(runAs.User) {
		_, err = user.LookupId(runAs.User)
	} else {
		_, err = user.Lookup(runAs.User)
	}

	if err != nil {
		log.Warnf("The units run the instances as the user %s, which this host does not "+
			"have: create it, or pass another to --%s.", runAs.User, runAsFlag)
	}

	if numeric(runAs.Group) {
		_, err = user.LookupGroupId(runAs.Group)
	} else {
		_, err = user.LookupGroup(runAs.Group)
	}

	if err != nil {
		log.Warnf("The units run the instances as the group %s, which this host does not "+
			"have: create it, or pass another to --%s.", runAs.Group, runAsFlag)
	}
}

// internalSystemdUninstallModule is tt service systemd uninstall. It works
// from the mark of the units alone, so that it removes the units of an
// application that has been uninstalled or whose pair is broken.
//
// Units that are not installed, with no mark, are no failure: there is
// nothing to remove, and a deployment that runs uninstall again must not
// fail on it. A mark that cannot be read still is: it may name units.
//
// An instance that runs under its unit is an error: with the units gone,
// systemd would lose track of it, and the instance commands would act on it
// behind the back of the unit.
func internalSystemdUninstallModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	name := args[0]

	_, root, err := systemdScope(cmdCtx, name)
	if err != nil {
		return err
	}

	hostDir := app.HostDir(root, name)

	mark, found, err := systemd.ReadMark(hostDir)
	if err != nil {
		return err
	}

	if !found {
		log.Infof("The systemd units of %s are not installed: there is no %s.", name,
			systemd.MarkPath(hostDir))

		return nil
	}

	var runningUnits []string

	for inst, unit := range mark.Instances {
		pidFile, err := running.PIDFile(root, name, inst)
		if err != nil {
			return err
		}

		if process_utils.ProcessStatus(pidFile).Code == process_utils.ProcessRunningCode {
			runningUnits = append(runningUnits, unit)
		}
	}

	if len(runningUnits) > 0 {
		slices.Sort(runningUnits)

		return fmt.Errorf("%w under the systemd units of %s: %s; stop them with "+
			"systemctl disable --now %s first", errInstancesRunning, name,
			strings.Join(runningUnits, ", "), strings.Join(runningUnits, " "))
	}

	kept, err := systemd.Uninstall(hostDir, mark)
	if err != nil {
		return err
	}

	for _, path := range kept {
		log.Warnf("Left %s in place: tt service systemd did not write it.", path)
	}

	log.Infof("Removed the systemd units of %s from %s.", name, mark.UnitDir)

	_, err = fmt.Fprintf(os.Stdout, "To make systemd forget the units, run:\n"+
		"  systemctl daemon-reload\n")
	if err != nil {
		return fmt.Errorf("cannot print the next steps: %w", err)
	}

	return nil
}
