package distbuild

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk"
	"github.com/tarantool/tt/sdk/log"
	"github.com/tarantool/tt/v3/internal/buildflags"
)

// The files of a bundle besides the main module.
const (
	// bundleEnv holds the build parameters of a bundle, as shell variables.
	bundleEnv = "build.env"
	// bundleScript builds a bundle offline.
	bundleScript = "build.sh"
	// bundleLocal holds the local modules of a bundle.
	bundleLocal = "local"
	// scriptPerm is the mode of build.sh.
	scriptPerm = 0o755
)

// bundleFiles are what a bundle consists of: an earlier bundle in the
// directory is replaced, anything else in it is refused.
var bundleFiles = []string{
	"go.mod", "go.sum", "main.go", "vendor", bundleLocal, bundleEnv, bundleScript,
}

// vcsDirs are the directories of version control systems, which no module
// copy carries.
var vcsDirs = []string{".git", ".hg", ".svn", ".bzr"}

// errNotBundle reports a --vendor directory that holds something other than
// a bundle.
var errNotBundle = errors.New("is neither empty nor a bundle tt made")

// prepareBundle makes dir the empty directory of a new bundle: it creates
// it, or clears an earlier bundle out of it.
func prepareBundle(dir string) (string, error) {
	abs, err := filepath.Abs(dir)
	if err != nil {
		return "", sdk.Errorf(sdk.ExitFailure, "--vendor %s: %w", dir, err)
	}

	entries, err := os.ReadDir(abs)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return "", sdk.Errorf(sdk.ExitSystem, "--vendor %s: %w", dir, err)
	}

	if len(entries) > 0 {
		_, envErr := os.Stat(filepath.Join(abs, bundleEnv))
		if envErr != nil {
			return "", sdk.Errorf(sdk.ExitFailure, "--vendor %s: %w", dir, errNotBundle)
		}

		for _, entry := range entries {
			if !slices.Contains(bundleFiles, entry.Name()) {
				return "", sdk.Errorf(sdk.ExitFailure, "--vendor %s: %w: %s",
					dir, errNotBundle, entry.Name())
			}
		}

		for _, entry := range entries {
			err = os.RemoveAll(filepath.Join(abs, entry.Name()))
			if err != nil {
				return "", sdk.Errorf(sdk.ExitSystem, "--vendor %s: %w", dir, err)
			}
		}
	}

	err = os.MkdirAll(abs, dirPerm)
	if err != nil {
		return "", sdk.Errorf(sdk.ExitSystem, "--vendor %s: %w", dir, err)
	}

	return abs, nil
}

// localize copies the local modules - a local core with its repository's
// modules, the local tt modules - into the bundle and returns the core and
// the modules with their directories relative to it, so that neither the
// go.mod of the bundle nor its vendor/modules.txt names a directory of the
// machine that made it. The copies are the modules as the go command would
// zip them: no VCS directories, no nested modules, no vendor directories,
// regular files only.
func (b builder) localize(resolved core, modules []module) (core, []module, error) {
	local := filepath.Join(b.work, bundleLocal)

	if resolved.Dir != "" {
		coreDir := resolved.Dir

		dst := filepath.Join(local, "core")

		err := b.copyModule(coreDir, dst)
		if err != nil {
			return core{}, nil, err
		}

		resolved.Dir = bundlePath(b.work, dst)
		resolved.Repository = slices.Clone(resolved.Repository)

		for idx, repo := range resolved.Repository {
			dst := filepath.Join(local, "core-external", filepath.FromSlash(repo.Module))

			rel, relErr := filepath.Rel(coreDir, repo.Dir)
			if relErr == nil && filepath.IsLocal(rel) {
				dst = filepath.Join(local, "core", rel)
			}

			err = b.copyModule(repo.Dir, dst)
			if err != nil {
				return core{}, nil, err
			}

			resolved.Repository[idx].Dir = bundlePath(b.work, dst)
		}
	}

	modules = slices.Clone(modules)

	for idx, mod := range modules {
		if mod.Dir == "" {
			continue
		}

		dst := filepath.Join(local, "modules", mod.Name)

		err := b.copyModule(mod.Dir, dst)
		if err != nil {
			return core{}, nil, err
		}

		modules[idx].Dir = bundlePath(b.work, dst)
	}

	return resolved, modules, nil
}

