package cluster

import (
	"errors"
	"fmt"
)

var (
	errDataMissing   = errors.New("data does not exist")
	errWrongRevision = errors.New("wrong revision")

	errFetchData   = errors.New("failed to fetch data")
	errPutData     = errors.New("failed to put data")
	errPublishData = errors.New("failed to publish data")
)

// NotExistError error type for non-existing path.
type NotExistError struct {
	path []string
}

// Error - error interface implementation for NotExistError.
func (e NotExistError) Error() string {
	return fmt.Sprintf("path %q does not exist", e.path)
}

// CollectEmptyError responses on DataCollector.Collect() if
// config was not found with specified prefix.
type CollectEmptyError struct {
	storage string
	prefix  string
}

// Error - error interface implementation for NoConfigError.
func (e CollectEmptyError) Error() string {
	return fmt.Sprintf("a configuration data not found in %s for prefix %q",
		e.storage, e.prefix)
}
