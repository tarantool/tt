package cmd

import (
	"errors"
	"fmt"
	"maps"
	"os"
	"slices"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/tarantool/tt/sdk"
	sdkconnect "github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/dial"
	"github.com/tarantool/tt/sdk/formatter"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/cli/cmd/internal"
	"github.com/tarantool/tt/v3/cli/cmdcontext"
	"github.com/tarantool/tt/v3/cli/connect"
	"github.com/tarantool/tt/v3/cli/connector"
	"github.com/tarantool/tt/v3/cli/running"
	"github.com/tarantool/tt/v3/cli/util"
	terminal "golang.org/x/term"
)

var (
	errControlSocketCredentialsUnsupported = errors.New(
		"username and password are not supported with a connection via a control socket",
	)
	errCredentialsSpecifiedByFlagsAndURI = errors.New(
		"username and password are specified with flags and a URI",
	)
	errConnectionStringRequired = errors.New("should be specified one connection string")
	errSpecifyInstanceName      = errors.New("specify instance name")
	errURIParamSpecifiedByFlag  = errors.New(
		"is specified with a flag and in the query of a URI",
	)
	errUnsupportedURIParam = errors.New("unsupported parameter in the query of a URI")
	errUnknownTransport    = errors.New(`unknown transport in the query of a URI, ` +
		`use "ssl" or "plain"`)
	errPlainTransportWithTLS = errors.New(
		"is a TLS setting, which conflicts with transport=plain in the query of a URI",
	)
	errNotATarget = errors.New("neither an instance of an application nor a connection URI")
)

// URI parameters tt connect takes from the query of a URI, as it takes the
// same settings from flags. The password of the credentials is not among
// them: it has the place before the "@".
const (
	uriTransportParam       = "transport"
	uriSslKeyFileParam      = "ssl_key_file"
	uriSslCertFileParam     = "ssl_cert_file"
	uriSslCaFileParam       = "ssl_ca_file"
	uriSslCiphersParam      = "ssl_ciphers"
	uriSslPasswordParam     = "ssl_password"
	uriSslPasswordFileParam = "ssl_password_file"
)

// uriParamFlags names the flag that sets each TLS setting of the query of a
// URI.
var uriParamFlags = map[string]string{
	uriSslKeyFileParam:      "sslkeyfile",
	uriSslCertFileParam:     "sslcertfile",
	uriSslCaFileParam:       "sslcafile",
	uriSslCiphersParam:      "sslciphers",
	uriSslPasswordParam:     "sslpassword",
	uriSslPasswordFileParam: "sslpasswordfile",
}

var (
	connectUser            string
	connectPassword        string
	connectFile            string
	connectLanguage        string
	connectFormat          string
	connectSslKeyFile      string
	connectSslCertFile     string
	connectSslCaFile       string
	connectSslCiphers      string
	connectSslPassword     string
	connectSslPasswordFile string
	connectTimeout         = connector.DefaultConnectTimeout
	connectInteractive     bool
	connectBinary          bool
	connectEvaler          string
)

// NewConnectCmd creates tt connect, the documented facade of
// tt instance connect: the same command, with the same flags, which has no
// behaviour of its own.
func NewConnectCmd() *cobra.Command {
	connectCmd := newConnectCmd("connect")

	connectCmd.Short = "Connect to the tarantool instance (tt instance connect)"

	return connectCmd
}

