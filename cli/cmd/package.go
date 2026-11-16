package cmd

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/integrity"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/output"
	"github.com/tarantool/tt/sdk/scope"

	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/buildreg"
	"github.com/tarantool/tt/v3/cli/manifest/deps"
	"github.com/tarantool/tt/v3/cli/manifest/install"
	"github.com/tarantool/tt/v3/cli/manifest/inventory"
	"github.com/tarantool/tt/v3/cli/manifest/pack"
	"github.com/tarantool/tt/v3/cli/manifest/registry"
	"github.com/tarantool/tt/v3/cli/manifest/rockindex"
	manifestrocks "github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/state"
	oldrocks "github.com/tarantool/tt/v3/cli/rocks"
	"github.com/tarantool/tt/v3/cli/util"
	switcher "github.com/tarantool/tt/v3/internal/binary/switch"
	"github.com/tarantool/tt/v3/internal/integrityprovider"
	"github.com/tarantool/tt/v3/internal/systemd"
)

// ErrNoTarantool reports that no tarantool executable was found, to the
// commands that cannot go without one: the re-resolving dependency commands,
// tt package search and download, and tt test. tt package build, fetch and
// pack go without (buildTarantoolInfo).
var ErrNoTarantool = errors.New("tarantool executable not found in PATH")

// Shared flags for the package subcommands.
var (
	packageProduct string
	packageLocked  bool
	// packageRebuild builds the incremental components whatever they recorded.
	packageRebuild bool
	// packageJobs is -j, --jobs: the most jobs each component build runs at
	// once, 0 leaving it to the backends (build.Options.Jobs).
	packageJobs        int
	packageWithoutDeps bool
	// packageIntegrityKey is --with-integrity-check of tt package pack: the
	// private key that signs the archive. The flag exists only when the
	// integrity provider of the distribution signs.
	packageIntegrityKey string
	// packageFormats, packageOut and packageRevision are --format, --out and
	// --revision of tt package pack: what it makes, where, and the revision
	// of the rock.
	packageFormats  []string
	packageOut      string
	packageRevision int
	// packageDebugInfo is --debuginfo of tt package pack: keep or strip the
	// debug information of the native files the package's builds made.
	packageDebugInfo string
)

// Flags shared by the scope-addressed subcommands: install, list, switch and
// uninstall.
var (
	packageScope   string
	packageUpgrade bool
	packageForce   bool
	packageYes     bool
)

// packageTree is --tree of `tt package list`.
var packageTree bool

// Flags specific to `tt package add`.
var packageDev bool

// packageRemoveDev is --dev of `tt package remove`.
var packageRemoveDev bool

// packageRegistries collects --registry, the highest-precedence rock-server
// list: it replaces TT_REGISTRIES, [platform].registries and the defaults.
var packageRegistries []string

// packageDownloadDir is --dir of `tt package download`.
var packageDownloadDir string

// packageIndexFile and packageIndexBaseURL are --index and --base-url of
// `tt package index`: the index to write and where the files it lists are
// downloaded from.
var (
	packageIndexFile    string
	packageIndexBaseURL string
)

const packageAddMaxArgs = 2

// packageSwitchMaxArgs is NAME and VERSION of `tt package switch`.
const packageSwitchMaxArgs = 2

// errNoSwitchVersion reports `tt package switch NAME` without a terminal to
// choose the version on.
var errNoSwitchVersion = errors.New("no version to switch to")

// manifestFileName is the package manifest the directory-scoped registry
// commands read [platform].registries from.
const manifestFileName = "app.manifest.toml"

// NewPackageCmd creates the `tt package` command group: the manifest build
// pipeline (build, fetch, pack and clean), the dependency-changing commands (add,
// remove and update), installation, and the inventory over what is installed
// (list and uninstall).
func NewPackageCmd() *cobra.Command {
	packageCmd := &cobra.Command{
		Use:   "package",
		Short: "Manage tt manifest packages",
		Long: "Build, fetch, pack and install Tarantool packages described by " +
			"app.manifest.toml, manage their dependencies, and manage what is " +
			"installed. The commands that change a project hold its lock, " +
			"_build/tt.lock, and wait while another tt holds it; a tt that a " +
			"hook or a build runs works under the lock of the tt that runs it. " +
			"What the exit codes mean is in tt help errors.",
	}

	packageCmd.AddCommand(
		newPackageBuildCmd(),
		newPackageFetchCmd(),
		newPackagePackCmd(),
		newPackageCleanCmd(),
		newPackageAddCmd(),
		newPackageRemoveCmd(),
		newPackageUpdateCmd(),
		newPackageResolveCmd(),
		newPackageDepsCmd(),
		newPackageInstallCmd(),
		newPackageListCmd(),
		newPackageSwitchCmd(),
		newPackageUninstallCmd(),
		newPackageSearchCmd(),
		newPackageDownloadCmd(),
		newPackageIndexCmd(),
	)

	addPersistentProjectDirFlag(packageCmd)

	return packageCmd
}

// newPackageAddCmd wires `tt package add NAME [CONSTRAINT]`.
func newPackageAddCmd() *cobra.Command {
	addCmd := &cobra.Command{
		Use:   "add NAME [CONSTRAINT]",
		Short: "Declare a dependency and re-resolve the lock",
		Long: "Add NAME to [dependencies] — or, with --dev, to " +
			"[dev_dependencies] — at the given version constraint, then " +
			"re-resolve app.manifest.lock around it. Without a constraint a new " +
			"dependency is declared as '*': whatever the registry serves. A name " +
			"the table already declares keeps its declaration when no constraint " +
			"is given, and the lock is resolved only if it is missing or stale; " +
			"with a constraint, the constraint is rewritten in place, and " +
			"the rest of the declaration, and every comment and blank line in the " +
			"file, is left exactly as it was. Every version the lock already " +
			"holds is kept: only the new dependency and whatever it requires can " +
			"move, because an edit to one dependency is not a request to upgrade " +
			"the others — that is what tt package update is for. The manifest " +
			"and the lock are written only once the resolution succeeded: when " +
			"it fails — an unknown rock, a constraint no version satisfies, a " +
			"rock server that does not answer — both are left exactly as they " +
			"were. Nothing is fetched into .rocks/; the next tt package build " +
			"picks the new lock up.",
		Args: cobra.RangeArgs(1, packageAddMaxArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			constraint := ""
			if len(args) == packageAddMaxArgs {
				constraint = args[1]
			}

			return commandError(cmd, runPackageAdd(args[0], constraint))
		},
	}

	addCmd.Flags().BoolVar(&packageDev, "dev", false,
		"declare the dependency in [dev_dependencies] instead of [dependencies]")
	addRegistryFlag(addCmd)

	return addCmd
}

// runPackageAdd declares one dependency and reports what moved.
func runPackageAdd(name, constraint string) error {
	ctx, stop := packageContext()
	defer stop()

	opts, err := dependencyOptions()
	if err != nil {
		return err
	}

	result, err := deps.Declare(ctx, opts, name, constraint, packageDev)
	if err != nil {
		return err
	}

	table := "dependencies"
	if packageDev {
		table = "dev_dependencies"
	}

	if result.Kept {
		printKept(name, table, result.Change.Previous)
		printResolution(result, "the lock is up to date")

		return nil
	}

	declared := result.Manifest.Dependencies[name].Version
	if packageDev {
		declared = result.Manifest.DevDependencies[name].Version
	}

	if result.Change.Existed {
		_, _ = fmt.Fprintf(os.Stdout, "changed %s in [%s]: %s -> %s\n",
			name, table, result.Change.Previous, declared)
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "added %s %s to [%s]\n", name, declared, table)
	}

	printMoves(result.Moves)

	return nil
}

// printKept reports a declaration tt package add kept, given no constraint
// for a name the table declares already.
func printKept(name, table, constraint string) {
	if constraint == "" {
		_, _ = fmt.Fprintf(os.Stdout,
			"%s is already declared in [%s]; the declaration is left as it is\n", name, table)

		return
	}

	_, _ = fmt.Fprintf(os.Stdout,
		"%s is already declared in [%s] as %s; the declaration is left as it is"+
			" (give a constraint to change it)\n", name, table, constraint)
}

