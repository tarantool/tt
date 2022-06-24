// This file contains functions for working with the router.
// Working with logic on the router side is implemented by eval calls in router session storage.

package crud

import (
	"encoding/json"
	"fmt"

	"github.com/tarantool/tt/cli/connector"
	ttcsv "github.com/tarantool/tt/cli/ttparsers"
	"gopkg.in/yaml.v2"
)

// initRouterConnection establishes a connection to the router.
func initRouterConnection(uri string, crudImportFlags *ImportOpts) (*connector.Conn, error) {
	conn, err := connector.Connect(uri, crudImportFlags.ConnectUsername,
		crudImportFlags.ConnectPassword)
	if err != nil {
		return nil, fmt.Errorf("Unable to establish connection: %s", err)
	}

	return conn, nil
}

// initCrudImportSessionStorageEvals sets eval to session storage.
func initCrudImportSessionStorageEvals(conn *connector.Conn) error {
	res, err := conn.Eval(initCrudImport)
	if err != nil {
		return err
	}
	crudInitComplete := make([]bool, 1)
	err = yaml.Unmarshal([]byte((res[0]).(string)), &crudInitComplete)
	if err != nil {
		// crudInitComplete is string with error description.
		resYAML := []byte((res[0]).(string))
		var rawRes interface{}
		if err := yaml.Unmarshal(resYAML, &rawRes); err != nil {
			return err
		}
		fmt.Printf("Crud init complete:\t[false]\n")

		return fmt.Errorf("eval init error: " + string(resYAML))
	}
	fmt.Printf("Crud init complete:\t%t\n", crudInitComplete)

	return nil // crudInitComplete is true.
}

// setTargetSpace sets the target space.
func setTargetSpace(conn *connector.Conn, space string) error {
	_, err := conn.Eval(evalSetTargetspace(space))
	if err != nil {
		return err
	}
	res, err := conn.Eval(evalCheckTargetSpaceExist())
	if err != nil {
		return err
	}
	spaceExist := make([]bool, 1)
	err = yaml.Unmarshal([]byte((res[0]).(string)), &spaceExist)
	if err != nil {
		return err
	}
	fmt.Printf("Target space exist:\t%t\n", spaceExist)

	if !spaceExist[0] {
		return fmt.Errorf("Target space must exist.")
	}

	return nil
}

// setCrudOperation sets the crud stored procedure for import.
func setCrudOperation(conn *connector.Conn, operation string) error {
	res, err := conn.Eval(evalSetCrudOperation(operation))
	if err != nil {
		return err
	}
	opetarionSet := make([]bool, 1)
	err = yaml.Unmarshal([]byte((res[0]).(string)), &opetarionSet)
	if err != nil {
		resYAML := []byte((res[0]).(string))
		var rawRes interface{}
		if err := yaml.Unmarshal(resYAML, &rawRes); err != nil {
			return err
		}

		return fmt.Errorf(string(resYAML[5:]))
	}

	return nil
}

// setNullInterpretation sets the crud stored procedure for import.
func setNullInterpretation(conn *connector.Conn, nullVal string) error {
	res, err := conn.Eval(evalSetNullInterpretation(nullVal))
	if err != nil {
		return err
	}
	nullSet := make([]bool, 1)
	err = yaml.Unmarshal([]byte((res[0]).(string)), &nullSet)
	if err != nil {
		resYAML := []byte((res[0]).(string))
		var rawRes interface{}
		if err := yaml.Unmarshal(resYAML, &rawRes); err != nil {
			return err
		}

		return fmt.Errorf(string(resYAML[5:]))
	}

	return nil
}

// uploadBatchToRouter uploads the batch to the router side.
func uploadBatchToRouter(conn *connector.Conn, batch *Batch, csvReader *ttcsv.Reader) error {
	jsonBatch, err := json.Marshal(batch)
	jsonBatchStr := string(jsonBatch)
	if err != nil {
		return err
	}
	res, err := conn.Eval(evalUploadBatchToRouter(jsonBatchStr))
	if err != nil {
		return err
	}
	batchUploaded := make([]bool, 1)
	err = yaml.Unmarshal([]byte((res[0]).(string)), &batchUploaded)
	if err != nil {
		resYAML := []byte((res[0]).(string))
		var rawRes interface{}
		if err := yaml.Unmarshal(resYAML, &rawRes); err != nil {
			return err
		}

		return err
	}

	return nil
}

