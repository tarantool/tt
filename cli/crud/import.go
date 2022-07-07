// The batches are transmitted to router in the form of a serialized json (struct Batch).
// Feedback from crud is obtained through this way also.

// At the end of the program batches amount will be ⌈csvRecodrdsAmount / batchSize⌉,
// where csvRecodrdsAmount is the number of csv entries (both correct and incorrect syntax)
// in the input data.
// Example: 10 syntax correct entries (8 of them will be imported, and 2 will be not
// due to some crud error), and 3 with a syntax error.
// csvRecodrdsAmount: 8 + 2 + 3 = 13; batchSize: 5; batches amount will be ⌈13 / 5⌉ = 3.
// Finally, batches amount = number of batch uploading to the router = number of
// crud.insert_many/crud.replace_many iterations.

package crud

import (
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tarantool/tt/cli/cmdcontext"
	"github.com/tarantool/tt/cli/connector"
	ttcsv "github.com/tarantool/tt/cli/ttparsers"
)

// Init import summary container.
var importSummary ImportSummary = ImportSummary{}
var workStartTimestamp time.Time = time.Now()

// RunImport implements data import via crud.
func RunImport(cmdCtx *cmdcontext.CmdCtx, crudImportFlags *ImportOpts, uri string,
	inputFileName string, spaceName string) error {
	// Init signal interceptor to ignore SIGINT/SIGTERM signals.
	sigInterceptor(func() {}, syscall.SIGINT, syscall.SIGTERM)

	// Init csv readers.
	csvReader, unparsedCsvReaderCtx, csvReaderFile, err := initReaders(inputFileName)
	if err != nil {
		return err
	}
	defer csvReaderFile.Close()

	// Open connection to router.
	conn, err := openRouterConnection(uri, crudImportFlags)
	if err != nil {
		return err
	}
	defer conn.Close()

	// Init router for crud import work.
	if err := initRouter(conn, spaceName, crudImportFlags); err != nil {
		return err
	}

	// Init progress file context.
	var progressCtx *ProgressCtx = &ProgressCtx{
		StartTimestamp:    workStartTimestamp.String(),
		LastDumpTimestamp: "",
		EOF:               false,
		LastPosition:      1,
		RetryPosition:     []uint32{},
	}

	// Init dump subsystem context.
	dumpSubsystemFiles, err := initDumpSubsystemFiles(
		crudImportFlags.LogFileName+".log",
		crudImportFlags.ErrorFileName+".csv",
		crudImportFlags.SuccessFileName+".csv",
		progressCtx,
	)
	if err != nil {
		return err
	}
	defer dumpSubsystemFiles.close()

	var batchSequenceCtx *BatchSequenceCtx
	var batch *Batch
	batch, batchSequenceCtx, err = initContextsRelativeToHeader(crudImportFlags, csvReader,
		dumpSubsystemFiles, unparsedCsvReaderCtx)
	if err != nil {
		return err
	}

	if crudImportFlags.Progress {
		if _, err := os.Stat(".progress.json"); !errors.Is(err, os.ErrNotExist) {
			// If there is a shadow file ./.progress.json from the previous failed launch,
			// then its contents will be used.
			if err := os.Rename(".progress.json", "progress.json"); err != nil {
				return fmt.Errorf("cannot replace shadow ./.progress.json to "+
					"./progress.json: %v", err)
			}
		}
		if err := progressCtx.setPrevLaunchData(dumpSubsystemFiles); err != nil {
			return err
		}
	}

	fmt.Printf("In case of error:\t[%s]\n", crudImportFlags.OnError)
	fmt.Printf("PID of this process:\t[%d]\n", os.Getpid())
	fmt.Printf("\n\033[0;33m"+
		"WARNING: Process is not sensitive to SIGINT and SIGTERM, use kill -9 %d"+
		"\033[0m\n", os.Getpid())
	fmt.Println()

	err = mainParsingCycle(csvReader, progressCtx, batch, conn, dumpSubsystemFiles, crudImportFlags,
		unparsedCsvReaderCtx, batchSequenceCtx)
	if err != nil {
		return err
	}

	fmt.Println()
	printImportSummary(crudImportFlags)

	return nil
}

// sigInterceptor intercepts signals and calls a handler for them.
func sigInterceptor(sigHandler func(), signals ...os.Signal) {
	sigInput := make(chan os.Signal, 1)
	signal.Notify(sigInput, signals...)
	go func() {
		for range sigInput {
			sigHandler()
		}
	}()
}

