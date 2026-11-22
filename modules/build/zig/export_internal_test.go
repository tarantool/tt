package zig

import (
	"bytes"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/build"
)

// scriptRun is what a run of the script of an export made (runExport).
type scriptRun struct {
	// calls are the argv of the tools the script ran, in order.
	calls [][]string
	// dirs are the directories of the script by the variables that name
	// them: TT_BUILD_DIR, TT_OUTPUT_DIR, TT_SHARE_DIR, TT_PROGRAMS_DIR and
	// TT_LINK_DIR.
	dirs map[string]string
	// out is what the script printed.
	out string
	// err is how it failed; nil when it did not.
	err error
}

// logFunction is the function of sh the fakes of tools log their argv
// with: each argument ends with a NUL, and each call with a \001.
const logFunction = `tt_test_log() {
	printf '%s\000' "$@" >> "$TT_TEST_LOG"
	printf '\001' >> "$TT_TEST_LOG"
}
`

// runExport runs script as the script of a rock runs that of the export of
// a component: by sh with set -eu, in a directory of its own, with the
// variables of an export, each directory a new one, and env over them.
// prelude defines the tools the script runs as functions of sh, which log
// their argv by tt_test_log.
func runExport(t *testing.T, prelude, script string, env map[string]string) scriptRun {
	t.Helper()

	root := t.TempDir()
	run := scriptRun{calls: nil, dirs: map[string]string{}, out: "", err: nil}

	for _, key := range []string{
		"TT_BUILD_DIR", "TT_OUTPUT_DIR", "TT_SHARE_DIR", "TT_PROGRAMS_DIR", "TT_LINK_DIR",
	} {
		run.dirs[key] = filepath.Join(root, strings.ToLower(key))
		require.NoError(t, os.MkdirAll(run.dirs[key], 0o750))
	}

	cwd := filepath.Join(root, "cwd")
	require.NoError(t, os.MkdirAll(cwd, 0o750))

	file := filepath.Join(root, "export.sh")
	require.NoError(t, os.WriteFile(file,
		[]byte("set -eu\n"+logFunction+prelude+"\n"+script+"\n"), 0o600))

	vars := map[string]string{
		"TT_TEST_LOG": filepath.Join(root, "log"), "TT_MODULE_EXT": ".so", "TT_BUILD_JOBS": "3",
		"TT_PLATFORM_OS": "linux", "TT_PLATFORM_ARCH": "amd64",
	}
	maps.Copy(vars, run.dirs)
	maps.Copy(vars, env)

	cmd := exec.CommandContext(t.Context(), "sh", file)

	cmd.Dir = cwd
	cmd.Env = os.Environ()

	for _, key := range slices.Sorted(maps.Keys(vars)) {
		cmd.Env = append(cmd.Env, key+"="+vars[key])
	}

	out, err := cmd.CombinedOutput()

	run.out, run.err = string(out), err

	data, err := os.ReadFile(vars["TT_TEST_LOG"])
	if err != nil {
		return run
	}

	for call := range bytes.SplitSeq(bytes.TrimSuffix(data, []byte("\x01")), []byte("\x01")) {
		run.calls = append(run.calls,
			strings.Split(strings.TrimSuffix(string(call), "\x00"), "\x00"))
	}

	return run
}

// exportFakes are a zig of the version TT_TEST_ZIG_VERSION that logs its
// argv, prints TT_TEST_ZIG_HELP for zig build --help, and for zig build
// installs each of TT_TEST_INSTALLS, a path relative to the prefix of -p,
// its content its path.
const exportFakes = `zig() {
	tt_test_log zig "$@"
	case $1 in
	version) echo "$TT_TEST_ZIG_VERSION" ;;
	build)
		if [ "$2" = --help ]; then
			echo "$TT_TEST_ZIG_HELP"
			return 0
		fi
		prefix=
		while [ $# -gt 0 ]; do
			case $1 in
			-p) prefix=$2; shift ;;
			esac
			shift
		done
		for file in $TT_TEST_INSTALLS; do
			mkdir -p "$(dirname "$prefix/$file")"
			echo "$file" > "$prefix/$file"
		done
		;;
	esac
}
`

