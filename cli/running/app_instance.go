package running

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"

	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/tt/v3/cli/app"
	"github.com/tarantool/tt/v3/cli/cluster"
	"github.com/tarantool/tt/v3/cli/running/internal/layout"
	"github.com/tarantool/tt/v3/cli/tarantool"
	"github.com/tarantool/tt/v3/cli/util"
	"github.com/tarantool/tt/v3/internal/binary"
	"github.com/tarantool/tt/v3/internal/instances"
)

// ErrUnsupportedKind is wrapped by the error NewInstanceCtx returns for an
// instance record it cannot run yet: a Tarantool instance configured by its
// script.
var ErrUnsupportedKind = errors.New("not supported yet")

var (
	errNoApplication = errors.New("no application")
	errNotAbsolute   = errors.New("is not an absolute path")
	// errRelativePath is wrapped by the error for a path in the config of an
	// installed instance that is relative to its version directory.
	errRelativePath = errors.New("relative paths in the config of an installed instance")
	// errOldLayout is wrapped by the error for a project whose data is still
	// where tt kept it before the scope layout, <project>/var/lib.
	errOldLayout = errors.New("the project keeps instance data in the old layout")
	// errUnknownScope is wrapped by the error for an instance context whose
	// scope tt binary has no counterpart for.
	errUnknownScope = errors.New("unknown scope")
)

// varLibDir is the directory of the data of the instances: under the scope
// root now, and under the project directory before the scope layout.
const varLibDir = "var/lib"

// writtenPathKeys are the keys of the config of a Tarantool instance that
// name a file or a directory the instance writes to. Relative, they are
// resolved against the working directory of the instance, its application
// directory.
var writtenPathKeys = []string{
	"wal.dir",
	"snapshot.dir",
	"vinyl.dir",
	"log.file",
	"console.socket",
	"process.pid_file",
}

// NewInstanceCtx builds the context of the instance record inst of
// application, as app.Load and app.Resolve return them: what the commands
// that start, stop, inspect and clean up an instance need from it. It reads
// nothing but what application holds.
//
// The runtime artifacts of the instance go under the root of the scope of
// the application (layout.ScopeLayout), and its working directory is the
// application directory: the project directory, or the version directory
// <root>/apps/<app> pointed to when the application was loaded, never the
// link itself, so that an instance keeps running the version it started from
// when the link moves. The config of the instance has the last word on the
// paths it sets, as it always has: wal.dir, snapshot.dir, vinyl.dir and
// console.socket, relative ones resolved against the application directory,
// which is what tarantool resolves them against.
//
// What may stop an instance from starting, such as a relative path in the
// config of an installed one, is not checked here but at its start
// (IsAbleToStartInstances, Start, RunInstance and the Spec of every start):
// the context of an instance that cannot start is still the one to stop,
// inspect and clean it with.
//
// An external process, a record with binary, keeps its log and its pid files
// in the same layout. It runs in workdir of its record, or else in its data
// directory in the layout, the one data directory it has. It has no sockets
// and no config, so tt status asks it nothing.
//
// Tarantool instances configured by their script do not run yet; a record
// with script is an error wrapping ErrUnsupportedKind.
func NewInstanceCtx(application *app.App, inst instances.Instance) (InstanceCtx, error) {
	if application == nil {
		return InstanceCtx{}, fmt.Errorf("instance %q: %w", inst.Name, errNoApplication)
	}

	switch inst.Kind() {
	case instances.KindScript:
		return InstanceCtx{}, fmt.Errorf("instance %q of %q: an instance that runs its script "+
			"(script: %s) is %w", inst.Name, application.Name, inst.Script, ErrUnsupportedKind)
	case instances.KindExternal, instances.KindCluster:
	}

	for _, dir := range [...]struct{ what, path string }{
		{"application directory", application.Dir},
		{"scope root", application.Root},
	} {
		if !filepath.IsAbs(dir.path) {
			return InstanceCtx{}, fmt.Errorf("application %q: the %s %q %w",
				application.Name, dir.what, dir.path, errNotAbsolute)
		}
	}

	wrap := func(err error) error {
		return fmt.Errorf("instance %q of %q: %w", inst.Name, application.Name, err)
	}

	scopeLayout, err := layout.NewScopeLayout(application.Root, application.Name, inst.Name)
	if err != nil {
		return InstanceCtx{}, wrap(err)
	}

	instCtx := InstanceCtx{
		AppDir:            application.Dir,
		InstanceScript:    "",
		Kind:              inst.Kind(),
		Binary:            "",
		Args:              nil,
		Workdir:           "",
		DataDir:           "",
		LogrotateSignal:   0,
		AppName:           application.Name,
		InstName:          inst.Name,
		RunDir:            scopeLayout.RunDir(),
		LogDir:            scopeLayout.LogDir(),
		Log:               scopeLayout.LogFile(),
		WalDir:            scopeLayout.DataDir(),
		MemtxDir:          scopeLayout.DataDir(),
		VinylDir:          scopeLayout.DataDir(),
		LogMaxSize:        0,
		LogMaxBackups:     0,
		LogMaxAge:         0,
		PIDFile:           scopeLayout.PidFile(),
		ProcessPIDFile:    "",
		Restart:           inst.Restart,
		StopSignal:        inst.StopSignal,
		StopTimeout:       inst.StopTimeout,
		ConsoleSocket:     scopeLayout.ConsoleSocket(),
		BinaryPort:        scopeLayout.BinaryPort(),
		ClusterConfigPath: application.Pair.ConfigFile,
		Configuration:     nil,
		Scope:             application.Scope,
		Root:              application.Root,
		Template:          "",
		Env:               maps.Clone(inst.Env),
	}

	if inst.Kind() == instances.KindExternal {
		setExternalCtx(&instCtx, inst, scopeLayout)

		return instCtx, nil
	}

	if application.Config != nil {
		cfg, err := cluster.GetInstanceConfig(application.Config, inst.Name)
		if err != nil {
			return InstanceCtx{}, wrap(err)
		}

		instCtx.Configuration = &cfg
	}

	err = setInstCtxFromEffectiveConfig(&instCtx)
	if err != nil {
		return InstanceCtx{}, wrap(err)
	}

	err = renderInstCtxMembers(&instCtx)
	if err != nil {
		return InstanceCtx{}, wrap(err)
	}

	return instCtx, nil
}

