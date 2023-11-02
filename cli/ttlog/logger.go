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
	// Prefix is a log message prefix.
	Prefix string
}

// Logger represents an active logging object.
// A Logger can be used simultaneously from multiple goroutines;
// it guarantees serialize access to the Writer.
type Logger struct {
	// Embedded logger, the functionality of which will be extended.
	*log.Logger
	// ljLogger is an io.WriteCloser that writes to the specified filename.
	// Used to add logrotate functionality to log.Logger.
	ljLogger *lumberjack.Logger
	// opts describes the parameters that were used to create the logger.
	opts LoggerOpts
}

// NewLogger creates a new object of Logger.
func NewLogger(opts LoggerOpts) *Logger {
	ljLogger := &lumberjack.Logger{
		Filename:  opts.Filename,
		Compress:  false,
		LocalTime: true,
	}
	return &Logger{Logger: log.New(ljLogger, opts.Prefix, log.Flags()), ljLogger: ljLogger,
		opts: opts}
}

// NewCustomLogger creates a new logger object with custom `writer`, `prefix`
// and `flags`. Rotation does not work in this case. Such logger is widely
// used in tests.
func NewCustomLogger(writer io.Writer, prefix string, flags int) *Logger {
	return &Logger{Logger: log.New(writer, "", flags), ljLogger: nil}
}

// Rotate reopens the log file.
func (logger *Logger) Rotate() error {
	if logger.ljLogger == nil {
		return nil
	}

	savedLjLogger := logger.ljLogger
	newLjLogger := &lumberjack.Logger{
		Filename:  logger.opts.Filename,
		Compress:  false,
		LocalTime: true,
	}
	logger.Logger = log.New(newLjLogger, logger.opts.Prefix, log.Flags())
	logger.Println("(INFO) log file has been reopened")

	if err := savedLjLogger.Close(); err != nil {
		logger.Printf("(ERROR) failed to close previous log file: %s", err.Error())
	}
	return nil
}

// GetOpts returns the parameters that were used to create the logger.
func (logger *Logger) GetOpts() LoggerOpts {
	return logger.opts
}

// Close implements io.Closer, and closes the current logfile.
func (logger *Logger) Close() error {
	if logger.ljLogger == nil {
		return nil
	}

	return logger.ljLogger.Close()
}