// newPackageRemoveCmd wires `tt package remove NAME`.
func newPackageRemoveCmd() *cobra.Command {
	removeCmd := &cobra.Command{
		Use:   "remove NAME",
		Short: "Drop a declared dependency and re-resolve the lock",
		Long: "Remove NAME from [dependencies] — or, with --dev, from " +
			"[dev_dependencies] — then " +
			"re-resolve app.manifest.lock without it. A declaration of NAME in " +
			"the other table is left as it is, and a name only the other table " +
			"declares is an error that says so. The rocks that were only " +
			"there to satisfy it drop out of the closure with it; every other " +
			"version is held. A name the manifest does not declare is an error " +
			"rather than a silent success — it is almost always a typo — and so " +
			"is a name only a component declares, since which component owns a " +
			"dependency is not this command's decision to unmake. As with tt " +
			"package add, a resolution that fails leaves the manifest and the " +
			"lock exactly as they were. Nothing is deleted from .rocks/; tt " +
			"package uninstall is what removes files.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runPackageRemove(args[0]))
		},
	}

	removeCmd.Flags().BoolVar(&packageRemoveDev, "dev", false,
		"remove the dependency from [dev_dependencies] instead of [dependencies]")
	addRegistryFlag(removeCmd)

	return removeCmd
}

// runPackageRemove drops one dependency and reports what left the closure.
func runPackageRemove(name string) error {
	ctx, stop := packageContext()
	defer stop()

	opts, err := dependencyOptions()
	if err != nil {
		return err
	}

	result, err := deps.RemoveFrom(ctx, opts, name, packageRemoveDev)
	if err != nil {
		return err
	}

	table := "dependencies"
	if packageRemoveDev {
		table = "dev_dependencies"
	}

	declared := name
	if result.Change.Previous != "" {
		declared += " " + result.Change.Previous
	}

	_, _ = fmt.Fprintf(os.Stdout, "removed %s from [%s]\n", declared, table)

	printMoves(result.Moves)

	return nil
}

// newPackageUpdateCmd wires `tt package update [NAME]`.
func newPackageUpdateCmd() *cobra.Command {
	updateCmd := &cobra.Command{
		Use:   "update [NAME]",
		Short: "Re-resolve the lock against newer registry versions",
		Long: "Re-resolve app.manifest.lock, taking the newest version every " +
			"constraint allows. This is the only command that pulls newer " +
			"registry versions: a release upstream never makes a lock stale by " +
			"itself, so a build keeps the versions it has until an update is " +
			"asked for. With NAME only that dependency is freed and every other " +
			"version is held, so the diff stays confined to it and whatever its " +
			"new version requires. No declaration is changed — app.manifest.toml " +
			"is not touched at all — and nothing is fetched into .rocks/.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			if len(args) == 1 {
				name = args[0]
			}

			return commandError(cmd, runPackageUpdate(name))
		},
	}

	addRegistryFlag(updateCmd)

	return updateCmd
}

// runPackageUpdate re-resolves the lock and reports what moved.
func runPackageUpdate(name string) error {
	ctx, stop := packageContext()
	defer stop()

	opts, err := dependencyOptions()
	if err != nil {
		return err
	}

	result, err := deps.Update(ctx, opts, name)
	if err != nil {
		return err
	}

	printResolution(result, "everything is already up to date")

	return nil
}

// newPackageResolveCmd wires `tt package resolve`.
func newPackageResolveCmd() *cobra.Command {
	resolveCmd := &cobra.Command{
		Use:   "resolve",
		Short: "Rewrite the lock from the manifest without building",
		Long: "Re-resolve app.manifest.lock from app.manifest.toml, so a manifest " +
			"edited by hand gets its lock back in step without waiting for a " +
			"build. Nothing is fetched into .rocks/ and no component build " +
			"backend runs. Every version the lock already holds is kept: a hand " +
			"edit to one dependency is not a request to upgrade the others, and " +
			"pulling newer registry versions is what tt package update is for.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runPackageResolve())
		},
	}

	addRegistryFlag(resolveCmd)

	return resolveCmd
}

// runPackageResolve rewrites the lock and reports what moved.
func runPackageResolve() error {
	ctx, stop := packageContext()
	defer stop()

	opts, err := dependencyOptions()
	if err != nil {
		return err
	}

	result, err := deps.Resolve(ctx, opts)
	if err != nil {
		return err
	}

	printResolution(result, "the lock is up to date")

	return nil
}

// newPackageDepsCmd wires `tt package deps`.
func newPackageDepsCmd() *cobra.Command {
	depsCmd := &cobra.Command{
		Use:   "deps",
		Short: "Show the dependencies the manifest declares, at their locked versions",
		Long: "Print the runtime dependencies of every product and the dev " +
			"dependencies, each with the version app.manifest.lock resolved it " +
			"to. This is what the current manifest declares — not what is " +
			"installed on disk, which is what tt package list reports. Nothing " +
			"is resolved and no registry is contacted: the answer comes from the " +
			"manifest and the lock, so a lock that no longer matches the manifest " +
			"is reported as stale rather than silently re-resolved.",
		Args: cobra.NoArgs,
	}

	format := bindFormatFlag(depsCmd)

	depsCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return commandError(cmd, runPackageDeps(format))
	}

	return depsCmd
}

// runPackageDeps reads the manifest and the lock and renders the report.
//
// Unlike the commands that re-resolve, this one needs no Tarantool: nothing
// evaluates a rockspec here, so a project's dependencies can be listed on a
// machine that has no tarantool installed at all.
func runPackageDeps(format *output.FormatFlag) error {
	projectDir, err := packageRootDir()
	if err != nil {
		return err
	}

	// Nothing is resolved, so neither Tarantool nor registries are needed.
	report, err := deps.Deps(deps.Options{
		ProjectDir: projectDir,
		TtVersion:  "",
		Tarantool: manifestrocks.TarantoolInfo{
			Executable: "", Prefix: "", Version: "", MissingHeaders: "",
		},
		Registries: manifestrocks.Sources{
			Flag:       nil,
			Env:        nil,
			Manifest:   nil,
			WorkingDir: "",
			ProjectDir: "",
		},
		Logger:   nil,
		Warn:     func(msg string) { log.Warn(msg) },
		Backends: buildreg.Process(),
	})
	if err != nil {
		return err
	}

	return emitResult(format, report)
}

// dependencyOptions assembles the options the re-resolving dependency commands
// share, with the servers of their --registry.
func dependencyOptions() (deps.Options, error) {
	return dependencyOptionsFor(packageRegistries)
}

// dependencyOptionsFor assembles the options the re-resolving dependency
// commands share, with registries as the --registry layer of the rock
// servers.
//
// All of them re-resolve, and resolution evaluates rockspecs — which are Lua
// and may branch on the Tarantool version — so a missing tarantool is a real
// error here rather than something to work around.
func dependencyOptionsFor(registries []string) (deps.Options, error) {
	projectDir, err := packageRootDir()
	if err != nil {
		return deps.Options{}, err
	}

	return dependencyOptionsIn(projectDir, registries)
}

// dependencyOptionsIn is dependencyOptionsFor for the project in projectDir,
// as a module's project gives it: the directory it works in, with nothing
// searched for.
func dependencyOptionsIn(projectDir string, registries []string) (deps.Options, error) {
	tntInfo, err := tarantoolInfo()
	if err != nil {
		return deps.Options{}, err
	}

	return dependencyOptionsWith(projectDir, registries, tntInfo)
}

// dependencyOptionsWith is dependencyOptionsIn with the Tarantool facts
// tntInfo, for a resolution that goes without a Tarantool as a build does.
func dependencyOptionsWith(
	projectDir string, registries []string, tntInfo manifestrocks.TarantoolInfo,
) (deps.Options, error) {
	sources, err := registrySources(registries)
	if err != nil {
		return deps.Options{}, err
	}

	return deps.Options{
		ProjectDir: projectDir,
		TtVersion:  lockStamp(),
		Tarantool:  tntInfo,
		Registries: sources,
		Logger:     luarocksLogger(),
		Warn:       func(msg string) { log.Warn(msg) },
		Backends:   buildreg.Process(),
	}, nil
}

