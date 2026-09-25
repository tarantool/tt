package aeon

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"

	"github.com/spf13/cobra"
	goconfig "github.com/tarantool/go-config/v2"

	"github.com/tarantool/tt/modules/aeon/internal/client"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/console"
)

var (
	errFailedToRecognizeAConnectDestinationSeeTheCommandExamples = errors.New(
		"failed to recognize a connect destination, see the command examples",
	)
	errFilesKeyAndCertMustBeSpecifiedBoth = errors.New(
		"files Key and Cert must be specified both",
	)
	errInvalidConnectionURL             = errors.New("invalid connection url")
	errNotValidPathToAPrivateSSLKeyFile = errors.New(
		"not valid path to a private SSL key file=",
	)
	errNotValidPathToAnSSLCertificateFile = errors.New(
		"not valid path to an SSL certificate file=",
	)
	errNotValidPathToTrustedCertificateAuthoritiesCAFile = errors.New(
		"not valid path to trusted certificate authorities (CA) file=",
	)
	errTransportMustBeSSLOrPlain = errors.New(
		"transport must be ssl or plain",
	)
)

const (
	// historyFileName is the file in the home directory the console keeps
	// its history in.
	historyFileName = ".aeon_history"
	// historyLines is how many commands the history keeps.
	historyLines = console.DefaultHistoryLines
	// connectMaxArgs is the most arguments tt aeon connect takes: a cluster
	// configuration and an instance.
	connectMaxArgs = 2
	// advertisePath is where an instance's configuration keeps the address
	// and the transport aeon clients connect with.
	advertisePath = "roles_cfg/aeon.grpc/advertise"
)

// advertise is the aeon.grpc role's advertise section of an instance's
// configuration.
type advertise struct {
	// URI is the address clients connect to: a URL or a unix socket.
	URI string `yaml:"uri"`
	// Params are the connection parameters.
	Params advertiseParams `yaml:"params"`
}

// advertiseParams are the connection parameters of an advertise section.
type advertiseParams struct {
	// Transport is plain or ssl.
	Transport string `yaml:"transport"`
	// KeyFile is the path to the client's private SSL key file.
	KeyFile string `yaml:"ssl_key_file"`
	// CertFile is the path to the client's SSL certificate file.
	CertFile string `yaml:"ssl_cert_file"`
	// CaFile is the path to the trusted certificate authorities (CA) file.
	CaFile string `yaml:"ssl_ca_file"`
}

// connectCmd is tt aeon connect: the values of its flags and the connection
// its arguments resolve to.
type connectCmd struct {
	services sdk.Services
	// creds are the credentials of a configuration storage given as the
	// cluster configuration.
	creds sdk.Credentials
	// conn is where to connect and how.
	conn client.ConnectCtx
}

// newConnectCmd builds tt aeon connect.
func newConnectCmd(services sdk.Services) *cobra.Command {
	connectCmd := &connectCmd{
		services: services,
		creds:    sdk.Credentials{Username: "", Password: ""},
		conn: client.ConnectCtx{
			Ssl:       client.Ssl{KeyFile: "", CertFile: "", CaFile: ""},
			Transport: client.TransportPlain,
			Network:   "",
			Address:   "",
		},
	}

	help := connect.MakeURLHelp(map[string]any{
		"service":    "etcd or tarantool config storage",
		"param_key":  "a target configuration key in the prefix",
		"param_name": "a name of an instance in the cluster configuration",
		"prefix": "key prefix (optional)," +
			" points to a “namespace” or prefix for all key operations",
	})

	cmd := &cobra.Command{
		Use:   "connect (<URI> | <URI INSTANCE> | <PATH INSTANCE> | <APP:INSTANCE>)",
		Short: "Connect to the aeon instance",
		Long: `Connect to the aeon instance.
		tt aeon connect http://localhost:50051
		tt aeon connect unix://<socket-path>
		tt aeon connect /path/to/config INSTANCE_NAME
		tt aeon connect https://user:pass@localhost:2379/prefix INSTANCE` + "\n\n" +
			help,
		Args: cobra.MatchAll(cobra.RangeArgs(1, connectMaxArgs), connectCmd.validateArgs),
		RunE: connectCmd.run,
	}

	flags := cmd.Flags()
	flags.StringVarP(&connectCmd.creds.Username, "username", "u", "",
		"username (used as etcd credentials only)")
	flags.StringVarP(&connectCmd.creds.Password, "password", "p", "",
		"password (used as etcd credentials only)")
	flags.StringVar(&connectCmd.conn.Ssl.KeyFile, "sslkeyfile", "",
		"path to a private SSL key file")
	flags.StringVar(&connectCmd.conn.Ssl.CertFile, "sslcertfile", "",
		"path to a SSL certificate file")
	flags.StringVar(&connectCmd.conn.Ssl.CaFile, "sslcafile", "",
		"path to a trusted certificate authorities (CA) file")
	flags.Var(&connectCmd.conn.Transport, "transport",
		"allowed "+client.ListValidTransports())

	_ = cmd.RegisterFlagCompletionFunc("transport", transportCompletion)

	return cmd
}

// transportCompletion completes the value of --transport.
func transportCompletion(_ *cobra.Command, _ []string, _ string) (
	[]string, cobra.ShellCompDirective,
) {
	suggest := make([]string, 0, len(client.ValidTransport))
	for k, v := range client.ValidTransport {
		suggest = append(suggest, string(k)+"\t"+v)
	}

	return suggest, cobra.ShellCompDirectiveDefault
}

