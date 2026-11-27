package cargo

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

// exportFakes are a cargo and a rustc that log their argv. cargo names
// TT_TEST_WORKSPACE/Cargo.toml as the manifest of the workspace and as the
// nearest one, and, by cargo metadata, as that of the one package of the
// workspace, or TT_TEST_MEMBER when it is set, and builds
// into the directory of the profile of --profile and the target of --target,
// or of TT_TEST_BUILD_TARGET, as build.target of a configuration of cargo
// says it, in a CARGO_TARGET_DIR that is TT_BUILD_DIR: rustc makes each of
// TT_TEST_LIBS, build the binary of --bin, each of its content its path.
// rustc names TT_TEST_LIBDIR as the directory of a target.
const exportFakes = `cargo() {
	tt_test_log cargo "$@"
	[ "$CARGO_TARGET_DIR" = "$TT_BUILD_DIR" ] ||
		{ echo "CARGO_TARGET_DIR=$CARGO_TARGET_DIR" >&2; exit 1; }
	command=$1 profile= target=${TT_TEST_BUILD_TARGET-} bin=
	shift
	while [ $# -gt 0 ]; do
		case $1 in
		--profile) profile=$2; shift ;;
		--target) target=$2; shift ;;
		--bin) bin=$2; shift ;;
		esac
		shift
	done
	case $profile in
	dev | test) profile=debug ;;
	bench) profile=release ;;
	esac
	dir=$CARGO_TARGET_DIR/${target:+$target/}$profile
	case $command in
	locate-project) echo "$TT_TEST_WORKSPACE/Cargo.toml" ;;
	metadata)
		member=${TT_TEST_MEMBER:-$TT_TEST_WORKSPACE/Cargo.toml}
		echo '{"packages":[{"name":"m","manifest_path":"'"$member"'"}]}'
		;;
	rustc)
		mkdir -p "$dir"
		for lib in $TT_TEST_LIBS; do echo "$dir/$lib" > "$dir/$lib"; done
		;;
	build)
		mkdir -p "$dir"
		echo "$dir/$bin" > "$dir/$bin"
		;;
	esac
}
rustc() {
	tt_test_log rustc "$@"
	echo "$TT_TEST_LIBDIR"
}
`

// cargoCall is the argv of cargo of args, those Run gives it, but the format
// of its messages, which the script does not read.
func cargoCall(args []string) []string {
	return append([]string{cargoTool}, slices.DeleteFunc(slices.Clone(args),
		func(arg string) bool { return arg == messageFormat })...)
}