// swapAccordingToHeader performs the permutations in accordance with the match parameter.
func swapAccordingToHeader(conn *connector.Conn, batch *Batch) error {
	res, err := conn.Eval(evalSwapAccordingToHeader())
	if err != nil {
		return err
	}
	swapedOk := make([]bool, 1)
	err = yaml.Unmarshal([]byte((res[0]).(string)), &swapedOk)
	if err != nil {
		resYAML := []byte((res[0]).(string))
		var rawRes interface{}
		if err := yaml.Unmarshal(resYAML, &rawRes); err != nil {
			return err
		}

		return err
	}

	return nil
}

// castTuplesToSpaceFormat performs type conversion in accordance
// with the format of the target space.
func castTuplesToSpaceFormat(conn *connector.Conn, csvReader *ttcsv.Reader, batch *Batch) error {
	res, err := conn.Eval(evalcastTuplesToSpaceFormat())
	if err != nil {
		return err
	}
	batchCasted := make([]bool, 1)
	err = yaml.Unmarshal([]byte((res[0]).(string)), &batchCasted)
	if err != nil {
		resYAML := []byte((res[0]).(string))
		var rawRes interface{}
		if err := yaml.Unmarshal(resYAML, &rawRes); err != nil {
			return err
		}

		return err
	}

	return nil
}

// importUploadedBatch calls a crud stored procedure to import.
func importUploadedBatch(conn *connector.Conn) error {
	_, err := conn.Eval(evalImportPreparedBatch())
	if err != nil {
		return err
	}

	return nil
}

// getBatchImportedCtx allows to get the context of the batch after an attempt to import.
func getBatchImportedCtx(conn *connector.Conn, csvReader *ttcsv.Reader) (string, error) {
	res, err := conn.Eval(evalGetBatchImportCtx())
	if err != nil {
		return "", err
	}
	errorOccurred := make([]bool, 1) // it is false if import was ok.
	err = yaml.Unmarshal([]byte((res[0]).(string)), &errorOccurred)
	if err != nil {
		// (res[0]).(string) is not false, it has final batch ctx after import.
		resYAML := []byte((res[0]).(string))
		var rawRes interface{}
		if err := yaml.Unmarshal(resYAML, &rawRes); err != nil {
			return "", err
		}

		return string(resYAML), nil
	}
	fmt.Printf("\n Crud error: %t\n", errorOccurred) // errorOccurred is false.

	return "", nil
}

// evalSetTargetspace generates an eval for a call on the router side.
func evalSetTargetspace(space string) string {
	funcArg := "('" + space + "')"
	return "box.session.storage.crudimport_set_targetspace" + funcArg
}

// evalCheckTargetSpaceExist generates an eval for a call on the router side.
func evalCheckTargetSpaceExist() string {
	return "box.session.storage.crudimport_check_targetspace_exist()"
}

// evalSetNullInterpretation generates an eval for a call on the router side.
func evalSetNullInterpretation(nullVal string) string {
	funcArg := "('" + nullVal + "')"
	return "box.session.storage.crudimport_set_null_interpretation" + funcArg
}

// evalSetCrudOperation generates an eval for a call on the router side.
func evalSetCrudOperation(operation string) string {
	funcArg := "('" + operation + "')"
	return "box.session.storage.crudimport_set_stored_procedure" + funcArg
}

// evalUploadBatchToRouter generates an eval for a call on the router side.
func evalUploadBatchToRouter(batch string) string {
	// NOTE: will be problem with user data like '[========[' or ']========]'.
	// But what is the probability of such data from the user?
	funcArg := "([========[ " + batch + " ]========])"
	return "box.session.storage.crudimport_upload_batch_from_parser" + funcArg
}

// evalSwapAccordingToHeader generates an eval for a call on the router side.
func evalSwapAccordingToHeader() string {
	return "box.session.storage.crud_import_swap_according_to_header()"
}

// evalcastTuplesToSpaceFormat generates an eval for a call on the router side.
func evalcastTuplesToSpaceFormat() string {
	return "box.session.storage.crudimport_cast_tuples_to_scapce_format()"
}

// evalImportPreparedBatch generates an eval for a call on the router side.
func evalImportPreparedBatch() string {
	return "box.session.storage.crudimport_import_prepared_batch()"
}

// evalGetBatchImportCtx generates an eval for a call on the router side.
func evalGetBatchImportCtx() string {
	return "box.session.storage.crud_import_get_batch_final_ctx()"
}

