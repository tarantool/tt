package analyzer

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	luarocks "github.com/tarantool/go-luarocks"
	"github.com/tarantool/go-luarocks/deps"
	"github.com/tarantool/go-luarocks/remote"

	"github.com/tarantool/tt/sdk/fetch"
)

// The programs of EmmyLua outside a project come from the user cache
// (DefaultCacheDir), which holds, besides the assets tt emmylua rock
// downloads:
//
//	programs/<sha256>/      the programs of the rock whose sha256 that is
//	resolved/<key>.json     what a rock server, a version and an arch resolved to
//	config/<key>/emmyrc.json  the configuration for a directory
const (
	// programsDirName holds the programs of each rock, by its sha256.
	programsDirName = "programs"
	// resolvedDirName holds what each query resolved to, so that a later run
	// asks no server.
	resolvedDirName = "resolved"
	// configDirName holds the configurations of the directories outside
	// every project, by their path.
	configDirName = "config"
	// keyLength is the length of a key of the cache: hex digits of a
	// SHA-256.
	keyLength = 16
	// megabyte is the unit the size of a download is told in.
	megabyte = 1e6
)

var (
	// ErrNoCache reports a system that names no user cache directory, where
	// the programs outside a project would be kept.
	ErrNoCache = errors.New("no user cache directory")
	// ErrRockServer reports a rock server that could not be asked, or a rock
	// it names that could not be fetched.
	ErrRockServer = errors.New("cannot reach the rock server")
	// ErrNoRock reports a rock server that has no rock of the version for
	// the host.
	ErrNoRock = errors.New("no such rock")
)

// offlineHint says what works without a rock server. The directory of rocks
// is one tt package download fills from the rock index: tt emmylua rock packs
// the releases with builds to download and local builds alone, and not
// DefaultVersion, which has none.
const offlineHint = "outside a project tt emmylua fetches EmmyLua from a rock server once " +
	"and keeps it in the user cache; to work offline, run it in a project prepared with " +
	"tt emmylua prepare, or give --rock-server a directory of rocks, which " +
	"tt package download " + RockName + " --registry " + DefaultRockServer + " --dir DIR " +
	"fills where the network is"

// ToolsOptions configure Tools.
type ToolsOptions struct {
	// CacheDir is the cache of the module, DefaultCacheDir; empty fails with
	// ErrNoCache.
	CacheDir string
	// RockServer is the rock server to take the rock from: a URL, or a
	// directory, relative to WorkingDir. Empty is DefaultRockServer.
	RockServer string
	// WorkingDir is the directory a relative RockServer is taken from.
	WorkingDir string
	// Version is the release to run, a version constraint as a declaration
	// takes it: "0.30.0pre1" is any revision of it, the newest chosen.
	// Empty is DefaultVersion.
	Version string
	// Arch is the LuaRocks arch of the rock; empty is the host's.
	Arch string
	// Client downloads the rock.
	Client fetch.Client
	// Progress receives a line when a rock is fetched.
	Progress io.Writer
	// Log receives what is taken from where, at debug level; nil logs
	// nothing.
	Log *slog.Logger
}

// resolution is what a query of a rock server resolved to: the rock to run,
// recorded so that a later run asks no server.
type resolution struct {
	// Server, Version and Arch are the query.
	Server  string `json:"server"`
	Version string `json:"version"`
	Arch    string `json:"arch"`
	// Rock is the version-revision of the rock chosen, URL where it was
	// fetched from and SHA256 its digest, which names its programs.
	Rock   string `json:"rock"`
	URL    string `json:"url"`
	SHA256 string `json:"sha256"`
}