// TestExport checks the script of the export against Run: it checks the
// lock, runs the cargo moduleArgs and args make of the block, with no -j and
// for the machine, but the musl target of a static program, and copies the
// library and the binary cargo built where runModule and runBinary do.
func TestExport(t *testing.T) {
	t.Parallel()

	locate := []string{cargoTool, "locate-project", "--workspace", "--message-format", "plain"}
	// A library of a block of no package is checked to be of a package.
	nearest := []string{cargoTool, "locate-project", "--message-format", "plain"}
	metadata := []string{cargoTool, "metadata", "--no-deps", "--format-version", "1"}

	for _, test := range []struct {
		name string
		bld  cargoBuild
		// libs are the libraries cargo builds, by name.
		libs string
		// buildTarget is build.target of the configuration of cargo.
		buildTarget string
		goos        string
		// files are the files the script made, relative to the directories
		// of the variables, by their paths in the directory cargo built
		// them in, TT_BUILD_DIR.
		files map[string]string
	}{
		{
			name: "a module",
			bld: cargoBuild{
				config: Config{Module: "fastrs"}, bin: "", module: true, static: false,
			},
			libs: "libfast_rs.so",
			files: map[string]string{
				"TT_OUTPUT_DIR/fastrs.so": "release/libfast_rs.so",
			},
		},
		{
			name: "the library under its own name",
			bld:  cargoBuild{config: Config{}, bin: "", module: true, static: false},
			libs: "libdemo_lib.so",
			files: map[string]string{
				"TT_OUTPUT_DIR/demo_lib.so": "release/libdemo_lib.so",
			},
		},
		{
			name: "a module and a program of a package, features and a profile",
			bld: cargoBuild{
				config: Config{
					Module: "m", Package: "it's", Features: []string{"a", "b"}, Profile: "dev",
				},
				bin: "agent", module: true, static: false,
			},
			libs: "libit_s.so",
			files: map[string]string{
				"TT_OUTPUT_DIR/m.so":    "debug/libit_s.so",
				"TT_PROGRAMS_DIR/agent": "debug/agent",
			},
		},
		{
			name: "a program of a profile of its own",
			bld: cargoBuild{
				config: Config{Bin: "agent", Profile: "fast"}, bin: "agent", module: false,
				static: false,
			},
			files: map[string]string{
				"TT_PROGRAMS_DIR/agent": "fast/agent",
			},
		},
		{
			name: "a static program and a module",
			bld:  cargoBuild{config: Config{Module: "m"}, bin: "agent", module: true, static: true},
			libs: "libm.so",
			files: map[string]string{
				"TT_OUTPUT_DIR/m.so":    "release/libm.so",
				"TT_PROGRAMS_DIR/agent": "x86_64-unknown-linux-musl/release/agent",
			},
		},
		{
			name: "a target of the configuration",
			bld: cargoBuild{
				config: Config{Module: "m"}, bin: "agent", module: true, static: false,
			},
			libs:        "libm.so",
			buildTarget: "x86_64-unknown-linux-gnu",
			files: map[string]string{
				"TT_OUTPUT_DIR/m.so":    "x86_64-unknown-linux-gnu/release/libm.so",
				"TT_PROGRAMS_DIR/agent": "x86_64-unknown-linux-gnu/release/agent",
			},
		},
		{
			name: "a module of macOS",
			bld:  cargoBuild{config: Config{}, bin: "", module: true, static: false},
			libs: "libm.dylib",
			goos: darwinOS,
			files: map[string]string{
				"TT_OUTPUT_DIR/m.dylib": "release/libm.dylib",
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			export, err := test.bld.Export(&build.ExportContext{})
			require.NoError(t, err)
			assert.Equal(t, []string{cargoTool}, export.Tools)
			assert.Equal(t, []string{exportLost}, export.Lost)

			workspace := t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(workspace, lockfileName), nil, 0o600))

			goos, ext := "linux", ".so"
			if test.goos != "" {
				goos, ext = test.goos, ".dylib"
			}

			// The target directory of the environment gives way to that of
			// the script.
			run := runExport(t, exportFakes, export.Script, map[string]string{
				"TT_TEST_WORKSPACE": workspace, "TT_TEST_LIBS": test.libs,
				"TT_TEST_LIBDIR": workspace, "TT_TEST_BUILD_TARGET": test.buildTarget,
				"TT_PLATFORM_OS": goos, "TT_MODULE_EXT": ext, "CARGO_TARGET_DIR": workspace,
			})
			require.NoError(t, run.err, "%s\nscript:\n%s", run.out, export.Script)

			var want [][]string

			platform := build.Target{OS: goos, Arch: "amd64"}

			if test.bld.static {
				triple, err := test.bld.target(platform, true)
				require.NoError(t, err)

				want = append(want,
					[]string{rustcTool, "--print", "target-libdir", "--target", triple})
			}

			want = append(want, locate)

			if test.bld.module && test.bld.config.Package == "" {
				want = append(want, nearest, metadata)
			}

			if test.bld.module {
				want = append(want, cargoCall(test.bld.moduleArgs("", goos, 0, nil)))
			}

			if test.bld.bin != "" {
				triple := ""
				if test.bld.static {
					triple, err = test.bld.target(platform, true)
					require.NoError(t, err)
				}

				want = append(want, cargoCall(test.bld.args(triple, 0, nil)))
			}

			assert.Equal(t, want, run.calls, export.Script)

			for path, built := range test.files {
				key, rel, _ := strings.Cut(path, "/")
				data, err := os.ReadFile(filepath.Join(run.dirs[key], rel))
				require.NoError(t, err, path)
				assert.Equal(t, filepath.Join(run.dirs["TT_BUILD_DIR"], built)+"\n", string(data),
					path)
			}
		})
	}
}

