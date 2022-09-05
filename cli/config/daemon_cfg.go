package config

// DaemonCfg used to store all information from the
// tt_daemon.yaml configuration file.
type DaemonCfg struct {
	DaemonConfig *DaemonOpts `mapstructure:"daemon" yaml:"daemon"`
}

// DaemonOpts stores information about tt daemon configuration.
// Filled in when parsing the tt_daemon.yaml configuration file.
//
// tt_daemon.yaml file format:
// daemon:
//
//	run_dir: path
//	log_dir: path
//	log_maxsize: num (MB)
//	log_maxage: num (Days)
//	log_maxbackups: num
//	log_file: string (file name)
//	listen_interface: string
//	port: num
//	pidfile: string (file name)
type DaemonOpts struct {
	// PIDFile is name of file contains pid of daemon process.
	PIDFile string `mapstructure:"pidfile"`
	// Port is a port number to be used for daemon http server.
	Port int `mapstructure:"port"`
	// LogDir is a directory that stores log files.
	LogDir string `mapstructure:"log_dir"`
	// LogFile is a name of file contains log of daemon process.
	LogFile string `mapstructure:"log_file"`
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
	// ListenInterface is a network interface the IP address
	// should be found on to bind http server socket.
	ListenInterface string `mapstructure:"listen_interface"`
	// RunDir is a path to directory that stores various instance
	// runtime artifacts like console socket, PID file, etc.
	RunDir string `mapstructure:"run_dir" yaml:"run_dir"`
}
