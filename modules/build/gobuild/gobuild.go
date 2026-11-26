// Package gobuild is the go build backend: go build of a Go program, into
// the directory of the programs of the build (build.Dirs.Programs) under the
// name of the program. Its builds make programs only, and the files of the
// component are not laid out unless its include names them: Go sources are
// no files of a package, and the fixtures of their tests are not either.
//
// The target platform is that of the build, GOOS and GOARCH, another than
// the machine tt runs on too (build.Backend.Cross), and cgo is off unless
// the block turns it on: a program without cgo is static and runs whatever
// the glibc of the machine it is installed on. A build with cgo is made for
// the machine alone, whose C compiler cgo runs.
//
// The modules a program requires are downloaded ahead into the mirror tt
// package download makes (build.Vendorer), in the layout of a Go module
// proxy, and a build given the mirror (build.Context.Vendored) takes them
// from there as its GOPROXY, offline.
//
// A build is made without tt too (build.Backend.Exports): the script of its
// export runs the go build Run runs, for the machine it runs on, so that
// LuaRocks builds the source rock of a package of go as tt does.
//
// The package depends on the standard library and the contract of a build
// backend (github.com/tarantool/tt/sdk/build) only.
package gobuild

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"github.com/tarantool/tt/sdk/build"
)

// Name is the name of the go backend.
const Name = "go"

// goTool is the program the backend runs.
const goTool = "go"

// darwinOS is the GOOS spelling of macOS.
const darwinOS = "darwin"

// defaultPackage is the package a go build builds when its block names
// none: the one in its working directory.
const defaultPackage = "."

// staticTags make a cgo program static in practice and not only in its
// link: net and os/user otherwise reach for the NSS modules of glibc, which
// are loaded at run time whatever the linker was told.
var staticTags = []string{"netgo", "osusergo"}

// staticLdflags link a cgo program statically through the external linker,
// which is what links the C part.
var staticLdflags = []string{"-linkmode", "external", "-extldflags", "-static"}

const (
	// goSumName is the file of the hashes of the modules a module requires,
	// beside its go.mod, which go checks every module it downloads against.
	goSumName = "go.sum"
	// goWorkSumName is the file of the hashes of a workspace, beside its
	// go.work.
	goWorkSumName = "go.work.sum"
	// vendorManifest is the file of the vendor directory of a module, or of a
	// workspace, beside its go.mod or go.work: go build takes the modules
	// from that directory when it is there, and downloads none.
	vendorManifest = "vendor/modules.txt"
	// downloadDir is the directory of a module cache that holds what go
	// downloaded in the layout of a module proxy: <module>/@v/<version>.mod,
	// .info, .zip and .ziphash.
	downloadDir = "cache/download"
	// toolchainModule is the module of the Go toolchains go downloads for a
	// go or toolchain line of go.mod newer than itself: the toolchain is the
	// user's, and is not put into the mirror.
	toolchainModule = "golang.org/toolchain"
	// sumdbDir is the directory of download where go keeps what it read of the
	// checksum database, which is no module.
	sumdbDir = "sumdb"
	// noPrivateModules is a GONOPROXY of no pattern: a build from the mirror
	// takes every module from the mirror, while a private one, of GOPRIVATE
	// or GONOPROXY, is otherwise downloaded from its repository, around
	// GOPROXY. An empty GONOPROXY is unset for go, which takes GOPRIVATE,
	// of the environment or of go env -w, for it then; a comma is a list of
	// no pattern, and matches no module.
	noPrivateModules = ","
	// mirrorFilePerm is the mode of a file put into the mirror: what go keeps
	// in its module cache is read-only.
	mirrorFilePerm os.FileMode = 0o644
	// mirrorDirPerm is the mode of a directory made in the mirror.
	mirrorDirPerm os.FileMode = 0o755
)

// mirrorFileExts are the files of a module of a module proxy the mirror
// keeps: the go.mod, the version, the zip of the sources and its hash. The
// lists of versions and the locks of the module cache are left out.
var mirrorFileExts = []string{".mod", ".info", ".zip", ".ziphash"}