// TestExportFails checks the failures of the script: of a workspace with no
// Cargo.lock, of a static program for macOS, of a Rust target that is not
// installed, and of a build that made no library, or two.
func TestExportFails(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name string
		bld  cargoBuild
		env  map[string]string
		lock bool
		want string
	}{
		{
			name: "no lock",
			bld:  cargoBuild{config: Config{}, bin: "", module: true, static: false},
			env:  map[string]string{"TT_TEST_LIBS": "libm.so"},
			want: "the cargo package has no Cargo.lock in ",
		},
		{
			name: "a static program for macOS",
			bld:  cargoBuild{config: Config{}, bin: "agent", module: false, static: true},
			env:  map[string]string{"TT_PLATFORM_OS": darwinOS},
			lock: true,
			want: "a static program cannot be built for darwin: static = true needs a linux build",
		},
		{
			name: "a platform of no target",
			bld:  cargoBuild{config: Config{}, bin: "agent", module: false, static: true},
			env:  map[string]string{"TT_PLATFORM_ARCH": "riscv64"},
			lock: true,
			want: "no cargo target for the platform linux-riscv64",
		},
		{
			name: "a target not installed",
			bld:  cargoBuild{config: Config{}, bin: "agent", module: false, static: true},
			env:  map[string]string{"TT_TEST_LIBDIR": "/nonexistent"},
			lock: true,
			want: "the Rust target is not installed: x86_64-unknown-linux-musl, no /nonexistent; " +
				"rustup target add x86_64-unknown-linux-musl installs it",
		},
		{
			name: "no library",
			bld:  cargoBuild{config: Config{}, bin: "", module: true, static: false},
			env:  map[string]string{"TT_TEST_LIBS": "libm.a"},
			lock: true,
			want: "cargo built no library of the package in ",
		},
		{
			name: "two libraries",
			bld:  cargoBuild{config: Config{}, bin: "", module: true, static: false},
			env:  map[string]string{"TT_TEST_LIBS": "liba.so libb.so"},
			lock: true,
			want: "cargo built two libraries, ",
		},
		{
			name: "a library at the root of a workspace of no package",
			bld:  cargoBuild{config: Config{}, bin: "agent", module: true, static: false},
			env: map[string]string{
				"TT_TEST_LIBS": "libm.so", "TT_TEST_MEMBER": "/elsewhere/crates/m/Cargo.toml",
			},
			lock: true,
			want: "package: the Cargo.toml of <workspace> is the root of a cargo workspace " +
				"with no package of its own, and cargo builds the library of a package: name " +
				"the package to build, package = \"<name>\"\n",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			export, err := test.bld.Export(&build.ExportContext{})
			require.NoError(t, err)

			workspace := t.TempDir()
			if test.lock {
				require.NoError(t, os.WriteFile(filepath.Join(workspace, lockfileName), nil,
					0o600))
			}

			env := map[string]string{
				"TT_TEST_WORKSPACE": workspace, "TT_TEST_LIBS": "", "TT_TEST_LIBDIR": workspace,
			}
			maps.Copy(env, test.env)

			run := runExport(t, exportFakes, export.Script, env)
			require.Error(t, run.err, export.Script)
			assert.Contains(t, run.out, strings.ReplaceAll(test.want, "<workspace>", workspace))
		})
	}
}