// printResolution reports a run that resolved the lock again without editing
// a declaration: the moves, then what else the run rewrote in the lock, one
// line each. unchanged is printed only for a lock the run left byte for byte
// as it was; a run that moved no version and still rewrote the lock - a
// manifest hash refreshed, a path dependency re-pinned - says so.
func printResolution(result *deps.Result, unchanged string) {
	if len(result.Moves) == 0 && len(result.Rewrites) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, unchanged)

		return
	}

	if len(result.Moves) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "no version moved, but the lock changed:")
	}

	printMoves(result.Moves)

	for _, note := range result.Rewrites {
		_, _ = fmt.Fprintf(os.Stdout, "  %s\n", note)
	}
}

// printMoves reports the closure changes one per line, as the SDK renders
// them for the modules too (sdk.DependencyMove.String): a rock that arrived
// shows only its new version, one that dropped out is marked as such, and one
// that moved shows both ends.
func printMoves(moves []deps.Move) {
	for _, move := range moves {
		_, _ = fmt.Fprintf(os.Stdout, "  %s\n", sdk.DependencyMove(move))
	}
}

// newPackageListCmd wires `tt package list`.
func newPackageListCmd() *cobra.Command {
	listCmd := &cobra.Command{
		Use:   listCmdName,
		Short: "List the packages installed in a scope",
		Long: "Show the packages present in the chosen scope: in the project " +
			"scope the project's own package plus every guest installed from an " +
			"archive, read from the metadata install records under " +
			".rocks/manifests/; in the user and system scopes every application " +
			"installed under <root>/versions/, with the version <root>/apps/<name> " +
			"selects and every installed one. Nothing is fetched and nothing is " +
			"resolved. " +
			"This is what is on disk — not what the current manifest declares, " +
			"which is what tt package deps reports. --tree additionally shows " +
			"each package's dependencies and, for every one of them, whether " +
			"another package holds it too — that is, whether it would be removed " +
			"along with the package or stay; it applies to the project scope, " +
			"the versions of the user and system scopes sharing no dependencies.",
		Args: cobra.NoArgs,
	}

	listCmd.Flags().StringVar(&packageScope, "scope", "project",
		"scope to list: project (the working directory), user or system "+
			"(the applications under the root tt binary uses)")

	format := bindFormatFlag(listCmd)

	listCmd.Flags().BoolVar(&packageTree, "tree", false,
		"show each package's dependencies and which of them are shared")

	listCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return commandError(cmd, runPackageList(format))
	}

	return listCmd
}

// runPackageList reads the inventory and renders it to stdout in the chosen
// format.
func runPackageList(format *output.FormatFlag) error {
	ctx, stop := packageContext()
	defer stop()

	projectDir, err := projectRootDir()
	if err != nil {
		return err
	}

	listing, err := inventory.List(ctx, inventory.ListOptions{
		ProjectDir: projectDir,
		Scope:      state.Scope(packageScope),
		Rocks:      packageTree,
	})
	if err != nil {
		return err
	}

	return emitResult(format, listing)
}

// newPackageSwitchCmd wires `tt package switch NAME [VERSION]`.
func newPackageSwitchCmd() *cobra.Command {
	switchCmd := &cobra.Command{
		Use:   "switch NAME [VERSION]",
		Short: "Make another installed version of an application active",
		Long: "Point <root>/apps/<name> at the installed version " +
			"<root>/versions/<name>/<name>_<version>/, as tt package install does " +
			"for the version it installs, and so roll back to an earlier version, " +
			"or forward again, with no archive. The link is replaced in one " +
			"rename: whoever resolves it at the same time gets the old version or " +
			"the new one. Nothing is stopped or restarted: an instance runs the " +
			"version it was started from, restarts after a failure included, until " +
			"tt instance restart, or systemctl restart for an instance under the " +
			"units of tt service systemd, which name the application rather than a " +
			"version. switch names the running instances that do not run the version " +
			"it made active, and tt instance status --details shows the version " +
			"every running instance runs. A version whose lock " +
			"bundles a tarantool that is no longer installed in the scope is " +
			"refused, as it could not start. Without VERSION, switch offers the " +
			"installed versions to choose from on a terminal, and otherwise lists " +
			"them, the active one marked, and fails. The project scope holds one " +
			"version of every package, so switch applies to the user and system " +
			"scopes only.",
		Example: `# Roll back to 1.0.0 while the instances run, then restart them.
  $ tt package switch kv-api 1.0.0 --scope user
  $ tt instance restart kv-api:* --scope user --yes

  # Choose a version interactively.
  $ tt package switch kv-api --scope user`,
		Args: cobra.RangeArgs(1, packageSwitchMaxArgs),
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runPackageSwitch(args))
		},
	}

	switchCmd.Flags().StringVar(&packageScope, "scope", "project",
		"scope of the application: user or system (the root tt binary uses); "+
			"the project scope has no versions to switch between")

	return switchCmd
}

// runPackageSwitch switches the active version of an application and tells
// which of its instances still run what they were started from.
func runPackageSwitch(args []string) error {
	projectDir, err := projectRootDir()
	if err != nil {
		return err
	}

	opts := inventory.SwitchOptions{
		ProjectDir: projectDir,
		Scope:      state.Scope(packageScope),
		Package:    args[0],
		Version:    "",
		Warn:       func(msg string) { log.Warn(msg) },
	}

	if len(args) == packageSwitchMaxArgs {
		opts.Version = args[1]
	} else {
		opts.Version, err = chooseAppVersion(opts)
		if err != nil {
			return err
		}
	}

	result, err := inventory.Switch(opts)
	if err != nil {
		return err
	}

	switch {
	case result.AlreadyActive:
		_, _ = fmt.Fprintf(os.Stdout, "%s %s is already active in %s scope\n",
			result.Package, result.Version, result.Scope)
	case result.Previous != "":
		_, _ = fmt.Fprintf(os.Stdout, "switched %s from %s to %s in %s scope\n",
			result.Package, result.Previous, result.Version, result.Scope)
	default:
		_, _ = fmt.Fprintf(os.Stdout, "switched %s to %s in %s scope\n",
			result.Package, result.Version, result.Scope)
	}

	warnRestart(result.Package, result.Scope, projectDir, result.Version, result.Running)

	return nil
}

// warnRestart warns about the running instances of the application pkg of
// scope that do not run version, which an install or a switch has just made
// active (restartHints).
func warnRestart(
	pkg string, scope state.Scope, projectDir, version string, running []inventory.RunningInstance,
) {
	if len(running) == 0 {
		return
	}

	units, err := systemdUnits(scope, projectDir, pkg)
	if err != nil {
		log.Warnf("cannot tell the systemd units of %s: %s", pkg, err)
	}

	for _, hint := range restartHints(pkg, scope, version, running, units) {
		log.Warn(hint)
	}
}

// systemdUnits returns the units tt service systemd installed for the
// instances of the application name, by instance, from the mark it keeps in
// <root>/etc/<name>/; none when it installed none.
//
// The units name the application, not a version: they run
// tt instance start <name>:<inst>, which resolves <root>/apps/<name> anew, so
// a unit restarted after an install or a switch runs the active version.
func systemdUnits(scope state.Scope, projectDir, name string) (map[string]string, error) {
	root, err := state.ScopeRoot(scope, projectDir)
	if err != nil {
		return nil, fmt.Errorf("locating the %s scope: %w", scope, err)
	}

	mark, _, err := systemd.ReadMark(app.HostDir(root, name))
	if err != nil {
		return nil, fmt.Errorf("reading the mark of the units: %w", err)
	}

	return mark.Instances, nil
}

