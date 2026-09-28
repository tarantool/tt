//go:build !unix

package integrity

// openNonBlock is empty where opening a file has no non-blocking flag.
const openNonBlock = 0
