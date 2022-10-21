package config

// Config used to store all information from the
// tarantool.yaml configuration file.
type Config struct {
	CliConfig *CliOpts `mapstructure:"tt" yaml:"tt"`
}

// CliOpts stores information about Tarantool CLI configuration.
// Filled in when parsing the tarantool.yaml configuration file.
//
// tarantool.yaml file format:
// tt:
//   modules:
//     directory: path/to
//   app:
//     available: path
//     run_dir: path
//     log_dir: path
//     log_maxsize: num (MB)
//     log_maxage: num (Days)
//     log_maxbackups: num
//     restart_on_failure: bool
//     bin_dir: path
//     inc_dir: path
//   repo:
//     rocks: path
//     distfiles: path
//   ee:
//     credential_path: path

// ModuleOpts is used to store all module options.
type ModulesOpts struct {
	// Directory is a path to directory where the external modules
	// are stored.
	Directory string
}

// EEOpts is used to store tarantool-ee options.
type EEOpts struct {
	// CredPath is a path to file with credentials for downloading tarantool-ee.
	CredPath string `mapstructure:"credential_path" yaml:"credential_path"`
}

// AppOpts is used to store all app options.
type AppOpts struct {
	// InstancesAvailable is a path to directory that stores all
	// available applications.
	InstancesAvailable string `mapstructure:"instances_available" yaml:"instances_available"`
	// RunDir is a path to directory that stores various instance
	// runtime artifacts like console socket, PID file, etc.
	RunDir string `mapstructure:"run_dir" yaml:"run_dir"`
	// LogDir is a directory that stores log files.
	LogDir string `mapstructure:"log_dir" yaml:"log_dir"`
	// LogMaxSize is a maximum size in MB of the log file before
	// it gets rotated.
	LogMaxSize int `mapstructure:"log_maxsize" yaml:"log_maxsize"`
	// LogMaxAge is the maximum number of days to retain old log files
	// based on the timestamp encoded in their filename. Note that a
	// day is defined as 24 hours and may not exactly correspond to
	// calendar days due to daylight savings, leap seconds, etc. The
	// default is not to remove old log files based on age.
	LogMaxAge int `mapstructure:"log_maxage" yaml:"log_maxage"`
	// LogMaxBackups is the maximum number of old log files to retain.
	// The default is to retain all old log files (though LogMaxAge may
	// still cause them to get deleted).
	LogMaxBackups int `mapstructure:"log_maxbackups" yaml:"log_maxbackups"`
	// If the instance is started under the watchdog it should
	// restart on if it crashes.
	Restartable bool `mapstructure:"restart_on_failure" yaml:"restart_on_failure"`
	// DataDir is the directory where all the instance artifacts
	// are stored.
	DataDir string `mapstructure:"data_dir" yaml:"data_dir"`
	// BinDir is the directory where all the binary files
	// are stored.
	BinDir string `mapstructure:"bin_dir" yaml:"bin_dir"`
	// IncludeDir is the directory where all the header files
	// are stored.
	IncludeDir string `mapstructure:"inc_dir" yaml:"inc_dir"`
	// InstancesEnabled is the directory where all enabled applications are stored.
	InstancesEnabled string `mapstructure:"instances_enabled" yaml:"instances_enabled"`
}

// TemplateOpts contains configuration for applications templates.
type TemplateOpts struct {
	// Path is a directory to search template in.
	Path string `mapstructure:"path"`
}

// RepoOpts is a struct used to store paths to local files.
type RepoOpts struct {
	// Rocks is the directory where local rocks files could be found.
	Rocks string `mapstructure:"rocks"`
	// Install is the directory where local installation files could be found.
	Install string `mapstructure:"distfiles" yaml:"distfiles"`
}

// CliOpts is used to store modules and app options.
type CliOpts struct {
	// Modules is a struct that contain module options.
	Modules *ModulesOpts
	// App is a struct that contains app options.
	App *AppOpts
	// EE is a struct that contains tarantool-ee options.
	EE *EEOpts
	// Templates options.
	Templates []TemplateOpts
	// Repo is a struct used to store paths to local files.
	Repo *RepoOpts
}
