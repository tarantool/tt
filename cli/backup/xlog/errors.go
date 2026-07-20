package xlog

import "errors"

var (
	ErrInvalidUUID          = errors.New("xlog: invalid instance UUID")
	ErrTrimFileNotFound     = errors.New("xlog: no xlog file contains the recovery point")
	ErrLSNOverflow          = errors.New("xlog: lsn out of range for int64<->uint64 conversion")
	ErrInPlaceWidthMismatch = errors.New(
		"xlog: on-disk UUID width differs from replacement; use distinct src and dst")
)