// newConnectCmd creates the connect command that the command line tt path
// runs: tt connect or tt instance connect.
func newConnectCmd(path string) *cobra.Command {
	connectCmd := &cobra.Command{
		Use: "connect (<APP>:<INSTANCE> | <URI>)" +
			" [flags] [-f <FILE>] [-- ARGS]\n" +
			"  COMMAND | tt " + path + " (<APP>:<INSTANCE> | <URI>)" +
			" [flags]\n" +
			"  COMMAND | tt " + path + " (<APP>:<INSTANCE> | <URI>)" +
			" [flags] [-f-] [-- ARGS]\n\n" +
			"  The URI can be specified in the following formats:\n" +
			"  * [tcp://][username:password@][host:port]\n" +
			"  * [unix://][username:password@]socketpath\n" +
			"  To specify relative path without `unix://` use `./`.\n\n" +
			"  Available commands:\n" +
			"  * \\shortcuts - get the full list of available shortcuts\n" +
			"  * \\set language <language> - set language (lua or sql)\n" +
			"  * \\set output <format> - set output format (lua[,line|block] or yaml)\n" +
			"  * \\set delimiter <delimiter> - set expression delimiter\n" +
			"  * \\help - show available backslash commands\n" +
			"  * \\quit - quit interactive console",
		Short: "Connect to the tarantool instance",
		Long: "Connect to the tarantool instance.\n\n" +
			sdkconnect.EnvTarantoolCredentialsHelp + "\n\n" +
			"You could pass command line arguments to the interpreted SCRIPT" +
			" or COMMAND passed via -f flag:\n\n" +
			`echo "print(...)" | tt ` + path + ` user:pass@localhost:3013 -f- 1, 2, 3`,
		RunE: internalRunE(internalConnectModule),
		Args: cobra.MinimumNArgs(1),
		ValidArgsFunction: func(
			cmd *cobra.Command,
			args []string,
			toComplete string,
		) ([]string, cobra.ShellCompDirective) {
			if len(args) != 0 {
				return nil, cobra.ShellCompDirectiveNoFileComp
			}

			validArgs, directive := internal.ValidArgsFunction(&cmdCtx, cmd, toComplete,
				internal.Active)
			if len(validArgs) == 0 {
				// A unix socket may be given by its path.
				return nil, cobra.ShellCompDirectiveDefault
			}

			return validArgs, directive
		},
	}

	addInstanceFlags(connectCmd)

	connectCmd.Flags().StringVarP(&connectUser, "username", "u", "", "username")
	connectCmd.Flags().StringVarP(&connectPassword, "password", "p", "", "password")
	connectCmd.Flags().StringVarP(&connectFile, "file", "f", "",
		`file to read the script for evaluation. "-" - read the script from stdin`)
	connectCmd.Flags().StringVarP(&connectLanguage, "language", "l",
		connect.DefaultLanguage.String(), `language: lua or sql`)
	connectCmd.Flags().StringVarP(&connectFormat, "outputformat", "x",
		formatter.DefaultFormat.String(), `output format: yaml, lua, table or ttable`)
	connectCmd.Flags().StringVar(&connectSslKeyFile, "sslkeyfile", "",
		`path to a private SSL key file`)
	connectCmd.Flags().StringVar(&connectSslCertFile, "sslcertfile", "",
		`path to an SSL certificate file`)
	connectCmd.Flags().StringVar(&connectSslCaFile, "sslcafile", "",
		`path to a trusted certificate authorities (CA) file`)
	connectCmd.Flags().StringVar(&connectSslCiphers, "sslciphers", "",
		`colon-separated (:) list of SSL cipher suites the connection`)
	connectCmd.Flags().StringVar(&connectSslPassword, "sslpassword", "",
		`password for decrypting the private SSL key file`)
	connectCmd.Flags().StringVar(&connectSslPasswordFile, "sslpasswordfile", "",
		`path to a file containing the password for decrypting the private SSL key file`)
	connectCmd.Flags().DurationVar(&connectTimeout, "connect-timeout",
		connector.DefaultConnectTimeout, `timeout for establishing a binary connection`)
	connectCmd.Flags().BoolVarP(&connectInteractive, "interactive", "i",
		false, `enter interactive mode after executing 'FILE'`)
	connectCmd.Flags().BoolVarP(&connectBinary, "binary", "",
		false, `connect to instance using binary port`)
	connectCmd.Flags().StringVar(&connectEvaler, "evaler", "",
		`use the provided Lua expression as an interpreter for user's input of the connection.
If the evaler code is prefixed with @, the rest should be a file name to read the evaler
code from`)

	_ = connectCmd.Flags().MarkHidden("evaler")

	return connectCmd
}

