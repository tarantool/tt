package context

// Ctx is the main structure of the program context.
// Contains within itself other structures of CLI modules.
type Ctx struct {
	Cli     CliCtx
	Running RunningCtx
}

// CliCtx - CLI context. Contains flags passed when starting
// Tarantool CLI and some other parameters.
type CliCtx struct {
	// Is CLI launch system (or local).
	IsSystem bool
	// Use internal module even if an external one is found.
	ForceInternal bool
	// Path to local directory (or empty string if CLI launch is system).
	LocalLaunchDir string
	// Path to Tarantool CLI (tarantool.yaml) config.
	ConfigPath string
	// Path to Tarantool executable (detected if CLI launch is local).
	TarantoolExecutable string
}

// RunningCtx contain information for running an application instance.
type RunningCtx struct {
	// Path to an application.
	AppPath string
	// Directory that stores various instance runtime artifacts like
	// console socket, PID file, etc.
	RunDir string
	// The name of the file with the watchdog PID under which the
	// instance was started.
	PIDFile string
	// If the instance is started under the watchdog it should
	// restart on if it crashes.
	Restartable bool
	// Control UNIX socket for started instance.
	ConsoleSocket string
}
