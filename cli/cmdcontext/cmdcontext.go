package cmdcontext

import (
	"github.com/tarantool/tt/cli/config"
)

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
	// Pack contains information to pack an application.
	Pack PackCtx
	// Init contains information to init tt environment.
	Init InitCtx
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
	// WorkDir is stores original tt working directory, before making chdir
	// to local launch directory.
	WorkDir string
	// Verbose logging flag. Enables debug log output.
	Verbose bool
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
	// Language to use for execution.
	Language string
	// Interactive mode is used.
	Interactive bool
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

//BuildCtx contains information for application building.
type BuildCtx struct {
	// BuildDir is an application directory.
	BuildDir string
	// SpecFile is a rockspec file to be used for build.
	SpecFile string
}

// PackCtx contains all flags for tt pack command.
type PackCtx struct {
	// Type contains a type of packing.
	Type string
	// Name contains the name of packing bundle.
	Name string
	// Version contains the version of packing bundle.
	Version string
	// AppList contains applications to be packed.
	AppList []string
	// FileName contains the name of file of result package.
	FileName string
	// WithBinaries put binaries into the package regardless if tarantool is system or not.
	WithBinaries bool
	// WithoutBinaries ignores binaries regardless if tarantool is system or not.
	WithoutBinaries bool
	// TarantoolExecutable is a path to tarantool executable path
	TarantoolExecutable string
	// TarantoolIsSystem shows if tarantool is system.
	TarantoolIsSystem bool
	// ConfigPath is a full path to tarantool.yaml file.
	ConfigPath string
	// App contains info about bundle.
	App *config.AppOpts
	// ModulesDirectory contains a path to modules directory.
	ModulesDirectory string
	// ArchiveCtx contains flags specific for tgz type.
	Archive ArchiveCtx
	// RpmDeb contains all information about rpm and deb type of packing.
	RpmDeb RpmDebCtx
}

// ArchiveCtx contains flags specific for tgz type.
type ArchiveCtx struct {
	// All means pack all artifacts from bundle, including pid files etc.
	All bool
}

// RpmDebCtx contains flags specific for RPM/DEB type.
type RpmDebCtx struct {
	// WithTarantoolDeps means to add to package dependencies versions
	// of tt and tarantool from the current environment.
	WithTarantoolDeps bool
	// PreInst is a path to pre-install script.
	PreInst string
	// PostInst is a path to post-install script.
	PostInst string
	// Deps is dependencies list. Format:
	// dependency_06>=4
	Deps []string
	// DepsFile is a path to a file of dependencies.
	DepsFile string
}

// InitCtx contains information for tt config creation.
type InitCtx struct {
	// SkipConfig - if set, disables cartridge & tarantoolctl config analysis,
	// so init does not try to get directories information from exitsting config files.
	SkipConfig bool
}