// Tools returns the directory of the programs of EmmyLua for a command that
// runs outside a project: those of the emmylua rock of Version from
// RockServer for Arch, unpacked into programs/<sha256 of the rock>/ of the
// cache.
//
// The first run asks the server which rock that is, fetches it, checks it
// against the sha256 the server gives, when it gives one, and unpacks its
// bin/ - written to a temporary directory and renamed into place, so a
// directory of the cache is never half there. It records what the query
// resolved to, and a later run of the same query takes the programs from the
// cache with no request, offline. A rock server that is a directory is asked
// every time: that costs nothing, and a rock packed anew there is taken.
func Tools(ctx context.Context, opts ToolsOptions) (string, error) {
	opts = opts.withDefaults()
	if opts.CacheDir == "" {
		return "", fmt.Errorf("%w for the programs of EmmyLua: run tt emmylua in a project "+
			"prepared with tt emmylua prepare", ErrNoCache)
	}

	query := resolution{
		Server: opts.RockServer, Version: opts.Version, Arch: opts.Arch,
		Rock: "", URL: "", SHA256: "",
	}
	record := filepath.Join(opts.CacheDir, resolvedDirName, cacheKey(
		query.Server+"\n"+query.Version+"\n"+query.Arch)+".json")

	if _, local := remote.LocalServerPath(opts.RockServer); !local {
		dir, ok := opts.recorded(record, query)
		if ok {
			return dir, nil
		}
	}

	rock, err := resolveRock(ctx, opts)
	if err != nil {
		return "", err
	}

	query.Rock, query.URL = rock.Version.Raw, rock.URL

	if rock.SHA256 != "" {
		dir := opts.programsDir(rock.SHA256)
		if hasPrograms(dir) {
			query.SHA256 = rock.SHA256
			opts.record(record, query)

			return dir, nil
		}
	}

	progressf(opts.Progress, "outside a project: fetching EmmyLua %s for %s%s from %s",
		rock.Version.Raw, opts.Arch, sizeOf(rock, opts.Arch), rock.URL)

	data, err := downloadRock(ctx, opts.Client, rock)
	if err != nil {
		return "", err
	}

	query.SHA256 = fetch.SHA256(data)

	dir := opts.programsDir(query.SHA256)
	if !hasPrograms(dir) {
		err = unpackPrograms(data, dir)
		if err != nil {
			return "", fmt.Errorf("%s: %w", rock.URL, err)
		}
	}

	opts.record(record, query)
	opts.debug("EmmyLua " + rock.Version.Raw + " is in " + dir)

	return dir, nil
}

// withDefaults fills the fields left empty, the cache aside, and makes a
// directory of RockServer absolute.
func (o ToolsOptions) withDefaults() ToolsOptions {
	if o.RockServer == "" {
		o.RockServer = DefaultRockServer
	}

	if path, local := remote.LocalServerPath(o.RockServer); local {
		if !filepath.IsAbs(path) {
			path = filepath.Join(o.WorkingDir, path)
		}

		o.RockServer = filepath.Clean(path)
	}

	if o.Version == "" {
		o.Version = DefaultVersion
	}

	if o.Arch == "" {
		o.Arch = remote.HostArch()
	}

	return o
}

// programsDir is the directory of the programs of the rock of sum.
func (o ToolsOptions) programsDir(sum string) string {
	return filepath.Join(o.CacheDir, programsDirName, sum)
}

// recorded returns the programs of what query resolved to before, when it
// is recorded at path and the programs are still in the cache.
func (o ToolsOptions) recorded(path string, query resolution) (string, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", false
	}

	var got resolution

	err = json.Unmarshal(data, &got)
	if err != nil || got.Server != query.Server || got.Version != query.Version ||
		got.Arch != query.Arch || got.SHA256 == "" {
		return "", false
	}

	dir := o.programsDir(got.SHA256)
	if !hasPrograms(dir) {
		return "", false
	}

	o.debug("EmmyLua " + got.Rock + " of " + got.Server + " from the user cache, " + dir)

	return dir, true
}

// record writes what a query resolved to at path. The record saves a later
// run the request; failing to write it fails nothing.
func (o ToolsOptions) record(path string, got resolution) {
	data, err := json.MarshalIndent(got, "", "  ")
	if err == nil {
		err = os.MkdirAll(filepath.Dir(path), dirPerm)
	}

	if err == nil {
		err = writeFileAtomic(path, append(data, '\n'))
	}

	if err != nil {
		o.debug("not recording what " + got.Server + " resolved to: " + err.Error())
	}
}

