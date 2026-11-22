package sdk

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// ErrEval reports a Lua error that the code [Conn.Eval] ran raised on the
// instance, rather than a failure of the connection or of the request. The
// error that wraps it reads as the message of the Lua error alone; test for
// it with errors.Is.
var ErrEval = errors.New("the code raised a Lua error")

// errUnknownAuthType reports an authentication method --auth-type does not
// know.
var errUnknownAuthType = errors.New("unknown authentication method")

// DefaultConnectTimeout is how long [Services.Connect] waits for a
// connection over iproto to be set up when [ConnectOptions] sets no
// timeout, as tt connect waits without --connect-timeout.
const DefaultConnectTimeout = time.Second

// authTypes are the authentication methods ConnectOptions.AuthType names,
// "auto" first, as the auth_type of Tarantool names them.
var authTypes = []string{"auto", "chap-sha1", "pap-sha256"}

// The flags with which [AddConnectFlags] declares the options of a
// connection, those of tt connect.
const (
	usernameFlag        = "username"
	passwordFlag        = "password"
	authTypeFlag        = "auth-type"
	sslKeyFileFlag      = "sslkeyfile"
	sslCertFileFlag     = "sslcertfile"
	sslCaFileFlag       = "sslcafile"
	sslCiphersFlag      = "sslciphers"
	sslPasswordFlag     = "sslpassword"
	sslPasswordFileFlag = "sslpasswordfile"
	connectTimeoutFlag  = "connect-timeout"
)

// ConnectOptions are how [Services.Connect] connects to an instance: the
// flags of tt connect, which [AddConnectFlags] declares. The zero
// ConnectOptions connects as tt connect with no flag does.
type ConnectOptions struct {
	// Lookup finds the application of an <app>:<instance> target, as
	// --scope and --template do.
	Lookup AppLookup
	// Username is the user a URI target that carries no credentials is
	// logged in as, --username; empty, the user TT_CLI_USERNAME names. An
	// <app>:<instance> target takes no user.
	Username string
	// Password is the password of Username, --password; empty, the one
	// TT_CLI_PASSWORD holds.
	Password string
	// AuthType is the authentication method of a connection over iproto,
	// --auth-type: "auto", which an empty one stands for, takes the server's
	// over TLS and chap-sha1 over a plain connection; "chap-sha1"; or
	// "pap-sha256", which sends the password as it is, and so needs TLS. Any
	// other name is an error of Connect.
	AuthType string
	// SslKeyFile is the private key a TLS connection presents, --sslkeyfile.
	// Any of the TLS settings makes a connection over iproto a TLS one, as
	// transport=ssl in the query of the URI does; transport=plain refuses
	// them. A setting the query sets as well is an error.
	SslKeyFile string
	// SslCertFile is the certificate of SslKeyFile, --sslcertfile.
	SslCertFile string
	// SslCaFile holds the certificate authorities the server's certificate
	// is checked against, --sslcafile.
	SslCaFile string
	// SslCiphers is a colon-separated list of the cipher suites of a TLS
	// connection, --sslciphers.
	SslCiphers string
	// SslPassword decrypts SslKeyFile, --sslpassword.
	SslPassword string
	// SslPasswordFile holds the passwords SslKeyFile is decrypted with, one
	// a line, --sslpasswordfile.
	SslPasswordFile string
	// ConnectTimeout bounds the setup of a connection over iproto - the
	// dial, the greeting and the login - as --connect-timeout does; zero is
	// DefaultConnectTimeout. It does not bound the requests.
	ConnectTimeout time.Duration
}

