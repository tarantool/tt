package layout

// Layout is an interface for file path generation.
type Layout interface {
	// PidFile returns pid file path.
	PidFile(dir string) string
	// ConsoleSocket returns console file path.
	ConsoleSocket(dir string) string
	// LogFile returns log file path.
	LogFile(dir string) string
	// DataDir returns data directory path.
	DataDir(dir string) string
	// BinaryPort returns binary port file path.
	BinaryPort(dir string) string
}
