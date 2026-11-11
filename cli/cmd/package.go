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

	"github.com/tarantool/tt/v3/cli/manifest"
	"github.com/tarantool/tt/v3/cli/manifest/build"
	"github.com/tarantool/tt/v3/cli/manifest/deps"
	"github.com/tarantool/tt/v3/cli/manifest/install"
	"github.com/tarantool/tt/v3/cli/manifest/inventory"
	"github.com/tarantool/tt/v3/cli/manifest/pack"
	"github.com/tarantool/tt/v3/cli/manifest/registry"
	manifestrocks "github.com/tarantool/tt/v3/cli/manifest/rocks"
	"github.com/tarantool/tt/v3/cli/manifest/state"
	oldrocks "github.com/tarantool/tt/v3/cli/rocks"
	"github.com/tarantool/tt/v3/cli/util"
	"github.com/tarantool/tt/v3/internal/integrityprovider"
)

// ErrNoTarantool reports that no tarantool executable was found; the manifest
// build needs it to configure the rocks adapter (and to compile c/lua-c
// components), and resolution to evaluate rockspecs.
var ErrNoTarantool = errors.New("tarantool executable not found in PATH")

// Shared flags for the package subcommands.
var (
	packageProduct string
	packageLocked  bool
	// packageRebuild builds the incremental components whatever they recorded.
	packageRebuild     bool
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
)

// Flags shared by the scope-addressed subcommands: install, list and uninstall.
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

// packageRegistries collects --registry, the highest-precedence rock-server
// list: it replaces TT_REGISTRIES, [platform].registries and the defaults.
var packageRegistries []string

// packageDownloadDir is --dir of `tt package download`.
var packageDownloadDir string

const packageAddMaxArgs = 2

// manifestFileName is the package manifest the directory-scoped registry
// commands read [platform].registries from.
const manifestFileName = "app.manifest.toml"

// NewPackageCmd creates the `tt package` command group: the manifest build
// pipeline (build, fetch and pack), the dependency-changing commands (add,
// remove and update), installation, and the inventory over what is installed
// (list and uninstall).
func NewPackageCmd() *cobra.Command {
	packageCmd := &cobra.Command{
		Use:   "package",
		Short: "Manage tt manifest packages",
		Long: "Build, fetch, pack and install Tarantool packages described by " +
			"app.manifest.toml, manage their dependencies, and manage what is " +
			"installed. What the exit codes mean is in tt help errors.",
	}

	packageCmd.AddCommand(
		newPackageBuildCmd(),
		newPackageFetchCmd(),
		newPackagePackCmd(),
		newPackageAddCmd(),
		newPackageRemoveCmd(),
		newPackageUpdateCmd(),
		newPackageResolveCmd(),
		newPackageDepsCmd(),
		newPackageInstallCmd(),
		newPackageListCmd(),
		newPackageUninstallCmd(),
		newPackageSearchCmd(),
		newPackageDownloadCmd(),
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
			"re-resolve app.manifest.lock around it. Without a constraint the " +
			"dependency is declared as '*': whatever the registry serves. A name " +
			"the table already declares has its constraint rewritten in place; " +
			"the rest of the declaration, and every comment and blank line in the " +
			"file, is left exactly as it was. Every version the lock already " +
			"holds is kept: only the new dependency and whatever it requires can " +
			"move, because an edit to one dependency is not a request to upgrade " +
			"the others — that is what tt package update is for. Nothing is " +
			"fetched into .rocks/; the next tt package build picks the new lock " +
			"up.",
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

	result, err := deps.Add(ctx, opts, name, constraint, packageDev)
	if err != nil {
		return err
	}

	table := "dependencies"
	if packageDev {
		table = "dev_dependencies"
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

// newPackageRemoveCmd wires `tt package remove NAME`.
func newPackageRemoveCmd() *cobra.Command {
	removeCmd := &cobra.Command{
		Use:   "remove NAME",
		Short: "Drop a declared dependency and re-resolve the lock",
		Long: "Remove NAME from [dependencies] and [dev_dependencies], then " +
			"re-resolve app.manifest.lock without it. The rocks that were only " +
			"there to satisfy it drop out of the closure with it; every other " +
			"version is held. A name the manifest does not declare is an error " +
			"rather than a silent success — it is almost always a typo — and so " +
			"is a name only a component declares, since which component owns a " +
			"dependency is not this command's decision to unmake. Nothing is " +
			"deleted from .rocks/; tt package uninstall is what removes files.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return commandError(cmd, runPackageRemove(args[0]))
		},
	}

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

	result, err := deps.Remove(ctx, opts, name)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stdout, "removed %s %s\n", name, result.Change.Previous)
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

	if len(result.Moves) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "everything is already up to date")

		return nil
	}

	printMoves(result.Moves)

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

	if len(result.Moves) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "the lock is up to date")

		return nil
	}

	printMoves(result.Moves)

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
	projectDir, err := projectRootDir()
	if err != nil {
		return err
	}

	// Nothing is resolved, so neither Tarantool nor registries are needed.
	report, err := deps.Deps(deps.Options{
		ProjectDir: projectDir,
		TtVersion:  "",
		Tarantool:  manifestrocks.TarantoolInfo{Executable: "", Prefix: "", Version: ""},
		Registries: manifestrocks.Sources{
			Flag:       nil,
			Env:        nil,
			Manifest:   nil,
			WorkingDir: "",
			ProjectDir: "",
		},
		Logger: nil,
		Warn:   func(msg string) { log.Warn(msg) },
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
	projectDir, err := projectRootDir()
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
	}, nil
}