// copyModule copies the module in src to dst.
func (b builder) copyModule(src, dst string) error {
	err := filepath.WalkDir(src, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		if entry.IsDir() {
			return b.copyDir(src, dst, path, entry)
		}

		if !entry.Type().IsRegular() {
			return nil
		}

		rel, err := filepath.Rel(src, path)
		if err != nil {
			return fmt.Errorf("copying %s: %w", path, err)
		}

		return copyFile(path, filepath.Join(dst, rel))
	})
	if err != nil {
		return sdk.Errorf(sdk.ExitSystem, "copying %s into the bundle: %w", src, err)
	}

	return nil
}

// copyDir creates the copy of the directory path of the module in src under
// dst, or skips it as skipModuleDir says.
func (b builder) copyDir(src, dst, path string, entry fs.DirEntry) error {
	if skipModuleDir(src, path, entry, b.work) {
		return filepath.SkipDir
	}

	rel, err := filepath.Rel(src, path)
	if err != nil {
		return fmt.Errorf("copying %s: %w", path, err)
	}

	err = os.MkdirAll(filepath.Join(dst, rel), dirPerm)
	if err != nil {
		return fmt.Errorf("copying %s: %w", path, err)
	}

	return nil
}

// skipModuleDir reports whether the directory path met walking the module
// in root holds none of its files: a VCS directory, a vendor directory, a
// nested module - a directory with a go.mod of its own - or work, the
// directory of the build. These are the directories the go command leaves
// out of a module zip, and work is not the module's at all.
func skipModuleDir(root, path string, entry fs.DirEntry, work string) bool {
	if path == root {
		return false
	}

	if slices.Contains(vcsDirs, entry.Name()) || entry.Name() == "vendor" || path == work {
		return true
	}

	_, err := os.Stat(filepath.Join(path, "go.mod"))

	return err == nil
}

// copyFile copies the regular file src to dst with its mode.
func copyFile(src, dst string) error {
	info, err := os.Stat(src)
	if err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}

	source, err := os.Open(src)
	if err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}

	defer func() { _ = source.Close() }()

	out, err := os.OpenFile(dst, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, info.Mode().Perm())
	if err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}

	_, copyErr := io.Copy(out, source)

	err = errors.Join(copyErr, out.Close())
	if err != nil {
		return fmt.Errorf("copying %s: %w", src, err)
	}

	return nil
}

// bundlePath is the path to dir, a directory of the bundle in work, as a
// replace directive names it.
func bundlePath(work, dir string) string {
	rel, err := filepath.Rel(work, dir)
	if err != nil {
		return dir
	}

	return "./" + filepath.ToSlash(rel)
}

// bundle vendors the main module, writes the build parameters and the
// script that builds the bundle, and builds it once offline to check it.
func (b builder) bundle(ctx context.Context, env []string, version versionInfo) (string, error) {
	log.Infof("Vendoring %s into %s", b.dist.Name(), b.work)

	err := b.opts.gocmd.Run(ctx, b.work, env, "mod", "vendor")
	if err != nil {
		return "", err
	}

	toolchain, err := b.opts.gocmd.Output(ctx, b.work, env, "env", "GOVERSION")
	if err != nil {
		return "", err
	}

	params, err := b.bundleParams(string(bytes.TrimSpace(toolchain)), version)
	if err != nil {
		return "", err
	}

	err = os.WriteFile(filepath.Join(b.work, bundleEnv), params, filePerm)
	if err != nil {
		return "", sdk.Errorf(sdk.ExitSystem, "writing %s: %w", bundleEnv, err)
	}

	err = os.WriteFile(filepath.Join(b.work, bundleScript), []byte(buildScript), scriptPerm)
	if err != nil {
		return "", sdk.Errorf(sdk.ExitSystem, "writing %s: %w", bundleScript, err)
	}

	err = b.checkBundle(ctx)
	if err != nil {
		return "", err
	}

	return b.work, nil
}

