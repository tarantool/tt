package tcm

type TcmCtx struct {
	Executable string
	TcmPidFile string

	Watchdog       bool
	WathdogPidFile string
}
