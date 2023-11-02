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
	// ListenInterface is a network interface the IP address
	// should be found on to bind http server socket.
	ListenInterface string `mapstructure:"listen_interface"`
	// RunDir is a path to directory that stores various instance
	// runtime artifacts like console socket, PID file, etc.
	RunDir string `mapstructure:"run_dir" yaml:"run_dir"`
}
