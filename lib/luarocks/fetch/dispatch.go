// Package fetch retrieves a rock's `source.url` into a working directory.
//
// The dispatcher selects a backend per URL scheme:
//
//	http, https                          → http.go (net/http GET + unpack)
//	git, git+http, git+https, git+ssh,
//	git+file                             → git.go  (go-git clone, no binary)
//	file                                 → file.go (copy local tree)
//
// Unknown schemes return ErrUnsupportedRockspecFeature wrapped with the
// scheme name.
//
// All backends honor ctx for cancellation at the network/transport level
// — the HTTP request and the go-git clone are ctx-bound. Note that
// local archive extraction after an HTTP fetch is not interrupted mid-unpack.
// None mutate process state — no os.Setenv, no os.Chdir.
package fetch

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	rocks "github.com/tarantool/tt/lib/luarocks"
)

// Options tunes a Fetch invocation. The zero value is the documented
// default: no insecure hosts, no extra User-Agent override, no source
// metadata. Pass via FetchWith.
type Options struct {
	// InsecureServers lists URL hosts for which the http backend skips
	// TLS certificate verification. Escape hatch for rocks.tarantool.org.
	InsecureServers []string

	// UserAgent overrides the default User-Agent header sent by
	// the http backend.
	UserAgent string

	// Tag is the value of `source.tag` from the rockspec, passed to the
	// git backend as `--branch <tag>`. Empty means default branch.
	Tag string

	// Branch is the value of `source.branch` from the rockspec, passed
	// to the git backend as `--branch <branch>`. Set Tag or Branch but
	// not both; if both are set Tag wins.
	Branch string
}

// Backend is the per-scheme fetch implementation. The dispatch table
// registers one Backend per scheme group (http*, git*, file).
type Backend interface {
	Fetch(ctx context.Context, rawURL, destDir string, opts Options) (string, error)
}

// Fetch retrieves rawURL into destDir using the default options and
// returns the on-disk path to the unpacked working tree.
//
// Equivalent to FetchWith(ctx, rawURL, destDir, Options{}).
func Fetch(ctx context.Context, rawURL, destDir string) (string, error) {
	return FetchWith(ctx, rawURL, destDir, Options{})
}

// FetchWith is the options-bearing form of Fetch.
func FetchWith(ctx context.Context, rawURL, destDir string, opts Options) (string, error) {
	scheme, err := schemeOf(rawURL)
	if err != nil {
		return "", err
	}

	b, err := backendFor(scheme)
	if err != nil {
		return "", err
	}

	return b.Fetch(ctx, rawURL, destDir, opts)
}

// schemeOf returns the lowercase URL scheme (the part before `://`). For
// `git+ssh://...` it returns `git+ssh`. Empty rawURL is an error.
func schemeOf(rawURL string) (string, error) {
	if rawURL == "" {
		return "", errors.New("fetch: empty URL")
	}
	// net/url.Parse rejects `git+ssh://...` in some Go versions; do a
	// manual split to avoid that and to preserve case-insensitive match.
	if i := strings.Index(rawURL, "://"); i > 0 {
		return strings.ToLower(rawURL[:i]), nil
	}
	// Fall back to net/url for SCP-like forms (we don't claim to support
	// those, but the error message stays consistent).
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("fetch: parse %q: %w", rawURL, err)
	}

	if u.Scheme == "" {
		return "", fmt.Errorf("fetch: URL %q has no scheme", rawURL)
	}

	return strings.ToLower(u.Scheme), nil
}

// backendFor returns the registered Backend for the given scheme, or
// ErrUnsupportedRockspecFeature wrapped with the scheme.
//
//nolint:ireturn // dispatcher intentionally returns the Backend interface
func backendFor(scheme string) (Backend, error) {
	if b, ok := backends[scheme]; ok {
		return b, nil
	}

	return nil, fmt.Errorf("fetch: scheme %q: %w", scheme, rocks.ErrUnsupportedRockspecFeature)
}

// backends is the scheme → Backend dispatch table, fixed at compile time.
// Tests may override entries by saving and restoring the original.
var backends = map[string]Backend{
	"http":      httpBackend{},
	"https":     httpBackend{},
	"git":       gitBackend{},
	"git+file":  gitBackend{},
	"git+http":  gitBackend{},
	"git+https": gitBackend{},
	"git+ssh":   gitBackend{},
	"file":      fileBackend{},
}