// mainRouterOperations performs communication actions with the router to import the batch.
// Returns the updated after import batch and a dump sybsystem fatal error if it occurred.
func mainRouterOperations(
	isLastBatch bool,
	batch *Batch,
	batchSequenceCtx *BatchSequenceCtx,
	progressCtx *ProgressCtx,
	crudImportFlags *ImportOpts,
	dumpSubsystemFd *DumpSubsystemFd,
	conn *connector.Conn,
	csvReader *ttcsv.Reader,
) (*Batch, error) {
	// Init struct with args for disk dump subsystem.
	var dumpSubsystemArgs *DumpSubsystemArgs = &DumpSubsystemArgs{
		dumpSubsystemFd: dumpSubsystemFd,
		progressCtx:     progressCtx,
		crudImportFlags: crudImportFlags,
	}

	if err := uploadBatchToRouter(conn, batch, csvReader); err != nil {
		dumpSubsystemArgs.batch = batch
		dumpSubsystemArgs.caughtErr = err
		if err := runDumpSubsystem(dumpSubsystemArgs); err != nil {
			logDumpSubsystemMalfunction()
			return batch, err
		}

		if !isLastBatch {
			// Clean batch and move batch window after fail.
			batch = moveBatchWindow(batchSequenceCtx)
		}

		return batch, nil
	}

	if crudImportFlags.Match == "header" {
		if err := swapAccordingToHeader(conn, batch); err != nil {
			dumpSubsystemArgs.batch = batch
			dumpSubsystemArgs.caughtErr = err
			if err := runDumpSubsystem(dumpSubsystemArgs); err != nil {
				logDumpSubsystemMalfunction()
				return batch, err
			}

			if !isLastBatch {
				// Clean batch and move batch window after fail.
				batch = moveBatchWindow(batchSequenceCtx)
			}

			return batch, nil
		}
	}

	if err := castTuplesToSpaceFormat(conn, csvReader, batch); err != nil {
		dumpSubsystemArgs.batch = batch
		dumpSubsystemArgs.caughtErr = err
		if err := runDumpSubsystem(dumpSubsystemArgs); err != nil {
			logDumpSubsystemMalfunction()
			return batch, err
		}

		if !isLastBatch {
			// Clean batch and move batch window after fail.
			batch = moveBatchWindow(batchSequenceCtx)
		}

		return batch, nil
	}

	if err := importUploadedBatch(conn); err != nil {
		dumpSubsystemArgs.batch = batch
		dumpSubsystemArgs.caughtErr = err
		if err := runDumpSubsystem(dumpSubsystemArgs); err != nil {
			logDumpSubsystemMalfunction()
			return batch, err
		}

		if !isLastBatch {
			// Clean batch and move batch window after fail.
			batch = moveBatchWindow(batchSequenceCtx)
		}

		return batch, nil
	}

	var finalBatchCtx string
	var err error
	finalBatchCtx, err = getBatchImportedCtx(conn, csvReader)
	if err != nil {
		dumpSubsystemArgs.batch = batch
		dumpSubsystemArgs.caughtErr = err
		if err := runDumpSubsystem(dumpSubsystemArgs); err != nil {
			logDumpSubsystemMalfunction()
			return batch, err
		}

		if !isLastBatch {
			// Clean batch and move batch window after fail.
			batch = moveBatchWindow(batchSequenceCtx)
		}

		return batch, nil
	}

	// updatedBatch will be at updatedBatch[0] (len(updatedBatch) = 1),
	// because tt connect responce slice.
	var updatedBatch []*Batch
	// No need for err check, because it already done at getBatchImportedCtx() level.
	_ = yaml.Unmarshal([]byte(finalBatchCtx), &updatedBatch)

	dumpSubsystemArgs.batch = updatedBatch[0]
	dumpSubsystemArgs.caughtErr = err
	if err := runDumpSubsystem(dumpSubsystemArgs); err != nil {
		logDumpSubsystemMalfunction()
		return batch, err
	}

	if !isLastBatch {
		// Clean batch and move batch window after fail.
		batch = moveBatchWindow(batchSequenceCtx)
	}

	if err := dumpProgressFile(dumpSubsystemArgs.dumpSubsystemFd, progressCtx); err != nil {
		logDumpSubsystemMalfunction()
		return batch, err
	}

	return batch, nil
}