// setInstCtxFromEffectiveConfig sets the paths the effective config of
// inst names, resolved against its application directory, over the paths of
// the layout: the config has the last word on where its instance writes.
func setInstCtxFromEffectiveConfig(inst *InstanceCtx) error {
	for _, target := range [...]struct {
		key  string
		dest *string
	}{
		{"wal.dir", &inst.WalDir},
		{"vinyl.dir", &inst.VinylDir},
		{"snapshot.dir", &inst.MemtxDir},
		{"console.socket", &inst.ConsoleSocket},
	} {
		value, found, err := effectiveString(inst, target.key)
		if err != nil {
			return err
		}

		if !found {
			continue
		}

		*target.dest, err = util.JoinAbspath(inst.AppDir, value)
		if err != nil {
			return err
		}
	}

	return nil
}

// effectiveString returns the value of the config key, dotted as in
// wal.dir, that tarantool will run inst with: the TT_ variable of the key in
// the env of the instance record, which tarantool ranks above the config
// files, or else the config of the instance. found is false when neither
// sets it; an empty value counts as unset, as tarantool takes it.
//
// The variables the environment of tt sets are in the config already:
// cluster.GetClusterConfig reads them the way tarantool does.
func effectiveString(inst *InstanceCtx, key string) (string, bool, error) {
	envName := "TT_" + strings.ToUpper(strings.ReplaceAll(key, ".", "_"))
	if value, ok := inst.Env[envName]; ok {
		return value, value != "", nil
	}

	if inst.Configuration == nil {
		return "", false, nil
	}

	var raw any

	_, err := inst.Configuration.Get(goconfig.NewKeyPath(strings.ReplaceAll(key, ".", "/")), &raw)
	if errors.Is(err, goconfig.ErrKeyNotFound) {
		return "", false, nil
	}

	if err != nil {
		return "", false, fmt.Errorf("getting %q from the config: %w", key, err)
	}

	value, ok := raw.(string)
	if !ok {
		return "", false, fmt.Errorf("%w%q as %T", errCannotGetConfigValueAtAs, key, value)
	}

	return value, value != "", nil
}

