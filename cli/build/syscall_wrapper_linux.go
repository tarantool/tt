//go:build linux

package build

import (
	"syscall"
)

func Dup2(oldfd int, newfd int) (err error) {
	return syscall.Dup3(oldfd, newfd, 0)
}