// initReaders init csv reader for getting parsed records.
func initReaders(inputFileName string) (*ttcsv.Reader, *UnparsedCsvReaderCtx, *os.File, error) {
	var csvReader *ttcsv.Reader
	var csvReaderFile *os.File
	var err error
	if inputFileName != "-" {
		// Case of regular file reading.
		csvReaderFile, err = os.Open(inputFileName)
		if err != nil {
			return nil, nil, nil, err
		}
		csvReader = ttcsv.NewReader(csvReaderFile)
	} else {
		// Case of STDIN reading.
		csvReaderFile = os.Stdin
		csvReader = ttcsv.NewReader(os.Stdin)
	}
	csvReader.FieldsPerRecord = -1
	csvReader.ReuseRecord = true

	// Init raw parser context.
	unparsedCsvReaderCtx := &UnparsedCsvReaderCtx{
		masterPosition: 0,
		currentRecord:  "",
		slavePosition:  0,
	}

	return csvReader, unparsedCsvReaderCtx, csvReaderFile, nil
}

// initContextsRelativeToHeader provides initialization logic for contexts with taking into account
// the installed or non-installed header in input data.
func initContextsRelativeToHeader(crudImportFlags *ImportOpts, csvReader *ttcsv.Reader,
	dumpSubsystemFiles *DumpSubsystemFiles, unparsedCsvReaderCtx *UnparsedCsvReaderCtx,
) (*Batch, *BatchSequenceCtx, error) {
	if crudImportFlags.Header {
		// Initialization in the case of a header use.
		headerRec, err := csvReader.Read()
		importSummary.importedSuccess--
		importSummary.importedError--
		if err == io.EOF {
			// Case for problem with getting header (empty file).
			if _, err := dumpSubsystemFiles.Log.WriteString("...\n" +
				"Empty input csv file" + "\n..."); err != nil {
				printDumpSubsystemMalfunction()
				return nil, nil, err
			}
			return nil, nil, fmt.Errorf("Empty input csv file!")
		}
		if err != nil {
			// Case for problem with getting header (bad syntax).
			if _, err := dumpSubsystemFiles.Log.WriteString("...\n" +
				err.Error() + "\n..."); err != nil {
				printDumpSubsystemMalfunction()
				return nil, nil, err
			}
			return nil, nil, fmt.Errorf("Cannot read header, check input csv file: " + err.Error())
		} else {
			// Case for init contexts with header.
			_, unparsedRec := unparsedCsvReaderCtx.updateOnSuccess(csvReader)
			var batchSequenceCtx *BatchSequenceCtx = &BatchSequenceCtx{
				batchSize:    crudImportFlags.BatchSize,
				batchCounter: 1,
				tupleNumber:  1,
				header:       make([]string, len(headerRec)),
			}
			copy(batchSequenceCtx.header, headerRec)
			var batch *Batch = makeEmptyBatch(batchSequenceCtx.batchCounter,
				batchSequenceCtx.batchSize, batchSequenceCtx.header)
			if err := dumpErrorFile(unparsedRec, dumpSubsystemFiles); err != nil {
				printDumpSubsystemMalfunction()
				return nil, nil, err
			}
			if err := dumpSuccessFile(unparsedRec, dumpSubsystemFiles); err != nil {
				printDumpSubsystemMalfunction()
				return nil, nil, err
			}

			return batch, batchSequenceCtx, nil
		}
	}

	// Case for init contexts without header.
	var batchSequenceCtx *BatchSequenceCtx = &BatchSequenceCtx{
		batchSize:    crudImportFlags.BatchSize,
		batchCounter: 1,
		tupleNumber:  1,
		header:       make([]string, 0),
	}
	var batch *Batch = makeEmptyBatch(batchSequenceCtx.batchCounter,
		batchSequenceCtx.batchSize, batchSequenceCtx.header)

	return batch, batchSequenceCtx, nil
}

// makeEmptyBatch allocates an empty batch in memory.
func makeEmptyBatch(batchCounter uint32, batchSize uint32, header []string) *Batch {
	return &Batch{
		BatchNumber:  batchCounter,
		Header:       header,
		TuplesAmount: 0,
		Tuples:       make([]Tuple, batchSize),
	}
}

// moveBatchWindow cleans the context of the current batch and prepares
// the batch sequence context for moving batch window further along the input data.
func moveBatchWindow(batchSequenceCtx *BatchSequenceCtx) *Batch {
	batchSequenceCtx.batchCounter++
	if batchSequenceCtx.batchSize != 1 {
		batchSequenceCtx.tupleNumber %= batchSequenceCtx.batchSize
	} else {
		batchSequenceCtx.tupleNumber = 1
	}

	return makeEmptyBatch(batchSequenceCtx.batchCounter,
		batchSequenceCtx.batchSize, batchSequenceCtx.header)
}

