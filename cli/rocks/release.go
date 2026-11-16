package rocks

import (
	"context"
	"fmt"
	"strings"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/client"
	"github.com/tarantool/go-luarocks/deps"
	"github.com/tarantool/tt/sdk/log"
)

// defaultServer is the rock server LuaRocks of go-luarocks queries when it is
// given none (extra/hardcoded.lua).
const defaultServer = "http://rocks.tarantool.org/"

// rockSearcher finds the versions of a rock on the servers of a run.
type rockSearcher func(ctx context.Context, servers []string, name string) (
	[]client.SearchResult, error)

// searchRocks is the rockSearcher of a run: the native engine of go-luarocks,
// on servers alone.
func searchRocks(cfg luarocks.Config) rockSearcher {
	return func(ctx context.Context, servers []string, name string) ([]client.SearchResult, error) {
		cfg.Servers = servers

		rocksClient, err := client.New(cfg, client.WithBackend(client.BackendNative))
		if err != nil {
			return nil, fmt.Errorf("searching the rock servers: %w", err)
		}

		return rocksClient.Search(ctx, name, client.SearchOpts{
			Version: "", Source: false, Binary: false, All: false, Servers: nil,
		})
	}
}

// pinRelease returns args, a LuaRocks command line, with the newest release
// of the rock args install added as its version, when args are `install NAME`
// with no version and no --dev: LuaRocks takes the newest version a server
// lists, and scm-1 sorts above every release, so tt rocks install checks
// installed a checkout of a branch where tt package add checks declares a
// release. A rock with no release, a rock file, and a search that fails
// leave args as they are, for LuaRocks to install or to fail on as before.
//
// servers are the servers the run queries, none for the default of
// LuaRocks; --only-server and --server in args choose as LuaRocks does.
func pinRelease(ctx context.Context, search rockSearcher, servers, args []string) []string {
	positions := positionals(args)
	if len(positions) != 2 || args[positions[0]] != "install" {
		return args
	}

	name := args[positions[1]]
	if strings.HasSuffix(name, ".rock") || strings.HasSuffix(name, ".rockspec") {
		return args
	}

	queried, ok := runServers(servers, args)
	if !ok {
		return args
	}

	results, err := search(ctx, queried, name)
	if err != nil {
		log.Debugf("Looking for the releases of %s: %s", name, err)

		return args
	}

	version := newestRelease(results, name)
	if version == "" {
		return args
	}

	log.Debugf("Installing %s %s, the newest release", name, version)

	pinned := make([]string, 0, len(args)+1)

	pinned = append(pinned, args[:positions[1]+1]...)
	pinned = append(pinned, version)

	return append(pinned, args[positions[1]+1:]...)
}

// runServers returns the servers a LuaRocks run of args queries, given
// servers, those it is configured with: those of --only-server alone, or
// those of --server ahead of servers, the default of LuaRocks for none. It
// reports false for a run of --dev, which takes scm and dev versions.
func runServers(servers, args []string) ([]string, bool) {
	var extra, only []string

	for idx := 0; idx < len(args); idx++ {
		arg := args[idx]

		var value string

		switch {
		case arg == "--dev":
			return nil, false
		case (arg == serverFlag || arg == onlyServerFlag) && idx+1 < len(args):
			idx++

			value = args[idx]
		case strings.HasPrefix(arg, serverFlag+"="), strings.HasPrefix(arg, onlyServerFlag+"="):
			_, value, _ = strings.Cut(arg, "=")
		default:
			continue
		}

		if strings.HasPrefix(arg, onlyServerFlag) {
			only = append(only, strings.Fields(value)...)
		} else {
			extra = append(extra, strings.Fields(value)...)
		}
	}

	if len(only) > 0 {
		return only, true
	}

	if len(servers) == 0 {
		servers = []string{defaultServer}
	}

	return append(extra, servers...), true
}

// newestRelease returns the newest version of the rock name among results
// that is no scm or dev version, "" for none. A name of a namespace,
// owner/rock, is the rock.
func newestRelease(results []client.SearchResult, name string) string {
	if _, rock, ok := strings.Cut(name, "/"); ok {
		name = rock
	}

	var (
		newest luarocks.Version
		found  bool
	)

	for _, result := range results {
		if result.Name != name {
			continue
		}

		version, err := deps.ParseVersion(result.Version)
		if err != nil || version.IsSCM || version.IsDev {
			continue
		}

		if !found || deps.Compare(version, newest) > 0 {
			newest, found = version, true
		}
	}

	if !found {
		return ""
	}

	return newest.Raw
}

// positionals returns the indexes of the positional arguments of args, a
// LuaRocks command line: the command and its arguments, not the options, the
// values of those that take one, and VAR=VALUE assignments.
func positionals(args []string) []int {
	var found []int

	for idx := 0; idx < len(args); idx++ {
		arg := args[idx]

		switch {
		case luarocksValueOptions[arg]:
			idx++
		case strings.HasPrefix(arg, "-"), strings.Contains(arg, "="):
		default:
			found = append(found, idx)
		}
	}

	return found
}
