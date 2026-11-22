package cargo

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"

	"github.com/tarantool/tt/sdk/build/v3"
)

// This file is the build of cargo for a platform other than the machine tt
// runs on (build.Context.Target): rustc compiles for any target whose
// standard library rustup installed, but the C toolchain of the machine
// links for the machine alone. A build for another Linux platform links with
// zig cc for the platform, as cargo-zigbuild does, which has the C library of
// every Linux target in itself; a build for macOS is made on macOS, whose SDK
// cargo links against.

const (
	// zigTool is the program a build for another Linux platform links with.
	zigTool = "zig"
	// rustcTool is the compiler cargo runs, which says where the standard
	// library of a target is.
	rustcTool = "rustc"
	// rustcEnv names the rustc cargo runs instead.
	rustcEnv = "RUSTC"
	// crossDirName is the directory of the state of the build of the project
	// the scripts of zig of the builds for other platforms go into: one a
	// target, which every component of the project shares.
	crossDirName = "cargo-zig"
	// linuxOS is the GOOS spelling of Linux.
	linuxOS = "linux"
	// scriptPerm is the mode of a script of zig.
	scriptPerm fs.FileMode = 0o755
	// crossDirPerm is the mode of the directory of the scripts.
	crossDirPerm fs.FileMode = 0o750
)

var (
	// ErrCrossDarwin reports a build for macOS on another system: cargo links
	// a library or a program of macOS against the SDK of macOS. It is a
	// build that cannot be made for its target, build.ErrTarget.
	ErrCrossDarwin = fmt.Errorf("%w: a build for darwin is made on macOS", build.ErrTarget)
	// ErrNoRustTarget reports a Rust target whose standard library the
	// toolchain has not installed.
	ErrNoRustTarget = errors.New("the Rust target is not installed")
)

// checkTargets checks what the build for the platform of c needs besides
// cargo: the standard library of every Rust target the build names
// (checkRustTarget), and, for a platform other than the machine tt runs on,
// zig in the PATH of the build for one of Linux; one of macOS on another
// system is refused.
func (b cargoBuild) checkTargets(ctx context.Context, c *build.Context) error {
	if c.Target.Cross() {
		where := fmt.Sprintf("a build for %s on %s", c.Target, build.HostTarget())

		switch c.Target.OS {
		case darwinOS:
			if runtime.GOOS != darwinOS {
				return fmt.Errorf("%w: %s cannot link against the SDK of macOS; build it "+
					"on macOS", ErrCrossDarwin, where)
			}
		case linuxOS:
			_, err := c.LookPath(zigTool)
			if err != nil {
				return fmt.Errorf("%w: %s links with zig cc, and %q is not in PATH",
					build.ErrNoTool, where, zigTool)
			}
		}
	}

	for _, triple := range b.triples(c.Target) {
		err := checkRustTarget(ctx, c, triple)
		if err != nil {
			return err
		}
	}

	return nil
}

// triples are the Rust targets the build names for platform, the --target
// of the module and of the program, each once: none for a build cargo makes
// for its host.
func (b cargoBuild) triples(platform build.Target) []string {
	var triples []string

	add := func(program bool) {
		triple, err := b.target(platform, program)
		if err == nil && triple != "" && !slices.Contains(triples, triple) {
			triples = append(triples, triple)
		}
	}

	if b.module {
		add(false)
	}

	if b.bin != "" {
		add(true)
	}

	return triples
}

// checkRustTarget checks that the toolchain of the build has the standard
// library of triple: rustc --print target-libdir --target <triple> names
// its directory, whichever is installed, and rustup target add makes it.
// rustc is that of RUSTC of the environment of the build, else the one of
// its PATH, run in the working directory, as cargo runs it: a toolchain
// rust-toolchain.toml selects is asked. A rustc that is not there is not
// asked: cargo says so.
func checkRustTarget(ctx context.Context, c *build.Context, triple string) error {
	rustc, _ := build.LookupEnv(command(ctx, c).Env, rustcEnv)
	if rustc == "" {
		rustc = rustcTool
	}

	_, err := c.LookPath(rustc)
	if err != nil {
		return nil //nolint:nilerr // cargo reports a rustc it cannot run.
	}

	out, err := c.Output(c.Command(ctx, rustc, "--print", "target-libdir", "--target", triple))
	if err != nil {
		return fmt.Errorf("asking rustc of the Rust target %s: %w", triple, err)
	}

	libdir := strings.TrimSpace(string(out))

	info, err := os.Stat(libdir)
	if err != nil || !info.IsDir() {
		return fmt.Errorf("%w: %s, no %s; rustup target add %s installs it", ErrNoRustTarget,
			triple, libdir, triple)
	}

	return nil
}

// zigTriple is the target of zig cc of the Rust target triple of Linux,
// <arch>-unknown-linux-<abi>: <arch>-linux-<abi>.
func zigTriple(triple string) string {
	return strings.Replace(triple, "-unknown-linux-", "-linux-", 1)
}

// envKey is triple as cargo and the cc crate spell it in the name of a
// variable: upper case for cargo, CARGO_TARGET_AARCH64_UNKNOWN_LINUX_GNU_,
// lower case for the cc crate, CC_aarch64_unknown_linux_gnu.
func envKey(triple string, upper bool) string {
	key := strings.ReplaceAll(triple, "-", "_")
	if upper {
		return strings.ToUpper(key)
	}

	return key
}