// restartHints tells about the running instances of the application pkg that
// do not run its active version yet, after an install or a switch made
// version active, and how to restart them onto it: with tt instance restart,
// or with systemctl for those under systemd units, units by instance, which
// tt instance restart refuses. An instance whose version is not known, as one
// an older tt started, counts as one that does not run it.
func restartHints(
	pkg string, scope state.Scope, version string, running []inventory.RunningInstance,
	units map[string]string,
) []string {
	var behind []inventory.RunningInstance

	for _, inst := range running {
		if inst.Version != version {
			behind = append(behind, inst)
		}
	}

	if len(behind) == 0 {
		return nil
	}

	var names, targets, managed []string

	same := behind[0].Version

	for _, inst := range behind {
		if inst.Version != same {
			same = ""
		}
	}

	for _, inst := range behind {
		switch {
		case same != "":
			names = append(names, inst.Name)
		case inst.Version != "":
			names = append(names, inst.Name+" ("+inst.Version+")")
		default:
			names = append(names, inst.Name+" (version unknown)")
		}

		if unit := units[inst.Name]; unit != "" {
			managed = append(managed, unit)
		} else {
			targets = append(targets, pkg+":"+inst.Name)
		}
	}

	counted := fmt.Sprintf("%d instances of %s run", len(behind), pkg)
	if len(behind) == 1 {
		counted = fmt.Sprintf("1 instance of %s runs", pkg)
	}

	what := "another version than " + version
	if same != "" {
		what = same
	}

	hints := []string{fmt.Sprintf("%s %s: %s", counted, what, strings.Join(names, ", "))}

	if len(targets) > 0 {
		hints = append(hints, fmt.Sprintf("to move them to %s: tt instance restart %s "+
			"--scope %s", version, strings.Join(targets, ","), scope))
	}

	if len(managed) > 0 {
		hints = append(hints, fmt.Sprintf("to move those under systemd to %s: "+
			"systemctl restart %s", version, strings.Join(managed, " ")))
	}

	return hints
}

// chooseAppVersion asks which installed version of the application to switch
// to, the way tt binary switch asks for a version: a menu on a terminal, the
// active version marked. Without a terminal there is nobody to ask, so the
// installed versions are listed on stdout and the command fails rather than
// switch to nothing in silence, as a script missing its version would.
func chooseAppVersion(opts inventory.SwitchOptions) (string, error) {
	installed, err := inventory.InstalledApp(opts)
	if err != nil {
		return "", err
	}

	if !isTerminal(os.Stdin) || !isTerminal(os.Stdout) {
		_, _ = fmt.Fprintf(os.Stdout, "installed versions of %s in %s scope:\n",
			installed.Name, opts.Scope)

		for _, ver := range installed.Versions {
			mark := " "
			if ver == installed.Active {
				mark = "*"
			}

			_, _ = fmt.Fprintf(os.Stdout, "%s %s\n", mark, ver)
		}

		return "", sdk.Errorf(sdk.ExitFailure, "%w; name one: tt package switch %s VERSION "+
			"--scope %s", errNoSwitchVersion, installed.Name, opts.Scope)
	}

	chosen, err := switcher.ChooseFrom("Select "+installed.Name+" version",
		installed.Versions, installed.Active)
	if err != nil {
		return "", fmt.Errorf("cannot switch %s: %w", installed.Name, err)
	}

	return chosen, nil
}

// newPackageUninstallCmd wires `tt package uninstall NAME`.
func newPackageUninstallCmd() *cobra.Command {
	uninstallCmd := &cobra.Command{
		Use:   "uninstall NAME",
		Short: "Remove an installed guest package from a scope",
		Long: "Remove a guest package: its files and its metadata, plus every " +
			"shared dependency that nothing else still needs. A dependency is " +
			"owned by every installed package whose lock pins it, so one that " +
			"another package still holds stays in place. The project's own " +
			"package — the one app.manifest.toml in the working directory " +
			"declares — is not a guest, and uninstall refuses to remove it. " +
			"In the user and system scopes uninstall removes an application: " +
			"every installed version under <root>/versions/<name>/ and the link " +
			"<root>/apps/<name>. It refuses while an instance of the application " +
			"runs, as its pid file under <root>/var/run/<name>/ tells, and keeps " +
			"the instance data under <root>/var/, the host files under " +
			"<root>/etc/<name>/ and the runtimes in <root>/versions/ other " +
			"applications may use.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runPackageUninstall(args[0]))
		},
	}

	uninstallCmd.Flags().StringVar(&packageScope, "scope", "project",
		"scope to remove from: project (the working directory), user or system "+
			"(an application under the root tt binary uses)")
	uninstallCmd.Flags().BoolVarP(&packageYes, "yes", "y", false,
		"skip the confirmation prompt")

	return uninstallCmd
}

// runPackageUninstall removes one package and reports what went with it.
func runPackageUninstall(name string) error {
	ctx, stop := packageContext()
	defer stop()

	projectDir, err := projectRootDir()
	if err != nil {
		return err
	}

	result, err := inventory.Uninstall(ctx, inventory.UninstallOptions{
		ProjectDir: projectDir,
		Scope:      state.Scope(packageScope),
		Package:    name,
		Yes:        packageYes,
		Warn:       func(msg string) { log.Warn(msg) },
		Confirm: func(prompt string) bool {
			ok, askErr := util.AskConfirm(os.Stdin, prompt)

			return askErr == nil && ok
		},
		Backends: buildreg.Process(),
	})
	if err != nil {
		return err
	}

	if len(result.Versions) > 0 {
		_, _ = fmt.Fprintf(os.Stdout, "removed %s from %s scope: versions %s\n",
			result.Package, result.Scope, strings.Join(result.Versions, ", "))
	} else {
		_, _ = fmt.Fprintf(os.Stdout, "removed %s %s from %s scope\n",
			result.Package, result.Version, result.Scope)
	}

	for _, dep := range result.RemovedDependencies {
		_, _ = fmt.Fprintf(os.Stdout, "  removed unused dependency %s\n", dep)
	}

	for _, dep := range result.KeptDependencies {
		_, _ = fmt.Fprintf(os.Stdout, "  kept %s (%s)\n", dep.Name, keptReason(dep))
	}

	return nil
}

// keptReason says why a dependency survived the uninstall. A rock another
// package pins is kept for that reference; a rock installed as a package in its
// own right is kept because only an uninstall naming it may remove it, which is
// a different thing and reads wrong as "required by itself".
func keptReason(dep inventory.KeptDependency) string {
	switch {
	case len(dep.HeldBy) > 0 && dep.Installed:
		return "installed as a package; also required by " + strings.Join(dep.HeldBy, ", ")
	case dep.Installed:
		return "installed as a package in its own right"
	default:
		return "required by " + strings.Join(dep.HeldBy, ", ")
	}
}

// newPackageInstallCmd wires `tt package install ARCHIVE...`.
func newPackageInstallCmd() *cobra.Command {
	installCmd := &cobra.Command{
		Use:   "install ARCHIVE...",
		Short: "Install a .tt package archive into a scope",
		Long: "Unpack a package archive into the chosen scope and bring the tree " +
			"to a runnable state. A with-deps archive is a plain offline " +
			"extraction; a --without-deps one is unpacked and its dependencies are " +
			"refetched from the registry using the lock's pins: from the servers " +
			"--registry or " + manifestrocks.EnvRegistries + " names, otherwise " +
			"from the default ones. The runtime a " +
			"with-deps archive bundles (_runtime/) is installed the way tt binary " +
			"installs it, into versions/ of the scope root — <root>/versions/ in " +
			"the user and system scopes, .tarantool/versions/ in the project — " +
			"unless that version is there already. " +
			"Several packages installed into one project share a .rocks/ tree, so a " +
			"dependency they lock at different versions is reconciled to one both " +
			"accept, or the install fails with an explanation; a project that " +
			"selects no tarantool yet gets the bundled one selected in " +
			".tarantool/bin. In the user and " +
			"system scopes the archive becomes one version of an application, " +
			"<root>/versions/<name>/<name>_<version>/, laid out like a project " +
			"next to what tt binary installs into the same root, and " +
			"<root>/apps/<name> is pointed at it: install activates the version it " +
			"installed, whether it is newer or older than the active one, so a " +
			"plain install of a newer archive is an upgrade already; --upgrade only " +
			"adds the check that the archive is newer than every installed version, " +
			"and does nothing otherwise. It runs on the tarantool its lock bundles, " +
			"whatever <root>/bin/tarantool selects. Versions live side by side; " +
			"installing one that is already there needs --force, which is refused " +
			"while instances of the application run. Running instances keep the " +
			"version they were started from until tt instance restart; install " +
			"names those that do not run the version it installed. To roll back, " +
			"or to make any other installed version active again, use " +
			"tt package switch: it needs no archive and no stop.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runPackageInstall(args))
		},
	}

	installCmd.Flags().StringVar(&packageScope, "scope", "project",
		"install scope: project (the working directory), user or system "+
			"(an application version under the root tt binary uses)")
	installCmd.Flags().BoolVar(&packageLocked, "locked", false,
		"fail if the archive lock does not match its manifest")
	installCmd.Flags().BoolVar(&packageUpgrade, "upgrade", false,
		"install only when the archive version is higher than the installed one "+
			"(in user and system, than every installed version)")
	installCmd.Flags().BoolVar(&packageForce, "force", false,
		"reinstall over an existing package or version (destructive)")
	installCmd.Flags().BoolVarP(&packageYes, "yes", "y", false,
		"skip the confirmation prompt when reconciling shared dependencies")
	addRegistryFlag(installCmd)

	return installCmd
}

