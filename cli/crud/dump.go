// This file contains functions for working with the disk dump subsystem.
// The system including work with files: error, success, log, progress.

package crud

import (
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"
)

// DumpSubsystemFiles contains file descriptors for the disk dump subsystem.
type DumpSubsystemFiles struct {
	// logs is file descriptor for writing logs about unsuccessful imported data.
	Log *os.File
	// errors is file descriptor for writing records that was not imported.
	Error *os.File
	// successes is file descriptor for writing records that was imported.
	Success *os.File
	// progress is file descriptor for writing import progress information.
	Progress *os.File
}

// DumpSubsystemArgs contains args for runDumpSubsystem func.
type DumpSubsystemArgs struct {
	// batch contains context for the current batch.
	batch *Batch
	// caughtErr is error that occurred during work.
	caughtErr error
	// dumpSubsystemFiles contains file descriptors of disk dump subsystem.
	dumpSubsystemFiles *DumpSubsystemFiles
	// progressCtx contains current context for the progress file.
	progressCtx *ProgressCtx
	// crudImportFlags contains import options.
	crudImportFlags *ImportOpts
}

// close performs closing of file descriptors of the disk dump subsystem.
func (dumpSubsystemFiles *DumpSubsystemFiles) close() {
	dumpSubsystemFiles.Log.Close()
	dumpSubsystemFiles.Error.Close()
	dumpSubsystemFiles.Success.Close()
}

// dumpLogFile implements the dumping of log file information to disk.
func dumpLogFile(csvRowRec string, csvRowPos uint32, errorDesc string,
	dumpSubsystemFiles *DumpSubsystemFiles) error {
	var timeMsg string = "timestamp: " + time.Now().String() + "\n"
	var lineMsg string = "line position: " + strconv.FormatUint(uint64(csvRowPos), 10) + "\n"
	var recordMsg string = "problem record: " + csvRowRec + "\n"
	var errorMsg string = "error description: " + errorDesc + "\n"
	if _, err := dumpSubsystemFiles.Log.WriteString("...\n" + timeMsg + lineMsg + recordMsg +
		errorMsg + "...\n"); err != nil {
		return err
	}

	return nil
}

// dumpErrorFile implements the dumping of error file information to disk.
func dumpErrorFile(csvRowRec string, dumpSubsystemFiles *DumpSubsystemFiles) error {
	if _, err := dumpSubsystemFiles.Error.WriteString(csvRowRec + "\n"); err != nil {
		return err
	}
	importSummary.importedError++

	return nil
}

// dumpSuccessFile implements the dumping of success file information to disk.
func dumpSuccessFile(csvRowRec string, dumpSubsystemFiles *DumpSubsystemFiles) error {
	if _, err := dumpSubsystemFiles.Success.WriteString(csvRowRec + "\n"); err != nil {
		return err
	}
	importSummary.importedSuccess++

	return nil
}

// dumpProgressFile implements the dumping of current progress file information to disk.
func dumpProgressFile(dumpSubsystemFiles *DumpSubsystemFiles, progressCtx *ProgressCtx) error {
	dumpSubsystemFiles.Progress.Truncate(0)
	dumpSubsystemFiles.Progress.Seek(0, 0)
	progressCtx.LastDumpTimestamp = time.Now().String()
	if progressCtxBytes, err := json.Marshal(progressCtx); err != nil {
		return nil
	} else {
		dumpSubsystemFiles.Progress.Write(progressCtxBytes)
	}

	return nil
}