// debug logs msg at debug level, when there is a logger.
func (o ToolsOptions) debug(msg string) {
	if o.Log != nil {
		o.Log.Debug(msg)
	}
}

// resolveRock asks the rock server of opts for the emmylua rock of the
// version and the arch: the newest revision the constraint takes, passing
// over a yanked one unless the constraint pins it, as a resolve of the lock
// does.
func resolveRock(ctx context.Context, opts ToolsOptions) (luarocks.VersionedRock, error) {
	constraints, err := deps.ParseConstraints(opts.Version)
	if err != nil {
		return luarocks.VersionedRock{}, fmt.Errorf("version %q: %w", opts.Version, err)
	}

	index := remote.NewIndex(opts.RockServer, remote.IndexOptions{
		InsecureServers: nil, UserAgent: "", LuaVersion: "", Arch: opts.Arch, Cache: nil,
	})

	rows, err := index.Query(ctx, RockName, "")
	if err != nil {
		return luarocks.VersionedRock{}, fmt.Errorf("%w %s: %w; %s", ErrRockServer,
			opts.RockServer, err, offlineHint)
	}

	best := -1

	var offered []string

	for index, row := range rows {
		if row.Arch != opts.Arch {
			continue
		}

		offered = append(offered, row.Version.Raw)

		if !deps.MatchRock(row, constraints) {
			continue
		}

		if best < 0 || deps.Compare(row.Version, rows[best].Version) > 0 {
			best = index
		}
	}

	if best >= 0 {
		return rows[best], nil
	}

	slices.Sort(offered)

	has := strings.Join(offered, ", ")
	if has == "" {
		has = "none for this platform"
	}

	return luarocks.VersionedRock{}, fmt.Errorf("%w: the rock server %s has no %s %s for %s "+
		"(it has %s)", ErrNoRock, opts.RockServer, RockName, opts.Version, opts.Arch, has)
}

// sizeOf tells the size of the rock of arch, when the server gives it.
func sizeOf(rock luarocks.VersionedRock, arch string) string {
	for _, artifact := range rock.Artifacts {
		if artifact.Arch == arch && artifact.Size > 0 {
			return fmt.Sprintf(" (%.1f MB)", float64(artifact.Size)/megabyte)
		}
	}

	return ""
}

// downloadRock returns the bytes of rock: read from the directory of a local
// server, or downloaded, and checked against the sha256 the server gives.
func downloadRock(
	ctx context.Context, client fetch.Client, rock luarocks.VersionedRock,
) ([]byte, error) {
	path, local := remote.LocalServerPath(rock.URL)

	var (
		data []byte
		err  error
	)

	switch {
	case local:
		data, err = readRock(path)
	case rock.SHA256 != "":
		data, err = fetch.Fetcher{Client: client, CacheDir: ""}.Fetch(ctx, rock.URL, rock.SHA256)
	default:
		data, err = client.Bytes(ctx, rock.URL)
	}

	if errors.Is(err, fetch.ErrChecksum) {
		return nil, err
	}

	if err != nil {
		return nil, fmt.Errorf("%w: %w; %s", ErrRockServer, err, offlineHint)
	}

	if sum := fetch.SHA256(data); rock.SHA256 != "" && sum != rock.SHA256 {
		return nil, fmt.Errorf("%s: %w: sha256 %s, want %s", rock.URL, fetch.ErrChecksum,
			sum, rock.SHA256)
	}

	return data, nil
}

// readRock reads the rock at path, at most fetch.DefaultLimit bytes of it.
func readRock(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("reading the rock: %w", err)
	}

	defer func() { _ = file.Close() }()

	data, err := io.ReadAll(io.LimitReader(file, fetch.DefaultLimit+1))
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", path, err)
	}

	if len(data) > fetch.DefaultLimit {
		return nil, fmt.Errorf("%s: %w: more than %d bytes", path, fetch.ErrTooLarge,
			fetch.DefaultLimit)
	}

	return data, nil
}

