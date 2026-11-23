package coredump

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"time"

	"github.com/tarantool/tt/sdk/fetch"
)

// The gdb extensions inspect loads, by the names of their files. The one of
// LuaJIT is luajit_dbg.py since LuaJIT 5db8c217 "dbg: generalize
// extension", in Tarantool 3.8.0 and later and, backported, in 3.6.4 and
// 3.7.1, and luajit-gdb.py before: in 3.6.3, 3.7.0 and older.
const (
	luajitDbgName    = "luajit_dbg.py"
	luajitGDBName    = "luajit-gdb.py"
	tarantoolGDBName = "tarantool-gdb.py"
)

// What the copies of the extensions tt embeds are of: luajit_dbg.py is
// src/luajit_dbg.py of tarantool/luajit 8af934d9, the LuaJIT of Tarantool
// 3.9.0, and tarantool-gdb.py is tools/tarantool-gdb.py of
// tarantool/tarantool 3.9.0.
const (
	embeddedLuaJIT    = "8af934d9"
	embeddedTarantool = "3.9.0"
)

const (
	// fetchTimeout bounds the lookup of the extensions of a core on GitHub,
	// every request of it, and requestTimeout each request.
	fetchTimeout   = 30 * time.Second
	requestTimeout = 10 * time.Second
	// extensionLimit bounds a downloaded extension: they are some hundred
	// kilobytes.
	extensionLimit = 8 << 20
	// cacheDirMode and cacheFileMode are the modes the cache is written
	// with.
	cacheDirMode  = 0o755
	cacheFileMode = 0o644
)

// The URLs the extensions are looked up at.
const (
	defaultRawURL = "https://raw.githubusercontent.com"
	defaultAPIURL = "https://api.github.com"
)

var (
	// errNotFound reports a file or a ref GitHub does not have.
	errNotFound = errors.New("not found")
	// errNoVersion reports a version file that names no version of
	// Tarantool.
	errNoVersion = errors.New("cannot tell the version of Tarantool")
	// errNoFile reports a file tarantool/tarantool has at none of the refs
	// of a version.
	errNoFile = errors.New("tarantool/tarantool has no")
)

// commitPattern matches a commit, full or abbreviated as git describe
// abbreviates it: what the cache is keyed by, since what a commit holds
// never changes.
var commitPattern = regexp.MustCompile(`^[0-9a-f]{7,40}$`)

// fullCommitPattern matches a full commit, as GitHub names the commit of a
// submodule.
var fullCommitPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)

// extension is an extension of a debugger inspect loads, and the debuggers
// it loads in: inspect hands a debugger only those that load in it (see
// forDebugger).
type extension struct {
	// Name is the name of its file.
	Name string
	// Data is its content.
	Data []byte
	// Debuggers are the debuggers it loads in, by their names.
	Debuggers []string
	// After are the names of the extensions that are loaded before it.
	After []string
	// Tarantool and Arch are the versions of Tarantool and the
	// architectures its manifest says it was checked on; an upstream one
	// has none. inspect warns of a core of another for the universal
	// extensions alone (see warnUnchecked): a copy of a release is of
	// that release, and is loaded for a core of another with a warning of
	// its own.
	Tarantool versionRange
	Arch      []string
}

// upstreamDebuggers are the debuggers the upstream extensions load in, by
// the names of their files: what inspect knows of one it fetches from
// GitHub or takes from the sources, which no manifest describes.
// luajit_dbg.py has an adapter for gdb and one for lldb; the older
// luajit-gdb.py and tarantool-gdb.py import gdb. None of them is loaded
// after another: they come in the order resolve returns them, the one of
// LuaJIT first.
var upstreamDebuggers = map[string][]string{
	luajitDbgName:    {debuggerGDB, debuggerLLDB},
	luajitGDBName:    {debuggerGDB},
	tarantoolGDBName: {debuggerGDB},
}

