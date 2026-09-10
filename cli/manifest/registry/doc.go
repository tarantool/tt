// Package registry implements the commands that address rock servers
// directly: reporting the effective server list, searching it, and downloading
// rock files into a directory.
//
// It is the composition layer over cli/manifest/rocks for those three. The
// adapter supplies the primitives - search, download, and the admin command
// that indexes a directory - and this package decides what each command means:
// which servers are consulted, which versions are taken, where the files land,
// and how the answer is rendered. Nothing here resolves a dependency graph or
// writes into .rocks/; that belongs to resolve and build.
//
// The download command is a mirror builder rather than a file fetcher. What
// makes the downloaded files useful is the LuaRocks `manifest` written beside
// them: a directory carrying one is a rock server, so the result can be handed
// straight back to any command that takes a registry, and the project builds
// with no network at all.
package registry