// bundleParams are the build parameters of the bundle as shell variables:
// the tags, the linker flags and cgo of each TLS implementation, the
// implementation the bundle was made with, the toolchain and the version.
func (b builder) bundleParams(toolchain string, version versionInfo) ([]byte, error) {
	var buf bytes.Buffer

	buf.WriteString("# Generated by tt binary build tt --vendor. DO NOT EDIT.\n" +
		"# The build parameters of this bundle; build.sh reads them.\n\n")

	vars := [][2]string{
		{"TT_DIST_NAME", b.dist.Name()},
		{"TT_DIST_MANIFEST_HASH", b.dist.Hash()},
		{"TT_DIST_VERSION", version.Tag},
		{"TT_DIST_COMMIT", version.Commit},
		{"TT_DIST_TOOLCHAIN", toolchain},
		{"TT_DIST_SSL", string(b.ssl)},
	}

	for _, ssl := range []buildflags.SSL{
		buildflags.Default, buildflags.OpenSSL, buildflags.OpenSSLStatic,
	} {
		suffix := strings.ToUpper(string(ssl))

		tags, ldflags, err := b.flags(ssl, "linux")
		if err != nil {
			return nil, err
		}

		_, darwinLdflags, err := b.flags(ssl, "darwin")
		if err != nil {
			return nil, err
		}

		cgo := "0"
		if b.cgo(ssl) {
			cgo = "1"
		}

		vars = append(vars,
			[2]string{"TT_DIST_TAGS_" + suffix, tags},
			[2]string{"TT_DIST_LDFLAGS_" + suffix, ldflags},
			[2]string{"TT_DIST_LDFLAGS_DARWIN_" + suffix, darwinLdflags},
			[2]string{"TT_DIST_CGO_" + suffix, cgo},
		)
	}

	for _, v := range vars {
		buf.WriteString(v[0] + "=" + shellQuote(v[1]) + "\n")
	}

	return buf.Bytes(), nil
}

// checkBundle builds the bundle as build.sh does - from vendor/ alone, with
// the network off - and runs the binary's tt version.
func (b builder) checkBundle(ctx context.Context) error {
	log.Infof("Checking that the bundle builds offline")

	tmp, err := os.MkdirTemp("", "tt-bundle-check-")
	if err != nil {
		return sdk.Errorf(sdk.ExitSystem, "checking the bundle: %w", err)
	}

	defer func() { _ = os.RemoveAll(tmp) }()

	binary := filepath.Join(tmp, b.dist.Name())

	args, err := b.buildArgs(b.ssl, runtime.GOOS, binary)
	if err != nil {
		return err
	}

	env := append(b.goEnv(b.ssl), "GOFLAGS=-mod=vendor", "GOPROXY=off")

	err = b.opts.gocmd.Run(ctx, b.work, env, args...)
	if err != nil {
		return fmt.Errorf("the bundle does not build offline: %w", err)
	}

	return smoke(ctx, binary, tmp, env)
}

// shellQuote quotes value for a POSIX shell.
func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'\''`) + "'"
}

// buildScript builds a bundle offline.
var buildScript = `#!/bin/sh
# Generated by tt binary build tt --vendor. DO NOT EDIT.
#
# Builds this bundle into a tt binary with the Go toolchain alone: no
# network, no module cache, no tt.
#
#   sh build.sh [default|openssl|openssl_static] [OUTPUT]
#
# The TLS implementation defaults to the one the bundle was made with, the
# output to the name of the distribution in the working directory. The
# OpenSSL implementations need a C compiler and the OpenSSL headers.
# GOTOOLCHAIN defaults to local: the go in PATH must be at least the version
# go.mod names.
set -eu

bundle=$(cd "$(dirname "$0")" && pwd)
. "$bundle/build.env"

ssl=${1:-$TT_DIST_SSL}
output=${2:-$TT_DIST_NAME}

case $ssl in
default)
	tags=$TT_DIST_TAGS_DEFAULT ldflags=$TT_DIST_LDFLAGS_DEFAULT
	darwin=$TT_DIST_LDFLAGS_DARWIN_DEFAULT cgo=$TT_DIST_CGO_DEFAULT ;;
openssl)
	tags=$TT_DIST_TAGS_OPENSSL ldflags=$TT_DIST_LDFLAGS_OPENSSL
	darwin=$TT_DIST_LDFLAGS_DARWIN_OPENSSL cgo=$TT_DIST_CGO_OPENSSL ;;
openssl_static)
	tags=$TT_DIST_TAGS_OPENSSL_STATIC ldflags=$TT_DIST_LDFLAGS_OPENSSL_STATIC
	darwin=$TT_DIST_LDFLAGS_DARWIN_OPENSSL_STATIC cgo=$TT_DIST_CGO_OPENSSL_STATIC ;;
*)
	echo "build.sh: unknown TLS implementation '$ssl': want default, openssl or openssl_static" >&2
	exit 1 ;;
esac

if [ "$(uname -s)" = Darwin ]; then
	ldflags=$darwin
fi

case $output in
/*) ;;
*) output=$(pwd)/${output#./} ;;
esac

cd "$bundle"
GOFLAGS=-mod=vendor GOPROXY=off GOWORK=off GOTOOLCHAIN=${GOTOOLCHAIN:-local} \
CGO_ENABLED=$cgo \
	go build -trimpath -tags "$tags" -ldflags "$ldflags" -o "$output" .
echo "$output"
`