// upstreamExtension returns the upstream extension name of content data,
// with the debuggers it loads in.
func upstreamExtension(name string, data []byte) *extension {
	return &extension{
		Name: name, Data: data, Debuggers: upstreamDebuggers[name], After: nil,
		Tarantool: versionRange{Min: "", Max: ""}, Arch: nil,
	}
}

// resolver finds the gdb extensions that match the Tarantool of a core: of
// its sources, of its version on GitHub, or the copies of tt.
type resolver struct {
	log    *slog.Logger
	client fetch.Client
	// rawURL serves the file of a repository at a ref:
	// <rawURL>/<owner>/<repo>/<ref>/<path>.
	rawURL string
	// apiURL is the root of the GitHub REST API.
	apiURL string
	// cacheDir keeps what was downloaded, by the commits it is of; empty
	// keeps nothing.
	cacheDir string
	// offline is the error of a request that failed otherwise than with a
	// file or a ref GitHub does not have: GitHub is not asked again.
	offline error
	// announced is set once the lookup on GitHub is logged.
	announced bool
}

// newResolver returns a resolver that looks the extensions up on GitHub
// and caches them in the cache directory of the user.
func newResolver(log *slog.Logger) *resolver {
	return &resolver{
		log:       log,
		client:    fetch.Client{HTTP: &http.Client{Timeout: requestTimeout}, Limit: extensionLimit},
		rawURL:    defaultRawURL,
		apiURL:    defaultAPIURL,
		cacheDir:  defaultCacheDir(),
		offline:   nil,
		announced: false,
	}
}

// defaultCacheDir is where the downloaded extensions are cached:
// <user cache>/tt/coredump, or nowhere when the system names no cache
// directory.
func defaultCacheDir() string {
	dir, err := os.UserCacheDir()
	if err != nil {
		return ""
	}

	return filepath.Join(dir, "tt", "coredump")
}

// resolve returns the gdb extensions for a core of the Tarantool whose
// version line is versionLine, the one of LuaJIT first:
//
//  1. those of the sources in sourceDir, when it is not empty;
//  2. those of the version on GitHub: of the commit of a Community build,
//     else of the tag of its release, the Community one for Enterprise;
//  3. the copies of tt, with a warning that they may not match the core.
//
// An extension is looked up the next way when the previous one has none.
func (r *resolver) resolve(ctx context.Context, sourceDir, versionLine string,
) ([]extension, error) {
	var luajit, tarantool *extension

	if sourceDir != "" {
		luajit = r.fromSources(sourceDir, filepath.Join("third_party", "luajit", "src"),
			luajitDbgName, luajitGDBName)
		tarantool = r.fromSources(sourceDir, "tools", tarantoolGDBName)
	}

	version, parsed := parseVersion(versionLine)

	var luajitErr, tarantoolErr error

	switch {
	case luajit != nil && tarantool != nil:
	case !parsed:
		luajitErr = fmt.Errorf("%w from %q", errNoVersion, versionLine)
		tarantoolErr = luajitErr
	default:
		ctx, cancel := context.WithTimeout(ctx, fetchTimeout)
		defer cancel()

		if luajit == nil {
			luajit, luajitErr = r.luajitOf(ctx, version)
		}

		if tarantool == nil {
			tarantool, tarantoolErr = r.tarantoolOf(ctx, version)
		}
	}

	var err error

	if luajit == nil {
		luajit, err = r.embedded(luajitDbgName, version.Line, luajitErr,
			"of LuaJIT "+embeddedLuaJIT, "the LuaJIT of the core", "the lj-* commands")
		if err != nil {
			return nil, err
		}
	}

	if tarantool == nil {
		tarantool, err = r.embedded(tarantoolGDBName, version.Line, tarantoolErr,
			"of Tarantool "+embeddedTarantool, "the structures of the core",
			"the tt-* commands and the pretty-printers of Tarantool")
		if err != nil {
			return nil, err
		}
	}

	return []extension{*luajit, *tarantool}, nil
}