// printMoves reports the closure changes one per line: a rock that arrived
// shows only its new version, one that dropped out is marked as such, and one
// that moved shows both ends.
func printMoves(moves []deps.Move) {
	for _, move := range moves {
		switch {
		case move.From == "":
			_, _ = fmt.Fprintf(os.Stdout, "  %s %s\n", move.Name, move.To)
		case move.To == "":
			_, _ = fmt.Fprintf(os.Stdout, "  %s %s -> (dropped)\n", move.Name, move.From)
		default:
			_, _ = fmt.Fprintf(os.Stdout, "  %s %s -> %s\n", move.Name, move.From, move.To)
		}
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
			"installed. It runs on the tarantool its lock bundles, whatever " +
			"<root>/bin/tarantool selects. Versions live side by side; installing " +
			"one that is already there needs --force, which is refused while " +
			"instances of the application run.",
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
			"A cmake component keeps its build tree in _build/cmake/<component>/, " +
			"where cmake rebuilds what changed; --rebuild configures it anew.",
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
		"build the incremental components even when nothing changed, and the "+
			"cmake ones in a new build tree")
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
			"the version. The rock is written into --out, which is then indexed " +
			"as a rock server, keeping the rocks already there. --format tt,rock " +
			"makes both from one build.\n\n" +
			"--format rock-src packs the source rock: the same rockspec and the " +
			"files of the project git tracks, submodules included, with a VERSION " +
			"file of the version. A project that depends on the rock builds it " +
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
		"build the incremental components even when nothing changed, and the "+
			"cmake ones in a new build tree")
	packCmd.Flags().BoolVar(&packageWithoutDeps, "without-deps", false,
		"pack without _runtime/ and without foreign dependencies in .rocks/")
	packCmd.Flags().StringSliceVar(&packageFormats, "format", []string{pack.FormatTt},
		"what to make, comma-separated: tt (the .tt archive), rock (the rock and its "+
			"rockspec), rock-src (the source rock and its rockspec)")
	packCmd.Flags().StringVar(&packageOut, "out", "",
		"directory to write into (default _build/pack of the project)")
	packCmd.Flags().IntVar(&packageRevision, "revision", 1,
		"LuaRocks revision of the rock: raise it to publish a new build of a version")
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

	projectDir, err := projectRootDir()
	if err != nil {
		return err
	}

	tntInfo, err := tarantoolInfo()
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

	warnWasmSDK(projectDir)

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
			Locked:     false,
			FetchOnly:  false,
			TtVersion:  lockStamp(),
			Tarantool:  tntInfo,
			Registries: sources,
			ShowOutput: cmdCtx.Cli.Verbose,
			Rebuild:    packageRebuild,
			Now:        time.Time{},
			Logger:     luarocksLogger(),
			Warn:       nil,
			Check:      nil,
		},
		Runtime: runtimeRequest(ctx, tntInfo),
		Warn:    func(msg string) { log.Warn(msg) },
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
// the runtime cache first, the active tt environment as a fallback.
//
// TcmCli carries no version and nothing probes the binary, so the active TCM is
// deliberately not offered as a fallback: passing the path without a version
// would make pack report "the active tcm does not satisfy it" having never
// looked at its version. With [platform].tcm set, the cache is the only source.
func runtimeRequest(ctx context.Context, tntInfo manifestrocks.TarantoolInfo) pack.RuntimeOptions {
	selfPath, err := os.Executable()
	if err != nil {
		// Without a path to ourselves the tt component can still come from the
		// cache; pack reports a proper error if neither source has a match.
		selfPath = ""
	}

	return pack.RuntimeOptions{
		CacheDir: runtimeCacheDir(),
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
// The exact path is not yet finalized and nothing populates it automatically
// yet; until then this is the directory a user populates by hand.
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
		locked:     packageLocked,
		fetchOnly:  fetchOnly,
		rebuild:    packageRebuild,
		registries: packageRegistries,
	})
}

