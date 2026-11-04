package wasm

import "errors"

var (
	// ErrUnknownRelease reports a tarantool-wasm version tt has no table entry
	// for.
	ErrUnknownRelease = errors.New("unknown release")
	// ErrUnsupportedPlatform reports a host the release has no native build
	// for.
	ErrUnsupportedPlatform = errors.New("unsupported platform")
	// ErrChecksum reports a downloaded asset whose checksum differs from the
	// one tt holds the release to.
	ErrChecksum = errors.New("checksum mismatch")
	// ErrNoManifest reports a directory that is not a tt project.
	ErrNoManifest = errors.New("not a tt project")
	// ErrPython reports a Python that cannot host tarawasm.
	ErrPython = errors.New("no suitable python")
)

var (
	// ErrUnknownLanguage reports a language tt knows no toolchain of.
	ErrUnknownLanguage = errors.New("unknown language")
	// ErrToolchain reports tools the user has to install first.
	ErrToolchain = errors.New("the toolchain is not ready")
	// ErrNoTarawasm reports a command that needs the tarawasm of the project.
	ErrNoTarawasm = errors.New("no tarawasm in the project")
	// ErrSDKVersion reports a component built against another SDK than the
	// runtime the package declares.
	ErrSDKVersion = errors.New("SDK version mismatch")
	// ErrComponentExists reports a component the manifest already declares.
	ErrComponentExists = errors.New("component already declared")
)
