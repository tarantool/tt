// This file contains types for defining structs that store
// contexts during crud module work.

package crud

import (
	"bufio"
	"os"

	ttcsv "github.com/tarantool/tt/cli/ttparsers"
)

// ImportOpts describes import options.
type ImportOpts struct {
	// ConnectUsername is username using for establish connection to router via tt connect.
	ConnectUsername string
	// ConnectPassword is password using for establish connection to router via tt connect.
	ConnectPassword string
	// Format is input file format.
	Format string
	// Header indicates the presence of a header in input file.
	Header bool
	// LogFileName is name of log file with logs about unsuccessful imported data.
	LogFileName string
	// ErrorFileName is name of error file with unsuccessful imported data.
	ErrorFileName string
	// SuccessFileName is name of success file with successful imported data.
	SuccessFileName string
	// Match sets the match rule between input fields and space fields.
	Match string
	// BatchSize is tuples amount within batch.
	BatchSize uint32
	// Progress is flag for using the progress file from previous launch.
	Progress bool
	// Operation sets type of crud stored procedures during import (inser of replace).
	Operation string
	// OnError sets the action when an error is detected (stop or skip).
	OnError string
	// NullVal sets null interpretation for input data.
	NullVal string
}

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

// ParserCtx contains the tuple information that related to the parsing process.
type ParserCtx struct {
	// CsvRecordPosition is current position in input csv file.
	CsvRecordPosition uint32 `yaml:"csvRecordPosition" json:"csvRecordPosition"`
	// UnparsedCsvRecord is current unparsed csv record.
	UnparsedCsvRecord string `yaml:"unparsedCsvRecord" json:"unparsedCsvRecord"`
	// ParsedSuccess indicates the success of last parsing iteration.
	ParsedSuccess bool `yaml:"parsedSuccess" json:"parsedSuccess"`
	// ParsedCsvRecord contains parsed result of last parsing iteration.
	ParsedCsvRecord []string `yaml:"parsedCsvRecord" json:"parsedCsvRecord"`
	// ErrorMsg contains error description of last parsing iteration.
	ErrorMsg string `yaml:"errorMsg" json:"errorMsg"`
}

// CrudCtx contains the tuple information that related to the crud work on router side.
type CrudCtx struct {
	// Imported indicates the success of the import.
	Imported bool `yaml:"imported" json:"imported"`
	// CastedTuple сontains a set of converted (on router side) fields to insert into space.
	CastedTuple []interface{} `yaml:"castedTuple"  json:"castedTuple"`
	// Err сontains error description from crud stored procedure.
	Err string `yaml:"err" json:"err"`
}

// Tuple contains the context of a tuple within a batch.
type Tuple struct {
	// Number contains the number of the tuple within the batch.
	// It can take values from the range [1, batchSize] for initialized tuples within a batch.
	// For uninitialized tuples within a batch, a 0 value is used.
	// The current number of initialized tuples is recorded in struct Batch->TuplesAmount.
	Number uint32 `yaml:"Number" json:"Number"`
	// ParserCtx contains parser context related to this tuple.
	ParserCtx ParserCtx `yaml:"parserCtx" json:"parserCtx"`
	// CrudCtx contains router side context related to this tuple.
	CrudCtx CrudCtx `yaml:"crudCtx" json:"crudCtx"`
}

// Batch contains the context of the batch, including necessary meta information for crud.
type Batch struct {
	// BatchNumber is batch number during import work.
	BatchNumber uint32 `yaml:"batchNumber" json:"batchNumber"`
	// Header сontains a header of input data.
	Header []string `yaml:"header" json:"header"`
	// TuplesAmount сontains the number of initialized tuples within batch.
	TuplesAmount uint32 `yaml:"tuplesAmount" json:"tuplesAmount"`
	// Tuple is slice of tuples within this batch.
	Tuples []Tuple `yaml:"tuples" json:"tuples"`
}

// ProgressCtx contains the context for the progress file.
type ProgressCtx struct {
	// StartTimestamp contains the work start time of this launch.
	StartTimestamp string `yaml:"startTimestamp" json:"startTimestamp"`
	// LastDumpTimestamp contains the time of last write to progress file of this launch.
	LastDumpTimestamp string `yaml:"lastDumpTimestamp" json:"lastDumpTimestamp"`
	// EndOfFileReached indicates has the EOF been reached or not.
	EndOfFileReached bool `yaml:"endOfFileReached" json:"endOfFileReached"`
	// LastPosition contains last processing position of input file in this launch.
	LastPosition uint32 `yaml:"lastPosition" json:"lastPosition"`
	// RetryPosition contains positions of input file that could not be imported in this launch.
	RetryPosition []uint32 `yaml:"retryPositions" json:"retryPositions"`
	// lastPositionPrevProgress contains LastPosition from previous version of progress file.
	lastPositionPrevProgress uint32
	// retryPositionPrevProgress contains RetryPosition from previous version of progress file.
	retryPositionPrevProgress []uint32
}