// runPackageInstall assembles install.Options from the environment and installs
// every archive. Tarantool facts are gathered best-effort: an offline with-deps
// install needs none, so a missing tarantool is only fatal when a dependency
// must actually be refetched.
func runPackageInstall(archives []string) error {
	ctx, stop := packageContext()
	defer stop()

	projectDir, err := projectRootDir()
	if err != nil {
		return err
	}

	// Missing tarantool is tolerated here; refetch surfaces its own error only
	// if the install actually reaches the registry.
	tntInfo, _ := tarantoolInfo()

	// The archive's [platform].registries is not a layer here: its relative
	// entries were written against a project directory that is not this one.
	sources, err := registrySources(packageRegistries)
	if err != nil {
		return err
	}

	registries, err := manifestrocks.EffectiveRegistries(sources)
	if err != nil {
		return err
	}

	result, err := install.Run(ctx, install.Options{
		ProjectDir: projectDir,
		Scope:      install.Scope(packageScope),
		Archives:   archives,
		Locked:     packageLocked,
		Upgrade:    packageUpgrade,
		Force:      packageForce,
		Yes:        packageYes,
		Tarantool:  tntInfo,
		Servers:    manifestrocks.URLs(registries),
		Logger:     luarocksLogger(),
		Warn:       func(msg string) { log.Warn(msg) },
		Confirm: func(prompt string) bool {
			ok, askErr := util.AskConfirm(os.Stdin, prompt)

			return askErr == nil && ok
		},
		Integrity: installIntegrity(cmdCtx.Cli.IntegrityCheck),
	})
	if err != nil {
		return err
	}

	for _, one := range result.Installed {
		if one.Skipped {
			continue
		}

		_, _ = fmt.Fprintf(os.Stdout, "installed %s %s into %s scope\n",
			one.Package, one.Version, one.Scope)

		// An application version is made active as it is installed; the
		// instances that run go on running what they were started from.
		if one.Scope == install.ScopeProject {
			continue
		}

		running, err := inventory.RunningInstances(one.Scope, projectDir, one.Package)
		if err != nil {
			log.Warnf("cannot tell which instances of %s run: %s", one.Package, err)
		}

		warnRestart(one.Package, one.Scope, projectDir, one.Version, running)
	}

	return nil
}

// newPackageBuildCmd wires `tt package build [COMPONENT]`.
func newPackageBuildCmd() *cobra.Command {
	buildCmd := &cobra.Command{
		Use:   "build [COMPONENT]",
		Short: "Build a manifest package into .rocks/",
		Long: "Resolve dependencies, materialize .rocks/, run component build " +
			"backends and generate version.lua. With no argument every component " +
			"of the product is built; a component name narrows the build to one.\n\n" +
			"A make or shell component with incremental = true is built only when its " +
			"inputs changed since its last build - its build block and its files, but " +
			"those its .gitignore files ignore - or its outputs are gone; -V logs a " +
			"skipped one. --rebuild builds them all, for a change the inputs do not " +
			"cover, such as a new version of a compiler.\n\n" +
			"A component build keeps what it makes on the way in TT_BUILD_DIR, " +
			"_build/component/<component>/ - the build tree of a cmake one, where " +
			"cmake rebuilds what changed - and --rebuild empties it first. A cargo " +
			"build builds in _build/cargo/, its CARGO_TARGET_DIR.\n\n" +
			"-j N runs at most N jobs at once in each component build, one a CPU by " +
			"default: the backend hands N to its tool, and a make or shell build " +
			"reads it as TT_BUILD_JOBS. It changes no input: a component built with " +
			"other -j is not built again.",
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runPackage(args, false))
		},
	}

	buildCmd.Flags().StringVar(&packageProduct, "product", "",
		"product to build (default: the product marked default)")
	buildCmd.Flags().BoolVar(&packageLocked, "locked", false,
		"fail if the lock is out of date instead of re-resolving it")
	buildCmd.Flags().BoolVar(&packageRebuild, "rebuild", false,
		"build the incremental components even when nothing changed, and every "+
			"component in an empty TT_BUILD_DIR")
	sdk.AddJobsFlag(buildCmd, &packageJobs)
	addRegistryFlag(buildCmd)

	return buildCmd
}

// newPackageFetchCmd wires `tt package fetch`.
func newPackageFetchCmd() *cobra.Command {
	fetchCmd := &cobra.Command{
		Use:   "fetch",
		Short: "Materialize .rocks/ from the lock without building",
		Long: "Fetch and build the pinned dependency closure into .rocks/ " +
			"strictly from the lock, without re-resolving or running component " +
			"build backends.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runPackage(nil, true))
		},
	}

	fetchCmd.Flags().StringVar(&packageProduct, "product", "",
		"product to fetch (default: the product marked default)")
	addRegistryFlag(fetchCmd)

	return fetchCmd
}

// newPackagePackCmd wires `tt package pack`.
func newPackagePackCmd() *cobra.Command {
	packCmd := &cobra.Command{
		Use:   "pack",
		Short: "Build a manifest package and pack it into a .tt archive",
		Long: "Run the full build and assemble its result into a reproducible " +
			".tt archive. By default the archive is self-contained: it carries " +
			"_runtime/ (Tarantool, tt and TCM when [platform].tcm is set) and the " +
			"whole dependency closure in .rocks/, so installing it needs no " +
			"network. --without-deps drops both. The project's instance " +
			"templates in .templates/ travel at the archive root in either mode; " +
			"each .templates/<name>/ must hold config.yaml, instances.yaml or " +
			"both, and a broken template fails the pack. A separate tt package build " +
			"beforehand is not needed.\n\n" +
			"--format rock packs the package as a rock for other projects to " +
			"depend on: the binary rock of this platform, or an .all rock when " +
			"the package has no native file, with its rockspec beside it. The " +
			"rockspec is made from the manifest: the dependencies are the ranges " +
			"it declares, and the source is [package].repository at the tag of " +
			"the version, or at its commit, in full in source.tag, when no tag " +
			"names it. The rock is written into --out, which is then indexed " +
			"as a rock server, keeping the rocks already there: the LuaRocks " +
			"manifests and the rock index, " + rockindex.File + ", which has a " +
			"project that depends on the rock pin the sha256 of its files in the " +
			"lock (see tt package index). --format tt,rock " +
			"makes both from one build.\n\n" +
			"--format rock-src packs the source rock: the same rockspec and the " +
			"files of the project git tracks, submodules included, with a VERSION " +
			"file of the version. A pack of the source rock alone builds nothing, " +
			"so it needs no compiler and no Tarantool headers; the manifest and " +
			"the lock are checked as a build checks them. A project that depends " +
			"on the rock builds it " +
			"from the source rock, with its manifest, where the server has no " +
			"rock of the platform.\n\n" +
			"The paths of what the pack made are printed to stdout, one per line.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runPackagePack())
		},
	}

	packCmd.Flags().StringVar(&packageProduct, "product", "",
		"product to pack (default: the product marked default)")
	packCmd.Flags().BoolVar(&packageLocked, "locked", false,
		"fail if the lock is out of date instead of re-resolving it")
	packCmd.Flags().BoolVar(&packageRebuild, "rebuild", false,
		"build the incremental components even when nothing changed, and every "+
			"component in an empty TT_BUILD_DIR")
	sdk.AddJobsFlag(packCmd, &packageJobs)
	packCmd.Flags().BoolVar(&packageWithoutDeps, "without-deps", false,
		"pack without _runtime/ and without foreign dependencies in .rocks/")
	packCmd.Flags().StringSliceVar(&packageFormats, "format", []string{pack.FormatTt},
		"what to make, comma-separated: tt (the .tt archive), rock (the rock and its "+
			"rockspec), rock-src (the source rock and its rockspec)")
	packCmd.Flags().StringVar(&packageOut, "out", "",
		"directory to write into (default _build/pack of the project)")
	packCmd.Flags().IntVar(&packageRevision, "revision", 1,
		"LuaRocks revision of the rock: raise it to publish a new build of a version")
	packCmd.Flags().StringVar(&packageDebugInfo, "debuginfo", pack.DebugInfoKeep,
		"debug information of the modules and programs the package's builds made: keep, "+
			"or strip it (strip --strip-debug; strip -S on macOS) from what is packed; "+
			"the project's own files, the bundled runtime and the dependencies are not touched")
	addRegistryFlag(packCmd)

	// The flag exists when the integrity provider of the distribution signs,
	// and so does the paragraph that says what it does.
	usage := packCmd.Flags().FlagUsages()
	integrityprovider.Get().RegisterWithIntegrityFlag(packCmd.Flags(), &packageIntegrityKey)

	if packCmd.Flags().FlagUsages() != usage {
		packCmd.Long += integritySigningHelp
	}

	return packCmd
}