// Conn is a connection [Services.Connect] opened to an instance, over its
// console socket or over iproto, on which a command runs Lua. What Eval
// takes and returns is the same over either: a command does not know, and
// need not, which one it has.
//
// Only the core and [github.com/tarantool/tt/sdk/sdktest] implement Conn, as
// they implement Services: methods may be added to it in a minor release of
// the SDK. A Conn is safe for concurrent use.
type Conn interface {
	// Eval runs expr on the instance, the body of a Lua chunk of any number
	// of lines, comments included, and returns what the chunk returns. The
	// arguments args are its ..., in order: a Go string is a Lua string with
	// the same bytes, a number a number, a bool a boolean, a slice or a map
	// a table, and nil box.NULL.
	//
	// The results are every value the chunk returns, in order, nil
	// included: return nil, "msg" gives []any{nil, "msg"}, return nil gives
	// []any{nil}, and a chunk that returns nothing gives no value. Each is
	// one of:
	//
	//   - nil, for nil and box.NULL;
	//   - a bool, for a boolean;
	//   - an int64, for a number with no fraction and an integer cdata of
	//     LuaJIT, and a uint64 for one above math.MaxInt64; a float64 for a
	//     number with a fraction, an infinity or a NaN;
	//   - a string, for a string, with its bytes as they are: a Lua string
	//     holds bytes, which need not be UTF-8;
	//   - a []byte, for a varbinary;
	//   - a []any, for a table Tarantool encodes as an array - a sequence,
	//     the empty table, a tuple - and a map[any]any for any other table,
	//     with keys and values of these types in turn.
	//
	// A table is an array or a map as the MessagePack serializer of
	// Tarantool says, which a __serialize metamethod of it changes. A value
	// of another type, such as a decimal, a UUID or a datetime, is not
	// covered: return tostring of it.
	//
	// A Lua error the chunk raises is an error that wraps [ErrEval] and
	// reads as the message of the Lua error without the position Lua puts
	// in front of it: error("x") fails with "x", box.error with its message,
	// and an error raised by a function of the chunk without its line. An
	// expr that does not compile fails with an error that wraps ErrEval and
	// says so, with the line. Any other error - of the connection, or a
	// request the user may not make - does not wrap ErrEval.
	//
	// ctx bounds the wait for the answer, not the run of the chunk: when ctx
	// is done first, Eval returns an error that wraps ctx.Err(), the
	// instance runs the chunk to its end all the same, and the Conn is
	// closed, since a late answer could not be told from the next one.
	// Calls of Eval on one Conn run one at a time.
	Eval(ctx context.Context, expr string, args []any) ([]any, error)
	// Close closes the connection; the calls of Eval after it fail. Close
	// of a closed Conn does nothing and returns nil.
	Close() error
}

// AddConnectFlags declares on cmd the flags with which tt connect connects
// to an instance, bound to opts, with the names, the help and the
// completion of tt connect: -u, --username and -p, --password, --auth-type,
// the TLS settings --sslkeyfile, --sslcertfile, --sslcafile, --sslciphers,
// --sslpassword and --sslpasswordfile, --connect-timeout, and --scope and
// --template, which [AddAppFlags] declares, so that a command that calls
// AddConnectFlags does not call AddAppFlags as well. --auth-type refuses a
// method it does not know as it is parsed; the other values are checked by
// [Services.Connect].
func AddConnectFlags(cmd *cobra.Command, opts *ConnectOptions) {
	AddAppFlags(cmd, &opts.Lookup)

	flags := cmd.Flags()

	flags.StringVarP(&opts.Username, usernameFlag, "u", "", "username")
	flags.StringVarP(&opts.Password, passwordFlag, "p", "", "password")
	flags.StringVar(&opts.SslKeyFile, sslKeyFileFlag, "",
		`path to a private SSL key file`)
	flags.StringVar(&opts.SslCertFile, sslCertFileFlag, "",
		`path to an SSL certificate file`)
	flags.StringVar(&opts.SslCaFile, sslCaFileFlag, "",
		`path to a trusted certificate authorities (CA) file`)
	flags.StringVar(&opts.SslCiphers, sslCiphersFlag, "",
		`colon-separated (:) list of SSL cipher suites the connection can use`)
	flags.StringVar(&opts.SslPassword, sslPasswordFlag, "",
		`password for decrypting the private SSL key file`)
	flags.StringVar(&opts.SslPasswordFile, sslPasswordFileFlag, "",
		`path to a file containing the password for decrypting the private SSL key file`)
	flags.Var(authTypeValue{name: &opts.AuthType}, authTypeFlag,
		`authentication method: auto, chap-sha1 or pap-sha256; `+
			`pap-sha256 needs TLS and Tarantool EE or CE 3.9+`)
	flags.DurationVar(&opts.ConnectTimeout, connectTimeoutFlag, DefaultConnectTimeout,
		`timeout for establishing a binary connection`)

	_ = cmd.RegisterFlagCompletionFunc(authTypeFlag, cobra.FixedCompletions(
		authTypes, cobra.ShellCompDirectiveNoFileComp))
}

// authTypeValue is the value of --auth-type: the name of an authentication
// method, "auto" for none.
type authTypeValue struct {
	name *string
}

// String returns the name of the method.
func (v authTypeValue) String() string {
	if v.name == nil || *v.name == "" {
		return authTypes[0]
	}

	return *v.name
}

// Set sets the method by its name, refusing one it does not know, the empty
// one included, as tt connect does.
func (v authTypeValue) Set(name string) error {
	if !slices.Contains(authTypes, name) {
		return fmt.Errorf("%w %q, use %s", errUnknownAuthType, name,
			strings.Join(authTypes, ", "))
	}

	*v.name = name

	return nil
}

// Type names the type of the value in the help.
func (authTypeValue) Type() string {
	return "string"
}