// makeConnOpts makes and returns connect options from the arguments.
func makeConnOpts(network, address string, connCtx connect.ConnectCtx) connector.ConnectOpts {
	ssl := connector.SslOpts{
		KeyFile:      connCtx.SslKeyFile,
		CertFile:     connCtx.SslCertFile,
		CaFile:       connCtx.SslCaFile,
		Ciphers:      connCtx.SslCiphers,
		Password:     connCtx.SslPassword,
		PasswordFile: connCtx.SslPasswordFile,
		Transport:    connCtx.Transport,
	}

	return connector.ConnectOpts{
		Network:        network,
		Address:        address,
		Username:       connCtx.Username,
		Password:       connCtx.Password,
		Ssl:            ssl,
		ConnectTimeout: connCtx.ConnectTimeout,
	}
}

// resolveConnectOpts tries to resolve target argument to replace it
// with a control socket or as a URI with/without credentials.
// It returns connection options and the remaining args.
func resolveConnectOpts(cmdCtx *cmdcontext.CmdCtx,
	connectCtx *connect.ConnectCtx, target string) (
	connector.ConnectOpts, error,
) {
	var (
		connOpts connector.ConnectOpts
		err      error
	)

	// A target that resolves to no instance may still be a URI that happens
	// to fit <app>:<instance>, such as localhost:3301.
	instances, resolveErr := resolveInstances(cmdCtx, []string{target})

	switch {
	case resolveErr == nil:
		if len(instances) > 1 {
			err = errSpecifyInstanceName
			return connOpts, err
		}

		err = running.RequireTarantool(&instances[0])
		if err != nil {
			return connOpts, err
		}

		if (connectCtx.Username != "" || connectCtx.Password != "") && !connectCtx.Binary {
			err = errControlSocketCredentialsUnsupported
			return connOpts, err
		}

		if connectCtx.Binary {
			connOpts = makeConnOpts(
				connector.UnixNetwork, instances[0].BinaryPort, *connectCtx,
			)
		} else {
			connOpts = makeConnOpts(
				connector.UnixNetwork, instances[0].ConsoleSocket, *connectCtx,
			)
		}
	case sdkconnect.IsCredentialsURI(target):
		if connectCtx.Username != "" || connectCtx.Password != "" {
			err = errCredentialsSpecifiedByFlagsAndURI
			return connOpts, err
		}

		newURI, user, pass := sdkconnect.ParseCredentialsURI(target)

		newURI, err = applyURIQuery(connectCtx, newURI)
		if err != nil {
			return connOpts, err
		}

		network, address := sdkconnect.ParseBaseURI(newURI)

		connectCtx.Username = user
		connectCtx.Password = pass
		connOpts = makeConnOpts(network, address, *connectCtx)
		connectCtx.ConnectTarget = newURI
	case sdkconnect.IsBaseURI(target):
		// Environment variables do not overwrite values.
		if connectCtx.Username == "" {
			connectCtx.Username = os.Getenv(sdkconnect.TarantoolUsernameEnv)
		}

		if connectCtx.Password == "" {
			connectCtx.Password = os.Getenv(sdkconnect.TarantoolPasswordEnv)
		}

		var base string

		base, err = applyURIQuery(connectCtx, target)
		if err != nil {
			return connOpts, err
		}

		network, address := sdkconnect.ParseBaseURI(base)

		connOpts = makeConnOpts(network, address, *connectCtx)
		connectCtx.ConnectTarget = base
	default:
		return connOpts, unresolvedTargetErr(target, resolveErr)
	}

	if connectCtx.ConnectTarget == "" {
		connectCtx.ConnectTarget = target
	}

	return connOpts, err
}

// unresolvedTargetErr returns the error of a target that is no instance and
// no URI. A target with an "@" may be a URI that was typed wrong, and the
// error of the lookup names the pieces it cut the target into, which hold
// the password, so the lookup error is replaced by one that quotes the
// target redacted.
func unresolvedTargetErr(target string, resolveErr error) error {
	if !strings.Contains(target, "@") {
		return resolveErr
	}

	return sdk.Errorf(sdk.ExitFailure, "%w: %q", errNotATarget, log.RedactTarget(target))
}

