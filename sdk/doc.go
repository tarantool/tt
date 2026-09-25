// Package sdk is the contract between tt and the modules compiled into it.
//
// A module may import this module and the other modules under sdk/ and
// nothing else from the tt repository. The root package holds the exit-code
// contract; the subpackages hold the rest:
//
//   - [github.com/tarantool/tt/sdk/log] is the logging facade and the secret
//     redaction logic;
//   - [github.com/tarantool/tt/sdk/output] writes a command's result to stdout
//     in the format the user asked for.
//
// The process itself - which handlers log where, at what level, in which
// format - is configured by the tt core. Nothing in the SDK changes process
// state beyond what its documentation says.
package sdk
