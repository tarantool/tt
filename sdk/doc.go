// Package sdk is the contract between tt and the modules compiled into it.
//
// A module may import this module and the other modules under sdk/ and
// nothing else from the tt repository.
//
// The root package holds the module contract and the error contract:
//
//   - a module is a [Constructor]: given the core's [Services], it returns
//     cobra commands as [Mount]s, each with the path of the group it goes
//     under;
//   - [Services] give a module's commands a logger, the [Tarantool] to run
//     and its [TarantoolVersion], files read through the [Integrity] checks,
//     the [Project] directory, prompts ([Services.Confirm]) and the standard
//     [Streams] with their printers;
//   - [ExitError] and [ExitCode] carry the exit code a command's error should
//     end tt with, and [UsageError] marks an error in how a command was
//     invoked, which tt follows with the command's usage;
//   - [CoreVersion] reports the version of the tt core the binary was built
//     with;
//   - [ClusterSource] names where a cluster configuration comes from - an
//     application ([AppSource]), a file ([FileSource]) or a configuration
//     storage ([StorageSource]) - and [ParseClusterSource] tells them apart
//     in a command-line argument; [SplitInstance] splits an "app:instance"
//     reference, and [Instances] and [InstanceConfig] read the instances of
//     a cluster configuration and the configuration each of them resolves.
//
// The subpackages hold the rest:
//
//   - [github.com/tarantool/tt/sdk/console] runs an interactive console
//     with line editing and history over a command processor;
//   - [github.com/tarantool/tt/sdk/formatter] renders the YAML a Tarantool
//     console answers with as YAML, Lua or tables, the way tt's consoles
//     print it;
//   - [github.com/tarantool/tt/sdk/log] is the logging facade and the secret
//     redaction logic;
//   - [github.com/tarantool/tt/sdk/output] writes a command's result to stdout
//     in the format the user asked for;
//   - [github.com/tarantool/tt/sdk/sdktest] fakes the core's Services and
//     runs a module's commands in tests.
//
// The contract module depends on cobra, pflag and go-config
// (github.com/tarantool/go-config/v2), whose types appear in it, and pins
// their versions: a module builds against the versions the core uses. It
// also depends on [github.com/tarantool/tt/sdk/connect], whose URI rules
// [ParseClusterSource] applies. The formatter adds go-pretty and yaml.v2,
// which render its output, and the console adds go-prompt and x/term.
//
// The process itself - which handlers log where, at what level, in which
// format - is configured by the tt core. Nothing in the SDK changes process
// state beyond what its documentation says.
package sdk
