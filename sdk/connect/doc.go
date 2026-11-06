// Package connect parses the connection strings and URIs tt commands take
// and holds the names and the help of the credentials they read.
//
// A Tarantool URI has no scheme: host:port, tcp://host:port, unix/:path,
// unix://path or a path, optionally with user:password@ before the address
// and a ?query after host:port, tcp://host:port or unix/:path. [IsBaseURI]
// and [IsCredentialsURI] tell the forms apart, [ParseCredentialsURI] takes
// the credentials out of a URI, [SplitURIQuery] splits the query off and
// [ParseBaseURI] returns the network and the address, which a dialer of
// [github.com/tarantool/tt/sdk/dial] takes as network://address.
//
// A URL of an etcd or a Tarantool config storage has a scheme and a host,
// such as https://user:pass@host:2379/prefix?timeout=5: [CreateURIOpts]
// parses it into [URIOpts], with the SSL parameters and the timeout, and
// [MakeURLHelp] writes the help of a command argument that takes one.
//
// The package applies no precedence to credentials: a command chooses
// between the ones a URI carries, its flags and the environment itself.
// tt's commands take the URI's, which exclude the flags; otherwise the
// flags, and for an empty one [TarantoolUsernameEnv] and
// [TarantoolPasswordEnv] ([EtcdUsernameEnv] and [EtcdPasswordEnv] for
// etcd), which [EnvTarantoolCredentialsHelp] and [EnvEtcdCredentialsHelp]
// describe in a command's help. [RedactCredentials] hides the credentials
// of a URI in a message.
//
// The API of this package is alpha and may change incompatibly.
package connect