// fromSources returns the first of names in sub of sourceDir, or nil, with
// a warning, when there is none.
func (r *resolver) fromSources(sourceDir, sub string, names ...string) *extension {
	paths := make([]string, 0, len(names))

	for _, name := range names {
		paths = append(paths, filepath.Join(sub, name))

		path := filepath.Join(sourceDir, sub, name)

		data, err := os.ReadFile(path)
		if err == nil {
			r.log.Info(fmt.Sprintf("gdb extension %s: %s", name, path))

			return upstreamExtension(name, data)
		}

		if !errors.Is(err, fs.ErrNotExist) {
			r.log.Warn(fmt.Sprintf("cannot read the gdb extension %s: %s", path, err))
		}
	}

	r.log.Warn(fmt.Sprintf("the sources %s have no %s: looking it up by the version of "+
		"the core", sourceDir, strings.Join(paths, " or ")))

	return nil
}

// embedded returns the copy of tt of the extension name, with a warning
// that names the version of the core, why its own extension was not found,
// what tt's copy is of and what may not match.
func (r *resolver) embedded(name, version string, reason error, copyOf, match, what string,
) (*extension, error) {
	entries, err := embeddedManifest()
	if err != nil {
		return nil, err
	}

	index := slices.IndexFunc(entries, func(entry manifestEntry) bool {
		return entry.File == name
	})
	if index < 0 {
		return nil, fmt.Errorf("failed to find embedded GDB-extension %s in %s: %w", name,
			manifestFile, fs.ErrNotExist)
	}

	ext, err := readEmbedded(entries[index])
	if err != nil {
		return nil, err
	}

	if version == "" {
		version = "the Tarantool of the core"
	}

	r.log.Warn(fmt.Sprintf("cannot get the %s of %s: %s; using the one of tt, %s, which may "+
		"not match %s: %s may fail or show wrong data", name, version, reason, copyOf, match,
		what))

	return ext, nil
}

// extensionSet is a directory of extensions tt embeds, which the manifest
// in it describes.
type extensionSet struct {
	// files holds the directory.
	files fs.FS
	// dir is the directory in files.
	dir string
}

var (
	// embeddedSet are the copies of the upstream extensions, which inspect
	// loads for a core it finds none of and pack puts into the archive.
	embeddedSet = extensionSet{files: extensions, dir: extensionsDir}
	// gdbToolsSet are the universal extensions of tarantool-gdb-tools.
	gdbToolsSet = extensionSet{files: extensions, dir: gdbToolsDir}
)

// name returns how a message names file of the set: by its path under
// extensionsDir, which is the file alone for the copies of the upstream
// extensions, as their messages named it before there was another set.
func (s extensionSet) name(file string) string {
	return strings.TrimPrefix(path.Join(s.dir, file), extensionsDir+"/")
}

// manifest returns the entries of the manifest of the set, in their load
// order: the files of the directory that are extensions, which the manifest
// itself, the record of where they come from, vendor.json, and the license
// files are not.
func (s extensionSet) manifest() ([]manifestEntry, error) {
	data, err := fs.ReadFile(s.files, s.dir+"/"+manifestFile)
	if err != nil {
		return nil, fmt.Errorf("failed to find the manifest of the embedded GDB-extensions: %w",
			err)
	}

	entries, err := parseManifest(data)
	if err != nil {
		return nil, fmt.Errorf("embedded %s: %w", s.name(manifestFile), err)
	}

	return entries, nil
}

// read returns the extension of entry, an entry of the manifest of the set,
// with what the manifest says of it.
func (s extensionSet) read(entry manifestEntry) (*extension, error) {
	data, err := fs.ReadFile(s.files, s.dir+"/"+entry.File)
	if err != nil {
		return nil, fmt.Errorf("failed to read embedded GDB-extension %s: %w", entry.File, err)
	}

	return &extension{
		Name: entry.File, Data: data, Debuggers: entry.Debuggers, After: entry.After,
		Tarantool: entry.Tarantool, Arch: entry.Arch,
	}, nil
}

