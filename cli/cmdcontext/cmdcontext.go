package cmdcontext

// CmdCtx is the main structure of the program context.
// Contains within itself other structures of CLI modules.
type CmdCtx struct {
	// Cli - CLI context. Contains flags passed when starting
	// Tarantool CLI and some other parameters.
	Cli CliCtx
	// Running contains information for running an application instance.
	Running []RunningCtx
	// Connect contains information for connecting to the instance.
	Connect ConnectCtx
	// CommandName contains name of the command.
	CommandName string
	// Create contains information to create an application.
	Create CreateCtx
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
	// WorkDir is stores original tt working directory, before making chdir
	// to local launch directory.
	WorkDir string
}

// RunningCtx contain information for running an application instance.
type RunningCtx struct {
	// Path to an application.
	AppPath string
	// AppName contains the name of the application as it was passed on start.
	AppName string
	// Instance name.
	InstName string
	// Directory that stores various instance runtime artifacts like
	// console socket, PID file, etc.
	RunDir string
	// Directory that stores log files.
	LogDir string
	// Log is the name of log file.
	Log string
	// DataDir is the directory where all the instance artifacts
	// are stored.
	DataDir string
	// LogMaxSize is the maximum size in megabytes of the log file
	// before it gets rotated. It defaults to 100 megabytes.
	LogMaxSize int
	// LogMaxBackups is the maximum number of old log files to retain.
	// The default is to retain all old log files (though LogMaxAge may
	// still cause them to get deleted).
	LogMaxBackups int
	// LogMaxAge is the maximum number of days to retain old log files
	// based on the timestamp encoded in their filename. Note that a
	// day is defined as 24 hours and may not exactly correspond to
	// calendar days due to daylight savings, leap seconds, etc. The
	// default is not to remove old log files based on age.
	LogMaxAge int
	// The name of the file with the watchdog PID under which the
	// instance was started.
	PIDFile string
	// If the instance is started under the watchdog it should
	// restart on if it crashes.
	Restartable bool
	// Control UNIX socket for started instance.
	ConsoleSocket string
	// True if this is a single instance application (no instances.yml).
	SingleApp bool
}

// ConnectCtx contains information for connecting to the instance.
type ConnectCtx struct {
	// Username of the tarantool user.
	Username string
	// Password of the tarantool.user.
	Password string
	// SrcFile describes the source of code for the evaluation.
	SrcFile string
}

// CreateCtx contains information for creating applications from templates.
type CreateCtx struct {
	// AppName is application name to create.
	AppName string
	// WorkDir is tt launch working directory.
	WorkDir string
	// DestinationDir is the path where an application will be created.
	DestinationDir string
	// TemplateSearchPaths is a set of path to search for a template.
	TemplateSearchPaths []string
	// TemplateName is a template to use for application creation.
	TemplateName string
	// VarsFromCli base directory for instances available.
	VarsFromCli []string
	// ForceMode - if flag is set, remove application existing application directory.
	ForceMode bool
	// SilentMode if set, disables user interaction. All invalid format errors fail
	// app creation.
	SilentMode bool
	// VarsFile is a file with variables definitions.
	VarsFile string
	// ConfigLocation stores config file location.
	ConfigLocation string
}