// TestExport checks the script of the export against Run: it runs the zig
// build args makes of the block, with the directories dirArgs has of the
// version of zig, no -j, and the musl target targetArg gives a static
// program alone, and copies the module and the program where copyModule and
// copyProgram do.
func TestExport(t *testing.T) {
	t.Parallel()

	help := "  -Dtarget=[string]            The CPU architecture, OS, and ABI to build for"

	for _, test := range []struct {
		name     string
		bld      zigBuild
		version  string
		help     string
		env      map[string]string
		installs string
		// musl is the target the script passes; empty for none.
		musl string
		// files are the files the script made, relative to the directories
		// of the variables, by the path in the prefix of each.
		files map[string]string
	}{
		{
			name: "a module and a program",
			bld: zigBuild{
				config: Config{Module: "kv", Bin: "kv-cli"}, module: "kv", bin: "kv-cli",
				static: false,
			},
			version: "0.17.1", installs: "lib/kv.so bin/kv-cli",
			files: map[string]string{
				"TT_OUTPUT_DIR/kv.so": "lib/kv.so", "TT_PROGRAMS_DIR/kv-cli": "bin/kv-cli",
			},
		},
		{
			name: "steps, a mode and options of zig 0.16",
			bld: zigBuild{
				config: Config{
					Module: "a.b", Steps: []string{"lib", "it's"}, Optimize: "ReleaseFast",
					Options: map[string]string{"word": "it's $HOME `echo no`", "a": "1"},
				},
				module: "a.b", bin: "", static: false,
			},
			version: "0.16.0", installs: "lib/a/libb.so",
			files: map[string]string{"TT_OUTPUT_DIR/a/b.so": "lib/a/libb.so"},
		},
		{
			name: "Debug, and the packages of the environment",
			bld: zigBuild{
				config: Config{Module: "kv", Optimize: "Debug"}, module: "kv", bin: "",
				static: false,
			},
			env:      map[string]string{pkgDirEnv: "/pkgs"},
			installs: "lib/libkv.so lib/kv.dylib",
			files:    map[string]string{"TT_OUTPUT_DIR/kv.so": "lib/kv.dylib"},
		},
		{
			name: "a module of macOS",
			bld: zigBuild{
				config: Config{Module: "kv"}, module: "kv", bin: "", static: false,
			},
			version: "0.17.1", env: map[string]string{"TT_MODULE_EXT": ".dylib"},
			installs: "lib/libkv.dylib",
			files:    map[string]string{"TT_OUTPUT_DIR/kv.dylib": "lib/libkv.dylib"},
		},
		{
			name: "a static program for musl",
			bld: zigBuild{
				config: Config{Bin: "cli"}, module: "", bin: "cli", static: true,
			},
			version: "0.17.1", help: help, installs: "bin/cli", musl: "x86_64-linux-musl",
			files: map[string]string{"TT_PROGRAMS_DIR/cli": "bin/cli"},
		},
		{
			name: "a static program of arm64 for musl",
			bld: zigBuild{
				config: Config{Bin: "cli"}, module: "", bin: "cli", static: true,
			},
			version: "0.17.1", help: help, installs: "bin/cli", musl: "aarch64-linux-musl",
			env:   map[string]string{"TT_PLATFORM_ARCH": "arm64"},
			files: map[string]string{"TT_PROGRAMS_DIR/cli": "bin/cli"},
		},
		{
			name: "a static program of a build.zig of no target",
			bld: zigBuild{
				config: Config{Bin: "cli"}, module: "", bin: "cli", static: true,
			},
			version: "0.17.1", help: "  -Dfoo=[bool]", installs: "bin/cli",
			files: map[string]string{"TT_PROGRAMS_DIR/cli": "bin/cli"},
		},
		{
			name: "a static program and a module",
			bld: zigBuild{
				config: Config{Module: "kv", Bin: "cli"}, module: "kv", bin: "cli", static: true,
			},
			version: "0.17.1", help: help, installs: "lib/kv.so bin/cli",
			files: map[string]string{
				"TT_OUTPUT_DIR/kv.so": "lib/kv.so", "TT_PROGRAMS_DIR/cli": "bin/cli",
			},
		},
		{
			name: "a static program of a target of its own",
			bld: zigBuild{
				config: Config{
					Bin: "cli", Options: map[string]string{"target": "x86_64-linux-gnu"},
				},
				module: "", bin: "cli", static: true,
			},
			version: "0.17.1", help: help, installs: "bin/cli",
			files: map[string]string{"TT_PROGRAMS_DIR/cli": "bin/cli"},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			export, err := test.bld.Export(&build.ExportContext{})
			require.NoError(t, err)
			assert.Equal(t, []string{zigTool}, export.Tools)
			assert.Empty(t, export.Lost)

			env := map[string]string{
				"TT_TEST_ZIG_VERSION": test.version, "TT_TEST_ZIG_HELP": test.help,
				"TT_TEST_INSTALLS": test.installs,
			}
			maps.Copy(env, test.env)

			run := runExport(t, exportFakes, export.Script, env)
			require.NoError(t, run.err, "%s\nscript:\n%s", run.out, export.Script)

			work := run.dirs["TT_BUILD_DIR"]
			dirs := []string{"--cache-dir", filepath.Join(work, cacheDir)}

			var want [][]string

			if _, own := env[pkgDirEnv]; !own {
				want = append(want, []string{zigTool, "version"})

				if takesPkgDir(test.version) {
					dirs = append(dirs, "--pkg-dir", filepath.Join(work, pkgDir))
				}
			}

			// Of a static program alone, for musl, as wantsMusl asks.
			if test.bld.wantsMusl(build.Target{OS: "linux", Arch: "amd64"}) {
				want = append(want, slices.Concat([]string{zigTool, "build", "--help"}, dirs))
			}

			want = append(want,
				append([]string{zigTool}, test.bld.args(work, dirs, test.musl, 0)...))
			assert.Equal(t, want, run.calls, export.Script)

			for path, installed := range test.files {
				key, rel, _ := strings.Cut(path, "/")
				data, err := os.ReadFile(filepath.Join(run.dirs[key], rel))
				require.NoError(t, err, path)
				assert.Equal(t, installed+"\n", string(data), path)
			}
		})
	}
}

