package ttlog

import (
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
)

const (
	logOpenFlags   = os.O_CREATE | os.O_WRONLY | os.O_APPEND
	logCreatePerms = 0o640
)

// LoggerOpts describes the logger options.
type LoggerOpts struct {
	// Filename is the name of log file.
	Filename string
	// Prefix is a log message prefix.
	Prefix string
}

type Logger interface {
	// Logger API.
	Printf(format string, v ...any)
	Println(v ...any)
	Fatal(v ...any)
	Fatalf(format string, v ...any)
	Write(p []byte) (int, error)

	// Rotate re-opens a log file.
	Rotate() error
	// GetOpts returns the logger options that were used during creation.
	GetOpts() LoggerOpts
	// Close closes a log file.
	Close() error
}

// writerLogger is a thin log.Logger wrapper providing the Logger interface.
type writerLogger struct {
	*log.Logger
}

// NewCustomLogger creates a new logger object with custom `writer`, `prefix`
// and `flags`. Rotation does not work in this case.
func NewCustomLogger(writer io.Writer, prefix string, flags int) Logger {
	return &writerLogger{
		Logger: log.New(writer, prefix, flags),
	}
}

// Write implements io.Writer interface.
func (logger *writerLogger) Write(p []byte) (int, error) {
	return logger.Logger.Writer().Write(p)
}

// Rotate is no-op for custom logger.
func (logger *writerLogger) Rotate() error {
	return nil
}

// GetOpts returns the parameters that were used to create the logger.
func (logger *writerLogger) GetOpts() LoggerOpts {
	return LoggerOpts{Prefix: logger.Logger.Prefix()}
}

// Close implements io.Closer, no-op.
func (logger *writerLogger) Close() error {
	return nil
}

// fileLogger is a logger that writes log messages to the file.
type fileLogger struct {
	*log.Logger

	logFile io.WriteCloser

	// opts describes the parameters that were used to create the logger.
	opts LoggerOpts

	// mu is a mutex to avoid racing between Write and Rotate.
	mu sync.Mutex
}

// NewFileLogger creates a new object of file logger.
func NewFileLogger(opts LoggerOpts) (Logger, error) {
	dir := filepath.Dir(opts.Filename)
	if _, err := os.Stat(dir); err != nil &&
		errors.Is(err, os.ErrNotExist) {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}

	file, err := os.OpenFile(opts.Filename, logOpenFlags, 0o640)
	if err != nil {
		return nil, fmt.Errorf("cannot open the log file %q: %s", opts.Filename, err)
	}
	return &fileLogger{
		Logger:  log.New(file, opts.Prefix, log.LstdFlags),
		opts:    opts,
		logFile: file,
	}, nil
}

// GetOpts returns the parameters that were used to create the logger.
func (logger *fileLogger) GetOpts() LoggerOpts {
	return logger.opts
}

// Write implements io.Writer interface.
func (logger *fileLogger) Write(p []byte) (int, error) {
	logger.mu.Lock()
	defer logger.mu.Unlock()

	return logger.Logger.Writer().Write(p)
}

// Rotate reopens the log file.
func (logger *fileLogger) Rotate() error {
	if logger.logFile == nil {
		return nil
	}

	logger.mu.Lock()
	defer logger.mu.Unlock()

	savedFile := logger.logFile
	var err error

	if logger.logFile, err = os.OpenFile(logger.opts.Filename, logOpenFlags,
		logCreatePerms); err != nil {
		return fmt.Errorf("cannot open the log file %q: %s", logger.opts.Filename, err)
	}
	logger.Logger = log.New(logger.logFile, logger.opts.Prefix, log.LstdFlags)
	logger.Println("(INFO) log file has been reopened")

	if err := savedFile.Close(); err != nil {
		logger.Printf("(ERROR) failed to close previous log file: %s", err)
	}

	return nil
}

// Close implements io.Closer, and closes the current logfile.
func (logger *fileLogger) Close() error {
	if logger.logFile != nil {
		return logger.logFile.Close()
	}
	return nil
}
