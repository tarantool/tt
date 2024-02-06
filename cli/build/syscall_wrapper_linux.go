//go:build linux

package build

import (
	"syscall"
)

// Dup2 is a dup3 syscall wrapper to use on Linux instead of dup2 syscall.
func Dup2(oldfd int, newfd int) (err error) {
	if oldfd != newfd { // dup2 should not fail if FDs are equal.
		return syscall.Dup3(oldfd, newfd, 0)
	}
	return nil
}
