package integrity

import "errors"

var (
	ErrNoSignerInCE   = errors.New("integrity signer should never be created in ce")
	ErrNoVerifierInCE = errors.New("integrity checks should never be initialized in ce")
	ErrNotConfigured  = errors.New("integrity check is not configured")
)
