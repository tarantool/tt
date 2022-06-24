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

// dumpLogFile implements the dumping of log file information to disk.
func dumpLogFile(
	csvRowRec string,
	csvRowPos uint32,
	errorDesc string,
	dumpSubsystemFd *DumpSubsystemFd,
) error {
	var timeMsg string = "timestamp: " + time.Now().String() + "\n"
	var lineMsg string = "line position: " + strconv.FormatUint(uint64(csvRowPos), 10) + "\n"
	var recordMsg string = "problem record: " + csvRowRec + "\n"
	var errorMsg string = "error description: " + errorDesc + "\n"
	if _, err := dumpSubsystemFd.logFile.WriteString("...\n" + timeMsg + lineMsg + recordMsg +
		errorMsg + "...\n"); err != nil {
		return err
	}

	return nil
}

// dumpErrorFile implements the dumping of error file information to disk.
func dumpErrorFile(csvRowRec string, dumpSubsystemFd *DumpSubsystemFd) error {
	if _, err := dumpSubsystemFd.errorFile.WriteString(csvRowRec + "\n"); err != nil {
		return err
	}
	importSummary.importedError++

	return nil
}

// dumpSuccessFile implements the dumping of success file information to disk.
func dumpSuccessFile(csvRowRec string, dumpSubsystemFd *DumpSubsystemFd) error {
	if _, err := dumpSubsystemFd.successFile.WriteString(csvRowRec + "\n"); err != nil {
		return err
	}
	importSummary.importedSuccess++

	return nil
}

// dumpProgressFile implements the dumping of current progress file information to disk.
func dumpProgressFile(
	dumpSubsystemFd *DumpSubsystemFd,
	progressCtx *ProgressCtx,
) error {
	dumpSubsystemFd.progressFile.Truncate(0)
	dumpSubsystemFd.progressFile.Seek(0, 0)
	progressCtx.LastDumpTimestamp = time.Now().String()
	if progressCtxBytes, err := json.Marshal(progressCtx); err != nil {
		return nil
	} else {
		dumpSubsystemFd.progressFile.Write(progressCtxBytes)
	}

	return nil
}

// runDumpSubsystem implements the dumping of batch information to disk.
// Including work with files: error, success, log, progress.
func runDumpSubsystem(dumpArgs *DumpSubsystemArgs) error {
	for _, tuple := range dumpArgs.batch.Tuples {
		if !tuple.CrudCtx.Imported && tuple.Number != 0 {
			dumpArgs.progressCtx.RetryPosition = append(dumpArgs.progressCtx.RetryPosition,
				tuple.ParserCtx.CsvRecordPosition)
			if err := dumpProgressFile(dumpArgs.dumpSubsystemFd, dumpArgs.progressCtx); err != nil {
				return err
			}
			if err := dumpErrorFile(
				tuple.ParserCtx.UnparsedCsvRecord,
				dumpArgs.dumpSubsystemFd); err != nil {
				return err
			}
			if !tuple.ParserCtx.ParsedSuccess {
				// Dump with parsing level error.
				if err := dumpLogFile(
					tuple.ParserCtx.UnparsedCsvRecord,
					tuple.ParserCtx.CsvRecordPosition,
					tuple.ParserCtx.ErrorMsg,
					dumpArgs.dumpSubsystemFd); err != nil {
					return err
				}
			} else if len(tuple.CrudCtx.Err) != 0 {
				// Dump with crud level error.
				if err := dumpLogFile(
					tuple.ParserCtx.UnparsedCsvRecord,
					tuple.ParserCtx.CsvRecordPosition,
					tuple.CrudCtx.Err,
					dumpArgs.dumpSubsystemFd); err != nil {
					return err
				}
			} else if dumpArgs.caughtErr != nil {
				// Dump with else level error (for example, connection to router lost,
				// description in caughtErr).
				if err := dumpLogFile(
					tuple.ParserCtx.UnparsedCsvRecord,
					tuple.ParserCtx.CsvRecordPosition,
					dumpArgs.caughtErr.Error(),
					dumpArgs.dumpSubsystemFd); err != nil {
					return err
				}
			} else {
				// Dump with unknown error (probably uncaught on router level).
				if err := dumpLogFile(
					tuple.ParserCtx.UnparsedCsvRecord,
					tuple.ParserCtx.CsvRecordPosition,
					"unknown error, probably it was on router level",
					dumpArgs.dumpSubsystemFd); err != nil {
					return err
				}
			}
			if err := dumpProgressFile(dumpArgs.dumpSubsystemFd, dumpArgs.progressCtx); err != nil {
				return err
			}
		}
		if tuple.CrudCtx.Imported && tuple.Number != 0 {
			if err := dumpSuccessFile(
				tuple.ParserCtx.UnparsedCsvRecord,
				dumpArgs.dumpSubsystemFd); err != nil {
				return err
			}
		}
	}
	for _, tuple := range dumpArgs.batch.Tuples {
		// Checking received from router batch for any errors.
		if !tuple.CrudCtx.Imported && tuple.Number != 0 {
			if dumpArgs.crudImportFlags.OnError == "stop" {
				stopOnError(dumpArgs.crudImportFlags)
			}
		}
	}

	return nil
}

