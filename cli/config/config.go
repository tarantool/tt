package config

// CliOpts stores information about Tarantool CLI configuration.
// Filled in when parsing the tt.yaml configuration file.
//
// tt.yaml file format:
//  env:
//    instances_enabled: path
//    tarantoolctl_layout: false
//    restart_on_failure: bool
//  modules:
//    directory: path/to
//  app:
//    run_dir: path
//    log_dir: path
//    bin_dir: path
//    inc_dir: path
//  repo:
//    rocks: path
//    distfiles: path
//  ee:
//    credential_path: path

// ModuleOpts is used to store all module options.
type ModulesOpts struct {
	// Directories is a list of paths to directories where the external modules
	// are stored.
	Directories FieldStringArrayType `mapstructure:"directory" yaml:"directory"`
}

// EEOpts is used to store tarantool-ee options.
type EEOpts struct {
	// CredPath is a path to file with credentials for downloading tarantool-ee.
	CredPath string `mapstructure:"credential_path" yaml:"credential_path"`
}

// AppOpts is used to store all app options.
type AppOpts struct {
	// RunDir is a path to directory that stores various instance
	// runtime artifacts like console socket, PID file, etc.
	RunDir string `mapstructure:"run_dir" yaml:"run_dir"`
	// LogDir is a directory that stores log files.
	LogDir string `mapstructure:"log_dir" yaml:"log_dir"`
	// WalDir is a directory where write-ahead log (.xlog) files are stored.
	WalDir string `mapstructure:"wal_dir" yaml:"wal_dir"`
	// MemtxDir is a directory where memtx stores snapshot (.snap) files.
	MemtxDir string `mapstructure:"memtx_dir" yaml:"memtx_dir"`
	// VinylDir is a directory where vinyl files or subdirectories will be stored.
	VinylDir string `mapstructure:"vinyl_dir" yaml:"vinyl_dir"`
}

// TtEnvOpts is tt environment configuration. Everything that affects
// application building/starting, but applicable for all apps.
type TtEnvOpts struct {
	// BinDir is the directory where all the binary files
	// are stored.
	BinDir string `mapstructure:"bin_dir" yaml:"bin_dir"`
	// IncludeDir is the directory where all the header files
	// are stored.
	IncludeDir string `mapstructure:"inc_dir" yaml:"inc_dir"`
	// InstancesEnabled is the directory where all enabled applications are stored.
	InstancesEnabled string `mapstructure:"instances_enabled" yaml:"instances_enabled"`
	// Restartable - if set the instance is started under the watchdog it should
	// restart on if it crashes.
	Restartable bool `mapstructure:"restart_on_failure" yaml:"restart_on_failure"`
	// TarantoolctlLayout enables artifact files layout compatibility with tarantoolctl:
	// application sub-directories are not created for runtime artifacts like
	// control socket, pid files and logs.
	TarantoolctlLayout bool `mapstructure:"tarantoolctl_layout" yaml:"tarantoolctl_layout"`
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
	// Env is struct describing tt environment options.
	Env *TtEnvOpts
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
