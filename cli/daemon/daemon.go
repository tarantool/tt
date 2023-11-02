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
	// ListenInterface is a network interface the IP address
	// should be found on to bind http server socket.
	ListenInterface string
}

// NewDaemonCtx creates the DaemonCtx context.
func NewDaemonCtx(opts *config.DaemonOpts) *DaemonCtx {
	return &DaemonCtx{
		PIDFile: filepath.Join(opts.RunDir, opts.PIDFile),
		Port:    opts.Port,
		LogPath: filepath.Join(opts.LogDir, opts.LogFile),
	}
}

// RunHTTPServerOnBackground starts http daemon process.
func RunHTTPServerOnBackground(daemonCtx *DaemonCtx) error {
	logOpts := ttlog.LoggerOpts{
		Filename: daemonCtx.LogPath,
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