// checkStart checks what has to hold before an instance starts.
//
// It runs before the directories of the instance are created, which would
// hide data left in the old layout, and again before every start of the
// watchdog, since a restart reads a config that may have changed. Every
// problem is reported at once, so that one pass fixes them. An external
// process has checks of its own (checkExternal): the TT_ variables in the env
// of its record are not a config of tarantool.
func checkStart(inst *InstanceCtx) error {
	if IsExternal(inst) {
		return checkExternal(inst)
	}

	_, _, socketErr := socketArgs(inst)

	return errors.Join(checkOldLayout(inst), checkRelativePaths(inst), socketErr)
}

// checkOldLayout refuses to start an instance of a project that still keeps
// the data of its instances where tt did before, <project>/var/lib,
// while nothing is in <project>/.tarantool/var/lib yet.
//
// The instance would start on an empty database next to its data, and the
// data would look lost. Moving it is not done for the user: whether the
// directories hold the data meant, and where each instance goes, is theirs
// to say. An instance whose config puts all of its data elsewhere is let
// through: it does not use the new tree, so starting it moves nothing.
func checkOldLayout(inst *InstanceCtx) error {
	if inst.Scope != app.ScopeProject {
		return nil
	}

	newData := filepath.Join(inst.Root, varLibDir)
	if !slices.ContainsFunc([]string{inst.WalDir, inst.MemtxDir, inst.VinylDir},
		func(dir string) bool { return isWithin(dir, newData) }) {
		return nil
	}

	oldData := filepath.Join(inst.AppDir, varLibDir)

	oldInfo, err := os.Stat(oldData)
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR) {
		return nil
	}

	if err != nil {
		return fmt.Errorf("cannot look for the data of the old layout: %w", err)
	}

	if !oldInfo.IsDir() {
		return nil
	}

	_, err = os.Lstat(newData)

	switch {
	case err == nil:
		return nil
	case !errors.Is(err, fs.ErrNotExist):
		return fmt.Errorf("cannot look for the data of the new layout: %w", err)
	}

	return fmt.Errorf("instance %q of %q: %w: %s exists and %s does not; "+
		"the instances of a project keep their data in %s now, and tt does not move it: "+
		"move each %s to %s, or remove %s if its data is not needed",
		inst.InstName, inst.AppName, errOldLayout, oldData, newData,
		filepath.Join(newData, "<app>", "<instance>"),
		filepath.Join(oldData, "<instance>"),
		filepath.Join(newData, inst.AppName, "<instance>"), oldData)
}

// checkRelativePaths refuses a relative path in writtenPathKeys in the
// effective config of an instance installed into the user or the system
// scope.
//
// An installed instance runs in its version directory, and a relative path
// would have it write into that version, which is immutable: the next
// version would not have the data, and removing the version would remove it.
// In a project a relative path is resolved against the project directory,
// like everything else in the project, and is fine.
func checkRelativePaths(inst *InstanceCtx) error {
	if inst.Scope == app.ScopeProject {
		return nil
	}

	var relative []string

	for _, key := range writtenPathKeys {
		value, found, err := effectiveString(inst, key)
		if err != nil {
			return err
		}

		if found && !filepath.IsAbs(value) {
			relative = append(relative, fmt.Sprintf("%s %q", key, value))
		}
	}

	if len(relative) == 0 {
		return nil
	}

	return fmt.Errorf("instance %q of %q: %w: %s; the instance runs in %s, which belongs "+
		"to the installed version and is never written to: make the paths absolute, "+
		"or remove them to keep the files under %s", inst.InstName, inst.AppName,
		errRelativePath, strings.Join(relative, ", "), inst.AppDir, inst.Root)
}

