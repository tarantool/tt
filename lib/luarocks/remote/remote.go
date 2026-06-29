// Package remote implements the default rocks.RemoteIndex against an
// HTTP(S) rock server. It is consumed by the Rocks facade's New
// constructor and by deps.Resolve via the rocks.RemoteIndex interface.
//
// The package lives outside the root rocks package because it consumes
// manif (Lua-source manifest parser) which itself imports rocks for the
// shared data types — placing HTTPRemoteIndex at the root would create
// an import cycle.
package remote

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	rocks "github.com/tarantool/tt/lib/luarocks"
	"github.com/tarantool/tt/lib/luarocks/deps"
	"github.com/tarantool/tt/lib/luarocks/manif"
)

// HTTPRemoteIndex is the default rocks.RemoteIndex backed by one or more
// rock servers reachable over HTTP/HTTPS.
//
// Manifest filename probe order, per server:
//
//  1. manifest-<lua_version>.json   (parsed as JSON)
//  2. manifest-<lua_version>         (Lua-source via manif.Parse)
//  3. manifest                        (Lua-source)
//
// HTTPRemoteIndex caches the parsed manifest per (server, lua-version)
// pair for the lifetime of the struct so the resolver does not re-fetch
// for every Query call.
type HTTPRemoteIndex struct {
	// Servers is the ordered list of base URLs (with or without trailing
	// slash) to consult. Empty is an error from Query.
	Servers []string

	// InsecureServers contains hostnames whose TLS certificates should not
	// be verified. Pulled from Config.InsecureServers by the facade.
	InsecureServers []string

	// UserAgent overrides the default User-Agent header for outbound GETs.
	UserAgent string

	// LuaVersion is the Lua dialect string used to build the manifest
	// filename. Defaults to "5.1" if empty (Tarantool case).
	LuaVersion string

	// cache memoizes parsed manifests across calls. Keyed by the server
	// string as passed to load (the raw, un-normalized entry from Servers).
	cache map[string]*remoteManifest
}

// Compile-time check that HTTPRemoteIndex satisfies rocks.RemoteIndex.
var _ rocks.RemoteIndex = (*HTTPRemoteIndex)(nil)

const (
	// httpClientTimeout bounds a single manifest GET.
	httpClientTimeout = 2 * time.Minute

	// httpStatusErrorThreshold is the lowest HTTP status code treated as an
	// error (4xx and 5xx).
	httpStatusErrorThreshold = 400
)

// Arch priorities ranking arch entries when one rock version is listed
// under multiple arches: richer payloads win (src > all > installed >
// rockspec).
const (
	prioRockspec = iota
	prioInstalled
	prioAll
	prioSrc
)

// archPriority ranks arch entries by the priorities above.
var archPriority = map[string]int{
	"src":       prioSrc,
	"all":       prioAll,
	"installed": prioInstalled,
	"rockspec":  prioRockspec,
}

// remoteManifest is the in-memory projection of a parsed rock server
// manifest needed to satisfy Query. The full upstream shape carries more
// data (modules, commands, dependencies) but the resolver only needs the
// (name → versions → arch) triplet to construct URLs.
type remoteManifest struct {
	server     string
	repository map[string]map[string][]archEntry
}

type archEntry struct {
	arch string
}

// Query implements rocks.RemoteIndex. For each known server, it returns
// every VersionedRock listed under `name` in the manifest, with the URL
// pre-computed using path.make_url-equivalent rules (see makeRockURL).
func (h *HTTPRemoteIndex) Query(ctx context.Context, name string) ([]rocks.VersionedRock, error) {
	if len(h.Servers) == 0 {
		return nil, errors.New("remote.HTTPRemoteIndex: no servers configured")
	}

	out := []rocks.VersionedRock{}

	for _, srv := range h.Servers {
		mf, err := h.load(ctx, srv)
		if err != nil {
			return nil, fmt.Errorf("remote.HTTPRemoteIndex: load %s: %w", srv, err)
		}

		versions, ok := mf.repository[name]
		if !ok {
			continue
		}

		for verStr, arches := range versions {
			v, err := deps.ParseVersion(verStr)
			if err != nil {
				return nil, fmt.Errorf("remote.HTTPRemoteIndex: parse version %q for %q: %w", verStr, name, err)
			}

			pick := pickArch(arches)
			out = append(out, rocks.VersionedRock{
				Name:    name,
				Version: v,
				URL:     makeRockURL(srv, name, verStr, pick),
			})
		}
	}

	return out, nil
}

