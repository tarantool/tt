// This file contains types for defining structs that store
// contexts during crud module work.

package crud

import (
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
	// RollbackOnError is rollback-on-error opts in crud batching operation.
	RollbackOnError bool
}

// Record contains information that related to the parsing process and crud working.
type Record struct {
	// Position is line number in input file.
	Position uint32 `yaml:"position" json:"position"`
	// Raw contains unparsed record.
	Raw string `yaml:"raw" json:"raw"`
	// Parsed contains parsed record.
	Parsed []string `yaml:"parsed" json:"parsed"`
	// Casted сontains a set of converted (on router side) fields to insert into space.
	Casted []interface{} `yaml:"casted"  json:"casted"`
	// ParserSuccess indicates the success of parsing process.
	ParserSuccess bool `yaml:"parserSuccess" json:"parserSuccess"`
	// ParserErr contains error description of parsing process.
	ParserErr string `yaml:"parserErr" json:"parserErr"`
	// ImportSuccess indicates the success of import.
	ImportSuccess bool `yaml:"importSuccess" json:"importSuccess"`
	// CrudErr сontains error description from crud stored procedure.
	CrudErr string `yaml:"crudErr" json:"crudErr"`
}

// Tuple contains the context of a tuple within a batch.
type Tuple struct {
	// Number contains the number of the tuple within the batch.
	// It can take values from the range [1, batchSize] for initialized tuples within a batch.
	// For uninitialized tuples within a batch, a 0 value is used.
	// The current number of initialized tuples is recorded in struct Batch->TuplesAmount.
	Number uint32 `yaml:"Number" json:"Number"`
	// Record contains parser and crud contexts related to this tuple.
	Record Record `yaml:"record" json:"record"`
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
	// EOF indicates has the EOF been reached or not.
	EOF bool `yaml:"endOfFileReached" json:"endOfFileReached"`
	// LastPosition contains last processing position of input file in this launch.
	LastPosition uint32 `yaml:"lastPosition" json:"lastPosition"`
	// RetryPosition contains positions of input file that could not be imported in this launch.
	RetryPosition []uint32 `yaml:"retryPositions" json:"retryPositions"`
	// prevLastPosition contains LastPosition from previous version of progress file.
	prevLastPosition uint32
	// prevRetryPosition contains RetryPosition from previous version of progress file.
	prevRetryPosition []uint32
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
// It also contains fields for implementing a mechanism to eliminate
// the inconsistency of the position counter due to a multi-line CSV records.
// WARNING: Fields inside this structure should not be read or overwritten by the programmer.
// Exclusion are methods updateOnError, updateOnSuccess.
type UnparsedCsvReaderCtx struct {
	masterPosition uint32
	slavePosition  uint32
	currentRecord  string
}

// getCurrentTupleIndex allows to get the index of the current tuple within the batch.
func (ctx *BatchSequenceCtx) getCurrentTupleIndex() uint32 {
	return (ctx.tupleNumber - 1) % ctx.batchSize
}

// updateOnError updates the raw read context in case of reading error of the csv parser.
// WARNING: this method must not be called multiple times in one iteration of the csv parser!
func (ctx *UnparsedCsvReaderCtx) updateOnError(csvReader *ttcsv.Reader) (uint32, string) {
	ctx.masterPosition++
	ctx.currentRecord = csvReader.RawRecord
	currentPosition, currentRec := ctx.masterPosition, ctx.currentRecord
	// Clearing the current line in the ctx to write a new line on the next iteration of parser.
	ctx.currentRecord = ""

	return currentPosition, currentRec
}

// updateOnSuccess updates the raw read context in case of reading success of the csv parser.
// WARNING: this method must not be called multiple times in one iteration of the csv parser!
func (ctx *UnparsedCsvReaderCtx) updateOnSuccess(csvReader *ttcsv.Reader) (uint32, string) {
	ctx.masterPosition++
	ctx.currentRecord = csvReader.RawRecord
	ctx.slavePosition = ctx.masterPosition
	for uint32(csvReader.NumLine) > ctx.masterPosition {
		// Value of current position in raw reading should catch up
		// with the reading position of the csv parser.
		// If a single entry in a CSV file could not make up several lines,
		// then this mechanism would not be needed.
		// Example: 123,"da\nta",321 is one csv record, but unparsed form is two lines,
		// parsed form is slice ["123", "da\nta", 321].
		ctx.masterPosition++
		ctx.currentRecord = csvReader.RawRecord
	}

	var currentPosition uint32
	if ctx.masterPosition != ctx.slavePosition {
		currentPosition = ctx.slavePosition
	} else {
		currentPosition = ctx.masterPosition
	}
	currentRec := ctx.currentRecord
	// Clearing the current line in the ctx to write a new line on the next iteration of parser.
	ctx.currentRecord = ""

	return currentPosition, currentRec
}
