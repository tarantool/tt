package rocks

import (
	"fmt"
	"log/slog"
	"net/url"
	"path/filepath"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"
)

// TarantoolInfo carries the Tarantool facts the adapter needs to build a
// luarocks.Config. The caller fills it from cmdcontext (the resolved binary
// path and cached version) and the install prefix (tt's GetTarantoolPrefix);
// tests fill it directly. The library never parses `tarantool --version`
// itself, so these come from one source of truth on the tt side.
type TarantoolInfo struct {
	// Executable is the path to the tarantool binary.
	Executable string
	// Prefix is the Tarantool install prefix, e.g. "/usr".
	Prefix string
	// Version is the "x.y.z" string used in diagnostic / golden headers.
	Version string
}

// IncludeDir returns the Tarantool C-header directory under the prefix
// (<prefix>/include/tarantool), where the builtin/c backends find the headers.
func (info TarantoolInfo) IncludeDir() string {
	return filepath.Join(info.Prefix, "include", "tarantool")
}

// ConfigOptions tune BuildConfig beyond the Tarantool facts.
type ConfigOptions struct {
	// Tree is the rocks tree root to read from and install into (e.g.
	// <app>/.rocks). Required.
	Tree string
	// WorkingDir is the base directory for relative paths and build staging.
	// Required; the library never reads the process cwd.
	WorkingDir string
	// Servers is the ordered rock-server list; nil falls back to
	// DefaultServers().
	Servers []string
	// Logger receives structured operation logs; nil disables logging.
	Logger *slog.Logger
}

// BuildConfig maps the tt environment to a luarocks.Config. Servers default to
// DefaultServers(); rocks.tarantool.org is marked insecure whenever it is in
// the list (upstream luarocks distrusts its certificate).
func BuildConfig(info TarantoolInfo, opts ConfigOptions) luarocks.Config {
	servers := opts.Servers
	if servers == nil {
		servers = DefaultServers()
	}

	return luarocks.Config{
		Tree:            opts.Tree,
		WorkingDir:      opts.WorkingDir,
		Servers:         servers,
		InsecureServers: insecureHosts(servers),
		Logger:          opts.Logger,
		Rockspec:        luarocks.RockspecConfig{},
		Tarantool: luarocks.TarantoolConfig{
			Executable: info.Executable,
			Prefix:     info.Prefix,
			IncludeDir: info.IncludeDir(),
			Version:    info.Version,
		},
	}
}

// Client builds a go-luarocks client for the bound config. backend selects the
// engine: pass client.BackendLua for operations the native backend does not
// implement (search/download), client.BackendNative otherwise.
func (a *Adapter) Client(backend client.Backend) (*client.Rocks, error) {
	rocksClient, err := client.New(a.cfg, client.WithBackend(backend))
	if err != nil {
		return nil, fmt.Errorf("rocks: new client: %w", err)
	}

	return rocksClient, nil
}

// insecureHosts returns the subset of server hosts whose TLS certificate
// the engine should not verify. Only rocks.tarantool.org qualifies today;
// unparseable entries are skipped.
func insecureHosts(servers []string) []string {
	var hosts []string

	for _, server := range servers {
		parsed, err := url.Parse(server)
		if err != nil {
			continue
		}

		if parsed.Host == insecureHost {
			hosts = append(hosts, parsed.Host)
		}
	}

	return hosts
}