// runPackagePack assembles pack.Options from the environment, packs, and prints
// the resulting archive path to stdout so it can be piped.
func runPackagePack() error {
	ctx, stop := packageContext()
	defer stop()

	projectDir, err := packageRootDir()
	if err != nil {
		return err
	}

	tntInfo, err := buildTarantoolInfo()
	if err != nil {
		return err
	}

	sources, err := registrySources(packageRegistries)
	if err != nil {
		return err
	}

	signer, err := packageSigner()
	if err != nil {
		return err
	}

	out := ""
	if packageOut != "" {
		out, err = filepath.Abs(packageOut)
		if err != nil {
			return fmt.Errorf("--out: %w", err)
		}
	}

	result, err := pack.Run(ctx, pack.Options{
		Signer:      signer,
		ProjectDir:  projectDir,
		Product:     packageProduct,
		Locked:      packageLocked,
		WithoutDeps: packageWithoutDeps,
		Formats:     packageFormats,
		Revision:    packageRevision,
		OutputDir:   out,
		// Pack sets the project, product, lock mode, component and check of
		// the build itself, pins its timestamp and routes its warnings to
		// Warn.
		Build: build.Options{
			ProjectDir: "",
			Product:    "",
			Component:  "",
			Components: nil,
			Args:       nil,
			Locked:     false,
			FetchOnly:  false,
			TtVersion:  lockStamp(),
			Tarantool:  tntInfo,
			Registries: sources,
			ShowOutput: cmdCtx.Cli.Verbose,
			Rebuild:    packageRebuild,
			Jobs:       packageJobs,
			Now:        time.Time{},
			Logger:     luarocksLogger(),
			Warn:       nil,
			// The output of pack is the paths of what it packed: what the
			// build removed goes to the log.
			Removed:  func(msg string) { log.Info(msg) },
			Check:    nil,
			Backends: buildreg.Process(),
		},
		Runtime:   runtimeRequest(ctx, projectDir, tntInfo),
		Warn:      func(msg string) { log.Warn(msg) },
		DebugInfo: packageDebugInfo,
	})
	if err != nil {
		return err
	}

	for _, path := range []string{result.Path, result.Rock, result.SrcRock, result.Rockspec} {
		if path != "" {
			_, _ = fmt.Fprintln(os.Stdout, path)
		}
	}

	return nil
}

// runtimeRequest describes where pack may take the _runtime/ components from:
// the runtime cache first, the tt binary installations of the project in
// projectDir second, the active tt environment as a fallback. With no
// tarantool, tntInfo empty, the first two are the only sources of Tarantool.
//
// TcmCli carries no version and nothing probes the binary, so the active TCM is
// deliberately not offered as a fallback: passing the path without a version
// would make pack report "the active tcm does not satisfy it" having never
// looked at its version. With [platform].tcm set, the cache and the
// installations of the project are the only sources.
func runtimeRequest(
	ctx context.Context, projectDir string, tntInfo manifestrocks.TarantoolInfo,
) pack.RuntimeOptions {
	selfPath, err := os.Executable()
	if err != nil {
		// Without a path to ourselves the tt component can still come from the
		// cache; pack reports a proper error if neither source has a match.
		selfPath = ""
	}

	return pack.RuntimeOptions{
		CacheDir:    runtimeCacheDir(),
		ProjectRoot: scope.ProjectRoot(projectDir),
		// Pack fills the platform from the built manifest.
		Platform: manifest.Platform{
			Tarantool:  manifest.Constraint{Version: "", Flavor: ""},
			Tt:         manifest.Constraint{Version: "", Flavor: ""},
			Tcm:        manifest.Constraint{Version: "", Flavor: ""},
			Platforms:  nil,
			Registries: nil,
		},
		ActiveTarantool:        tntInfo.Executable,
		ActiveTarantoolVersion: tntInfo.Version,
		ActiveTarantoolFlavor:  pack.DetectTarantoolFlavor(ctx, tntInfo.Executable),
		ActiveTarantoolPrefix:  tntInfo.Prefix,
		ActiveTt:               selfPath,
		// The short form is the bare "x.y.z"; the long form is a human-readable
		// sentence that no version constraint can match.
		ActiveTtVersion: coreVersion(),
		// tt publishes no CE/EE marker through its version, so the flavor stays
		// undetermined and only satisfies the [ce] default.
		ActiveTtFlavor:   "",
		ActiveTcm:        "",
		ActiveTcmVersion: "",
		Warn:             nil,
	}
}

// runtimeCacheDir returns the runtime cache root, <user cache>/tt/runtimes.
// Nothing populates it: it holds the builds a user places there to bundle
// instead of the tt binary installations of the project.
func runtimeCacheDir() string {
	base, err := os.UserCacheDir()
	if err != nil {
		return ""
	}

	return filepath.Join(base, "tt", "runtimes")
}

// runPackage drives the build, or with fetchOnly the fetch, of the product,
// the component in args and the lock the flags of tt package build and
// tt package fetch select.
func runPackage(args []string, fetchOnly bool) error {
	ctx, stop := packageContext()
	defer stop()

	component := ""
	if len(args) == 1 {
		component = args[0]
	}

	return buildPackage(ctx, packageRun{
		product:    packageProduct,
		component:  component,
		components: nil,
		locked:     packageLocked,
		fetchOnly:  fetchOnly,
		rebuild:    packageRebuild,
		jobs:       packageJobs,
		registries: packageRegistries,
		showOutput: false,
		args:       nil,
	})
}

// packageRun is what one run of the build pipeline is asked to do: the
// choices the flags of tt package build and tt package fetch make, and
// those a module's project makes (BuildProject).
type packageRun struct {
	// product is the product to build; "" is the default one.
	product string
	// component narrows the build to one component; "" builds them all.
	component string
	// components narrow the build to these components, with component.
	components []string
	// locked fails on a stale lock instead of re-resolving it.
	locked bool
	// fetchOnly materializes .rocks/ from the lock and builds nothing.
	fetchOnly bool
	// rebuild builds the incremental components whatever they recorded.
	rebuild bool
	// jobs is the most jobs each component build runs at once, as -j sets
	// it (build.Options.Jobs).
	jobs int
	// registries are the rock servers of --registry.
	registries []string
	// showOutput shows what the tools print, as --verbose does.
	showOutput bool
	// args are passed through to the build of the one component built
	// (build.Options.Args).
	args []string
}

// buildPackage assembles build.Options for run from the environment and
// drives the build of the project.
func buildPackage(ctx context.Context, run packageRun) error {
	projectDir, err := packageRootDir()
	if err != nil {
		return err
	}

	return buildPackageIn(ctx, projectDir, run)
}

// buildPackageIn is buildPackage for the project in projectDir, as a
// module's project gives it: the directory it works in, with nothing searched
// for.
func buildPackageIn(ctx context.Context, projectDir string, run packageRun) error {
	_, err := buildPackageResult(ctx, projectDir, run)

	return err
}