// runDumpSubsystem implements the dumping of batch information to disk.
// Including work with files: error, success, log, progress.
func runDumpSubsystem(dumpArgs *DumpSubsystemArgs) error {
	for _, tuple := range dumpArgs.batch.Tuples {
		if !tuple.Record.ImportSuccess && tuple.Number != 0 {
			dumpArgs.progressCtx.RetryPosition = append(dumpArgs.progressCtx.RetryPosition,
				tuple.Record.Position)
			if err := dumpProgressFile(dumpArgs.dumpSubsystemFiles,
				dumpArgs.progressCtx); err != nil {
				return err
			}
			if err := dumpErrorFile(tuple.Record.Raw, dumpArgs.dumpSubsystemFiles); err != nil {
				return err
			}
			if !tuple.Record.ParserSuccess {
				// Dump with parsing level error.
				if err := dumpLogFile(tuple.Record.Raw, tuple.Record.Position,
					tuple.Record.ParserErr, dumpArgs.dumpSubsystemFiles); err != nil {
					return err
				}
			} else if len(tuple.Record.CrudErr) != 0 {
				// Dump with crud level error.
				if err := dumpLogFile(tuple.Record.Raw, tuple.Record.Position,
					tuple.Record.CrudErr, dumpArgs.dumpSubsystemFiles); err != nil {
					return err
				}
			} else if dumpArgs.caughtErr != nil {
				// Dump with else level error (for example, connection to router lost,
				// description in caughtErr).
				if err := dumpLogFile(tuple.Record.Raw, tuple.Record.Position,
					dumpArgs.caughtErr.Error(), dumpArgs.dumpSubsystemFiles); err != nil {
					return err
				}
			} else {
				// Dump with unknown error (probably uncaught on router level).
				if err := dumpLogFile(tuple.Record.Raw, tuple.Record.Position,
					"unknown error, probably it was on router level",
					dumpArgs.dumpSubsystemFiles); err != nil {
					return err
				}
			}
			if err := dumpProgressFile(dumpArgs.dumpSubsystemFiles,
				dumpArgs.progressCtx); err != nil {
				return err
			}
		}
		if tuple.Record.ImportSuccess && tuple.Number != 0 {
			if err := dumpSuccessFile(
				tuple.Record.Raw,
				dumpArgs.dumpSubsystemFiles); err != nil {
				return err
			}
		}
	}
	for _, tuple := range dumpArgs.batch.Tuples {
		// Checking received from router batch for any errors.
		if !tuple.Record.ImportSuccess && tuple.Number != 0 {
			if dumpArgs.crudImportFlags.OnError == "stop" {
				stopOnError(dumpArgs.crudImportFlags)
			}
		}
	}

	return nil
}

// initDumpSubsystemFiles opens dump subsystem files and performs initializing actions with them.
func initDumpSubsystemFiles(logFileName string, errorFileName string, successFileName string,
	progressCtx *ProgressCtx) (*DumpSubsystemFiles, error) {
	var err error
	var dumpSubsystemFiles *DumpSubsystemFiles = &DumpSubsystemFiles{
		Log:      new(os.File),
		Error:    new(os.File),
		Success:  new(os.File),
		Progress: new(os.File),
	}

	// Opening for appending, file mode is -rw-r--r--.
	dumpSubsystemFiles.Log, err = os.OpenFile(logFileName,
		os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}

	// Below "header" (meta information) of log file.
	var logFileInitMsg string = "\t\t\t" +
		"Crud import start work timestamp: " + time.Now().String() + "\n"
	logFileInitMsg += "\t\t\t" +
		"File description: this file contains logs information about occurred errors\n"
	logFileInitMsg += "\t\t\t" +
		"Note: errors are indicated relative to the space format, not relative to the header\n"
	if _, err := dumpSubsystemFiles.Log.WriteString("\n\n\n" +
		logFileInitMsg + "\n\n\n"); err != nil {
		return nil, err
	}

	// Open with overwriting, file mode is -rw-r--r--.
	dumpSubsystemFiles.Error, err = os.OpenFile(errorFileName,
		os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return nil, err
	}

	// Open with overwriting, file mode is -rw-r--r--.
	dumpSubsystemFiles.Success, err = os.OpenFile(successFileName,
		os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return nil, err
	}

	// Open without affecting the current file content, file mode is -rw-r--r--.
	dumpSubsystemFiles.Progress, err = os.OpenFile("progress.json",
		os.O_RDWR|os.O_CREATE, 0644)
	if err != nil {
		return nil, err
	}

	return dumpSubsystemFiles, nil
}

// setPrevLaunchData set progress context with progress file of last launch.
func (ctx *ProgressCtx) setPrevLaunchData(dumpSubsystemFiles *DumpSubsystemFiles) error {
	lastProgressFileBytes, err := os.ReadFile("progress.json")
	if err != nil {
		fmt.Println("Problem with reading progress.json file.")
		return err
	}
	var lastProgressFile *ProgressCtx = &ProgressCtx{}
	if err := json.Unmarshal(lastProgressFileBytes,
		&lastProgressFile); err != nil {
		fmt.Println("Problem with deserialize progress.json file.")
		return err
	}
	ctx.prevLastPosition = lastProgressFile.LastPosition
	ctx.prevRetryPosition = make([]uint32, len(lastProgressFile.RetryPosition))
	copy(ctx.prevRetryPosition, lastProgressFile.RetryPosition)
	dumpSubsystemFiles.Progress.Truncate(0)

	return nil
}

// stopOnError stops the program and displays import summary.
func stopOnError(crudImportFlags *ImportOpts) {
	fmt.Printf("\n")
	fmt.Println("\033[0;31m" +
		"[on-error]: An error has occurred, the import has been stopped. " +
		"See the details in the log, error, success and progress files." +
		"\033[0m")
	fmt.Printf("\n")
	printImportSummary(crudImportFlags)
	os.Exit(1)
}