// TestExportPkgDir checks that the script gives --pkg-dir to the versions
// of zig takesPkgDir takes, and to no other.
func TestExportPkgDir(t *testing.T) {
	t.Parallel()

	export, err := zigBuild{config: Config{Bin: "cli"}, module: "", bin: "cli", static: false}.
		Export(&build.ExportContext{})
	require.NoError(t, err)

	for _, version := range []string{
		"0.9.1", "0.10.0", "0.16.0", "0.16.1-dev.4+abc", "0.17", "0.17.0-dev.12+abc", "0.17.0",
		"0.17.0+abc", "0.17.1", "0.17.1-dev.3", "0.17.10", "0.18.0-dev.1+x", "0.20.1",
		"0.100.0", "0.170.0", "1.0.0", "10.2.3", "",
	} {
		run := runExport(t, exportFakes, export.Script, map[string]string{
			"TT_TEST_ZIG_VERSION": version, "TT_TEST_INSTALLS": "bin/cli",
		})
		require.NoError(t, run.err, run.out)
		require.Len(t, run.calls, 2, version)
		assert.Equal(t, takesPkgDir(version), slices.Contains(run.calls[1], "--pkg-dir"),
			version)
	}
}

// TestExportFails checks the failures of the script: of a module and a
// program zig build did not install, naming the files it looked for, and of
// a static program for macOS, as Preflight refuses it.
func TestExportFails(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		bld  zigBuild
		env  map[string]string
		want string
	}{
		{
			name: "no module",
			bld:  zigBuild{config: Config{Module: "a.kv"}, module: "a.kv", bin: "", static: false},
			want: `zig build installed no module "a.kv": ` + "%s/zig-out/lib has none of " +
				"a/kv.so, a/kv.dylib, a/libkv.so, a/libkv.dylib; install it with installModule " +
				"of zig-tarantool, or name the step that installs it in steps",
		},
		{
			name: "no program",
			bld:  zigBuild{config: Config{Bin: "cli"}, module: "", bin: "cli", static: false},
			want: `zig build installed no program "cli": %s/zig-out/bin has no cli; install it ` +
				"with b.installArtifact, or name the step that installs it in steps",
		},
		{
			name: "a static program for macOS",
			bld:  zigBuild{config: Config{Bin: "cli"}, module: "", bin: "cli", static: true},
			env:  map[string]string{"TT_PLATFORM_OS": darwinOS},
			want: "a static program cannot be built for darwin: static = true needs a linux build",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			export, err := test.bld.Export(&build.ExportContext{})
			require.NoError(t, err)

			env := map[string]string{"TT_TEST_ZIG_VERSION": "0.17.1", "TT_TEST_INSTALLS": ""}
			maps.Copy(env, test.env)

			run := runExport(t, exportFakes, export.Script, env)
			require.Error(t, run.err, export.Script)
			assert.Contains(t, run.out, strings.ReplaceAll(test.want, "%s",
				run.dirs["TT_BUILD_DIR"]))
		})
	}
}