// all returns the extensions of the manifest of the set, in their load
// order.
func (s extensionSet) all() ([]extension, error) {
	entries, err := s.manifest()
	if err != nil {
		return nil, err
	}

	exts := make([]extension, 0, len(entries))

	for _, entry := range entries {
		ext, err := s.read(entry)
		if err != nil {
			return nil, err
		}

		exts = append(exts, *ext)
	}

	return exts, nil
}

// readEmbedded returns the copy of tt of the extension of entry, an entry
// of the embedded manifest, with what the manifest says of it.
func readEmbedded(entry manifestEntry) (*extension, error) {
	return embeddedSet.read(entry)
}

// embeddedManifest returns the entries of the manifest of the copies of the
// upstream extensions tt embeds, in their load order (see
// extensionSet.manifest).
func embeddedManifest() ([]manifestEntry, error) {
	return embeddedSet.manifest()
}

// tarantoolOf returns tools/tarantool-gdb.py of tarantool/tarantool at the
// first ref of version that has it.
func (r *resolver) tarantoolOf(ctx context.Context, version tarantoolVersion,
) (*extension, error) {
	refs := version.refs()

	for _, ref := range refs {
		cached := r.cachePath(ref, "tarantool", ref, tarantoolGDBName)

		data, ok := r.readCache(cached)
		if !ok {
			var err error

			data, err = r.fetch(ctx, version.Line,
				r.rawURL+"/tarantool/tarantool/"+url.PathEscape(ref)+"/tools/"+tarantoolGDBName)
			if errors.Is(err, errNotFound) {
				continue
			}

			if err != nil {
				return nil, err
			}

			r.writeCache(cached, data)
		}

		r.log.Info(fmt.Sprintf("gdb extension %s: tools/%s of tarantool/tarantool %s",
			tarantoolGDBName, tarantoolGDBName, ref))

		return upstreamExtension(tarantoolGDBName, data), nil
	}

	return nil, fmt.Errorf("%w tools/%s at %s", errNoFile, tarantoolGDBName,
		strings.Join(refs, " or "))
}

// luajitOf returns the gdb extension of the LuaJIT that third_party/luajit
// of tarantool/tarantool is at, at the first ref of version that has the
// submodule.
func (r *resolver) luajitOf(ctx context.Context, version tarantoolVersion,
) (*extension, error) {
	refs := version.refs()

	for _, ref := range refs {
		commit, err := r.submodule(ctx, version.Line, ref)
		if errors.Is(err, errNotFound) {
			continue
		}

		if err != nil {
			return nil, err
		}

		ext, err := r.luajitAt(ctx, version.Line, commit)
		if err != nil {
			return nil, fmt.Errorf("tarantool/luajit %s, the LuaJIT of tarantool/tarantool "+
				"%s: %w", commit, ref, err)
		}

		r.log.Info(fmt.Sprintf("gdb extension %s: src/%s of tarantool/luajit %s, the LuaJIT "+
			"of tarantool/tarantool %s", ext.Name, ext.Name, commit, ref))

		return ext, nil
	}

	return nil, fmt.Errorf("%w third_party/luajit at %s", errNoFile, strings.Join(refs, " or "))
}

// luajitAt returns the gdb extension of tarantool/luajit at commit:
// src/luajit_dbg.py, or src/luajit-gdb.py of an older LuaJIT.
func (r *resolver) luajitAt(ctx context.Context, version, commit string) (*extension, error) {
	names := []string{luajitDbgName, luajitGDBName}

	for _, name := range names {
		data, ok := r.readCache(r.cachePath(commit, "luajit", commit, name))
		if ok {
			return upstreamExtension(name, data), nil
		}
	}

	for _, name := range names {
		data, err := r.fetch(ctx, version, r.rawURL+"/tarantool/luajit/"+commit+"/src/"+name)
		if errors.Is(err, errNotFound) {
			continue
		}

		if err != nil {
			return nil, err
		}

		r.writeCache(r.cachePath(commit, "luajit", commit, name), data)

		return upstreamExtension(name, data), nil
	}

	return nil, fmt.Errorf("no src/%s or src/%s: %w", luajitDbgName, luajitGDBName, errNotFound)
}

