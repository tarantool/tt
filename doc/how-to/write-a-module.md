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
alone fails with `unknown revision sdk/connect/v0.0.0-0`. A placeholder
sorts below every pseudo-version, so the commit given to `go get` takes its
place. `sdk/cluster` has two such placeholders, for `sdk/connect` and
`sdk/dial`. After `v3.0.0-alpha.2`, `sdk` requires `sdk/build` at a
placeholder too: give `github.com/tarantool/tt/sdk/build@<commit>` as well.

The distribution build does not use these versions: it builds every module
on the SDK at the commit of its core, whatever the module's `go.mod`
requires. They are what `go test` of the module runs on, so keep them at the
commit of the core: when the core moves, `go get` the SDK modules at its new
commit and run `go mod tidy`. To build the module on a change of the SDK
that is not pushed yet, see
[Develop together with the core](#develop-together-with-the-core).

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

A module that provides build backends as well, for the `backend` of a
component's build block, or checks of the files of a package, has an `sdk.ModuleConstructor` instead,
`func(sdk.Services) sdk.Module`: the `sdk.Module` it returns holds its
`Commands`, its `Backends` and the `FileChecks` of the files of a package,
of `github.com/tarantool/tt/sdk/build`. tt calls it as it calls an
`sdk.Constructor`, and a distribution lists either kind the same way.

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
   is the authentication method: `tarantool.AutoAuth` (`dial.AuthAuto`) takes
   the server's over SSL and chap-sha1 over a plain connection, and
   `dial.ParseAuth` reads `auto`, `chap-sha1` or `pap-sha256`, as the
   `auth_type` parameter of a URI gives it. pap-sha256 sends the password as
   it is: over a plain connection `dial.New` refuses it with
   `dial.ErrPapSha256Unencrypted`. The SSL dialer is the Go implementation of
   TLS, and OpenSSL in a build with the `openssl` tag.
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

## Work on the project

`Services.Project` is the project a command works on, managed as
`tt package` manages it. `Dir` is the directory of the command, and `Root`
the project it is in, as the project commands of tt find it: the nearest
directory from `Dir` up with an `app.manifest.toml` or an instance
template, `Dir` itself when there is none. When it is above `Dir`, tt says
once on stderr which project the command works on. The other methods work
on `Root`.

`scaffold.go` declares a component of probes in the manifest:

```go
package probe

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/tarantool/tt/sdk"
)

func newScaffoldCmd(services sdk.Services) *cobra.Command {
	return &cobra.Command{
		Use:   "scaffold NAME",
		Short: "Declare a component of probes in the package",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			project := services.Project()

			// The check, the edit and the resolution are one change of the
			// project: no other tt is to change it in between.
			ctx, unlock, err := project.Lock(cmd.Context())
			if err != nil {
				return err
			}

			defer unlock()

			manifest, err := project.Manifest()
			if err != nil {
				return err
			}

			if _, ok := manifest.Components[args[0]]; ok {
				return sdk.Usagef("component %q is declared already", args[0])
			}

			err = project.AddComponent(ctx, sdk.Component{
				Name:    args[0],
				Comment: "Probes, declared by tt probe scaffold.",
				Path:    args[0],
				Include: []string{"*.lua"},
			})
			if err != nil {
				return err
			}

			_, _ = fmt.Fprintf(services.Streams().IO().Out,
				"declared component %s\n", args[0])

			_, err = project.Resolve(ctx, sdk.ResolveOptions{})

			return err
		},
	}
}
```

and `New` adds `newScaffoldCmd(services)` to the commands of `tt probe`.
`tt probe scaffold checks` in a project appends to its `app.manifest.toml`,
after a blank line,

```toml
# Probes, declared by tt probe scaffold.
[components.checks]
path = "checks"
include = ["*.lua"]
```

then prints `declared component checks` and resolves `app.manifest.lock`
again, as `tt package resolve` does, all of it under the lock of the
project.

- `Lock` takes the lock of the project that the `tt package` commands
  hold while they change it, `_build/tt.lock`, and returns the context that
  carries it and its release. It waits while another tt holds the lock,
  saying so, until the context is done: Ctrl-C stops it. The methods that
  change the project - `AddComponent`, `UpdateComponent`,
  `RemoveComponent`, `Resolve`, `AddDependency`, `RemoveDependency`,
  `Install`, `Build` and `Clean` - take the lock themselves, each for
  itself; given the context `Lock` returned, they work under it instead.
  A command takes the lock for a change of several steps, such as the
  check and the two edits of `scaffold`, or `tt wasm bind`, which writes
  files of its own and then builds. A method given another context while
  the command holds the lock waits for the lock forever, and so does
  another goroutine, as another tt would: pass the context on. A tt the
  command runs on the project works under its lock, which tt names in
  `TT_PROJECT_LOCK`. `Manifest`, `ReadLock` and `BuildContext` read, and
  take no lock.
- `Manifest` returns what the manifest declares, as written: the name of
  the package, the dependencies and the development dependencies, each with
  its version, registry and path, and the components, each with its path,
  `include`, `exclude`, namespace and build block: `backend`, `cwd`, `env`,
  `output`, `incremental`, `static`, and in `Fields` the fields of the
  backend as the TOML decoder reads them. `ManifestBuild.Decode(backend)`
  decodes those into the configuration of a backend of the module, with the
  decoder of the core, a field it has no place for an error: a command reads
  the block its backend builds, as `tt package build` reads it. The manifest
  is parsed and not checked; a project with no manifest is an error that
  wraps `sdk.ErrNotFound`.
- `AddComponent` appends the component - its comment, `path`, `include` and
  a build block, `sdk.ComponentBuild`, whose `Backend` and `Fields` are
  written in their order, a value of a field being a string, a bool, an
  integer, a slice or a map of them, `output = "dist/calc.wasm"` or
  `env = { PROFILE = "release" }` - after the rest of the manifest, every
  line and comment of which stays. The manifest it makes is checked as
  `tt package build` checks it, with the backends of the tt, before it is
  written: a name declared already, a component the check refuses and a
  project with no manifest write nothing. `UpdateComponent` changes the
  fields of a component, or of its build block, where they are written,
  adds those it does not have and removes those `Remove` names;
  `RemoveComponent` removes a component, its tables and the keys that
  declare it. None of the three resolves the lock or needs a Tarantool: the
  lock is stale after them until `Resolve`, or the next `tt package build`,
  resolves it. A field written inside an inline table, such as
  `build = { backend = "make" }`, is not edited: the error names it.
- `Resolve` resolves the lock against the manifest, as `tt package resolve`
  does, every version the lock chose kept; a tt with no Tarantool resolves
  as `tt package build` does without one. `ReadLock` returns what the lock
  pins, by product, and whether it is fresh, resolving nothing.
- `AddDependency` declares a rock in `[dependencies]`, or with `Dev` in
  `[dev_dependencies]`, as `tt package add` does: a declaration there gets
  the constraint given, and the registry, when the dependency has one, and
  the change says what it had. A command that keeps a declaration someone
  wrote reads `Manifest` first and calls `Resolve` instead, as
  `tt wasm prepare` does. `RemoveDependency` removes one, as
  `tt package remove` does, and `Install` fills `.rocks/` from the lock, as
  `tt package fetch` does. They and `Resolve` take the rock servers a
  `--registry` of the command would give: a module declares the flag
  itself, with the help `tt package` gives it, `sdk.RegistryFlagUsage`.
  The change lists the rocks whose locked version moved; a
  `sdk.DependencyMove` prints as `tt package` reports one, `name 1.0.0-1`,
  `name 1.0.0-1 -> 1.1.0-1` or `name 1.0.0-1 -> (dropped)`.
- `Build` builds the package, or the components `Components` names, with
  the code of `tt package build`: `Build(ctx, sdk.BuildOptions{Components:
  []string{"calc"}})` is `tt package build calc`, its hooks, lock, layout
  and checks included. `Args` are passed through to the build of the one
  component, as `Context.Args`, for its tool. The result says what each
  component made, by type, where it was installed in `.rocks`, and whether
  an incremental build was up to date. `BuildContext` gives the
  `build.Context` a build of a component runs in, and the build its backend
  makes of the block, building nothing: a command of the module of the
  backend runs its tools there, in the environment a build has.
- `Clean` removes what the builds made, as `tt package clean` does:
  `Clean(ctx, sdk.CleanOptions{Components: []string{"calc"}})` is
  `tt package clean calc`, which runs the `Clean` of the backend of `calc`
  ([build.Cleaner](write-a-build-backend.md#clean)) and removes its build
  tree, its records, its files in the stage and the rock of the package,
  so that the next `Build` builds it even when it is incremental and up to
  date. The result has the paths removed and the components whose backend
  cleaned. `DryRun` removes nothing. `tt wasm clean NAME` removes the files
  the module recorded and then cleans the core's state of the component
  with it, under one `Lock`.
- `ScopeDir` is the `.tarantool` of the project, the project scope
  `tt binary` installs into, whose `bin/` is first in the `PATH` of a
  build (`build.Dirs.Tools`). It is not where a build keeps its state,
  `_build/` (`build.Dirs.State`).
  `PrepareEnvironment` writes `.env/activate.sh` and `deactivate.sh` when
  they are missing, as the first `tt binary install` into the project does,
  for a module that installs a tool there.

`Services.Rocks().Index` makes a directory a rock server of the rocks in
it: the LuaRocks manifests and `tt-rocks-index.json`, as
`tt package index` writes them. A module that packs rocks for others serves
them so. A program or a tool a module writes, unpacked from an archive say,
goes through `github.com/tarantool/tt/sdk/execfile`, which writes it with no
fork of tt in between, so that it can be started at once while tt starts
other processes.

In a test the project of `sdktest` touches no file: its manifest is the one
`sdktest.WithManifest` gives, `AddComponent` declares into it,
`AddedComponents` returns what it was given, `Resolves` the calls of
`Resolve`, and `Locks` how many locks `Lock` took and how many are not
released; its `Lock` waits for nothing, and `Clean` records its calls
(`Cleans`) and answers as `sdktest.WithClean` says:

```go
func TestScaffold(t *testing.T) {
	services := sdktest.New(t, sdktest.WithManifest(sdk.Manifest{
		Components: map[string]sdk.ManifestComponent{"lua": {Path: "."}},
	}))

	got := services.Run(probe.New, "probe", "scaffold", "checks")

	require.NoError(t, got.Err)
	assert.Equal(t, "declared component checks\n", got.Stdout)
	require.Len(t, services.AddedComponents(), 1)
	assert.Equal(t, "checks", services.AddedComponents()[0].Path)
	assert.Len(t, services.Resolves(), 1)

	taken, held := services.Locks()
	assert.Equal(t, 1, taken)
	assert.Zero(t, held, "released")

	again := sdktest.New(t, sdktest.WithManifest(sdk.Manifest{
		Components: map[string]sdk.ManifestComponent{"checks": {Path: "checks"}},
	}))

	got = again.Run(probe.New, "probe", "scaffold", "checks")

	var usage *sdk.UsageError
	require.ErrorAs(t, got.Err, &usage)
	assert.Empty(t, again.AddedComponents())
}
```

## Provide a build backend

A module may build the components of a tt package as well: a component
whose build block says `backend = "template"` is built by the backend named
`template` that the module provides, a `build.Backend` of
`github.com/tarantool/tt/sdk/build` among the `Backends` of its
`sdk.Module`. A module may also check the files of every package tt builds,
whatever backend made them, with a `build.FileCheck` among its
`FileChecks`. The backends of tt are a module too, the standard module
`build` in `modules/build` of the repository of tt; the core has no backend
of its own.

[Write a build backend](write-a-build-backend.md) writes such a module from
scratch: the fields of the block and their checks, the build and its
directories, the types of what it makes, incremental builds, file checks,
tests with `sdk/build/buildtest`, and what the core refuses.

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
`WithStdin` and `WithIntegrity` set the other services, and `WithRoot`,
`WithManifest` and `WithNoManifest` the project
([Work on the project](#work-on-the-project)). The `Result` holds
the error, stdout, stderr and the log records; `ExitCode` is the code the
SDK gives the error, without the network and permission rules of the core.
The printers of sdktest encode `json` but not `yaml`, which the core
encodes. A mount the core would refuse fails the test. For a module of an
`sdk.ModuleConstructor`, `sdktest.RunModule` and `services.RunModule` run
the commands of the `sdk.Module` it returns, and fail the test as well on a
backend or a file check the core would refuse when tt starts.

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

[distribution]
standard = false   # the core of v3.0.0-alpha.2 lists no standard modules

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

## Develop together with the core

A change of the module may need a change of the SDK, or of the core, that
no commit of tt you can require has yet. Make both in a local checkout of
tt next to the module, `../tt`, and have tt write a Go workspace that builds
the module on the checkout:

```console
$ cd dist
$ tt binary workspace --core ../tt
/home/me/src/tt-probe/go.work
```

`tt binary workspace` reads `tt.manifest.toml` as `tt binary build tt`
does, with the core of `--core`, or of `[core].path` when the manifest
names a checkout. The `go.work` it writes uses every module of
`[dependencies]` given by a path, and replaces the core and every Go module
of its repository - every `go.mod` under `sdk/`, and the standard modules -
with their directories in the checkout, with no version. Go then reads
nothing of the versions the `go.mod` of the module requires, a commit that
is not pushed as well:

```text
// Written by tt binary workspace: the local modules of the distribution
// ../dist/tt.manifest.toml on the SDK of the core ../tt,
// whatever their go.mod requires. tt binary workspace --remove deletes it.
// Keep it out of git.

go 1.27.4

use (
	.
)

replace (
	github.com/tarantool/tt/modules/aeon => ../tt/modules/aeon
	github.com/tarantool/tt/modules/build => ../tt/modules/build
	github.com/tarantool/tt/modules/emmylua => ../tt/modules/emmylua
	github.com/tarantool/tt/modules/wasm => ../tt/modules/wasm
	github.com/tarantool/tt/sdk => ../tt/sdk
	github.com/tarantool/tt/sdk/backup => ../tt/sdk/backup
	github.com/tarantool/tt/sdk/build => ../tt/sdk/build
	github.com/tarantool/tt/sdk/cluster => ../tt/sdk/cluster
	github.com/tarantool/tt/sdk/connect => ../tt/sdk/connect
	github.com/tarantool/tt/sdk/dial => ../tt/sdk/dial
	github.com/tarantool/tt/sdk/fetch => ../tt/sdk/fetch
	github.com/tarantool/tt/sdk/integrity => ../tt/sdk/integrity
	github.com/tarantool/tt/sdk/tuple => ../tt/sdk/tuple
	github.com/tarantool/tt/v3 => ../tt
)
```

The go command looks for `go.work` from the current directory up, so tt
writes one where `go test` in every local module finds it: beside the
manifest for the modules in the manifest's directory - a module whose
repository has its `tt.manifest.toml` at the root, with `path = "."`, or a
distribution with its modules in subdirectories - and in the directory of
every other module, as `tt-probe` here. Each one uses every local module
of the distribution. The `go` directive is the newest of all their
`go.mod` files; `[replace]` directories of the manifest are replaced too.
`tt binary workspace` prints the paths of the files it wrote, and
`--dry-run` prints what it would write.

The cycle is then:

1. Change the core in `../tt`: add a function to `sdk`, say.
2. Use it in the module and run `go test ./...` in `tt-probe`: the module
   builds on the SDK of `../tt`.
3. Build the distribution on the same checkout, `tt binary build tt --core
   ../tt` in `dist` (see
   [Building on another core](build-distribution.md#building-on-another-core)).
   The build reads no `go.work`: it builds with `GOWORK=off` on the core
   `--core` names, and the content hash of a local module leaves `go.work`
   and `go.work.sum` out, so writing the workspace changes no lock.
4. Once the change of the core is pushed, at `<commit>`, remove the
   workspace and require the SDK at that commit, as
   [Set up the Go module](#set-up-the-go-module) does; build the
   distribution on it with `[core] tt = "<commit>"`:

```console
$ cd ../dist
$ tt binary workspace --remove
/home/me/src/tt-probe/go.work
$ cd ../tt-probe
$ go get github.com/tarantool/tt/sdk@<commit> \
    github.com/tarantool/tt/sdk/build@<commit> \
    github.com/tarantool/tt/sdk/connect@<commit> \
    github.com/tarantool/tt/sdk/dial@<commit>
$ go mod tidy
$ go test ./...
ok  	example.com/tt-probe	0.005s
```

`--remove` deletes the `go.work` files tt wrote and their `go.work.sum`,
and needs no checkout: the manifest may name a version of the core again.
tt rewrites a `go.work` it wrote; one it did not write - one without its
first comment line - it overwrites only with `--force`, and never removes.
A distribution kept for development may name the checkout in
the manifest, `[core] path = "../tt"`, and then neither command needs
`--core`.

A module that starts to import an SDK module its `go.mod` does not
require, one the change of the core adds say, needs a requirement for it,
and `go get` looks the version up even in a workspace, which fails before
the push. Require the placeholder by hand until then:

```sh
go mod edit -require=github.com/tarantool/tt/sdk/build@v0.0.0-0
```

The `go get` of step 4 puts the commit in its place.

`gopls`, and the editors built on it, take the workspace the go command
finds - `go env GOWORK` in the module prints its path; restart the
language server after the file is written or removed. Keep `go.work` and
`go.work.sum` out of git, in the `.gitignore` of the module: they name
directories of your machine, and `go mod tidy` ignores them anyway.

Without tt, the same workspace is written by the go command, one `-replace`
for each module of the checkout: every `go.mod` under `sdk/` and the
modules the `go.mod` of tt replaces with its directories. The list grows
with the SDK; `tt binary workspace` reads it from the checkout.

```sh
cd tt-probe
go work init .
go work edit \
    -replace github.com/tarantool/tt/v3=../tt \
    -replace github.com/tarantool/tt/modules/aeon=../tt/modules/aeon \
    -replace github.com/tarantool/tt/modules/build=../tt/modules/build \
    -replace github.com/tarantool/tt/modules/emmylua=../tt/modules/emmylua \
    -replace github.com/tarantool/tt/modules/wasm=../tt/modules/wasm \
    -replace github.com/tarantool/tt/sdk=../tt/sdk \
    -replace github.com/tarantool/tt/sdk/backup=../tt/sdk/backup \
    -replace github.com/tarantool/tt/sdk/build=../tt/sdk/build \
    -replace github.com/tarantool/tt/sdk/cluster=../tt/sdk/cluster \
    -replace github.com/tarantool/tt/sdk/connect=../tt/sdk/connect \
    -replace github.com/tarantool/tt/sdk/dial=../tt/sdk/dial \
    -replace github.com/tarantool/tt/sdk/fetch=../tt/sdk/fetch \
    -replace github.com/tarantool/tt/sdk/integrity=../tt/sdk/integrity \
    -replace github.com/tarantool/tt/sdk/tuple=../tt/sdk/tuple
```

`go work use ../tt/sdk ...` instead of the replacements works only while
the `go.mod` of the module requires the SDK at a commit Go can download:
the go command still reads the `go.mod` of every version a used module is
required at, and a commit that is not pushed fails with
`invalid version: unknown revision`. A replacement with no version reads
none.

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
  its `-C`, and gets that directory, which is its `Root` too.
