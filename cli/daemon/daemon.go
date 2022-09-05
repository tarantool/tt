package daemon

import (
	"log"
	"os"
	"path/filepath"

	"github.com/tarantool/tt/cli/config"
	"github.com/tarantool/tt/cli/process_utils"
	"github.com/tarantool/tt/cli/ttlog"
)

// DaemonCtx contains information for running an daemon instance.
type DaemonCtx struct {
	// Port is a port number to be used for daemon http server.
	Port int
	// PIDFile is a path of a file contains pid of daemon process.
	PIDFile string
	// LogPath is a path to a file contains log of daemon process.
	LogPath string
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
	// ListenInterface is a network interface the IP address
	// should be found on to bind http server socket.
	ListenInterface string
}

// NewDaemonCtx creates the DaemonCtx context.
func NewDaemonCtx(opts *config.DaemonOpts) *DaemonCtx {
	return &DaemonCtx{
		PIDFile:       filepath.Join(opts.RunDir, opts.PIDFile),
		Port:          opts.Port,
		LogPath:       filepath.Join(opts.LogDir, opts.LogFile),
		LogMaxAge:     opts.LogMaxAge,
		LogMaxBackups: opts.LogMaxBackups,
		LogMaxSize:    opts.LogMaxSize,
	}
}

// RunHTTPServerOnBackground starts http daemon process.
func RunHTTPServerOnBackground(daemonCtx *DaemonCtx) error {
	logOpts := ttlog.LoggerOpts{
		Filename:   daemonCtx.LogPath,
		MaxSize:    daemonCtx.LogMaxSize,
		MaxBackups: daemonCtx.LogMaxBackups,
		MaxAge:     daemonCtx.LogMaxAge,
	}

	args := []string{"daemon", "start"}
	proc := NewProcess(NewHTTPServer(daemonCtx.ListenInterface, daemonCtx.Port),
		daemonCtx.PIDFile, logOpts).CmdPath(os.Args[0]).CmdArgs(args)

	if err := proc.Start(); err != nil {
		return err
	}

	log.Printf("Starting tt daemon...")

	return nil
}

// StopDaemon starts http daemon process.
func StopDaemon(daemonCtx *DaemonCtx) error {
	pid, err := process_utils.StopProcess(daemonCtx.PIDFile)
	if err != nil {
		return err
	}

	log.Printf("The Daemon (PID = %v) has been terminated.\n", pid)

	return nil
}
