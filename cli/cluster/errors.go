package cluster

import (
	"fmt"
)

// NotExistError error type for non-existing path.
type NotExistError struct {
	path []string
}

// Error - error interface implementation for NotExistError.
func (e NotExistError) Error() string {
	return fmt.Sprintf("path %q does not exist", e.path)
}