// load fetches and parses the manifest for one server, using the cache if
// present.
func (h *HTTPRemoteIndex) load(ctx context.Context, server string) (*remoteManifest, error) {
	if h.cache == nil {
		h.cache = map[string]*remoteManifest{}
	}

	if m, ok := h.cache[server]; ok {
		return m, nil
	}

	lv := h.LuaVersion
	if lv == "" {
		lv = "5.1"
	}

	base := strings.TrimRight(server, "/") + "/"

	type probe struct {
		path   string
		isJSON bool
	}

	probes := []probe{
		{path: base + "manifest-" + lv + ".json", isJSON: true},
		{path: base + "manifest-" + lv, isJSON: false},
		{path: base + "manifest", isJSON: false},
	}

	var lastErr error

	for _, p := range probes {
		body, err := h.get(ctx, p.path)
		if err != nil {
			lastErr = err

			continue
		}

		var raw map[string]any
		if p.isJSON {
			err := json.Unmarshal(body, &raw)
			if err != nil {
				lastErr = fmt.Errorf("decode JSON manifest at %s: %w", p.path, err)

				continue
			}
		} else {
			v, err := manif.Parse(body)
			if err != nil {
				lastErr = fmt.Errorf("parse manifest at %s: %w", p.path, err)

				continue
			}

			rawMap, ok := v.(map[string]any)
			if !ok {
				lastErr = fmt.Errorf("manifest at %s: top-level is %T, want map", p.path, v)

				continue
			}

			raw = rawMap
		}

		m, err := projectManifest(server, raw)
		if err != nil {
			lastErr = fmt.Errorf("project manifest from %s: %w", p.path, err)

			continue
		}

		h.cache[server] = m

		return m, nil
	}

	if lastErr == nil {
		lastErr = fmt.Errorf("no manifest variant found at %s", server)
	}

	return nil, lastErr
}

// get performs a single HTTP GET with sensible defaults. It returns the
// response body for any status below 400; a status of 400 or above is an
// error. (Redirects are followed by the underlying http.Client, so 3xx
// responses are not normally seen here.)
func (h *HTTPRemoteIndex) get(ctx context.Context, rawURL string) ([]byte, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return nil, fmt.Errorf("parse url: %w", err)
	}

	tr := &http.Transport{}

	for _, host := range h.InsecureServers {
		if strings.EqualFold(host, u.Host) {
			tr.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in

			break
		}
	}

	client := &http.Client{Transport: tr, Timeout: httpClientTimeout}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}

	ua := h.UserAgent
	if ua == "" {
		ua = "go-luarocks/0.1"
	}

	req.Header.Set("User-Agent", ua)

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}

	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode >= httpStatusErrorThreshold {
		return nil, fmt.Errorf("GET %s: status %d", rawURL, resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// projectManifest extracts the (name → version → arches) shape from the
// raw parsed manifest. We only consume `repository`.
func projectManifest(server string, raw map[string]any) (*remoteManifest, error) {
	repoRaw, ok := raw["repository"]
	if !ok {
		return nil, errors.New("missing `repository` key")
	}

	repoMap, ok := repoRaw.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("repository is %T, want map", repoRaw)
	}

	out := &remoteManifest{
		server:     server,
		repository: map[string]map[string][]archEntry{},
	}

	for name, vAny := range repoMap {
		versions, ok := vAny.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("repository.%s is %T, want map", name, vAny)
		}

		inner := map[string][]archEntry{}

		for ver, entry := range versions {
			arr, err := toArchArr(entry)
			if err != nil {
				return nil, fmt.Errorf("repository.%s.%s: %w", name, ver, err)
			}

			inner[ver] = arr
		}

		out.repository[name] = inner
	}

	return out, nil
}

func toArchArr(v any) ([]archEntry, error) {
	switch arr := v.(type) {
	case []any:
		out := make([]archEntry, 0, len(arr))

		for i, e := range arr {
			em, ok := e.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("entry %d is %T, want map", i, e)
			}

			arch, _ := em["arch"].(string)
			out = append(out, archEntry{arch: arch})
		}

		return out, nil
	case map[string]any:
		// JSON serialization of the upstream manifest can flatten the single-
		// element arch array into a bare map. Accept that shape too.
		arch, _ := arr["arch"].(string)

		return []archEntry{{arch: arch}}, nil
	default:
		return nil, fmt.Errorf("expected array or map, got %T", v)
	}
}

// pickArch chooses one arch entry from the list, preferring richer
// payloads (src > all > installed > rockspec). For URL construction the
// choice only matters when one server lists the same rock under multiple
// arches; for typical rock servers each version has one entry.
func pickArch(arches []archEntry) string {
	if len(arches) == 0 {
		return ""
	}

	best := arches[0].arch

	bestP, ok := archPriority[best]
	if !ok {
		bestP = -1
	}

	for _, a := range arches[1:] {
		p, ok := archPriority[a.arch]
		if !ok {
			p = -1
		}

		if p > bestP {
			best = a.arch
			bestP = p
		}
	}

	return best
}

// makeRockURL constructs the resource URL for one rock entry, matching
// upstream `path.make_url(repo, name, version, arch)`.
//
//	arch == "rockspec"   → <server>/<name>-<version>.rockspec
//	arch == "installed"  → <server>/<name>/<version>/<name>-<version>.rockspec
//	otherwise            → <server>/<name>-<version>.<arch>.rock
func makeRockURL(server, name, version, arch string) string {
	base := strings.TrimRight(server, "/") + "/"

	switch arch {
	case "rockspec":
		return base + name + "-" + version + ".rockspec"
	case "installed":
		return base + name + "/" + version + "/" + name + "-" + version + ".rockspec"
	default:
		return base + name + "-" + version + "." + arch + ".rock"
	}
}