// fillTupleWithinBatch fill a tuple within the batch.
func fillTupleWithinBatch(batch *Batch, batchSequenceCtx *BatchSequenceCtx, currentPosition uint32,
	unparsedRec string, record []string, parserErr error, progressCtx *ProgressCtx) {
	batch.Tuples[batchSequenceCtx.getCurrentTupleIndex()].Record.Position = currentPosition
	batch.Tuples[batchSequenceCtx.getCurrentTupleIndex()].Record.Raw = unparsedRec
	batch.Tuples[batchSequenceCtx.getCurrentTupleIndex()].Record.Parsed =
		make([]string, len(record))
	copy(batch.Tuples[batchSequenceCtx.getCurrentTupleIndex()].Record.Parsed, record)
	batch.Tuples[batchSequenceCtx.getCurrentTupleIndex()].Number = batchSequenceCtx.tupleNumber
	if parserErr != nil {
		batch.Tuples[batchSequenceCtx.getCurrentTupleIndex()].Record.ParserSuccess = false
		batch.Tuples[batchSequenceCtx.getCurrentTupleIndex()].Record.ParserErr = parserErr.Error()
	} else {
		batch.Tuples[batchSequenceCtx.getCurrentTupleIndex()].Record.ParserSuccess = true
	}
	batch.TuplesAmount++
	batchSequenceCtx.tupleNumber++
	progressCtx.LastPosition = currentPosition
}

// contains determines whether the element is in the slice.
func contains(slice []uint32, element uint32) bool {
	for _, val := range slice {
		if val == element {
			return true
		}
	}

	return false
}

// mainParsingCycle performs the process of line-by-line parsing of the input file,
// and also calls the logic of import.
func mainParsingCycle(csvReader *ttcsv.Reader, progressCtx *ProgressCtx, batch *Batch,
	conn *connector.Conn, dumpSubsystemFiles *DumpSubsystemFiles, crudImportFlags *ImportOpts,
	unparsedCsvReaderCtx *UnparsedCsvReaderCtx, batchSequenceCtx *BatchSequenceCtx) error {
	// The main parsing cycle.
	// Performs a line-by-line reading of the input data.
	// As soon as the batch is filled with parsed records, it will be sent to the router.
	// Next, the batch is cleared and the filling goes with the following lines.
	for {
		// Try to read record from input data.
		record, parserErr := csvReader.Read()
		importSummary.readTotal++

		if parserErr == io.EOF {
			// Section with processing of EOF.
			progressCtx.EOF = true
			importSummary.readTotal--

			if batch.Tuples[0].Number != 0 {
				// If an incomplete batch (tuples amoutn in batch < BatchSize) is formed
				// by this time (time of EOF), it will be sent to router.
				var err error
				_, err = mainRouterOperations(true, batch, batchSequenceCtx,
					progressCtx, crudImportFlags, dumpSubsystemFiles, conn, csvReader)
				if err != nil {
					return err
				}
			} else {
				// If there is no incomplete batch.
				if err := dumpProgressFile(dumpSubsystemFiles, progressCtx); err != nil {
					printDumpSubsystemMalfunction()
					return err
				}
			}

			printImportProgressBar()
			break
		}

		var currentPosition uint32
		var unparsedRec string
		if parserErr != nil {
			// Records with parsing errors are also sent to the router,
			// but they are not submitted to the import stored procedure of crud.
			// Also, no type conversion is performed with such strings on router side.
			importSummary.parsedError++
			currentPosition, unparsedRec = unparsedCsvReaderCtx.updateOnError(csvReader)
		} else {
			importSummary.parsedSuccess++
			currentPosition, unparsedRec = unparsedCsvReaderCtx.updateOnSuccess(csvReader)
		}

		if crudImportFlags.Progress {
			// Skipping with taking into account the previous progress file.
			if !contains(progressCtx.prevRetryPosition, currentPosition) &&
				currentPosition <= progressCtx.prevLastPosition {
				importSummary.ignoredDueToProgress++
				continue
			}
		}

		fillTupleWithinBatch(batch, batchSequenceCtx, currentPosition, unparsedRec, record,
			parserErr, progressCtx)

		if batchSequenceCtx.getCurrentTupleIndex() != 0 {
			// Before uploading to the router, the batch must be completely filled.
			continue
		}

		var err error
		batch, err = mainRouterOperations(false, batch, batchSequenceCtx,
			progressCtx, crudImportFlags, dumpSubsystemFiles, conn, csvReader)
		if err != nil {
			return err
		}

		printImportProgressBar()
	}

	return nil
}
