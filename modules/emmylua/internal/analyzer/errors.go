package analyzer

import "errors"

var (
	// ErrUnknownRelease reports an EmmyLua version tt has no table entry for.
	ErrUnknownRelease = errors.New("unknown release")
	// ErrUnsupportedPlatform reports a host the release has no build for,
	// while it has builds for others.
	ErrUnsupportedPlatform = errors.New("unsupported platform")
	// ErrNoBuilds reports a release with no builds to download for any
	// platform: one tt knows from a commit of EmmyLua.
	ErrNoBuilds = errors.New("no builds to download")
	// ErrNoManifest reports a directory that is not a tt project.
	ErrNoManifest = errors.New("not a tt project")
	// ErrBuild reports a local build that lacks a program or is not one.
	ErrBuild = errors.New("not an EmmyLua build")
	// ErrNotPrepared reports a project whose rocks have no EmmyLua.
	ErrNotPrepared = errors.New("EmmyLua is not installed in the project")
)