// buildPackageResult is buildPackageIn, reporting what the build did.
func buildPackageResult(ctx context.Context, projectDir string, run packageRun) (
	*build.Result, error,
) {
	//nolint:contextcheck // tarantool --version is asked once, and takes no context.
	tntInfo, err := buildTarantoolInfo()
	if err != nil {
		return nil, err
	}

	sources, err := registrySources(run.registries)
	if err != nil {
		return nil, err
	}

	return build.RunResult(ctx, build.Options{
		ProjectDir: projectDir,
		Product:    run.product,
		Component:  run.component,
		Components: run.components,
		Args:       run.args,
		Locked:     run.locked,
		FetchOnly:  run.fetchOnly,
		TtVersion:  lockStamp(),
		Tarantool:  tntInfo,
		Registries: sources,
		ShowOutput: cmdCtx.Cli.Verbose || run.showOutput,
		Rebuild:    run.rebuild,
		Jobs:       run.jobs,
		Now:        time.Time{},
		Logger:     luarocksLogger(),
		Warn:       func(msg string) { log.Warn(msg) },
		Removed:    printRemoved,
		Check:      nil,
		Backends:   buildreg.Process(),
	})
}

// printRemoved prints a line of what a build or a fetch removed from the
// tree of the project (build.Options.Removed), as tt package uninstall prints
// the dependencies it removes.
func printRemoved(msg string) {
	_, _ = fmt.Fprintln(os.Stdout, msg)
}

// addRegistryFlag declares --registry on a command that queries rock servers,
// with the help of the SDK, which the flags of the modules share so they
// cannot drift apart.
func addRegistryFlag(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&packageRegistries, "registry", nil, sdk.RegistryFlagUsage)
}

// registrySources collects the rock-server lists the process itself carries:
// flag, the servers of the --registry flag, and the TT_REGISTRIES environment
// variable, with the working directory a relative entry in either is written
// against. The manifest's own list is filled in by whichever pipeline reads
// the manifest.
func registrySources(flag []string) (manifestrocks.Sources, error) {
	workingDir, err := absoluteWorkingDir()
	if err != nil {
		return manifestrocks.Sources{}, err
	}

	return manifestrocks.Sources{
		Flag:       flag,
		Env:        manifestrocks.ParseRegistryList(os.Getenv(manifestrocks.EnvRegistries)),
		Manifest:   nil,
		WorkingDir: workingDir,
		ProjectDir: "",
	}, nil
}

// newPackageSearchCmd wires `tt package search TERM`.
func newPackageSearchCmd() *cobra.Command {
	searchCmd := &cobra.Command{
		Use:   "search TERM",
		Short: "Find rocks whose name contains TERM on the configured servers",
		Long: "Search the rock servers for rocks whose name contains TERM, " +
			"reporting every version each server offers. Unlike resolution, " +
			"which stops at the first server that has a rock, a search asks all " +
			"of them: the point is to see what exists. Each version of a server " +
			"is one row; the rock named TERM comes first, then the others by " +
			"name, the versions of each newest first and scm or dev versions " +
			"after the releases. Nothing is downloaded " +
			"and nothing is written. A term nothing matches is not an error.",
		Args: cobra.ExactArgs(1),
	}

	format := bindFormatFlag(searchCmd)
	addRegistryFlag(searchCmd)

	searchCmd.RunE = func(cmd *cobra.Command, args []string) error {
		return commandError(cmd, runPackageSearch(format, args[0]))
	}

	return searchCmd
}

// runPackageSearch queries the effective servers and renders the matches.
func runPackageSearch(format *output.FormatFlag, term string) error {
	ctx, stop := packageContext()
	defer stop()

	registries, err := effectiveRegistries()
	if err != nil {
		return err
	}

	tntInfo, err := tarantoolInfo()
	if err != nil {
		return err
	}

	workingDir, err := absoluteWorkingDir()
	if err != nil {
		return err
	}

	matches, err := registry.Search(ctx, registry.SearchOptions{
		Term:       term,
		Registries: registries,
		Tarantool:  tntInfo,
		WorkingDir: workingDir,
		Logger:     luarocksLogger(),
	})
	if err != nil {
		return err
	}

	return emitResult(format, registry.SearchResult{Term: term, Matches: matches})
}

// newPackageDownloadCmd wires `tt package download [REF...]`.
func newPackageDownloadCmd() *cobra.Command {
	downloadCmd := &cobra.Command{
		Use:   "download [NAME[@VERSION]...]",
		Short: "Download rock files into a directory and index it as a server",
		Long: "Fetch rock files from the configured servers into --dir and " +
			"write the LuaRocks manifest and the rock index (" + rockindex.File +
			") that index them, so the directory is " +
			"itself a rock server: point --registry or " +
			manifestrocks.EnvRegistries + " at it and the project builds with " +
			"no network at all. With no arguments the project's locked closure " +
			"is mirrored at its exact versions — every product's dependencies " +
			"and the dev closure — which needs a lock, so run tt package " +
			"resolve first. A dependency that does not come from a registry has " +
			"no rock file to mirror and is skipped with a warning. Re-running " +
			"overwrites and re-indexes, so a mirror can be extended in place. " +
			"Each written path is printed to stdout.",
		Args: cobra.ArbitraryArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runPackageDownload(args))
		},
	}

	downloadCmd.Flags().StringVar(&packageDownloadDir, "dir", "",
		"directory to download into (default: the current directory)")
	addRegistryFlag(downloadCmd)

	return downloadCmd
}

// runPackageDownload builds or extends a mirror and prints what it wrote.
func runPackageDownload(args []string) error {
	ctx, stop := packageContext()
	defer stop()

	refs := make([]registry.Ref, 0, len(args))

	for _, arg := range args {
		ref, err := registry.ParseRef(arg)
		if err != nil {
			return err
		}

		refs = append(refs, ref)
	}

	registries, err := effectiveRegistries()
	if err != nil {
		return err
	}

	tntInfo, err := tarantoolInfo()
	if err != nil {
		return err
	}

	workingDir, err := absoluteWorkingDir()
	if err != nil {
		return err
	}

	// The lock comes from the project; the rocks go where the command runs.
	projectDir, err := projectRootDir()
	if err != nil {
		return err
	}

	dir := workingDir
	if packageDownloadDir != "" {
		dir, err = filepath.Abs(packageDownloadDir)
		if err != nil {
			return fmt.Errorf("failed to get absolute path of %q: %w", packageDownloadDir, err)
		}
	}

	written, err := registry.Download(ctx, registry.DownloadOptions{
		Refs:       refs,
		Dir:        dir,
		ProjectDir: projectDir,
		Registries: registries,
		Tarantool:  tntInfo,
		Logger:     luarocksLogger(),
		Warn:       func(msg string) { log.Warn(msg) },
	})
	if err != nil {
		return err
	}

	for _, path := range written {
		_, _ = fmt.Fprintln(os.Stdout, path)
	}

	_, _ = fmt.Fprintf(os.Stderr, "indexed %d rock(s) in %s\n", len(written), dir)

	return nil
}

// newPackageIndexCmd wires `tt package index DIR`.
func newPackageIndexCmd() *cobra.Command {
	indexCmd := &cobra.Command{
		Use:   "index DIR",
		Short: "Write the rock index of a directory of rocks",
		Long: "Write " + rockindex.File + ", the rock index tt package reads before " +
			"any LuaRocks manifest, from the rocks and rockspecs in DIR: every file " +
			"of every version by its URL, with its sha256 and size. A project that " +
			"depends on a rock of the index pins the sha256 of its files in the lock, " +
			"and every install checks the file of its platform against it. tt package " +
			"pack, tt package download and tt wasm rock write the index of the " +
			"directory they write into; this is for one filled otherwise, with the " +
			"rocks the jobs of a CI matrix packed, say.\n\n" +
			"By default the index goes into DIR, the LuaRocks manifests beside it, and " +
			"names the files relative to itself: DIR is a rock server for tt and " +
			"LuaRocks alike. --index names an index kept apart, a file of a git branch " +
			"say, and --base-url the URL the files of DIR are downloaded from, the " +
			"release page of a tag say; without --base-url the files are named " +
			"relative to the index, which DIR must be below. With either, no " +
			"manifest is written.\n\n" +
			"An index that is there is updated, not replaced: the versions of DIR are " +
			"listed anew, a version marked \"yanked\": true staying so; a version it " +
			"lists with files of DIR that are gone is dropped; a version it lists " +
			"with files elsewhere is kept. A version in DIR that the index lists with " +
			"files elsewhere fails the command, since a published version does not " +
			"change: pack the new build as a new revision. The path of the index is " +
			"printed to stdout.",
		Example: `# A directory of rocks as a rock server.
  $ tt package index dist

  # The rocks of a tag on its release page, the index in a branch of its own.
  $ gh release upload v1.0.0 dist/*.rock dist/*.rockspec
  $ tt package index dist --index index/tt-rocks-index.json \
      --base-url https://github.com/example/mylib/releases/download/v1.0.0`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runPackageIndex(args[0]))
		},
	}

	indexCmd.Flags().StringVar(&packageIndexFile, "index", "",
		"the index to write (default DIR/"+rockindex.File+")")
	indexCmd.Flags().StringVar(&packageIndexBaseURL, "base-url", "",
		"the URL the files of DIR are downloaded from (default: relative to the index)")

	return indexCmd
}

