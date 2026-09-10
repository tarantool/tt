package rocks

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/tarantool/go-luarocks/remote"
)

// EnvRegistries is the environment variable naming the rock servers to use,
// exported so a command's help text and its reader cannot disagree.
const EnvRegistries = "TT_REGISTRIES"

var (
	// ErrEmptyRegistry reports a blank entry in a registry list.
	ErrEmptyRegistry = errors.New("empty registry entry")
	// ErrDuplicateRegistry reports the same server named twice in one list.
	ErrDuplicateRegistry = errors.New("duplicate registry entry")
	// ErrNoBaseDir reports a relative registry path with no directory to
	// resolve it against.
	ErrNoBaseDir = errors.New("no base directory to resolve a relative registry against")
)

// Source names where an effective registry entry came from. It is what makes
// the effective list explainable: the same URL means something different when
// it came from a flag than when it came from the manifest.
type Source string

const (
	// SourceFlag is a registry named on the command line.
	SourceFlag Source = "flag"
	// SourceEnv is a registry named by the environment.
	SourceEnv Source = "env"
	// SourceManifest is a registry named by [platform].registries.
	SourceManifest Source = "manifest"
	// SourceDefault is a built-in default server.
	SourceDefault Source = "default"
)

// Registry is one entry of the effective rock-server list: the server as the
// engine will use it - a local path already made absolute - and where it was
// configured.
type Registry struct {
	// URL is the server: an HTTP(S) URL, or an absolute directory path.
	URL string `json:"url" yaml:"url"`
	// Source is the configuration layer the entry came from.
	Source Source `json:"source" yaml:"source"`
}

// Sources carries the registry lists a command collected, one per
// configuration layer, plus the directories relative paths resolve against.
//
// The layers do not merge. A rock-server list is ordered and first-found-wins,
// so appending one layer to another would change which server answers rather
// than adding a fallback; the outermost layer that names anything replaces the
// ones below it, which is also how a per-dependency registry key behaves.
type Sources struct {
	// Flag holds the servers named on the command line.
	Flag []string
	// Env holds the servers named by the environment.
	Env []string
	// Manifest holds [platform].registries.
	Manifest []string

	// WorkingDir resolves relative paths from Flag and Env: those are typed
	// against the shell's current directory. Empty makes a relative entry an
	// error rather than a path resolved against something unstated.
	WorkingDir string
	// ProjectDir resolves relative paths from Manifest: those are written
	// against the directory holding the manifest, so the same manifest means
	// the same mirror whatever directory tt was started in.
	ProjectDir string
}

// EffectiveRegistries computes the rock-server list a command must use:
// the first layer of flag, environment and manifest that names anything,
// falling back to the built-in defaults.
//
// Every entry is validated (non-empty, no duplicates within its layer) and a
// relative local path is made absolute against the layer's base directory, so
// the result is what the engine can be handed unchanged.
func EffectiveRegistries(sources Sources) ([]Registry, error) {
	layers := []struct {
		values []string
		source Source
		base   string
	}{
		{values: sources.Flag, source: SourceFlag, base: sources.WorkingDir},
		{values: sources.Env, source: SourceEnv, base: sources.WorkingDir},
		{values: sources.Manifest, source: SourceManifest, base: sources.ProjectDir},
	}

	for _, layer := range layers {
		if len(layer.values) == 0 {
			continue
		}

		return resolveLayer(layer.values, layer.source, layer.base)
	}

	return resolveLayer(DefaultServers(), SourceDefault, "")
}

// URLs projects the effective list onto the plain server list the engine
// configuration takes.
func URLs(registries []Registry) []string {
	out := make([]string, 0, len(registries))
	for _, registry := range registries {
		out = append(out, registry.URL)
	}

	return out
}

// ParseRegistryList splits a comma-separated registry list, the grammar
// TT_REGISTRIES uses. Surrounding whitespace is trimmed so a list written for
// readability behaves like one written compactly; an entry that is blank after
// trimming is kept, so EffectiveRegistries reports it rather than silently
// dropping it.
func ParseRegistryList(raw string) []string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}

	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))

	for _, part := range parts {
		out = append(out, strings.TrimSpace(part))
	}

	return out
}

// resolveLayer validates one layer's entries and renders them as registries.
func resolveLayer(values []string, source Source, base string) ([]Registry, error) {
	out := make([]Registry, 0, len(values))
	seen := make(map[string]bool, len(values))

	for _, value := range values {
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%s registries: %w", source, ErrEmptyRegistry)
		}

		if seen[value] {
			return nil, fmt.Errorf("%s registries: %w %q", source, ErrDuplicateRegistry, value)
		}

		seen[value] = true

		resolved, err := resolveServer(value, source, base)
		if err != nil {
			return nil, err
		}

		out = append(out, Registry{URL: resolved, Source: source})
	}

	return out, nil
}

// resolveServer makes one entry usable from anywhere: a local directory
// becomes an absolute path, an HTTP(S) server is left alone.
//
// The file:// prefix is dropped along the way. The engine accepts both forms
// and a bare path is what every other consumer of the value - an error
// message, a listing, a comparison against another entry - reads most easily.
func resolveServer(value string, source Source, base string) (string, error) {
	path, local := remote.LocalServerPath(value)
	if !local {
		return value, nil
	}

	if filepath.IsAbs(path) {
		return filepath.Clean(path), nil
	}

	if base == "" {
		return "", fmt.Errorf("%s registries: %w: %q", source, ErrNoBaseDir, value)
	}

	return filepath.Join(base, path), nil
}
