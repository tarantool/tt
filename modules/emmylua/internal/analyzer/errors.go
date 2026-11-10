package analyzer

import "errors"

var (
	// ErrUnknownRelease reports an EmmyLua version tt has no table entry for.
	ErrUnknownRelease = errors.New("unknown release")
	// ErrUnsupportedPlatform reports a host the release has no build for.
	ErrUnsupportedPlatform = errors.New("unsupported platform")
	// ErrNoManifest reports a directory that is not a tt project.
	ErrNoManifest = errors.New("not a tt project")
	// ErrBuild reports a local build that lacks a program or is not one.
	ErrBuild = errors.New("not an EmmyLua build")
	// ErrNotPrepared reports a project whose rocks have no EmmyLua.
	ErrNotPrepared = errors.New("EmmyLua is not installed in the project")
)