// unpackPrograms writes the programs of the rock in data, its bin/, to dir,
// executable: to a temporary directory next to it first, renamed into place
// once every program is there. A dir that another run filled meanwhile is
// kept as it is.
func unpackPrograms(data []byte, dir string) error {
	archive, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return fmt.Errorf("reading the rock: %w", err)
	}

	err = os.MkdirAll(filepath.Dir(dir), dirPerm)
	if err != nil {
		return fmt.Errorf("creating the cache: %w", err)
	}

	stage, err := os.MkdirTemp(filepath.Dir(dir), "."+filepath.Base(dir)+".*")
	if err != nil {
		return fmt.Errorf("creating the cache: %w", err)
	}

	defer func() { _ = os.RemoveAll(stage) }()

	for _, file := range archive.File {
		name, ok := strings.CutPrefix(file.Name, rockBinDir+"/")
		if !ok || !slices.Contains(Programs, Program(name)) {
			continue
		}

		err = extractProgram(file, filepath.Join(stage, name))
		if err != nil {
			return err
		}
	}

	if !hasPrograms(stage) {
		return fmt.Errorf("%w: the rock lacks one of %s/%s", ErrBuild, rockBinDir,
			strings.Join(programNames(), ", "))
	}

	// A temporary directory is the owner's alone; the cache is not.
	err = os.Chmod(stage, dirPerm)
	if err != nil {
		return fmt.Errorf("unpacking the rock: %w", err)
	}

	err = os.Rename(stage, dir)
	if err != nil && !hasPrograms(dir) {
		return fmt.Errorf("unpacking the rock into %s: %w", dir, err)
	}

	return nil
}

// extractProgram writes the program file of a rock to path, executable, at
// most maxProgramSize bytes of it.
func extractProgram(file *zip.File, path string) error {
	reader, err := file.Open()
	if err != nil {
		return fmt.Errorf("reading %s of the rock: %w", file.Name, err)
	}

	defer func() { _ = reader.Close() }()

	out, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, programPerm)
	if err != nil {
		return fmt.Errorf("unpacking %s: %w", file.Name, err)
	}

	written, err := io.Copy(out, io.LimitReader(reader, maxProgramSize+1))
	if err == nil && written > maxProgramSize {
		err = fmt.Errorf("%w: more than %d bytes", fetch.ErrTooLarge, maxProgramSize)
	}

	if err == nil {
		err = out.Chmod(programPerm)
	}

	closeErr := out.Close()
	if err == nil {
		err = closeErr
	}

	if err != nil {
		return fmt.Errorf("unpacking %s: %w", file.Name, err)
	}

	return nil
}

// hasPrograms reports whether dir holds every program of Programs, each a
// regular file its owner may execute.
func hasPrograms(dir string) bool {
	for _, program := range Programs {
		info, err := os.Stat(filepath.Join(dir, string(program)))
		if err != nil || !info.Mode().IsRegular() || info.Mode().Perm()&0o100 == 0 {
			return false
		}
	}

	return true
}

// programNames are the names of Programs.
func programNames() []string {
	names := make([]string, 0, len(Programs))
	for _, program := range Programs {
		names = append(names, string(program))
	}

	return names
}

// cacheKey is the key of s in the cache: the first hex digits of its
// SHA-256.
func cacheKey(s string) string {
	return fetch.SHA256([]byte(s))[:keyLength]
}

// GlobalConfigPath is the path of the configuration tt writes for dir, a
// directory outside every project, under the cache: config/<key of dir>/,
// so that it does not litter dir.
func GlobalConfigPath(cacheDir, dir string) string {
	return filepath.Join(cacheDir, configDirName, cacheKey(dir), ConfigFileName)
}