// runPackageIndex writes the index of dir, and its manifests when the index
// is that of dir as a rock server, and reports what it listed.
func runPackageIndex(dir string) error {
	ctx, stop := packageContext()
	defer stop()

	dir, err := filepath.Abs(dir)
	if err != nil {
		return fmt.Errorf("failed to get absolute path of %q: %w", dir, err)
	}

	index := filepath.Join(dir, rockindex.File)
	if packageIndexFile != "" {
		index, err = filepath.Abs(packageIndexFile)
		if err != nil {
			return fmt.Errorf("--index: %w", err)
		}
	}

	var result rockindex.Result

	if index == filepath.Join(dir, rockindex.File) && packageIndexBaseURL == "" {
		result, err = rockindex.MakeServer(ctx, dir, rockindex.ServerOptions{
			HTML: false, IfIndexed: false, Warn: func(msg string) { log.Warn(msg) },
		})
	} else {
		result, err = rockindex.Write(ctx, rockindex.Options{
			Dir: dir, Index: index, BaseURL: packageIndexBaseURL,
		})
	}

	if err != nil {
		return err
	}

	_, _ = fmt.Fprintln(os.Stdout, result.Index)

	kept := ""
	if result.Kept > 0 {
		kept = fmt.Sprintf(", and kept %d listed with files elsewhere", result.Kept)
	}

	_, _ = fmt.Fprintf(os.Stderr, "indexed %d version(s) of %s%s\n", result.Listed, dir, kept)

	for _, dropped := range result.Dropped {
		_, _ = fmt.Fprintf(os.Stderr, "dropped %s: its files are no longer in %s\n", dropped, dir)
	}

	return nil
}

// effectiveRegistries settles the rock-server list for the commands that have
// no build pipeline to settle it for them: the flag and the environment over
// the manifest of the project the working directory is in, if there is one,
// over the built-in defaults.
func effectiveRegistries() ([]manifestrocks.Registry, error) {
	sources, err := registrySources(packageRegistries)
	if err != nil {
		return nil, err
	}

	// These commands work outside a project too, where the working directory
	// stands for it and has no manifest to read.
	sources.ProjectDir, err = projectRootDir()
	if err != nil {
		return nil, err
	}

	sources.Manifest, err = manifestRegistries(sources.ProjectDir)
	if err != nil {
		return nil, err
	}

	registries, err := manifestrocks.EffectiveRegistries(sources)
	if err != nil {
		return nil, err
	}

	return registries, nil
}

// manifestRegistries reads [platform].registries out of the manifest in dir.
//
// A directory holding no manifest configures no registries: these commands are
// meant to work outside a project too, where that layer is empty rather than
// missing. A manifest that is there and cannot be parsed is an error, because
// nothing else in these commands would ever report it.
func manifestRegistries(dir string) ([]string, error) {
	data, err := os.ReadFile(filepath.Join(dir, manifestFileName))
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}

		return nil, fmt.Errorf("reading %s: %w", manifestFileName, err)
	}

	man, _, err := manifest.ParseManifest(data)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", manifestFileName, err)
	}

	return man.Platform.Registries, nil
}

// packageContext returns the context of a tt package command, cancelled when
// tt gets SIGINT or SIGTERM. The build tools go-luarocks runs are in a process
// group of their own (rocks.Config.IsolateBuilds), which the terminal's Ctrl-C
// does not reach: cancelling the context is what kills them, the compilers a
// make started included, rather than leaving them to run on and write into
// the tree once tt is gone. After the first signal the default handling is
// back, so a second one ends tt at once. stop releases the context.
func packageContext() (context.Context, context.CancelFunc) {
	return packageContextOf(context.Background())
}

// packageContextOf is packageContext derived from parent: cancelled as well
// when parent is.
func packageContextOf(parent context.Context) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)

	go func() {
		<-ctx.Done()
		stop()
	}()

	return ctx, stop
}

// luarocksLogger is the logger handed to go-luarocks through the manifest
// pipeline's Logger options. go-luarocks logs every step and every line
// luarocks prints at Info, which is detail from tt's point of view, so its
// Info reaches the log as Debug - shown with -V - while its warnings and
// errors stay as they are.
func luarocksLogger() *slog.Logger {
	return log.Library("go-luarocks")
}

// tarantoolInfo gathers the Tarantool facts the rocks adapter needs from the
// configured CLI context: the executable path, its version and its install
// prefix, resolved as tt rocks resolves it (oldrocks.ResolveTarantoolPrefix),
// with where its headers were looked for when they are not there.
func tarantoolInfo() (manifestrocks.TarantoolInfo, error) {
	if cmdCtx.Cli.TarantoolCli.Executable == "" {
		return manifestrocks.TarantoolInfo{}, ErrNoTarantool
	}

	ver, err := cmdCtx.Cli.TarantoolCli.GetVersion()
	if err != nil {
		return manifestrocks.TarantoolInfo{}, err
	}

	prefix, err := oldrocks.ResolveTarantoolPrefix(&cmdCtx.Cli)
	if err != nil {
		return manifestrocks.TarantoolInfo{}, err
	}

	return manifestrocks.TarantoolInfo{
		Executable:     cmdCtx.Cli.TarantoolCli.Executable,
		Prefix:         prefix.Dir,
		Version:        ver.Str,
		MissingHeaders: prefix.MissingHeaders(),
	}, nil
}

// buildTarantoolInfo is tarantoolInfo for tt package build, fetch and pack,
// which go without a tarantool: with none found they build with no Tarantool
// facts, the zero TarantoolInfo, since a package of Lua needs none, and what
// does need one fails saying so - a c or lua-c component, or a rock of the
// registry, compiled against the headers of Tarantool, and a .tt archive
// with its runtime when the runtime cache has no Tarantool to bundle. A make,
// shell or cmake build is then not told where Tarantool is (TT_TARANTOOL_*).
// A tarantool that is there but cannot tell its version or prefix is still an
// error.
func buildTarantoolInfo() (manifestrocks.TarantoolInfo, error) {
	info, err := tarantoolInfo()
	if errors.Is(err, ErrNoTarantool) {
		log.Debugf("Building without a Tarantool: %s", err)

		return manifestrocks.TarantoolInfo{
			Executable: "", Prefix: "", Version: "", MissingHeaders: "",
		}, nil
	}

	return info, err
}

// packageSigner returns the signer of tt package pack --with-integrity-check,
// or nil when the archive is not signed.
func packageSigner() (integrity.Signer, error) {
	if packageIntegrityKey == "" {
		return nil, nil //nolint:nilnil // No key, no signer: the archive is not signed.
	}

	signer, err := integrityprovider.Get().NewSigner(packageIntegrityKey)
	if err != nil {
		return nil, sdk.WithUsage(fmt.Errorf("--with-integrity-check: %w", err))
	}

	return signer, nil
}

// integritySigningHelp is what the help of tt package pack says about
// signing, in a distribution whose integrity provider signs.
const integritySigningHelp = " With --with-integrity-check the archive is signed for the " +
	"integrity checks: the package and each runtime it bundles get their hashes files, and " +
	"the instance records of its templates may run only the files of the package. A signed " +
	"archive is installed into the user or the system scope with tt package install " +
	"--integrity-check; it is not packed --without-deps."