var (
	// ErrStaticDarwin reports a static program for darwin: macOS has no
	// static libSystem to link against, so a static program cannot be made
	// there.
	ErrStaticDarwin = errors.New("a static program cannot be built for darwin")
	// ErrNoGoSum reports a module that requires modules and has no go.sum:
	// nothing pins what the build downloads.
	ErrNoGoSum = errors.New("the go module has no " + goSumName)
	// errUnquotableFlag reports a linker flag go build cannot be given.
	errUnquotableFlag = errors.New(
		"a linker flag with a space and both kinds of quotes cannot be passed to go build")
)

// programNameRe is the shape of the name of a program: a plain file name,
// which tt instance finds in .rocks/bin/ by the binary of an instance
// description given without a slash.
var programNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._+-]*$`)

// Backend returns the go backend.
func Backend() build.Backend {
	return build.Backend{
		Name:           Name,
		Produces:       []build.Result{build.Program},
		Config:         func() any { return new(Config) },
		New:            build.Typed(newBuild),
		Incremental:    false,
		OutputFrom:     build.OutputNone,
		Exclude:        nil,
		NoSourceLayout: true,
		Hook:           false,
		// go builds for another platform with GOOS and GOARCH, but with cgo
		// (Preflight).
		Cross: true,
		// The script of a build runs the go build Run runs (Export).
		Exports: true,
	}
}

// Config are the fields of a go build block.
type Config struct {
	// Package is the Go package built, "." by default.
	Package string `toml:"package"`
	// Name is the name of the program; the name of the component by
	// default.
	Name string `toml:"name"`
	// Tags are the build tags.
	Tags []string `toml:"tags"`
	// Ldflags are the flags of the linker, one per entry, after tt's own.
	Ldflags []string `toml:"ldflags"`
	// Cgo allows cgo; CGO_ENABLED=0 otherwise.
	Cgo bool `toml:"cgo"`
}

// newBuild checks a go build block of the component.
func newBuild(config *Config, component build.Component, common build.Common) (
	build.Build, error,
) {
	if config.Name != "" && !programNameRe.MatchString(config.Name) {
		return nil, build.FieldErrorf("name", "%q is not a file name", config.Name)
	}

	for _, tag := range config.Tags {
		if tag == "" || strings.ContainsAny(tag, ", \t\n") {
			return nil, build.FieldErrorf("tags", "%q is not a build tag", tag)
		}
	}

	if slices.Contains(config.Ldflags, "") {
		return nil, build.FieldErrorf("ldflags", "must not contain an empty entry")
	}

	program := config.Name
	if program == "" {
		program = component.Name
	}

	return goBuild{config: *config, program: program, static: common.Static}, nil
}

// goBuild is a checked go build block.
type goBuild struct {
	config  Config
	program string
	static  bool
}

// Produces says the build makes a program.
func (b goBuild) Produces() []build.Result {
	return []build.Result{build.Program}
}

// Programs names the one program the build makes.
func (b goBuild) Programs() ([]string, bool) {
	return []string{b.program}, true
}

// Preflight checks that a static program is not asked for darwin, that a
// build for another platform than the machine tt runs on is made without
// cgo, and that go is in the PATH of the build.
//
// go compiles and links the Go of a program for any platform it has a port
// to, but cgo compiles the C of the program with the C compiler of CC, the
// machine's: a build with cgo = true for another platform is refused,
// wrapping build.ErrTarget, rather than fail at the compile.
func (b goBuild) Preflight(_ context.Context, c *build.Context) error {
	if b.static && c.Target.OS == darwinOS {
		return fmt.Errorf("%w: static = true needs a linux build", ErrStaticDarwin)
	}

	if b.config.Cgo && c.Target.Cross() {
		return fmt.Errorf("%w: cgo = true compiles C with the C compiler of the machine, %s, "+
			"and the build is for %s; build it there, or without cgo", build.ErrTarget,
			build.HostTarget(), c.Target)
	}

	_, err := c.LookPath(goTool)
	if err != nil {
		return fmt.Errorf("%w: the %s backend runs %q, which is not in PATH",
			build.ErrNoTool, Name, goTool)
	}

	return nil
}

// Run builds the program in the working directory as
// go build -trimpath -o <programs>/<name> <package>, for the target of the
// build, from the modules of the mirror of the build when it has one
// (usesMirror), with -mod=readonly and the mirror for GOPROXY (mirrorEnv).
func (b goBuild) Run(ctx context.Context, c *build.Context) error {
	err := b.Preflight(ctx, c)
	if err != nil {
		return err
	}

	mirror, err := usesMirror(ctx, c)
	if err != nil {
		return err
	}

	var options, env []string

	if mirror {
		// go build checks every module against go.sum, and the checksum
		// database is off: -mod=readonly fails a module go.sum does not
		// pin, which go would otherwise add, whatever GOFLAGS says.
		options = []string{"-mod=readonly"}
		env = mirrorEnv(c.Vendored)
	}

	args, err := goArgs(b.config, filepath.Join(c.Dirs.Programs, b.program), b.static, c.Jobs,
		options)
	if err != nil {
		return err
	}

	cmd := c.Command(ctx, goTool, args...)

	cmd.Env = append(cmd.Env, env...)
	cmd.Env = append(cmd.Env, targetEnv(b.config, c.Target)...)

	err = c.Run(cmd)
	if err != nil && mirror {
		return explainVendored(err, c)
	}

	return err
}

// Vendor downloads the modules the module of the working directory requires
// into dir, as go mod download downloads them, with the environment of the
// build - GOPROXY, GOPRIVATE, the credentials of git - as a build downloads
// them, and the module cache of the user first, as a build takes what is
// there (downloadEnv): dir gets them in the layout of a module proxy,
// <module>/@v/<version>.mod, .info, .zip and .ziphash, which every module of
// the mirror shares, and what is there stays (mergeDownloads). The go.mod
// and go.sum of the module are not written: go mod download is given copies
// of them; go mod download of a workspace downloads for every module of it.
// A module that requires modules and has no go.sum fails it (ErrNoGoSum);
// one with a vendor directory, which the build takes the modules from, and
// one of no workspace that requires none download nothing, and neither does
// a package of no module.
func (b goBuild) Vendor(ctx context.Context, c *build.Context, dir string) error {
	_, err := c.LookPath(goTool)
	if err != nil {
		return fmt.Errorf("%w: the %s backend runs %q, which is not in PATH",
			build.ErrNoTool, Name, goTool)
	}

	mod, err := findModule(ctx, c)
	if err != nil || mod.goMod == "" || mod.vendored() {
		return err
	}

	required, err := requiresDownloads(ctx, c, mod.goMod)
	if err != nil {
		return err
	}

	switch {
	case !required && mod.goWork == "":
		return nil
	case required && !mod.pinned():
		return fmt.Errorf("%w in %s: its go.mod requires modules, and the module pins nothing "+
			"to download; run go mod tidy there, and keep %s with the sources",
			ErrNoGoSum, filepath.Dir(mod.goMod), goSumName)
	}

	scratch, err := os.MkdirTemp("", "tt-go-vendor-")
	if err != nil {
		return fmt.Errorf("creating the directory of the download: %w", err)
	}

	defer func() { _ = removeModuleCache(scratch) }()

	args := []string{"mod", "download"}

	// The workspace has no -modfile: go records what it lacks in go.work.sum,
	// as a build of the workspace does.
	if mod.goWork == "" {
		modfile, err := copyModFiles(mod.goMod, filepath.Join(scratch, "mod"))
		if err != nil {
			return err
		}

		args = append(args, "-modfile="+modfile)
	}

	cache := filepath.Join(scratch, "cache")
	cmd := c.Command(ctx, goTool, args...)

	cmd.Env = append(cmd.Env, downloadEnv(mod, cache)...)

	err = c.Run(cmd)
	if err != nil {
		return err
	}

	return mergeDownloads(filepath.Join(cache, filepath.FromSlash(downloadDir)), dir)
}

// goModule is the module go builds the package of the working directory in,
// as go env names it.
type goModule struct {
	// goMod is the go.mod of the module; empty for a package of no module.
	goMod string
	// goWork is the go.work of the workspace of the module; empty for a
	// module of none.
	goWork string
	// modCache is the module cache of the user, GOMODCACHE.
	modCache string
	// proxy is the GOPROXY of the user, its default when it sets none.
	proxy string
}

// findModule asks go env for the module of the working directory, and for the
// module cache and the GOPROXY of the environment of the build.
func findModule(ctx context.Context, c *build.Context) (goModule, error) {
	out, err := c.Output(c.Command(ctx, goTool, "env", "-json", "GOMOD", "GOWORK", "GOMODCACHE",
		"GOPROXY"))
	if err != nil {
		return goModule{}, fmt.Errorf("finding the go module: %w", err)
	}

	var env struct {
		GOMOD      string `json:"GOMOD"`
		GOWORK     string `json:"GOWORK"`
		GOMODCACHE string `json:"GOMODCACHE"`
		GOPROXY    string `json:"GOPROXY"`
	}

	err = json.Unmarshal(out, &env)
	if err != nil {
		return goModule{}, fmt.Errorf("reading go env: %w", err)
	}

	mod := goModule{goMod: env.GOMOD, goWork: env.GOWORK, modCache: env.GOMODCACHE,
		proxy: env.GOPROXY}

	// go env names os.DevNull for a package of no module in module mode,
	// and GOWORK=off off.
	if mod.goMod == os.DevNull {
		mod.goMod = ""
	}

	if mod.goWork == "off" {
		mod.goWork = ""
	}

	return mod, nil
}

// root is the directory of the workspace of the module, or of the module
// outside a workspace.
func (m goModule) root() string {
	if m.goWork != "" {
		return filepath.Dir(m.goWork)
	}

	return filepath.Dir(m.goMod)
}

// vendored reports whether the module, or its workspace, has a vendor
// directory, which go build takes the modules from.
func (m goModule) vendored() bool {
	return fileExists(filepath.Join(m.root(), filepath.FromSlash(vendorManifest)))
}

// pinned reports whether the module has the hashes of what it requires: its
// go.sum, or the go.work.sum of its workspace.
func (m goModule) pinned() bool {
	if fileExists(filepath.Join(filepath.Dir(m.goMod), goSumName)) {
		return true
	}

	return m.goWork != "" && fileExists(filepath.Join(filepath.Dir(m.goWork), goWorkSumName))
}

// fileExists reports whether path is a file.
func fileExists(path string) bool {
	info, err := os.Stat(path)

	return err == nil && info.Mode().IsRegular()
}

// requiresDownloads reports whether the go.mod at goMod requires a module that
// is downloaded: one it does not replace by a directory, as go mod edit -json
// reads it.
func requiresDownloads(ctx context.Context, c *build.Context, goMod string) (bool, error) {
	out, err := c.Output(c.Command(ctx, goTool, "mod", "edit", "-json", goMod))
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", goMod, err)
	}

	var file struct {
		Require []moduleVersion `json:"Require"`
		Replace []moduleReplace `json:"Replace"`
	}

	err = json.Unmarshal(out, &file)
	if err != nil {
		return false, fmt.Errorf("reading %s: %w", goMod, err)
	}

	for _, req := range file.Require {
		local := slices.ContainsFunc(file.Replace, func(replace moduleReplace) bool {
			return replace.Old.Path == req.Path &&
				(replace.Old.Version == "" || replace.Old.Version == req.Version) &&
				replace.New.Version == ""
		})
		if !local {
			return true, nil
		}
	}

	return false, nil
}

// moduleVersion is a module of go mod edit -json: a version of it, or none
// for every version, or for a directory.
type moduleVersion struct {
	Path    string `json:"Path"`
	Version string `json:"Version"`
}

// moduleReplace is a replace of go mod edit -json: New of no version is a
// directory.
type moduleReplace struct {
	Old moduleVersion `json:"Old"`
	New moduleVersion `json:"New"`
}

// copyModFiles copies the go.mod at goMod and the go.sum beside it, when it
// has one, into dir, and returns the copy of go.mod: go mod download given
// it as -modfile writes what it adds to the hashes into the copy of go.sum,
// not into the sources.
func copyModFiles(goMod, dir string) (string, error) {
	modfile := filepath.Join(dir, "go.mod")

	err := build.CopyFile(modfile, goMod)
	if err != nil {
		return "", fmt.Errorf("copying %s: %w", goMod, err)
	}

	sum := filepath.Join(filepath.Dir(goMod), goSumName)

	if fileExists(sum) {
		err = build.CopyFile(filepath.Join(dir, goSumName), sum)
		if err != nil {
			return "", fmt.Errorf("copying %s: %w", sum, err)
		}
	}

	return modfile, nil
}

// downloadEnv is the environment go mod download of Vendor runs with over the
// environment of the build: the module cache cache, of its own, so that it
// holds what the build of the module needs and nothing else, and the module
// cache of the user before GOPROXY, as a module proxy, so that what the user
// downloaded is taken from there rather than downloaded again. A file that
// is not there - go build keeps no .info of a version it was given - is
// taken from GOPROXY.
func downloadEnv(mod goModule, cache string) []string {
	env := []string{"GOMODCACHE=" + cache}

	if mod.modCache != "" && mod.proxy != "" {
		env = append(env, "GOPROXY="+fileURL(filepath.Join(mod.modCache,
			filepath.FromSlash(downloadDir)))+","+mod.proxy)
	}

	return env
}

// mergeDownloads copies the modules of download, the cache/download of a
// module cache, into dir, which may not exist, keeping the files that are
// there: <module>/@v/<version> and an extension of mirrorFileExts. A
// toolchain go downloaded and what it read of the checksum database are left
// out. A file is copied beside its place and renamed into it, so that a
// mirror holds no half of one.
func mergeDownloads(download, dir string) error {
	err := filepath.WalkDir(download, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) && path == download {
				return nil
			}

			return err
		}

		rel, err := filepath.Rel(download, path)
		if err != nil {
			return fmt.Errorf("copying %s: %w", path, err)
		}

		slashed := filepath.ToSlash(rel)

		if entry.IsDir() {
			if slashed == sumdbDir || slashed == toolchainModule {
				return fs.SkipDir
			}

			return nil
		}

		if !entry.Type().IsRegular() || !slices.Contains(mirrorFileExts, filepath.Ext(rel)) ||
			filepath.Base(filepath.Dir(rel)) != "@v" {
			return nil
		}

		return copyIntoMirror(path, filepath.Join(dir, rel))
	})
	if err != nil {
		return fmt.Errorf("copying the downloaded modules: %w", err)
	}

	return nil
}

// copyIntoMirror copies the file src to dst unless dst is there, through a
// file beside dst renamed into it.
func copyIntoMirror(src, dst string) error {
	_, err := os.Lstat(dst)
	if err == nil {
		return nil
	}

	err = os.MkdirAll(filepath.Dir(dst), mirrorDirPerm)
	if err != nil {
		return fmt.Errorf("creating the directory of %s: %w", dst, err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}

	_ = tmp.Close()

	err = build.CopyFile(tmp.Name(), src)
	if err == nil {
		err = os.Chmod(tmp.Name(), mirrorFilePerm)
	}

	if err == nil {
		err = os.Rename(tmp.Name(), dst)
	}

	if err != nil {
		_ = os.Remove(tmp.Name())

		return fmt.Errorf("copying %s into the mirror: %w", src, err)
	}

	return nil
}

// removeModuleCache removes the module cache dir, whose directories go makes
// read-only.
func removeModuleCache(dir string) error {
	err := os.RemoveAll(dir)
	if err == nil {
		return nil
	}

	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err == nil && entry.IsDir() {
			// The owner's own, to remove what is in it.
			_ = os.Chmod(path, 0o700) //nolint:gosec,mnd // A directory, searched.
		}

		return nil
	})

	err = os.RemoveAll(dir)
	if err != nil {
		return fmt.Errorf("removing the module cache of the download: %w", err)
	}

	return nil
}

// usesMirror reports whether the build takes the modules from the mirror of
// its rock servers (build.Context.Vendored): when it has one, and the package
// is of a module with no vendor directory, which go build takes them from
// rather than download any.
func usesMirror(ctx context.Context, c *build.Context) (bool, error) {
	if c.Vendored == "" {
		return false, nil
	}

	mod, err := findModule(ctx, c)
	if err != nil {
		return false, err
	}

	return mod.goMod != "" && !mod.vendored(), nil
}

// mirrorEnv is the environment of go build of a build from the modules of
// the mirror dir, which Vendor downloaded: dir is the module proxy, of no
// fallback, the checksum database is off - every module is still checked
// against go.sum - and no module is private, so that none is downloaded
// from its repository around the proxy.
func mirrorEnv(dir string) []string {
	return []string{
		"GOPROXY=" + fileURL(dir),
		"GOSUMDB=off",
		"GONOPROXY=" + noPrivateModules,
	}
}

// fileURL is the file:// URL of the directory dir, as GOPROXY takes it: each
// element escaped, a comma and a bar among them, which separate the proxies
// of GOPROXY.
func fileURL(dir string) string {
	abs, err := filepath.Abs(dir)
	if err == nil {
		dir = abs
	}

	elements := strings.Split(filepath.ToSlash(dir), "/")
	for i, element := range elements {
		elements[i] = url.PathEscape(element)
	}

	return "file://" + strings.Join(elements, "/")
}

// explainVendored adds to the failure err of a build from the modules of a
// mirror where they were taken from, and what fills the mirror: a module the
// go.sum of the module pins that the mirror lacks fails the build, and so
// does a toolchain go.mod asks for that go does not have, which no mirror
// has.
func explainVendored(err error, c *build.Context) error {
	return fmt.Errorf("%w; the build takes the modules from %s, offline, which tt package "+
		"download fills from the %s of the module: run it again after a change of %s, and "+
		"install the Go toolchain a go or toolchain line of go.mod asks for, which is not "+
		"in the mirror", err, c.Vendored, goSumName, goSumName)
}

// targetEnv is what selects the target of go build. It comes after the env
// of the block, as the TT_ variables do, so that the block does not build
// for another platform than the one the build checks the program against;
// CC and the rest of it still reach the compiler.
func targetEnv(config Config, target build.Target) []string {
	return []string{
		"GOOS=" + target.OS, "GOARCH=" + target.Arch, "CGO_ENABLED=" + cgoEnabled(config),
	}
}

// cgoEnabled is CGO_ENABLED of a build of config: 1 with cgo = true, 0
// otherwise.
func cgoEnabled(config Config) string {
	if config.Cgo {
		return "1"
	}

	return "0"
}

// goArgs builds the go build argv of the program output.
//
// Every entry of ldflags is one argument of the linker, as every entry of
// args is one of a shell command: "-X", "main.version=1.0" is two. go build
// takes them as one -ldflags string it splits on spaces, so an entry with a
// space or a quote is quoted for it.
//
// A static cgo program gets its tags and linker flags first, so that the
// block's own come after them and may override them; the build checks the
// program it gets whatever the flags were.
//
// jobs, the Jobs of the build (build.Context.Jobs), is -p, the number of
// the compiles and links go build runs at once, when they are set; without
// them go build runs one a CPU, GOMAXPROCS, its own default. -p says how
// fast the program is built, not what it is: it is of no key of the build
// cache of go.
//
// options are other flags of go build, put before the package.
func goArgs(config Config, output string, static bool, jobs int, options []string) (
	[]string, error,
) {
	args := []string{"build", "-trimpath", "-o", output}

	if jobs > 0 {
		args = append(args, "-p", strconv.Itoa(jobs))
	}

	var tags, ldflags []string

	if config.Cgo && static {
		tags = append(tags, staticTags...)
		ldflags = append(ldflags, staticLdflags...)
	}

	tags = append(tags, config.Tags...)
	ldflags = append(ldflags, config.Ldflags...)

	if len(tags) > 0 {
		args = append(args, "-tags", strings.Join(tags, ","))
	}

	if len(ldflags) > 0 {
		quoted, err := QuoteFlags(ldflags)
		if err != nil {
			return nil, err
		}

		args = append(args, "-ldflags", quoted)
	}

	pkg := config.Package
	if pkg == "" {
		pkg = defaultPackage
	}

	args = append(args, options...)

	return append(args, pkg), nil
}

// QuoteFlags joins flags into the string go build splits back into them: on
// spaces, with single or double quotes around a field that has a space, and
// no escapes. A flag with a space and both kinds of quotes cannot be passed.
func QuoteFlags(flags []string) (string, error) {
	quoted := make([]string, 0, len(flags))

	for _, flag := range flags {
		switch {
		case !strings.ContainsAny(flag, " \t\n\r'\""):
			quoted = append(quoted, flag)
		case !strings.Contains(flag, "'"):
			quoted = append(quoted, "'"+flag+"'")
		case !strings.Contains(flag, `"`):
			quoted = append(quoted, `"`+flag+`"`)
		default:
			return "", fmt.Errorf("%w: %q", errUnquotableFlag, flag)
		}
	}

	return strings.Join(quoted, " "), nil
}
