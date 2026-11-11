package native

import (
	"debug/macho"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestProgramModuleExtras(t *testing.T) {
	t.Parallel()

	const libcxx = "/usr/lib/libc++.1.dylib"

	linux := []Platform{{OS: "linux", Arch: archAmd64}}
	darwin := []Platform{{OS: darwinOS, Arch: archArm64}}
	universal := []Platform{{OS: darwinOS, Arch: archAmd64}, {OS: darwinOS, Arch: archArm64}}

	tests := []struct {
		name      string
		platforms []Platform
		needed    []string
		want      []string
	}{
		{name: "no library", platforms: linux},
		{
			name: "glibc and the gcc runtime", platforms: linux,
			needed: []string{
				"libc.so.6", "libm.so.6", "libdl.so.2", "libpthread.so.0", "librt.so.1",
				"libgcc_s.so.1",
			},
		},
		{
			name: "the loaders", platforms: linux,
			needed: []string{
				"ld-linux-x86-64.so.2", "ld-linux-aarch64.so.1", "ld-musl-x86_64.so.1",
				"libc.musl-aarch64.so.1",
			},
		},
		{
			name: "memcached's sasl", platforms: linux,
			needed: []string{"libsasl2.so.2", "libc.so.6"},
			want:   []string{"libsasl2.so.2"},
		},
		{
			name: "the C++ library", platforms: linux,
			needed: []string{"libstdc++.so.6", "libm.so.6", "libgcc_s.so.1", "libc.so.6"},
			want:   []string{"libstdc++.so.6"},
		},
		{
			name: "every extra, in order", platforms: linux,
			needed: []string{"libz.so.1", "libc.so.6", "libssl.so.3", "libcrypto.so.3"},
			want:   []string{"libz.so.1", "libssl.so.3", "libcrypto.so.3"},
		},
		{
			name: "a library of glibc named by path", platforms: linux,
			needed: []string{"/lib/x86_64-linux-gnu/libc.so.6"},
			want:   []string{"/lib/x86_64-linux-gnu/libc.so.6"},
		},
		{name: "libSystem", platforms: darwin, needed: []string{libSystem}},
		{
			name: "libc++", platforms: darwin, needed: []string{libcxx, libSystem},
			want: []string{libcxx},
		},
		{
			name: "libresolv, which a Go program may need", platforms: darwin,
			needed: []string{libResolv},
			want:   []string{libResolv},
		},
		{
			name: "a library of Linux on macOS", platforms: darwin,
			needed: []string{"libc.so.6"},
			want:   []string{"libc.so.6"},
		},
		{
			name: "a library of macOS on Linux", platforms: linux,
			needed: []string{libSystem},
			want:   []string{libSystem},
		},
		{name: "a universal file", platforms: universal, needed: []string{libSystem}},
		{
			name: "no platform", needed: []string{"libc.so.6"},
			want: []string{"libc.so.6"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			prog := Program{Platforms: tt.platforms, Needed: tt.needed}
			assert.Equal(t, tt.want, prog.ModuleExtras())
		})
	}
}

// TestInspectModuleUniversal pins that every slice of a universal Mach-O
// module is checked: libc++ of one slice is an extra of the file.
func TestInspectModuleUniversal(t *testing.T) {
	t.Parallel()

	const libcxx = "/usr/lib/libc++.1.dylib"

	cpus := []macho.Cpu{macho.CpuAmd64, macho.CpuArm64}

	prog, err := inspect(t, universalMacho(t, cpus, [][]byte{
		machoWithDylib(t, macho.CpuAmd64, libSystem),
		machoWithDylib(t, macho.CpuArm64, libcxx, libSystem),
	}))
	require.NoError(t, err)
	assert.Equal(t, []string{libcxx}, prog.ModuleExtras())

	prog, err = inspect(t, universalMacho(t, cpus, [][]byte{
		machoWithDylib(t, macho.CpuAmd64, libSystem),
		machoWithDylib(t, macho.CpuArm64, libSystem),
	}))
	require.NoError(t, err)
	assert.Empty(t, prog.ModuleExtras())
}

func TestCompareGlibc(t *testing.T) {
	t.Parallel()

	tests := []struct {
		a, b string
		want int
	}{
		{"2.17", "2.17", 0},
		{"2.17", "2.2.5", 1},
		{"2.2.5", "2.3", -1},
		{"2.34", "2.4", 1},
		{"2.38", "2.38.1", -1},
		{"PRIVATE", "2.2.5", -1},
	}

	for _, tt := range tests {
		assert.Equal(t, tt.want, CompareGlibc(tt.a, tt.b), "%s against %s", tt.a, tt.b)
	}
}

// requireCC returns the path of cc, skipping the test where there is none,
// and off Linux, where cc does not make the ELF modules the tests expect.
func requireCC(t *testing.T) string {
	t.Helper()

	if runtime.GOOS != "linux" {
		t.Skip("the modules cc builds here are ELF ones")
	}

	cc, err := exec.LookPath("cc")
	if err != nil {
		t.Skip("cc not found in PATH")
	}

	return cc
}

// compileModule compiles source into a shared object in dir with cc and the
// arguments given after it, and returns its path (see requireCC).
func compileModule(t *testing.T, dir, name, source string, args ...string) string {
	t.Helper()

	compiler := requireCC(t)

	src := filepath.Join(dir, name+".c")
	require.NoError(t, os.WriteFile(src, []byte(source), testFilePerm))

	out := filepath.Join(dir, name+".so")
	argv := append([]string{"-shared", "-fPIC", "-o", out, src}, args...)

	output, err := exec.CommandContext(t.Context(), compiler, argv...).CombinedOutput()
	require.NoError(t, err, string(output))

	return out
}

// TestInspectModuleBuiltByCC inspects modules a real compiler and linker
// made: one that needs only the C library, the GCC runtime and libm, and one
// that needs a library of its own besides.
func TestInspectModuleBuiltByCC(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()

	// The math call and the unwinding call make libm and libgcc_s needed,
	// as a module of C++ or one built with -fexceptions needs the latter.
	plain := compileModule(t, dir, "plain", `#include <math.h>
#include <string.h>
#include <unwind.h>
double plain(double x, const char *s) {
	_Unwind_Backtrace(0, 0);
	return cos(x) + (double)strlen(s);
}
`, "-lm", "-Wl,--no-as-needed", "-lgcc_s")

	prog, err := InspectProgram(plain)
	require.NoError(t, err)
	assert.Contains(t, prog.Needed, "libc.so.6")
	assert.Contains(t, prog.Needed, "libgcc_s.so.1")
	assert.Empty(t, prog.ModuleExtras())
	assert.NotEmpty(t, prog.Glibc, "a module of glibc needs one of its versions")
	assert.Positive(t, CompareGlibc(prog.Glibc, "2.0"), prog.Glibc)

	// A library of the test's own, named by its soname as a library of the
	// system is.
	compileModule(t, dir, "libextra", "int extra(void) { return 1; }\n",
		"-Wl,-soname,libextra.so.1")

	linked := compileModule(t, dir, "linked", "int extra(void);\n"+
		"int linked(void) { return extra(); }\n", "-L"+dir, "-l:libextra.so", "-lm")

	prog, err = InspectProgram(linked)
	require.NoError(t, err)
	assert.Equal(t, []string{"libextra.so.1"}, prog.ModuleExtras())
}

// TestInspectModuleLinkedWithZlib is the case of a module that needs a
// library of the system, zlib, where its development files are there.
func TestInspectModuleLinkedWithZlib(t *testing.T) {
	t.Parallel()

	compiler := requireCC(t)
	dir := t.TempDir()

	probe := exec.CommandContext(t.Context(), compiler, "-shared", "-o",
		filepath.Join(dir, "probe.so"), "-xc", "/dev/null", "-Wl,--no-as-needed", "-lz")
	if probe.Run() != nil {
		t.Skip("cc cannot link -lz here")
	}

	module := compileModule(t, dir, "zipped", "unsigned long crc32(unsigned long, "+
		"const unsigned char *, unsigned);\n"+
		"unsigned long zipped(const unsigned char *b, unsigned n) {\n"+
		"\treturn crc32(0, b, n);\n}\n", "-lm", "-lz")

	prog, err := InspectProgram(module)
	require.NoError(t, err)
	assert.True(t, slices.Contains(prog.Needed, "libz.so.1"), prog.Needed)
	assert.Equal(t, []string{"libz.so.1"}, prog.ModuleExtras())
}
