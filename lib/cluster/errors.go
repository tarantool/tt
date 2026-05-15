package cluster

import (
	"errors"
	"fmt"
)

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

var (
	errDataMissing   = errors.New("data does not exist")
	errWrongRevision = errors.New("wrong revision")
)
