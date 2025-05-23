package tcm

// LoggerOpts contains configuration for displaying TCM logs.
type LoggerOpts struct {
	// Level used to start TCM application.
	Level string
	// Lines number of tail lines to show in logs.
	Lines int
	// IsFollow indicates whether to monitor the logs.
	IsFollow bool
	// NoColor disables colored output in logs.
	NoColor bool
	// ForceColor forces colored output in logs, even if the output stream does not support it.
	// This is useful for testing purposes.
	ForceColor bool
	// NoFormat indicates whether to disable log formatting.
	NoFormat bool
}

// TcmCtx holds parameters and state for managing the TCM process and its watchdog.
type TcmCtx struct {
	// Path to the TCM executable file.
	Executable string
	// Flag indicating whether the watchdog is enabled.
	Watchdog bool
	// Path to the file storing the watchdog process PID.
	WatchdogPidFile string
	// Log configuration to display TCM logs.
	Log LoggerOpts
}
