package tcm

// TcmCtx holds parameters and state for managing the TCM process and its watchdog.
type TcmCtx struct {
	// Path to the TCM executable file.
	Executable string
	// Path to the file storing the TCM process PID.
	TcmPidFile string
	// Flag indicating whether the watchdog is enabled.
	Watchdog bool
	// Path to the file storing the watchdog process PID.
	WathdogPidFile string
}
