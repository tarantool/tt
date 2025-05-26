//go:build darwin

package build

import (
	"syscall"
)

func Dup2(oldfd, newfd int) (err error) {
	return syscall.Dup2(oldfd, newfd)
}
