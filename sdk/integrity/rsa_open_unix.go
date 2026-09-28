//go:build unix

package integrity

import "syscall"

// openNonBlock makes an open return at once for a file, such as a FIFO,
// that would otherwise wait for another party.
const openNonBlock = syscall.O_NONBLOCK
