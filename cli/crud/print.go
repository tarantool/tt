// This file contains functions for working with console output.
// Сontains the implementation of progress bar and summary information printing.

package crud

import (
	"fmt"
	"time"
)

// ImportSummary contains information for the import summary.
type ImportSummary struct {
	// readTotal is counter of total iterations of the parser.
	readTotal uint32
	// ignoredDueToProgress is counter of skipped iterations of the parser due to progress file.
	ignoredDueToProgress uint32
	// parsedSuccess is counter of succsess iterations of the parser.
	parsedSuccess uint32
	// parsedError is counter of fail iterations of the parser.
	parsedError uint32
	// importedSuccess is counter of succsess iterations of crud stored procedure.
	importedSuccess uint32
	// importedError is counter of fail iterations of crud stored procedure.
	importedError uint32
}

// printImportSummary prints the summary.
func printImportSummary(crudImportFlags *ImportOpts) {
	var speed float64 = float64(importSummary.readTotal) /
		float64(time.Since(workStartTimestamp).Seconds())

	fmt.Println()
	fmt.Println("\tIMPORT SUMMARY")
	fmt.Println()
	fmt.Println("\ttotal read:\t\t", importSummary.readTotal)
	fmt.Println("\tignored (--progress):\t", importSummary.ignoredDueToProgress)
	fmt.Println("\tparsed success:\t\t", importSummary.parsedSuccess)
	fmt.Println("\tparsed error:\t\t", importSummary.parsedError)
	fmt.Println("\timport success:\t\t", importSummary.importedSuccess)
	fmt.Println("\timport error:\t\t", importSummary.importedError)
	fmt.Println("\tspeed (rec per sec):\t", uint64(speed))
	fmt.Println()
	fmt.Println("\timport logs file:\t", crudImportFlags.LogFileName+".log")
	fmt.Println("\tfailed recs file:\t", crudImportFlags.ErrorFileName+".csv")
	fmt.Println("\timported recs file:\t", crudImportFlags.SuccessFileName+".csv")
	fmt.Println("\timport progress file:\tprogress.json")
	fmt.Println()
}

// printImportProgressBar prints the progress bar.
func printImportProgressBar() {
	fmt.Printf("\r[ read/ignored : %d/%d | parsed ok/err : %d/%d | import ok/err : %d/%d ]",
		importSummary.readTotal,
		importSummary.ignoredDueToProgress,
		importSummary.parsedSuccess,
		importSummary.parsedError,
		importSummary.importedSuccess,
		importSummary.importedError)
}

// printDumpSubsystemMalfunction logs information about dump log subsystem malfunction.
func printDumpSubsystemMalfunction() {
	// This subsystem is critical, work stops without it.
	fmt.Println("Failure of the disk dump subsystem, emergency shutdown!")
	fmt.Println("Work without the disk dump subsystem is impossible.")
	fmt.Println("Possible damage of error.csv, success.csv, import.log, progress.json!")
	fmt.Println("Consistency of the imported data in the storage is not guaranteed.")
}
