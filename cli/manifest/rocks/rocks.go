// Package rocks is the single point in tt that knows about the go-luarocks
// engine (github.com/tarantool/go-luarocks). It builds a luarocks.Config
// from the tt environment and the manifest platform, hands out a client
// factory, and wraps the primitives the resolver, build and registry layers
// compose: ordered multi-server resolve, rock metadata and source checksum,
// source fetch, and registry search/download. It also re-exports the
// compile/link flag derivation so the manifest c / lua-c build backends share
// one source of per-OS flags with the rock builtin backend.
//
// The package returns primitives; policy lives in the callers. Resolving a
// lock, laying components out by namespace, and the search/download CLI
// surface are built by other packages on top of these calls.
package rocks

import (
	"errors"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/build"
)

// Default rock servers, queried in order (first-found-wins) unless a
// dependency pins its own registry. rocks.tarantool.org carries the Tarantool
// rocks; luarocks.org carries generic ones (inspect, luasocket, …).
const (
	// ServerTarantool is the default primary rock server.
	ServerTarantool = "https://rocks.tarantool.org/"
	// ServerLuaRocks is the default fallback rock server for generic rocks.
	ServerLuaRocks = "https://luarocks.org/"

	// insecureHost is the one server whose TLS certificate the engine does not
	// verify: upstream luarocks distrusts rocks.tarantool.org's cert.
	insecureHost = "rocks.tarantool.org"
)

var (
	// ErrNotFound reports that no queried server has the requested rock.
	ErrNotFound = errors.New("rock not found on any server")
	// ErrNoMatch reports that a rock exists but no version satisfies the
	// requested constraints.
	ErrNoMatch = errors.New("no version satisfies the constraints")
	// errNoRockspec reports that a fetched rock artifact carries no rockspec.
	errNoRockspec = errors.New("no rockspec in fetched artifact")
	// errMultipleRockspec reports that a directory ships more than one top-level
	// rockspec, so which version to pin is ambiguous.
	errMultipleRockspec = errors.New("multiple top-level rockspecs")
)

// DefaultServers returns the ordered default server list: Tarantool rocks
// first, then generic luarocks.org.
func DefaultServers() []string {
	return []string{ServerTarantool, ServerLuaRocks}
}

// Adapter wraps a single luarocks.Config and exposes the rock primitives bound
// to it. Construct it with New; every method works against the same config, so
// the cc flags a rock is built with match the flags a component is built with.
type Adapter struct {
	cfg   luarocks.Config
	index luarocks.RemoteIndex
}

// New builds an Adapter around cfg. The ordered multi-server index is derived
// from cfg.Servers / cfg.InsecureServers and reused across Resolve calls that
// do not pin a registry.
func New(cfg luarocks.Config) *Adapter {
	return &Adapter{
		cfg:   cfg,
		index: newOrderedIndex(httpIndexes(cfg.Servers, cfg.InsecureServers)...),
	}
}

// Config returns the luarocks.Config the Adapter is bound to.
func (a *Adapter) Config() luarocks.Config {
	return a.cfg
}

// Flags derives the compile/link toolchain flags (-fPIC, -I<include>, the
// per-OS shared-link flag, the .so extension) for the bound config. It is the
// one source of flags for both the rock builtin backend and the manifest
// c / lua-c component backends, so per-OS logic is not duplicated.
func (a *Adapter) Flags() build.Flags {
	return build.DeriveFlags(a.cfg)
}