// crossArgs returns the options and the environment of the cargo command of
// a build for the Rust target triple of a Linux platform other than the
// machine tt runs on: target.<triple>.linker, through --config, the script
// of zig cc for the target (writeScripts), and CC_<triple> and AR_<triple>
// the scripts of zig cc and zig ar, for the build scripts that compile C
// with the cc crate. A linker, a C compiler or an archiver the environment
// of the build names for the target is kept: CARGO_TARGET_<TRIPLE>_LINKER,
// CC_<triple> or TARGET_CC, AR_<triple> or TARGET_AR. None for a build for
// the machine, or for macOS.
func crossArgs(ctx context.Context, c *build.Context, triple string) ([]string, []string,
	error,
) {
	if !c.Target.Cross() || c.Target.OS != linuxOS || triple == "" {
		return nil, nil, nil
	}

	zig, err := c.LookPath(zigTool)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %q is not in PATH", build.ErrNoTool, zigTool)
	}

	if !filepath.IsAbs(zig) {
		zig = filepath.Join(c.Dirs.Cwd, zig)
	}

	compiler, archiver, err := writeScripts(filepath.Join(c.Dirs.State, crossDirName), zig,
		triple)
	if err != nil {
		return nil, nil, err
	}

	environ := command(ctx, c).Env
	set := func(keys ...string) bool {
		return slices.ContainsFunc(keys, func(key string) bool {
			value, _ := build.LookupEnv(environ, key)

			return value != ""
		})
	}

	var options, env []string

	if !set("CARGO_TARGET_" + envKey(triple, true) + "_LINKER") {
		options = append(options, "--config",
			"target."+triple+".linker="+tomlString(compiler))
	}

	if !set("CC_"+triple, "CC_"+envKey(triple, false), "TARGET_CC") {
		env = append(env, "CC_"+envKey(triple, false)+"="+compiler)
	}

	if !set("AR_"+triple, "AR_"+envKey(triple, false), "TARGET_AR") {
		env = append(env, "AR_"+envKey(triple, false)+"="+archiver)
	}

	return options, env, nil
}

// writeScripts writes into dir the scripts of zig of the Rust target triple,
// <zig triple>-cc and <zig triple>-ar, and returns their paths: zig cc and
// zig ar of the zig at the path zig, for the target. The script of cc leaves
// out the arguments of rustc and of the cc crate zig does not take, as
// cargo-zigbuild does: the --target of a clang, which -target says, the fix
// of a Cortex-A53 erratum only the linkers of GNU know, -O1 of the linker,
// which zig warns of, and, for musl, the start files of the C library rustc
// brings, which zig links its own of. A script that is there as it would be
// written is left alone.
func writeScripts(dir, zig, triple string) (string, string, error) {
	target := zigTriple(triple)

	skip := "--target=*|-Wl,--fix-cortex-a53-843419|-Wl,-O1"
	if strings.HasSuffix(triple, "-musl") {
		skip += "|*/self-contained/*.o"
	}

	compiler := fmt.Sprintf(`#!/bin/sh
# zig cc for %[1]s, the linker and the C compiler of the cargo builds for
# %[2]s, which tt writes: what rustc and the cc crate pass that zig does
# not take is left out.
for arg do
	shift
	case $arg in
	%[3]s) continue ;;
	esac
	set -- "$@" "$arg"
done
exec %[4]s cc -target %[1]s "$@"
`, target, triple, skip, shellQuote(zig))

	archiver := fmt.Sprintf(`#!/bin/sh
# zig ar, the archiver of the cargo builds for %[1]s, which tt writes.
exec %[2]s ar "$@"
`, triple, shellQuote(zig))

	paths := []string{filepath.Join(dir, target+"-cc"), filepath.Join(dir, target+"-ar")}

	for i, script := range []string{compiler, archiver} {
		err := writeScript(paths[i], script)
		if err != nil {
			return "", "", err
		}
	}

	return paths[0], paths[1], nil
}

// writeScript writes the script content at path, executable, unless the
// file is there with it. It is written aside and copied into place with no
// fork in between (build.CopyFile), so that cargo is not refused to run it.
func writeScript(path, content string) error {
	data, err := os.ReadFile(path)
	if err == nil && bytes.Equal(data, []byte(content)) {
		return nil
	}

	err = os.MkdirAll(filepath.Dir(path), crossDirPerm)
	if err != nil {
		return fmt.Errorf("creating the directory of %s: %w", path, err)
	}

	aside, err := os.CreateTemp(filepath.Dir(path), ".script-*")
	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	defer func() { _ = os.Remove(aside.Name()) }()

	_, err = aside.WriteString(content)
	if err == nil {
		err = aside.Chmod(scriptPerm)
	}

	err = errors.Join(err, aside.Close())
	if err == nil {
		err = build.CopyFile(path, aside.Name())
	}

	if err != nil {
		return fmt.Errorf("writing %s: %w", path, err)
	}

	return nil
}

// shellQuote quotes s for sh: in single quotes, a single quote of it closed
// out of them.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
