// Package instances parses the description of the instances an application
// runs: the instances.yaml of a project template or of a host. It takes
// bytes rather than a path, because the records of the instance registry in
// etcd, when it comes, are in the same format and go through the same
// parser.
//
// # Format
//
// A description is a YAML mapping from an instance name to its record, like
// the instances.yml of tt2, so a tt2 file is a valid description and
// migrating one comes down to adding fields. An empty record, written as a
// bare name or as {}, is valid:
//
//	router-1:
//	storage-1: {}
//	legacy-1:
//	  script: init.lua
//	worker-1:
//	  binary: ./bin/worker
//	  args: [--port, 8080]
//	  env:
//	    LOG_LEVEL: info
//	  workdir: ./var/worker
//	  stop_signal: SIGINT
//	  stop_timeout: 30s
//	  restart: on-failure
//
// The fields a record sets tell what the instance is (Kind): with neither
// binary nor script it is a Tarantool instance configured by the cluster
// config, with script a Tarantool instance that configures itself in its
// script (box.cfg), with binary an external process. binary and script are
// mutually exclusive.
//
// Parse returns the records in the order of the description: the order the
// instances start in and the order status lists them in follow it, so
// neither may depend on the iteration order of a map.
//
// # Strictness
//
// Whatever the parser does not understand is an error, never dropped: an
// unknown field, a field without a value, a name declared twice, a second
// YAML document. A misspelt field that is silently dropped leaves the
// instance running with the default its author meant to change; tt2 read
// instances.yml that way, and that is what users complained about.
//
// Aliases and merge keys are refused as well. Without them every value in
// the result is written out in the input, so a small input cannot expand
// into a huge result; and accepting them later breaks no description written
// before.
//
// Parse reports every problem it finds, not only the first, each as an
// *Error that wraps ErrInvalid and names the line, the instance and the
// field, so that a broken description is fixed in one pass.
//
// # Values
//
// A field that holds a string, and every item of args and value of env,
// takes a plain scalar by the text it is written with: "args: [--port,
// 8080]" passes "8080", and "RATIO: 1.50" in env sets "1.50", not the "1.5"
// a round trip through a number would give. A scalar with a tag that is not
// a plain YAML type (!!binary, a local tag) is refused, because its text is
// not the value its author meant.
//
// stop_signal is a signal name with or without the SIG prefix, in upper
// case; only the signals that Linux and macOS share are known, so that a
// description runs on either. stop_timeout is a positive Go duration such as
// 30s. restart is one of the values of systemd's Restart= that the
// supervisor can follow: no (the default), on-failure and always.
//
// # Paths
//
// A relative binary, script or workdir is resolved against the application
// directory given to Parse, not against the location of the description:
// the description of a host lives in <root>/etc/<app>/, while the paths in
// it point into the version of the application, so ./bin/worker means the
// same in a template and in the description of a host. The one exception is
// a binary without a slash: "binary: python3" names a program, looked up in
// PATH when the instance starts, the way a shell looks up a command. A file
// shipped with the application is spelled with a slash, as bin/worker or
// ./worker; a script is always a path, since it is never looked up.
//
// # Out of scope
//
// The parser reads no files. Whether the names of the Tarantool instances
// are in the topology of the cluster config is checked by whoever loads the
// config together with the description. Every field is accepted in every
// record, and fields a record leaves out stay zero, except restart: which
// fields an instance of each kind honours, and the defaults for the stop
// signal and the stop timeout, belong to whoever builds its supervisor.Spec,
// and they differ between Tarantool and external processes.
package instances
