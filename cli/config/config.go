package config

// Config used to store all information from the
// tarantool.yaml configuration file.
type Config struct {
	CliConfig *CliOpts `mapstructure:"tt"`
}

// CliOpts stores information about Tarantool CLI configuration.
// Filled in when parsing the tarantool.yaml configuration file.
//
// tarantool.yaml file format:
// tt:
//   modules:
//     directory: path/to
// app:
//     available: path
//     run_dir: path
//     log_dir: path
//     log_maxsize: num (MB)
//     log_maxage: num (Days)
//     log_maxbackups: num
//     restart_on_failure: bool

// ModuleOpts is used to store all module options.
type ModulesOpts struct {
	// Directory is a path to directory where the external modules
	// are stored.
	Directory string
}

// AppOpts is used to store all app options.
type AppOpts struct {
	// InstancesAvailable is a path to directory that stores all
	// available applications.
	InstancesAvailable string `mapstructure:"instances_available"`
	// RunDir is a path to directory that stores various instance
	// runtime artifacts like console socket, PID file, etc.
	RunDir string `mapstructure:"run_dir"`
	// LogDir is a directory that stores log files.
	LogDir string `mapstructure:"log_dir"`
	// LogMaxSize is a maximum size in MB of the log file before
	// it gets rotated.
	LogMaxSize int `mapstructure:"log_maxsize"`
	// LogMaxAge is the maximum number of days to retain old log files
	// based on the timestamp encoded in their filename. Note that a
	// day is defined as 24 hours and may not exactly correspond to
	// calendar days due to daylight savings, leap seconds, etc. The
	// default is not to remove old log files based on age.
	LogMaxAge int `mapstructure:"log_maxage"`
	// LogMaxBackups is the maximum number of old log files to retain.
	// The default is to retain all old log files (though LogMaxAge may
	// still cause them to get deleted).
	LogMaxBackups int `mapstructure:"log_maxbackups"`
	// If the instance is started under the watchdog it should
	// restart on if it crashes.
	Restartable bool `mapstructure:"restart_on_failure"`
	// DataDir is the directory where all the instance artifacts
	// are stored.
	DataDir string `mapstructure:"data_dir"`
}

// CliOpts is used to store modules and app options.
type CliOpts struct {
	// Modules is a struct that contain module options.
	Modules *ModulesOpts
	// App is a struct that contains app options.
	App *AppOpts
}