// BatchSequenceCtx contains the context for
// organizing movement of batch window on input file.
type BatchSequenceCtx struct {
	// batchSize is tuples amount within batch.
	batchSize uint32
	// batchCounter is batch counter during import work (used to fill Batch->BatchNumber).
	batchCounter uint32
	// tupleNumber is contains the number of the tuple within the current batch.
	// Used to fill Tuple->Number.
	tupleNumber uint32
	// header сontains a header of input data.
	header []string
}

// UnparsedCsvReaderCtx contains private fields for organizing context of
// raw reading of input data for import.
// Raw reading is necessary to get a string of data in an unparsed form.
// The standard parser does not allow this.
// It also contains fields for implementing a mechanism to eliminate
// the inconsistency of the position counter due to a multi-line CSV records.
// WARNING: Fields inside this structure should not be read or overwritten by the programmer.
// Exclusion are methods updateOnError, updateOnSuccess.
type UnparsedCsvReaderCtx struct {
	masterPosition uint32
	slavePosition  uint32
	currentRecord  string
	scanner        *bufio.Scanner
}

// getCurrentTupleIndex allows to get the index of the current tuple within the batch.
func (ctx *BatchSequenceCtx) getCurrentTupleIndex() uint32 {
	return (ctx.tupleNumber - 1) % ctx.batchSize
}

// updateOnError updates the raw read context in case of a record.
// reading error of the main parser.
// WARNING: this method must not be called multiple times in one iteration of the main csv parser!
func (ctx *UnparsedCsvReaderCtx) updateOnError() (uint32, string) {
	ctx.masterPosition++
	ctx.scanner.Scan()
	ctx.currentRecord += ctx.scanner.Text()
	currentPosition, currentRec := ctx.masterPosition, ctx.currentRecord
	// clearing the current line in the ctx to write a new line on the next iteration of parser.
	ctx.currentRecord = ""

	return currentPosition, currentRec
}

// updateOnSuccess updates the raw read context in case
// of a record success reading of the main parser.
// WARNING: this method must not be called multiple times in one iteration of the main csv parser!
func (ctx *UnparsedCsvReaderCtx) updateOnSuccess(csvReader *ttcsv.Reader) (uint32, string) {
	ctx.masterPosition++
	ctx.scanner.Scan()
	ctx.currentRecord += ctx.scanner.Text()

	ctx.slavePosition = ctx.masterPosition
	for uint32(csvReader.NumLine) > ctx.masterPosition {
		// Value of current position in raw reader should catch up
		// with the reading position of the main parser.
		// if a single entry in a CSV file could not make up several lines,
		// then this mechanism would not be needed.
		// Example: 123,"da\nta",321 is one csv record, but unparsed form is two lines,
		// parsed form is slice ["123", "da\nta", 321].
		ctx.masterPosition++
		ctx.scanner.Scan()
		ctx.currentRecord += "\n" + ctx.scanner.Text()
	}

	var currentPosition uint32
	if ctx.masterPosition != ctx.slavePosition {
		currentPosition = ctx.slavePosition
	} else {
		currentPosition = ctx.masterPosition
	}
	currentRec := ctx.currentRecord
	// clearing the current line in the ctx to write a new line on the next iteration of parser.
	ctx.currentRecord = ""

	return currentPosition, currentRec
}

// DumpSubsystemFd contains file descriptors for the disk dump subsystem.
type DumpSubsystemFd struct {
	// logFile is file descriptor for writing logs about unsuccessful imported data.
	logFile *os.File
	// errorFile is file descriptor for writing records that was not imported.
	errorFile *os.File
	// successFile is file descriptor for writing records that was imported.
	successFile *os.File
	// progressFile is file descriptor for writing import progress information.
	progressFile *os.File
}

// DumpSubsystemArgs contains args for runDumpSubsystem func
type DumpSubsystemArgs struct {
	// batch contains context for the current batch.
	batch *Batch
	// caughtErr is error that occurred during work.
	caughtErr error
	// dumpSubsystemFd contains file descriptors of disk dump subsystem.
	dumpSubsystemFd *DumpSubsystemFd
	// progressCtx contains current context for the progress file.
	progressCtx *ProgressCtx
	// crudImportFlags contains import options.
	crudImportFlags *ImportOpts
}

// close performs closing of file descriptors of the disk dump subsystem.
func (dumpSubsystemFd *DumpSubsystemFd) close() {
	dumpSubsystemFd.logFile.Close()
	dumpSubsystemFd.errorFile.Close()
	dumpSubsystemFd.successFile.Close()
}
