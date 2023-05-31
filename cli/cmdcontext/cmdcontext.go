package cmdcontext

// CmdCtx is the main structure of the program context.
// Contains within itself other structures of CLI modules.
type CmdCtx struct {
	// Cli - CLI context. Contains flags passed when starting
	// Tarantool CLI and some other parameters.
	Cli CliCtx
	// CommandName contains name of the command.
	CommandName string
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
	// Path to Tarantool CLI (tt.yaml) config.
	ConfigPath string
	// ConfigDir is tt configuration file directory.
	// And current working directory, if there is no config.
	ConfigDir string
	// Path to tt daemon (tt_daemon.yaml) config.
	DaemonCfgPath string
	// Path to Tarantool executable (detected if CLI launch is local).
	TarantoolExecutable string
	// Tarantool version.
	TarantoolVersion string
	// The flag determines if the tarantool binary is from the internal tt repository.
	IsTarantoolBinFromRepo bool
	// Verbose logging flag. Enables debug log output.
	Verbose bool
}
