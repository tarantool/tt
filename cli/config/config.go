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

type ModulesOpts struct {
	Directory string
}

type AppOpts struct {
	InstancesAvailable string `mapstructure:"instances_available"`
	RunDir             string `mapstructure:"run_dir"`
	LogDir             string `mapstructure:"log_dir"`
	LogMaxSize         int    `mapstructure:"log_maxsize"`
	LogMaxAge          int    `mapstructure:"log_maxage"`
	LogMaxBackups      int    `mapstructure:"log_maxbackups"`
	Restartable        bool   `mapstructure:"restart_on_failure"`
	DataDir            string `mapstructure:"data_dir"`
}

type CliOpts struct {
	Modules *ModulesOpts
	App     *AppOpts
}