// submoduleContent is what the contents API of GitHub replies for a
// submodule.
type submoduleContent struct {
	Type string `json:"type"`
	SHA  string `json:"sha"`
}

// submodule returns the commit of tarantool/luajit that third_party/luajit
// of tarantool/tarantool points to at ref.
func (r *resolver) submodule(ctx context.Context, version, ref string) (string, error) {
	cached := r.cachePath(ref, "tarantool", ref, "luajit")

	data, ok := r.readCache(cached)
	if ok && fullCommitPattern.Match(data) {
		return string(data), nil
	}

	data, err := r.fetch(ctx, version, r.apiURL+
		"/repos/tarantool/tarantool/contents/third_party/luajit?ref="+url.QueryEscape(ref))
	if err != nil {
		return "", err
	}

	var content submoduleContent

	err = json.Unmarshal(data, &content)
	if err != nil || content.Type != "submodule" || !fullCommitPattern.MatchString(content.SHA) {
		return "", fmt.Errorf("third_party/luajit of tarantool/tarantool %s is no submodule: %w",
			ref, errNotFound)
	}

	r.writeCache(cached, []byte(content.SHA))

	return content.SHA, nil
}

// cachePath returns the path under the cache directory of elems, or ""
// when nothing is cached: with no cache directory, or when id, what the
// content is of, is no commit.
func (r *resolver) cachePath(id string, elems ...string) string {
	if r.cacheDir == "" || !commitPattern.MatchString(id) {
		return ""
	}

	return filepath.Join(append([]string{r.cacheDir}, elems...)...)
}

// readCache returns the content cached at path, if any.
func (r *resolver) readCache(path string) ([]byte, bool) {
	if path == "" {
		return nil, false
	}

	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > extensionLimit {
		return nil, false
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}

	return data, true
}

// writeCache keeps data at path, unless path is empty. A cache that cannot
// be written fails nothing.
func (r *resolver) writeCache(path string, data []byte) {
	if path == "" {
		return
	}

	err := writeFileAtomic(path, data)
	if err != nil {
		r.log.Debug("cannot cache a gdb extension: " + err.Error())
	}
}

// fetch returns the body at link. A file or a ref GitHub does not have
// wraps errNotFound; any other failure makes the resolver offline, so that
// a network that is not there is waited for once. version names the
// Tarantool the files are looked up for.
func (r *resolver) fetch(ctx context.Context, version, link string) ([]byte, error) {
	if r.offline != nil {
		return nil, r.offline
	}

	if !r.announced {
		r.announced = true
		r.log.Info("looking up the gdb extensions of " + version + " on GitHub")
	}

	data, err := r.client.Bytes(ctx, link)
	if err == nil {
		return data, nil
	}

	status, ok := errors.AsType[*fetch.HTTPError](err)
	if ok &&
		(status.Status == http.StatusNotFound || status.Status == http.StatusUnprocessableEntity) {
		return nil, fmt.Errorf("%s: %w", link, errNotFound)
	}

	// A request that did not get a reply says so once, without the URL
	// the error of the client repeats.
	request, ok := errors.AsType[*url.Error](err)
	if ok {
		err = fmt.Errorf("cannot reach GitHub: %w", request.Err)
	}

	r.offline = err

	return nil, err
}

// writeFileAtomic writes data to path through a temporary file in its
// directory and a rename, so that a reader never sees a part of it.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)

	err := os.MkdirAll(dir, cacheDirMode)
	if err != nil {
		return fmt.Errorf("create %s: %w", dir, err)
	}

	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	defer func() { _ = os.Remove(tmp.Name()) }()

	_, err = tmp.Write(data)
	if err == nil {
		err = tmp.Chmod(cacheFileMode)
	}

	err = errors.Join(err, tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}

	if err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}
