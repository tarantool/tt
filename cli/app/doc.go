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
//
// # Scopes
//
// An application lives in one of three scopes, and --scope picks one
// outright. Without it, app:... is the project the command runs in when that
// project is called app, and the system scope otherwise; the user scope is
// reached through --scope only. No target is the project the command runs
// in, and there has to be one. A name missing from the chosen scope is never
// looked for in the others: what a command acts on must not depend on what
// happens to be installed where.
//
// The project is the nearest directory, up from the -C directory or the
// working directory, that holds a project marker (scope.FindProject). It is
// named by [package].name of its manifest, or after its directory when it
// has no manifest. A manifest that does not parse is an error, not a reason
// to try the system scope: the project could be the application meant.
//
// In the user and system scopes an application is <root>/apps/<app>, a link
// to its active version, and the link is resolved at once: the version
// directory is the application directory. The instances get it as their
// working directory and the description's paths point into it, so an
// instance keeps running the version it started from when the link moves.
//
// A caller may pin the application directory instead (Options.Dir): the
// watchdog of a running instance does, so that restarting it after an exit
// stays on the version it started from, while the pair is read anew.
//
// # Pairs
//
// An application runs from a pair: instances.yaml, what runs on this host,
// and config.yaml, the file tarantool -c gets. They must agree on the names
// of the Tarantool instances, and different templates describe different
// topologies, so a pair is taken whole from one directory and never put
// together from two.
//
// In the user and system scopes the pair of the host, <root>/etc/<app>/, wins
// when it is there; otherwise, and always in a project, the pair is the
// template .templates/<template>/ of the application, "default" unless
// --template names another. The pair of the host lives outside of the
// version, which is immutable, so that it survives switching versions. One
// file of a pair without the other is an error, never a reason to fall back
// to the template, which would start a topology the host was not given; and
// --template is refused when the host has a pair.
//
// The config is loaded by cluster.GetClusterConfig, so a config holding only
// config.etcd or config.storage brings in the rest of the cluster config
// from the storage, as for every other command. Every record without binary
// or script is a Tarantool instance configured by that config, and must be
// in its topology; the topology may hold more instances, which run on other
// hosts. Records with script or binary are not checked against it.
//
// A Tarantool instance, configured by the cluster config or by its script,
// runs in the application directory with the command line tt makes up, so
// workdir and args in its record are refused rather than silently ignored.
// They are fields of external processes.
//
// # Errors
//
// Every error carries sdk.ExitFailure: the fix is on the user's side, and a
// failure of the system underneath, such as a refused read, still shows
// through what it wraps. The failures a caller may want to tell apart wrap
// the Err* values. When the command runs where there is no project
// but tt2 left its files, tt.yaml or instances.yml, the error says so and
// points to the migration guide; the files are only looked for, never read.
package app