// validateArgs resolves the arguments to the connection and checks the SSL
// flags against it. It runs as the arguments' validator, so that its errors
// are reported with the command's usage.
func (c *connectCmd) validateArgs(cmd *cobra.Command, args []string) error {
	err := c.resolve(cmd.Context(), args)
	if err != nil {
		return err
	}

	flags := cmd.Flags()

	if !flags.Changed("transport") && (c.conn.Ssl.KeyFile != "" ||
		c.conn.Ssl.CertFile != "" || c.conn.Ssl.CaFile != "") {
		c.conn.Transport = client.TransportSsl
	}

	if c.conn.Transport == client.TransportPlain {
		return nil
	}

	if flags.Changed("sslkeyfile") != flags.Changed("sslcertfile") {
		return errFilesKeyAndCertMustBeSpecifiedBoth
	}

	checkFile := func(path string) bool {
		return path == "" || isRegularFile(path)
	}

	switch {
	case !checkFile(c.conn.Ssl.KeyFile):
		return fmt.Errorf("%w%q", errNotValidPathToAPrivateSSLKeyFile, c.conn.Ssl.KeyFile)
	case !checkFile(c.conn.Ssl.CertFile):
		return fmt.Errorf("%w%q", errNotValidPathToAnSSLCertificateFile, c.conn.Ssl.CertFile)
	case !checkFile(c.conn.Ssl.CaFile):
		return fmt.Errorf("%w%q",
			errNotValidPathToTrustedCertificateAuthoritiesCAFile, c.conn.Ssl.CaFile)
	}

	return nil
}

// resolve sets where to connect from the arguments: an aeon URL, an
// "app:instance" of the tt environment, or a cluster configuration file or
// storage URI followed by an instance name. An instance's address and
// transport are its aeon.grpc advertise section.
func (c *connectCmd) resolve(ctx context.Context, args []string) error {
	if len(args) == 1 && isURL(args[0]) {
		address, err := removeScheme(args[0])
		if err != nil {
			return err
		}

		c.conn.Network, c.conn.Address = connect.ParseBaseURI(address)

		return nil
	}

	if len(args) == 1 {
		app, instance := sdk.SplitInstance(args[0])

		return c.resolveInstance(ctx, sdk.ParseClusterSource(app, c.creds), instance)
	}

	src := sdk.ParseClusterSource(args[0], c.creds)
	if _, isApp := src.App(); isApp {
		return errFailedToRecognizeAConnectDestinationSeeTheCommandExamples
	}

	return c.resolveInstance(ctx, src, args[1])
}

// resolveInstance sets where to connect from the advertise section of the
// instance of the cluster configuration src names. The SSL files the section
// names fill the ones the flags leave empty; a relative path in a cluster
// configuration file is relative to the file's directory.
func (c *connectCmd) resolveInstance(
	ctx context.Context, src sdk.ClusterSource, instance string,
) error {
	cfg, err := c.services.ClusterConfig(ctx, src)
	if err != nil {
		return fmt.Errorf("failed to read cluster config: %w", err)
	}

	instanceCfg, err := sdk.InstanceConfig(cfg.Config, instance)
	if err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}

	var adv advertise

	_, err = instanceCfg.Get(goconfig.NewKeyPath(advertisePath), &adv)
	if err != nil {
		return fmt.Errorf("failed to get aeon advertise config: %w", err)
	}

	if adv.URI == "" {
		return errInvalidConnectionURL
	}

	address, err := removeScheme(adv.URI)
	if err != nil {
		return err
	}

	c.conn.Network, c.conn.Address = connect.ParseBaseURI(address)

	switch client.Transport(adv.Params.Transport) {
	case client.TransportPlain:
	case client.TransportSsl:
		c.conn.Transport = client.TransportSsl
		c.conn.Ssl.CaFile = orPath(c.conn.Ssl.CaFile, cfg.Dir, adv.Params.CaFile)
		c.conn.Ssl.KeyFile = orPath(c.conn.Ssl.KeyFile, cfg.Dir, adv.Params.KeyFile)
		c.conn.Ssl.CertFile = orPath(c.conn.Ssl.CertFile, cfg.Dir, adv.Params.CertFile)
	default:
		return errTransportMustBeSSLOrPlain
	}

	return nil
}

// orPath returns flag when it is set, and otherwise path from the
// configuration, relative to dir unless it is absolute.
func orPath(flag, dir, path string) string {
	switch {
	case flag != "", path == "":
		return flag
	case filepath.IsAbs(path):
		return path
	default:
		return filepath.Join(dir, path)
	}
}

// run connects to aeon and runs the console until the input ends or the
// connection is closed.
func (c *connectCmd) run(_ *cobra.Command, _ []string) error {
	hist, err := historyFile()
	if err != nil {
		return fmt.Errorf("can't open history file: %w", err)
	}

	handler, err := client.NewAeonHandler(c.conn)
	if err != nil {
		return err
	}

	opts := console.ConsoleOpts{
		Handler: handler,
		History: &hist,
		Format:  console.FormatAsTable(),
		Exit:    c.services.Exit,
	}

	cons, err := console.NewConsole(opts)
	if err != nil {
		return fmt.Errorf("can't create aeon console: %w", err)
	}

	err = cons.Run()
	if err != nil {
		return fmt.Errorf("can't start aeon console: %w", err)
	}

	return nil
}

// historyFile opens the console history in the user's home directory.
func historyFile() (console.History, error) {
	dir, err := homeDir()
	if err != nil {
		return console.History{}, fmt.Errorf("failed to get home directory: %w", err)
	}

	return console.NewHistory(filepath.Join(dir, historyFileName), historyLines)
}