// packageRun is what one run of the build pipeline is asked to do: the
// choices the flags of tt package build and tt package fetch make.
type packageRun struct {
	// product is the product to build; "" is the default one.
	product string
	// component narrows the build to one component; "" builds them all.
	component string
	// locked fails on a stale lock instead of re-resolving it.
	locked bool
	// fetchOnly materializes .rocks/ from the lock and builds nothing.
	fetchOnly bool
	// rebuild builds the incremental components whatever they recorded.
	rebuild bool
	// registries are the rock servers of --registry.
	registries []string
}

// buildPackage assembles build.Options for run from the environment and
// drives the build of the project.
func buildPackage(ctx context.Context, run packageRun) error {
	projectDir, err := projectRootDir()
	if err != nil {
		return err
	}

	return buildPackageIn(ctx, projectDir, run)
}

// buildPackageIn is buildPackage for the project in projectDir, as a
// module's project gives it: the directory it works in, with nothing searched
// for.
func buildPackageIn(ctx context.Context, projectDir string, run packageRun) error {
	//nolint:contextcheck // tarantool --version is asked once, and takes no context.
	tntInfo, err := tarantoolInfo()
	if err != nil {
		return err
	}

	sources, err := registrySources(run.registries)
	if err != nil {
		return err
	}

	warnWasmSDK(projectDir)

	return build.Run(ctx, build.Options{
		ProjectDir: projectDir,
		Product:    run.product,
		Component:  run.component,
		Locked:     run.locked,
		FetchOnly:  run.fetchOnly,
		TtVersion:  lockStamp(),
		Tarantool:  tntInfo,
		Registries: sources,
		ShowOutput: cmdCtx.Cli.Verbose,
		Rebuild:    run.rebuild,
		Now:        time.Time{},
		Logger:     luarocksLogger(),
		Warn:       func(msg string) { log.Warn(msg) },
		Check:      nil,
	})
}

// registryFlagUsage is the --registry help text, shared by every command that
// takes it so they cannot drift apart.
const registryFlagUsage = "rock server to use instead of the configured ones: " +
	"an http(s) URL or a local directory; repeat to give an ordered list " +
	"(overrides " + manifestrocks.EnvRegistries + " and [platform].registries)"

// addRegistryFlag declares --registry on a command that queries rock servers.
func addRegistryFlag(cmd *cobra.Command) {
	cmd.Flags().StringArrayVar(&packageRegistries, "registry", nil, registryFlagUsage)
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
			"of them: the point is to see what exists. Nothing is downloaded " +
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
			"write the LuaRocks manifest that indexes them, so the directory is " +
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
// prefix.
func tarantoolInfo() (manifestrocks.TarantoolInfo, error) {
	if cmdCtx.Cli.TarantoolCli.Executable == "" {
		return manifestrocks.TarantoolInfo{}, ErrNoTarantool
	}

	ver, err := cmdCtx.Cli.TarantoolCli.GetVersion()
	if err != nil {
		return manifestrocks.TarantoolInfo{}, err
	}

	prefix, err := oldrocks.GetTarantoolPrefix(&cmdCtx.Cli)
	if err != nil {
		return manifestrocks.TarantoolInfo{}, err
	}

	return manifestrocks.TarantoolInfo{
		Executable: cmdCtx.Cli.TarantoolCli.Executable,
		Prefix:     prefix,
		Version:    ver.Str,
	}, nil
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
