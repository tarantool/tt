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
	// Path to Tarantool CLI (tarantool.yaml) config.
	ConfigPath string
	// Path to Tarantool executable (detected if CLI launch is local).
	TarantoolExecutable string
	// Tarantool version.
	TarantoolVersion string
	// Tarantool install prefix path.
	TarantoolInstallPrefix string
	// Path to header files supplied with tarantool.
	TarantoolIncludeDir string
	// The flag determines if the tarantool binary is from the internal tt repository.
	IsTarantoolBinFromRepo bool
	// Verbose logging flag. Enables debug log output.
	Verbose bool
}
