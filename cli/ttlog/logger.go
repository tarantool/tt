package ttlog

import (
	"io"
	"log"

	"gopkg.in/natefinch/lumberjack.v2"
)

// LoggerOpts describes the logger options.
type LoggerOpts struct {
	// Filename is the name of log file.
	Filename string
	// MaxSize is the maximum size in megabytes of the log file
	// before it gets rotated.
	MaxSize int
	// MaxBackups is the maximum number of old log files to retain.
	MaxBackups int
	// MaxAge is the maximum number of days to retain old log files
	// based on the timestamp encoded in their filename.
	MaxAge int
}

// Logger represents an active logging object.
// Decorator https://pkg.go.dev/log#Logger .
// A Logger can be used simultaneously from multiple goroutines;
// it guaranteesto serialize access to the Writer.
type Logger struct {
	// Embedded logger, the functionality of which will be extended.
	*log.Logger
	// ljLogger is an io.WriteCloser that writes to the specified filename.
	// Used to add logrotate functionality to log.Logger.
	ljLogger *lumberjack.Logger
	// opts describes the parameters that were used to create the logger.
	opts *LoggerOpts
}

// NewLogger creates a new object of Logger.
func NewLogger(opts *LoggerOpts) *Logger {
	ljLogger := &lumberjack.Logger{
		Filename:   opts.Filename,
		MaxSize:    opts.MaxSize,
		MaxBackups: opts.MaxBackups,
		MaxAge:     opts.MaxAge,
		Compress:   false,
		LocalTime:  true,
	}
	return &Logger{Logger: log.New(ljLogger, "", log.Flags()), ljLogger: ljLogger, opts: opts}
}

// NewCustomLogger creates a new logger object with custom `writer`, `prefix`
// and `flags`. Rotation does not work in this case. Such logger is widely
// used in tests.
func NewCustomLogger(writer io.Writer, prefix string, flags int) *Logger {
	return &Logger{Logger: log.New(writer, "", flags), ljLogger: nil}
}

// Rotate causes Logger to close the existing log file and immediately create a
// new one. After rotating, this initiates compression and removal of old log
// files according to the configuration.
func (logger *Logger) Rotate() error {
	if logger.ljLogger == nil {
		return nil
	}

	return logger.ljLogger.Rotate()
}

// GetOpts returns the parameters that were used to create the logger.
func (logger *Logger) GetOpts() *LoggerOpts {
	return logger.opts
}

// Close implements io.Closer, and closes the current logfile.
func (logger *Logger) Close() error {
	if logger.ljLogger == nil {
		return nil
	}

	return logger.ljLogger.Close()
}
