// Package app resolves what an instance command acts on: the application a
// target names, the directory it lives in, the pair of instances description
// and cluster config it runs from, and the instances the target selects. It
// takes over from cli/running the part that took the directory of tt.yaml
// for the application and its name for the application's name.
//
// It finds and reads; it does not run anything. Where the data, the logs and
// the sockets of an instance go, and which tarantool runs it, are built on
// top of its result.
//
// # Targets
//
// An instance command takes app:inst, app:* or no target, and lists of the
// first two separated by commas or given as several arguments (ParseTargets).
// This is the target grammar every namespace shares, read the same way in
// each. A bare app is a cluster-level target there, so an instance command
// refuses it with a hint rather than taking it for app:*: forgetting an
// instance name must not stop every instance. There is no :inst short form;
// an empty application name is kept free for one, should it be needed.
package app