// applyURIQuery cuts the query off a URI, applies its parameters to
// connectCtx and returns the URI without it. The parameters are the TLS
// settings of the flags, a setting that a flag has set already being an
// error, as are credentials in both places, and the transport. Transport
// "ssl" takes TLS with no setting at all, "plain" refuses every setting, of
// the query or of a flag, and with no transport TLS is taken if a setting
// is set. An error names the parameter or the flag, not its value: it may
// be a password.
func applyURIQuery(connectCtx *connect.ConnectCtx, uri string) (string, error) {
	base, params, err := sdkconnect.SplitURIQuery(uri)
	if err != nil {
		// Only what is before the query: it may hold a password.
		head, _, _ := strings.Cut(uri, "?")

		return "", fmt.Errorf("%q: %w", log.RedactTarget(head), err)
	}

	settings := map[string]*string{
		uriSslKeyFileParam:      &connectCtx.SslKeyFile,
		uriSslCertFileParam:     &connectCtx.SslCertFile,
		uriSslCaFileParam:       &connectCtx.SslCaFile,
		uriSslCiphersParam:      &connectCtx.SslCiphers,
		uriSslPasswordParam:     &connectCtx.SslPassword,
		uriSslPasswordFileParam: &connectCtx.SslPasswordFile,
	}

	for _, key := range slices.Sorted(maps.Keys(params)) {
		value := params.Get(key)

		if key == uriTransportParam {
			transport, parseErr := dial.Parse(value)
			if parseErr != nil || transport == dial.TransportDefault {
				return "", fmt.Errorf("%q: %w", key, errUnknownTransport)
			}

			connectCtx.Transport = transport

			continue
		}

		setting, ok := settings[key]
		if !ok {
			return "", fmt.Errorf("%q: %w", key, errUnsupportedURIParam)
		}

		if *setting != "" {
			return "", fmt.Errorf("%q: %w", key, errURIParamSpecifiedByFlag)
		}

		*setting = value

		if key == uriSslPasswordParam {
			log.RegisterSecret(value)
		}
	}

	if connectCtx.Transport == dial.TransportPlain {
		for _, key := range slices.Sorted(maps.Keys(settings)) {
			if *settings[key] == "" {
				continue
			}

			name := key
			if !params.Has(key) {
				name = "--" + uriParamFlags[key]
			}

			return "", fmt.Errorf("%q: %w", name, errPlainTransportWithTLS)
		}
	}

	return base, nil
}

// internalConnectModule is a default connect module.
func internalConnectModule(cmdCtx *cmdcontext.CmdCtx, args []string) error {
	language, ok := connect.ParseLanguage(connectLanguage)
	if !ok {
		return util.NewArgError("unsupported language: " + connectLanguage)
	}

	format, ok := formatter.ParseFormat(connectFormat)
	if !ok {
		return util.NewArgError("unsupported output format: " + connectFormat)
	}

	connectCtx := connect.ConnectCtx{
		Username:        connectUser,
		Password:        connectPassword,
		ConnectTimeout:  connectTimeout,
		SrcFile:         connectFile,
		Language:        language,
		Format:          format,
		SslKeyFile:      connectSslKeyFile,
		SslCertFile:     connectSslCertFile,
		SslCaFile:       connectSslCaFile,
		SslCiphers:      connectSslCiphers,
		SslPassword:     connectSslPassword,
		SslPasswordFile: connectSslPasswordFile,
		Transport:       dial.TransportDefault,
		ConnectTarget:   "",
		Binary:          connectBinary,
		Evaler:          connectEvaler,
	}

	connOpts, err := resolveConnectOpts(cmdCtx, &connectCtx, args[0])
	if err != nil {
		return err
	}

	if connectFile != "" {
		res, err := connect.Eval(connectCtx, connOpts, args[1:])
		if err != nil {
			return err
		}

		// "Println" is used instead of "log..." to print the result without
		// any decoration.
		_, _ = fmt.Fprintln(os.Stdout, string(res))

		if !connectInteractive || !terminal.IsTerminal(syscall.Stdin) {
			return nil
		}
	} else if len(args) != 1 {
		return errConnectionStringRequired
	}

	if terminal.IsTerminal(syscall.Stdin) {
		log.Info("Connecting to the instance...")
	}

	return connect.Connect(connectCtx, connOpts)
}
