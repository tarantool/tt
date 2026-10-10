package binary

import (
	"fmt"
	"os"
)

const execOwnerPerm uint32 = 0o100

// IsExecOwner checks if specified file has owner execute permissions.
func IsExecOwner(path string) (bool, error) {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return false, fmt.Errorf("cannot stat the file: %w", err)
	}

	perm := fileInfo.Mode().Perm()

	return BitHas32(uint32(perm), execOwnerPerm), nil
}