// initDumpSubsystemFd opens dump subsystem files and performs some initializing actions with them.
func initDumpSubsystemFd(
	logFileName string,
	errorFileName string,
	successFileName string,
	progressCtx *ProgressCtx,
) (*DumpSubsystemFd, error) {
	var err error
	var dumpSubsystemFd *DumpSubsystemFd = &DumpSubsystemFd{
		logFile:      new(os.File),
		errorFile:    new(os.File),
		successFile:  new(os.File),
		progressFile: new(os.File),
	}

	dumpSubsystemFd.logFile, err = os.OpenFile(logFileName,
		os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
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
	if _, err := dumpSubsystemFd.logFile.WriteString("\n\n\n" +
		logFileInitMsg + "\n\n\n"); err != nil {
		return nil, err
	}

	dumpSubsystemFd.errorFile, err = os.OpenFile(errorFileName,
		os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}

	dumpSubsystemFd.successFile, err = os.OpenFile(successFileName,
		os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return nil, err
	}

	dumpSubsystemFd.progressFile, err = os.OpenFile("progress.json",
		os.O_RDWR|os.O_CREATE, 0o644)
	if err != nil {
		return nil, err
	}

	return dumpSubsystemFd, nil
}

// logDumpSubsystemMalfunction logs information about dump log subsystem malfunction.
func logDumpSubsystemMalfunction() {
	// This subsystem is critical, work stops without it.
	fmt.Println("Failure of the disk dump subsystem, emergency shutdown!")
	fmt.Println("Work without the disk dump subsystem is impossible.")
	fmt.Println("Possible damage of error.csv, success.csv, import.log, progress.json!")
	fmt.Println("Consistency of the imported data in the storage is not guaranteed.")
}

// initProgressCtxPrevLaunch init progress context with progress file of last launch.
func initProgressCtxPrevLaunch(
	progressCtx *ProgressCtx,
	dumpSubsystemFd *DumpSubsystemFd,
) error {
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
	progressCtx.lastPositionPrevProgress = lastProgressFile.LastPosition
	progressCtx.retryPositionPrevProgress = make([]uint32, len(lastProgressFile.RetryPosition))
	copy(progressCtx.retryPositionPrevProgress, lastProgressFile.RetryPosition)
	dumpSubsystemFd.progressFile.Truncate(0)

	return nil
}

// stopOnError stops the program and displays import summary.
func stopOnError(crudImportFlags *ImportOpts) {
	fmt.Printf("\n")
	fmt.Println("\033[0;31m[on-error]: An error has occurred, the import has been stopped. " +
		"See the details in the log, error, success and progress files.\033[0m")
	fmt.Printf("\n")
	printImportSummary(crudImportFlags)
	os.Exit(1)
}