// isWithin reports whether path is dir or lies under it.
func isWithin(path, dir string) bool {
	rel, err := filepath.Rel(dir, path)

	return err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// socketArgs returns the console socket and the iproto listen setting
// tarantool gets, as TT_CONSOLE_SOCKET_DEFAULT and TT_IPROTO_LISTEN_DEFAULT.
//
// A socket path that is too long for a unix socket address is given
// relative to the working directory of tarantool where tarantool takes it
// that way: the iproto listen URI whatever the path, and the console socket only
// inside the working directory, since the config of tarantool turns a
// console socket outside of it back into an absolute path
// (rebase_file_path). So in a project, whose root is in the project
// directory, a long path is shortened; in the user and system scopes the
// run directory is outside of the version, and a console socket too long
// there is an error. Either way a path that cannot be bound is an error
// naming it, rather than a failure to bind inside tarantool.
func socketArgs(inst *InstanceCtx) (string, string, error) {
	console, err := appSocketPath(inst.ConsoleSocket, inst.AppDir, false)
	if err != nil {
		return "", "", fmt.Errorf("instance %q of %q: the console socket: %w",
			inst.InstName, inst.AppName, err)
	}

	uri, err := appSocketPath(inst.BinaryPort, inst.AppDir, true)
	if err != nil {
		return "", "", fmt.Errorf("instance %q of %q: the binary port: %w",
			inst.InstName, inst.AppName, err)
	}

	// The URI parser of tarantool takes a relative unix socket only behind
	// unix/:, and only with a leading ./: a bare "../x" is not a URI, and
	// "./../x" alone loses its dot.
	if !filepath.IsAbs(uri) {
		uri = "./" + uri

		err = verifySocketLength(uri)
		if err != nil {
			return "", "", fmt.Errorf("instance %q of %q: the binary port: %w",
				inst.InstName, inst.AppName, err)
		}

		uri = "unix/:" + uri
	}

	listen, err := json.Marshal([]map[string]string{{"uri": uri}})
	if err != nil {
		return "", "", fmt.Errorf("encoding the iproto listen setting: %w", err)
	}

	return console, string(listen), nil
}

// appSocketPath returns socketPath when it fits a unix socket address, and
// the path relative to workDir, the working directory of tarantool, when it
// does not. The relative path may climb out of workDir with ".." only when
// outside is set.
//
// The relative path is taken between the physical paths, with the links
// resolved: the kernel resolves ".." in the physical tree, so ".." out of a
// directory reached through a link would lead elsewhere than the path says,
// and tarantool compares a path with its working directory as getcwd
// reports it, with the links resolved.
func appSocketPath(socketPath, workDir string, outside bool) (string, error) {
	if verifySocketLength(socketPath) == nil {
		return socketPath, nil
	}

	const hint = "shorten the scope root, the application name or the instance name"

	rel, err := filepath.Rel(physicalPath(workDir), physicalPath(socketPath))
	if err != nil {
		return "", fmt.Errorf("shortening the socket path %q: %w", socketPath, err)
	}

	if !outside && !filepath.IsLocal(rel) {
		return "", fmt.Errorf("%w%d symbols: %q, and it is outside of the working "+
			"directory %s, where tarantool binds it by the full path; %s",
			errSocketPathIsLongerThanSymbols, socketPathLimit()-1, socketPath, workDir, hint)
	}

	err = verifySocketLength(rel)
	if err != nil {
		return "", fmt.Errorf("%w; %s", err, hint)
	}

	return rel, nil
}

// physicalPath returns path with the links in it resolved, as far as it
// exists; the part that does not exist yet is joined as it is.
func physicalPath(path string) string {
	var missing []string

	for dir := filepath.Clean(path); ; {
		resolved, err := filepath.EvalSymlinks(dir)
		if err == nil {
			return filepath.Join(append([]string{resolved}, missing...)...)
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			return path
		}

		missing = append([]string{filepath.Base(dir)}, missing...)
		dir = parent
	}
}

// resolveTarantool chooses the tarantool an instance built from the
// application model runs on, by the rule tt run follows too
// (tarantool.Resolve). It is asked at every start rather than once when the
// context is built: stopping or inspecting an instance must not depend on
// its tarantool still being installed.
func resolveTarantool(inst *InstanceCtx) (tarantool.Executable, error) {
	var scope binary.Scope

	switch inst.Scope {
	case app.ScopeProject:
		scope = binary.ScopeProject
	case app.ScopeUser:
		scope = binary.ScopeUser
	case app.ScopeSystem:
		scope = binary.ScopeSystem
	default:
		return tarantool.Executable{}, fmt.Errorf("instance %q of %q: %w %q",
			inst.InstName, inst.AppName, errUnknownScope, inst.Scope)
	}

	exe, err := tarantool.Resolve(tarantool.App{Scope: scope, Dir: inst.AppDir})
	if err != nil {
		return tarantool.Executable{}, fmt.Errorf("instance %q of %q: %w",
			inst.InstName, inst.AppName, err)
	}

	return exe, nil
}
