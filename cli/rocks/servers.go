package rocks

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/tarantool/go-luarocks/remote"
	"github.com/tarantool/tt/sdk/log"
)

// ErrIndexOnlyServer reports a rock server named on the command line of
// tt rocks that publishes nothing but a rock index, which LuaRocks does not
// read.
var ErrIndexOnlyServer = errors.New("the rock server serves only a rock index")

// indexOnlyAdvice says what to do with a server that serves only a rock index.
const indexOnlyAdvice = "LuaRocks reads only manifests; install from it with tt package"

// manifestNames are the manifests upstream LuaRocks probes a server for, in
// order, for the Lua version of Tarantool (manif.load_manifest): a server
// with any of them is one LuaRocks reads.
//
//nolint:gochecknoglobals // A constant list: Go has no constant slices.
var manifestNames = []string{"manifest-5.1.zip", "manifest-5.1", "manifest"}

// probeTimeout bounds each request of an indexProbe.
const probeTimeout = 10 * time.Second

// indexProbe tells a rock server that publishes only a tt-rocks-index.json
// (remote.RocksIndexFile) - which go-luarocks reads and upstream LuaRocks,
// what tt rocks runs, does not - from one LuaRocks can read.
//
// The check is cheap and errs on the side of keeping a server:
//
//   - a directory is index-only when it holds the index file and none of
//     manifestNames;
//   - an HTTP(S) server is index-only when a HEAD of <server>/tt-rocks-index.json
//     answers 2xx and a HEAD of each of manifestNames answers 403, 404 or 410,
//     the answers go-luarocks takes for a file a server does not have. A server
//     without the index costs one request; a request that fails, or any other
//     answer, keeps the server, for LuaRocks to report on.
//
// Any other form of server is kept.
type indexProbe struct {
	// client sends the requests of HTTP(S) servers.
	client *http.Client
}

// newIndexProbe returns the probe tt rocks uses.
func newIndexProbe() indexProbe {
	return indexProbe{client: &http.Client{Timeout: probeTimeout}}
}

// indexOnly reports whether server serves only a rock index.
func (p indexProbe) indexOnly(ctx context.Context, server string) bool {
	if dir, ok := remote.LocalServerPath(server); ok {
		return localIndexOnly(dir)
	}

	parsed, err := url.Parse(server)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return false
	}

	base := strings.TrimSuffix(server, "/") + "/"

	if p.head(ctx, base+remote.RocksIndexFile) != filePresent {
		return false
	}

	for _, name := range manifestNames {
		if p.head(ctx, base+name) != fileAbsent {
			return false
		}
	}

	return true
}

// localIndexOnly reports whether the directory dir holds the rock index and
// no manifest.
func localIndexOnly(dir string) bool {
	if !isFile(filepath.Join(dir, remote.RocksIndexFile)) {
		return false
	}

	for _, name := range manifestNames {
		if isFile(filepath.Join(dir, name)) {
			return false
		}
	}

	return true
}

// isFile reports whether path is a regular file.
func isFile(path string) bool {
	info, err := os.Stat(path) //nolint:gosec // A file of the rock server the user configured.

	return err == nil && info.Mode().IsRegular()
}

// probeAnswer is what a HEAD request found of a file.
type probeAnswer int

const (
	// fileUnknown: the request failed or the answer says neither.
	fileUnknown probeAnswer = iota
	// filePresent: the server has the file.
	filePresent
	// fileAbsent: the server does not have the file.
	fileAbsent
)

// head asks for the file at rawURL, of a rock server the user configured,
// with a HEAD request.
func (p indexProbe) head(ctx context.Context, rawURL string) probeAnswer {
	//nolint:gosec // The rock server the user configured, which LuaRocks asks next.
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, rawURL, nil)
	if err != nil {
		return fileUnknown
	}

	resp, err := p.client.Do(req) //nolint:gosec // As above.
	if err != nil {
		log.Debugf("tt rocks: probing %s: %s", rawURL, err)

		return fileUnknown
	}

	_ = resp.Body.Close()

	switch {
	case resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusMultipleChoices:
		return filePresent
	case resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusNotFound,
		resp.StatusCode == http.StatusGone:
		return fileAbsent
	default:
		return fileUnknown
	}
}

// keepManifestServers returns servers without those that serve only a rock
// index, warning of each one it leaves out.
func keepManifestServers(ctx context.Context, probe indexProbe, servers []string) []string {
	kept := make([]string, 0, len(servers))

	for _, server := range servers {
		if probe.indexOnly(ctx, server) {
			log.Warnf("tt rocks leaves out %s: it serves only a rock index (%s); %s",
				server, remote.RocksIndexFile, indexOnlyAdvice)

			continue
		}

		kept = append(kept, server)
	}

	return kept
}

// checkNamedServers fails when a server args name for LuaRocks - with
// --server, which takes a list separated by spaces, --only-server or their
// aliases --from and --only-from - serves only a rock index: the user asked
// for it by name, so leaving it out would install from elsewhere.
func checkNamedServers(ctx context.Context, probe indexProbe, args []string) error {
	for _, server := range namedServers(args) {
		if probe.indexOnly(ctx, server) {
			return fmt.Errorf("%w: %s has nothing but %s; %s",
				ErrIndexOnlyServer, server, remote.RocksIndexFile, indexOnlyAdvice)
		}
	}

	return nil
}

// namedServers returns the servers the LuaRocks options of args name, up to
// "--", which ends the options.
func namedServers(args []string) []string {
	var servers []string

	for next := 0; next < len(args); next++ {
		arg := args[next]
		if arg == "--" {
			break
		}

		name, value, inline := strings.Cut(arg, "=")

		switch name {
		case "--server", "--from", "--only-server", "--only-from":
		default:
			continue
		}

		if !inline {
			if next+1 >= len(args) {
				break
			}

			next++

			value = args[next]
		}

		servers = append(servers, strings.Fields(value)...)
	}

	return servers
}
