# Write a tt module

A tt module is a Go module compiled into a
[distribution of tt](build-distribution.md): its package exports a
constructor that returns the commands it adds. It is built on the
[tt SDK](https://github.com/tarantool/tt/blob/v3/sdk/README.md) and imports
nothing else from tt. This page writes one from scratch, `tt probe`, which
lists the instances of an application, pings an instance and makes a
snapshot on it, tests it and builds it into tt. To add a command with an
executable that tt runs instead, with no rebuild, see
[External modules](external-modules.md).

The SDK is alpha: its API may change in any release of tt.

## Set up the Go module

```sh
mkdir tt-probe && cd tt-probe
go mod init example.com/tt-probe
go get github.com/tarantool/tt/sdk@9997ea8b \
    github.com/tarantool/tt/sdk/connect@9997ea8b \
    github.com/tarantool/tt/sdk/dial@9997ea8b
go get github.com/tarantool/go-tarantool/v3 github.com/stretchr/testify
```

The SDK modules have no versions yet: tt has no `sdk/...` tags, and its own
tags, such as `v3.0.0-alpha.2`, version the core,
`github.com/tarantool/tt/v3`, and no other module. Require the SDK modules
at a commit of tt instead, which Go turns into a pseudo-version:

```text
require (
	github.com/tarantool/tt/sdk v0.0.0-20261105160839-9997ea8b6840
	github.com/tarantool/tt/sdk/connect v0.0.0-20261105160839-9997ea8b6840
	github.com/tarantool/tt/sdk/dial v0.0.0-20261105160839-9997ea8b6840
)
```

`9997ea8b` is the commit of `v3.0.0-alpha.2`
(`git rev-parse 'v3.0.0-alpha.2^{commit}'` in a clone of tt); take the
commit of the core the module is built on. Require every SDK module at the
same commit, and `sdk/connect` even when the module does not import it:
the `go.mod` of `sdk` requires it at `v0.0.0-0`, a placeholder that only the
`replace` directives of tt's own repository resolve, and `go get` of `sdk`
alone fails with `unknown revision sdk/connect/v0.0.0-0`. `sdk/cluster` has
two such placeholders, for `sdk/connect` and `sdk/dial`.

The distribution build does not use these versions: it builds every module
on the SDK at the commit of its core, whatever the module's `go.mod`
requires. They are what `go test` of the module runs on, so keep them at the
commit of the core: when the core moves, `go get` the SDK modules at its new
commit and run `go mod tidy`.

### Develop against a checkout of tt

To build the module on SDK changes not yet pushed, put the SDK modules of a
local checkout of tt in a Go workspace. Keep `go.work` out of git:

```sh
go work init . ../tt/sdk ../tt/sdk/connect ../tt/sdk/dial
go test ./...
```

and build the distribution on the same checkout with
`tt binary build tt --core ../tt` (see
[Building on another core](build-distribution.md#building-on-another-core)).
The distribution build ignores `go.work`, and the `replace` directives of
the module's `go.mod` as well: Go applies those of the main module only.

## Write the constructor

`probe.go`:

```go
// Package probe is a tt module: tt probe lists the instances of an
// application, pings an instance and makes a snapshot on it.
package probe

import (
	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk"
)

// New is the constructor of the module. It declares the commands and
// nothing else: the services other than Log are not ready yet.
func New(services sdk.Services) []sdk.Mount {
	group := &cobra.Command{
		Use:   "probe",
		Short: "Look at the instances of an application",
	}

	group.AddCommand(
		newInstancesCmd(services),
		newPingCmd(services),
		newSnapshotCmd(services),
	)

	return []sdk.Mount{{Path: "", Cmd: group}}
}
```

`New` is an `sdk.Constructor`, `func(sdk.Services) []sdk.Mount`; a
distribution finds it by that name in the root package of the module unless
its manifest says otherwise. tt calls it once per process, before it is
configured: of the services, only `Log` may be used there, and the others
panic, naming the module and the method. The commands use the services in
their hooks, which run after. A constructor declares commands and their
flags and nothing else: it reads no files and no environment.

A `Mount` is a command and the path of its parent: `""` is the root,
`"replicaset"` puts the command under `tt replicaset`. A group on the path
that no module provides is created, with no description. Each command is
fresh, with no parent; the core owns it once the constructor returns.

## List the instances of an application

`instances.go`:

```go
package probe

import (
	"fmt"
	"io"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/output"
)

// instancesResult is the result of tt probe instances. Its fields are what
// -o json and -o yaml print.
type instancesResult struct {
	App       string   `json:"app" yaml:"app"`
	Instances []string `json:"instances" yaml:"instances"`
}

// Human prints one instance per line.
func (r instancesResult) Human(w io.Writer) error {
	for _, name := range r.Instances {
		_, err := fmt.Fprintln(w, name)
		if err != nil {
			return err
		}
	}

	return nil
}

func newInstancesCmd(services sdk.Services) *cobra.Command {
	var lookup sdk.AppLookup

	cmd := &cobra.Command{
		Use:   "instances APP",
		Short: "List the instances of an application",
		Args:  cobra.ExactArgs(1),
	}

	format := output.BindFormat(cmd.Flags(), output.FormatHuman,
		output.FormatHuman, output.FormatJSON, output.FormatYAML)
	sdk.AddAppFlags(cmd, &lookup)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		app, instance := sdk.SplitInstance(args[0])
		if instance != "" {
			return sdk.Usagef("%q names an instance: give the application only", args[0])
		}

		cfg, err := services.ClusterConfig(cmd.Context(),
			sdk.AppSource(app).WithLookup(lookup))
		if err != nil {
			return err
		}

		names, err := sdk.Instances(cfg.Config)
		if err != nil {
			return err
		}

		services.Log().Debug("Read the cluster configuration", "app", app, "dir", cfg.Dir)

		printer, err := services.Streams().Printer(format.Format())
		if err != nil {
			return err
		}

		return printer.Emit(instancesResult{App: app, Instances: names})
	}

	return cmd
}
```

- `sdk.AddAppFlags` declares `--scope` and `--template`, with the help and
  the completion of tt's instance commands, and `sdk.AppSource(app)
  .WithLookup(lookup)` finds the application the way they do: the project
  tt runs in when it has the name, the system scope otherwise.
  `sdk.SplitInstance` splits `APP:INSTANCE`.
- `Services.ClusterConfig` returns the cluster configuration of the source
  as Tarantool 3 sees it, with the directory relative paths in it are
  relative to (`Dir`); an application without one wraps `sdk.ErrNotFound`.
  `sdk.Instances` lists its instances and `sdk.InstanceConfig` resolves the
  configuration of one. `sdk.ParseClusterSource` classifies an argument
  that may also be a cluster config file or a configuration storage URI, as
  `tt cluster show` does.
- `output.BindFormat` declares `-o/--format` with the formats the command
  accepts and its default. `Services.Streams().Printer` writes the result to
  stdout: `Human` in the human format (`table`), and the value itself in
  `json` and `yaml`, by its `json` and its `yaml` tags, so a field has both.
  stdout carries the result only; logs and prompts go to stderr.
- `Services.Log` is the logger of the module: records carry `module=probe`,
  and debug ones are shown with `tt -V`. `log.LibraryOf` makes a logger for
  a library, whose Info records it demotes to Debug.
- An error returned from `RunE` is reported by tt once, and sets its exit
  code. `sdk.Usagef` and `sdk.WithUsage` mark an error in how the command was
  called: tt prints the message, then the usage of the command. An error of
  `Args` or of the flags is printed with the usage too.

## Connect to an instance

`target.go`:

```go
package probe

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"
	goconfig "github.com/tarantool/go-config/v2"
	"github.com/tarantool/go-tarantool/v3"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/dial"
	"github.com/tarantool/tt/sdk/log"
)

// timeout bounds the connection and each request.
const timeout = 3 * time.Second

// connFlags are the flags of a command that connects to an instance.
type connFlags struct {
	username string
	password string
	lookup   sdk.AppLookup
}

// bind declares the flags on cmd.
func (f *connFlags) bind(cmd *cobra.Command) {
	flags := cmd.Flags()
	flags.StringVarP(&f.username, "username", "u", "", "the user to connect as")
	flags.StringVarP(&f.password, "password", "p", "", "the password of the user")
	sdk.AddAppFlags(cmd, &f.lookup)
}

// listen is an entry of iproto.listen in the configuration of an instance.
type listen struct {
	URI    string            `yaml:"uri"`
	Params map[string]string `yaml:"params"`
}

// connect connects to target: a URI, with or without credentials, or
// APP:INSTANCE. A failure to connect is a failure of the system: tt exits 2.
func (f *connFlags) connect(
	ctx context.Context, services sdk.Services, target string,
) (*tarantool.Connection, error) {
	opts, err := f.dialOpts(ctx, services, target)
	if err != nil {
		return nil, err
	}

	dialer, err := dial.New(opts)
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	conn, err := tarantool.Connect(ctx, dialer, tarantool.Opts{
		Timeout:    timeout,
		SkipSchema: true,
		Logger:     log.LibraryOf(services.Log(), "go-tarantool"),
	})
	if err != nil {
		return nil, sdk.WithCode(sdk.ExitSystem,
			fmt.Errorf("connecting to %s: %w", log.RedactTarget(target), err))
	}

	return conn, nil
}

// dialOpts returns the options of a dialer to target. The credentials are
// the ones a URI carries, which exclude -u and -p; otherwise -u and -p, and
// an empty one is taken from TT_CLI_USERNAME or TT_CLI_PASSWORD, as tt
// connect takes them.
func (f *connFlags) dialOpts(
	ctx context.Context, services sdk.Services, target string,
) (dial.Opts, error) {
	opts := dial.Opts{Auth: tarantool.AutoAuth, User: f.username, Password: f.password}
	params := map[string]string{}
	uri := target

	switch {
	case connect.IsCredentialsURI(target):
		if f.username != "" || f.password != "" {
			return opts, sdk.Usagef("the URI has credentials: -u and -p are not allowed")
		}

		uri, opts.User, opts.Password = connect.ParseCredentialsURI(target)
	case connect.IsBaseURI(target):
	default:
		entry, err := f.instanceListen(ctx, services, target)
		if err != nil {
			return opts, err
		}

		uri, params = entry.URI, entry.Params
	}

	if opts.User == "" {
		opts.User = os.Getenv(connect.TarantoolUsernameEnv)
	}

	if opts.Password == "" {
		opts.Password = os.Getenv(connect.TarantoolPasswordEnv)
	}

	log.RegisterSecret(opts.Password)

	// host:port?transport=ssl&ssl_ca_file=ca.crt
	base, query, err := connect.SplitURIQuery(uri)
	if err != nil {
		return opts, sdk.WithUsage(err)
	}

	for key := range query {
		params[key] = query.Get(key)
	}

	opts.Transport = params["transport"]
	opts.SslKeyFile = params["ssl_key_file"]
	opts.SslCertFile = params["ssl_cert_file"]
	opts.SslCaFile = params["ssl_ca_file"]
	opts.SslCiphers = params["ssl_ciphers"]

	network, address := connect.ParseBaseURI(base)
	opts.Address = network + "://" + address

	return opts, nil
}

// instanceListen returns the first iproto.listen entry of the instance
// target, APP:INSTANCE, names, with its SSL files relative to the
// application directory made absolute. The URI is taken as it is written.
func (f *connFlags) instanceListen(
	ctx context.Context, services sdk.Services, target string,
) (listen, error) {
	app, instance := sdk.SplitInstance(target)
	if instance == "" {
		return listen{}, sdk.Usagef("%q is neither a URI nor APP:INSTANCE",
			log.RedactTarget(target))
	}

	cfg, err := services.ClusterConfig(ctx, sdk.AppSource(app).WithLookup(f.lookup))
	if err != nil {
		return listen{}, err
	}

	instanceCfg, err := sdk.InstanceConfig(cfg.Config, instance)
	if err != nil {
		return listen{}, err
	}

	var entries []listen

	_, err = instanceCfg.Get(goconfig.NewKeyPath("iproto/listen"), &entries)
	if err != nil {
		return listen{}, fmt.Errorf("%s: iproto.listen: %w", target, err)
	}

	if len(entries) == 0 || entries[0].URI == "" {
		return listen{}, fmt.Errorf("%s: iproto.listen has no URI", target)
	}

	entry := entries[0]
	if entry.Params == nil {
		entry.Params = map[string]string{}
	}

	for key, value := range entry.Params {
		if strings.HasPrefix(key, "ssl_") && strings.HasSuffix(key, "_file") &&
			value != "" && !filepath.IsAbs(value) {
			entry.Params[key] = filepath.Join(cfg.Dir, value)
		}
	}

	return entry, nil
}
```

The SDK gives the parts; the module puts them together, credentials
included:

1. `connect.IsCredentialsURI` matches a URI with credentials,
   `user:password@host:port` and its unix forms, and
   `connect.ParseCredentialsURI` takes them out of it. `connect.IsBaseURI`
   matches one without: `host:port`, `tcp://host:port`, `unix/:path`,
   `unix://path` and a path that starts with `/`, `./`, `../` or `~/`.
2. Anything else is `APP:INSTANCE` here: the URI is the first one of
   `iproto.listen` of the instance, and `Dir` makes the SSL files of its
   parameters absolute. The example takes the URI as written; it does not
   expand `{{ instance_name }}` or make a relative socket path absolute.
3. The precedence of the credentials is the module's choice, and the SDK
   applies none. The example follows `tt connect`: credentials in the URI
   exclude `-u` and `-p`; otherwise `-u` and `-p`, and an empty one is taken
   from `TT_CLI_USERNAME` or `TT_CLI_PASSWORD`
   (`connect.TarantoolUsernameEnv`, `connect.TarantoolPasswordEnv`).
   `connect.EnvTarantoolCredentialsHelp` describes the two variables in the
   help of a command.
4. `connect.SplitURIQuery` splits the query off a URI, where `transport` and
   the `ssl_*` parameters go, and `connect.ParseBaseURI` returns the network
   and the address, which `dial.Opts.Address` takes as `network://address`.
5. `dial.New` returns a dialer for `tarantool.Connect` of go-tarantool. With
   an empty `Transport` it is SSL when any SSL field is set and plain
   otherwise; `"plain"` and `"ssl"` choose; anything else is an error. `Auth`
   is used over SSL only. The SSL dialer is the Go implementation of TLS, and
   OpenSSL in a build with the `openssl` tag.
6. `log.RegisterSecret` hides the password in every log record. A result
   and a prompt are not log records: `log.RedactTarget` hides the
   credentials of a target quoted in them, and in an error.

`connect.CreateURIOpts` parses a URL with a scheme and a host, such as
`https://user:pass@localhost:2379/prefix?timeout=5`, into `connect.URIOpts`:
the endpoint, the prefix, the credentials, the SSL files and the timeout
(3 seconds by default). It is for etcd and Tarantool config storages, not
for iproto URIs, which have no scheme; `connect.MakeURLHelp` writes the help
of such a URL argument. A cluster configuration in a storage is read by
`Services.ClusterConfig` of an `sdk.StorageSource`, which takes the
credentials from the URI, then from `sdk.Credentials`, then from
`TT_CLI_ETCD_USERNAME` and `TT_CLI_ETCD_PASSWORD` (`TT_CLI_USERNAME` and
`TT_CLI_PASSWORD` for a Tarantool config storage).

## Exit codes and confirmations

`ping.go`:

```go
package probe

import (
	"errors"
	"fmt"
	"io"

	"github.com/spf13/cobra"
	"github.com/tarantool/go-tarantool/v3"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/connect"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/sdk/output"
)

// errCancelled is a snapshot the user did not confirm.
var errCancelled = errors.New("cancelled: confirm, or give --force")

// pingResult is the result of tt probe ping.
type pingResult struct {
	Target string `json:"target" yaml:"target"`
	Status string `json:"status" yaml:"status"`
}

// Human prints the status of the instance.
func (r pingResult) Human(w io.Writer) error {
	_, err := fmt.Fprintf(w, "%s: %s\n", r.Target, r.Status)

	return err
}

func newPingCmd(services sdk.Services) *cobra.Command {
	var conn connFlags

	cmd := &cobra.Command{
		Use:   "ping (URI | APP:INSTANCE)",
		Short: "Connect to an instance and print its status",
		Long: "Connect to an instance and print box.info.status.\n\n" +
			"The URI is [user:password@]host:port, a path to a unix socket\n" +
			"or unix/:path, with an optional ?transport=ssl&ssl_ca_file=...\n\n" +
			connect.EnvTarantoolCredentialsHelp,
		Args: cobra.ExactArgs(1),
	}

	conn.bind(cmd)
	format := output.BindFormat(cmd.Flags(), output.FormatHuman,
		output.FormatHuman, output.FormatJSON, output.FormatYAML)

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		instance, err := conn.connect(cmd.Context(), services, args[0])
		if err != nil {
			return err
		}
		defer instance.Close()

		var status []string

		err = instance.Do(tarantool.NewEvalRequest("return box.info.status")).
			GetTyped(&status)
		if err != nil {
			return fmt.Errorf("box.info.status: %w", err)
		}

		printer, err := services.Streams().Printer(format.Format())
		if err != nil {
			return err
		}

		return printer.Emit(pingResult{Target: log.RedactTarget(args[0]), Status: status[0]})
	}

	return cmd
}

func newSnapshotCmd(services sdk.Services) *cobra.Command {
	var (
		conn  connFlags
		force bool
	)

	cmd := &cobra.Command{
		Use:   "snapshot (URI | APP:INSTANCE)",
		Short: "Make a snapshot on an instance",
		Args:  cobra.ExactArgs(1),
	}

	conn.bind(cmd)
	cmd.Flags().BoolVarP(&force, "force", "f", false, "do not ask for confirmation")

	cmd.RunE = func(cmd *cobra.Command, args []string) error {
		if !force {
			// Under tt --no-prompt this asks nothing and answers false.
			yes, err := services.Confirm(
				"Make a snapshot on "+log.RedactTarget(args[0])+"?", false)
			if err != nil {
				return err
			}

			if !yes {
				return errCancelled
			}
		}

		instance, err := conn.connect(cmd.Context(), services, args[0])
		if err != nil {
			return err
		}
		defer instance.Close()

		_, err = instance.Do(tarantool.NewEvalRequest("box.snapshot()")).Get()
		if err != nil {
			return fmt.Errorf("box.snapshot: %w", err)
		}

		services.Log().Info("Snapshot made", "target", log.RedactTarget(args[0]))

		return nil
	}

	return cmd
}
```

tt exits with the code of the error a command returns (see
[Exit codes](../reference/commands/errors.md)): 1 (`sdk.ExitFailure`) for
any error, 2 (`sdk.ExitSystem`) for a failure of the system, 3
(`sdk.ExitPartial`) for an operation that succeeded on some targets only.
`sdk.WithCode` and `sdk.Errorf` set the code; code 1 never hides a deeper
one in the chain. tt also exits 2 for an error that wraps a network error or
a lack of permission or space, without a code; the example sets the code
anyway, so that its tests see it.

`Services.Confirm` asks a yes or no question on stderr and reads the answer
from stdin. Under `tt --no-prompt` it asks nothing and returns its fallback,
so the fallback is the answer that is safe to assume. A flag such as
`--force` lets a script go ahead.

`Services.Exit` reports an error and ends tt with its code, for code that
cannot return an error, such as a callback of a library. A command that can
return its error must.

## Test it

`probe_test.go`:

```go
package probe_test

import (
	"net"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"example.com/tt-probe"
	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/sdktest"
)

// cluster is the cluster configuration of the application "app": nothing
// listens on the address of its storage.
const cluster = `
groups:
  g:
    replicasets:
      r:
        instances:
          router: {}
          storage:
            iproto:
              listen:
              - uri: '127.0.0.1:1'
`

func TestInstances(t *testing.T) {
	services := sdktest.New(t, sdktest.WithClusterConfig(sdk.AppSource("app"), cluster))

	got := services.Run(probe.New, "probe", "instances", "app", "-o", "json")

	require.NoError(t, got.Err)
	assert.JSONEq(t, `{"app": "app", "instances": ["router", "storage"]}`, got.Stdout)
}

func TestInstancesOfInstance(t *testing.T) {
	got := sdktest.Run(t, probe.New, "probe", "instances", "app:router")

	var usage *sdk.UsageError
	require.ErrorAs(t, got.Err, &usage)
	assert.Equal(t, sdk.ExitFailure, got.ExitCode())
}

func TestInstancesNoApp(t *testing.T) {
	got := sdktest.Run(t, probe.New, "probe", "instances", "app")

	require.ErrorIs(t, got.Err, sdk.ErrNotFound)
}

// closedAddress returns a local TCP address nothing listens on.
func closedAddress(t *testing.T) string {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)

	address := listener.Addr().String()
	require.NoError(t, listener.Close())

	return address
}

func TestPingUnreachable(t *testing.T) {
	got := sdktest.Run(t, probe.New, "probe", "ping", closedAddress(t))

	require.Error(t, got.Err)
	assert.Equal(t, sdk.ExitSystem, got.ExitCode())
}

func TestPingApp(t *testing.T) {
	services := sdktest.New(t, sdktest.WithClusterConfig(sdk.AppSource("app"), cluster))

	got := services.Run(probe.New, "probe", "ping", "app:storage")

	require.ErrorContains(t, got.Err, "connecting to app:storage")
	assert.Equal(t, sdk.ExitSystem, got.ExitCode())
}

func TestPingCredentialsTwice(t *testing.T) {
	got := sdktest.Run(t, probe.New, "probe", "ping", "-u", "admin",
		"admin:secret@"+closedAddress(t))

	var usage *sdk.UsageError
	require.ErrorAs(t, got.Err, &usage)
}

func TestSnapshotDeclined(t *testing.T) {
	services := sdktest.New(t, sdktest.WithAnswers(false))

	got := services.Run(probe.New, "probe", "snapshot", closedAddress(t))

	require.Error(t, got.Err)
	assert.Contains(t, got.Stderr, "Make a snapshot on")
	assert.Equal(t, sdk.ExitFailure, got.ExitCode())
}

func TestSnapshotNoPrompt(t *testing.T) {
	services := sdktest.New(t, sdktest.WithNoPrompt())

	got := services.Run(probe.New, "probe", "snapshot", closedAddress(t))

	require.ErrorContains(t, got.Err, "cancelled")
	assert.Empty(t, got.Stderr) // No question asked.
}
```

`sdktest.Run` hangs the commands of a constructor on a root, as the core
does, and runs a command line; `sdktest.New` takes options first:
`WithClusterConfig` gives a source a cluster configuration (any other is
not found), `WithAnswers` the answers of `Confirm`, `WithNoPrompt` makes
`Confirm` answer its fallback, and `WithTarantool`, `WithProject`,
`WithStdin` and `WithIntegrity` set the other services. The `Result` holds
the error, stdout, stderr and the log records; `ExitCode` is the code the
SDK gives the error, without the network and permission rules of the core.
The printers of sdktest encode `json` but not `yaml`, which the core
encodes. A mount the core would refuse fails the test.

```console
$ go mod tidy
$ go test ./...
ok  	example.com/tt-probe	0.031s
```

## Build it into tt

Next to the module, `dist/tt.manifest.toml`:

```toml
manifest_version = "0.1"
manifest_kind = "tt-distribution"

[core]
tt = "v3.0.0-alpha.2"

[dependencies]
probe = "../tt-probe"
```

```console
$ cd dist
$ tt binary build tt
$ ./tt version --modules
Tarantool CLI version 3.0.0-alpha.2, linux/amd64. commit: <unknown>
  builtin  v3.0.0-alpha.2  github.com/tarantool/tt/v3
  probe    (devel)         example.com/tt-probe
$ ./tt probe instances myapp -o yaml
app: myapp
instances:
  - a
  - b
$ ./tt probe ping 127.0.0.1:1
   ⨯ connecting to 127.0.0.1:1: failed to dial: dial tcp 127.0.0.1:1: connect: connection refused
$ echo $?
2
```

The name under `[dependencies]` is the name of the module in tt: in
`tt version --modules`, in its log records and in the errors about its
commands. Commit `tt.manifest.lock` and `tt.manifest.sum` the build writes.
Once the module is published, the manifest names it by its module path and
a version, `probe = "example.com/tt-probe@v0.1.0"`. Every field of the
manifest is in [tt.manifest.toml](../reference/tt-manifest.md).

## What the core refuses

tt hangs the commands of every module before it runs any, and refuses the
ones its tree cannot hold, naming the module; the binary then fails on
every command line.

- Flags: a module command cannot declare `-h`/`--help`, `-C`/`--directory`,
  a flag of the root - `-I`/`--internal`, `-V`/`--verbose`, `--log-format`,
  `--no-prompt`, and in a distribution with integrity checks
  `--integrity-check` and `--integrity-check-period` - or a persistent flag
  of a group it is mounted under, by name or by shorthand. A shorthand used
  twice within the module's commands is refused too.
- Names: a command whose name or alias is taken next to it is refused - a
  duplicate when the names are the same, an ambiguity when a name meets an
  alias - and so are `help`, `__complete` and `__completeNoDesc` at the root.
  A command named like one of tt's own at the root is a duplicate: to
  replace a command of tt, build the distribution with `builtin = false`, or
  use an external module.
- Paths: a mount path names commands by their names, not aliases, and
  cannot go under a runnable command of another module. A group that is not
  itself a command is shared: any module may mount under it.

How the commands run:

- The root flags come before the command: `tt -V probe ping ...`, not
  `tt probe ping -V`, where `-V` is an unknown flag. The commands of a module
  do not see `-V` or `--no-prompt`; they see their effects, through `Log`
  and `Confirm`.
- A non-persistent flag of a group is parsed only between the group and the
  subcommand, `tt probe --flag ping`, since the root of tt traverses the
  groups; the root of sdktest does not. A flag the subcommands share belongs
  in the persistent flags of the group.
- The core wraps `PersistentPreRunE`, `PreRunE`, `RunE`, `PostRunE` and
  `PersistentPostRunE` to report the errors they return. The hooks without
  `E` return nothing to report.
- `Services.Project().Dir()` is the working directory tt runs in. A module
  cannot declare `-C`; only a command mounted under `tt package` inherits
  its `-C`, and gets that directory.
