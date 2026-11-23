package coredump

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/sdk/output"
)

// writeCore writes at path the ELF header of a core of machine, which is
// what the architecture of a core is read from.
func writeCore(t *testing.T, path string, machine elf.Machine) {
	t.Helper()

	header := elf.Header64{
		Ident: [16]byte{
			0x7f, 'E', 'L', 'F', byte(elf.ELFCLASS64), byte(elf.ELFDATA2LSB),
			byte(elf.EV_CURRENT),
		},
		Type: uint16(elf.ET_CORE), Machine: uint16(machine), Version: uint32(elf.EV_CURRENT),
		Ehsize: 64, Phentsize: 56, Shentsize: 64,
	}

	var buffer bytes.Buffer

	require.NoError(t, binary.Write(&buffer, binary.LittleEndian, header))
	require.NoError(t, os.WriteFile(path, buffer.Bytes(), 0o644))
}

// mustParseVersion returns the version of line, a version of Tarantool.
func mustParseVersion(t *testing.T, line string) tarantoolVersion {
	t.Helper()

	version, ok := parseVersion(line)
	require.True(t, ok, line)

	return version
}

// TestReadCoreTarget checks that a core is of the version its version file
// names and of the architecture of the core, or of the tarantool when the
// core is no ELF file, and that what cannot be told is said once, at the
// debug level.
func TestReadCoreTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeArtefacts(t, dir)
	writeCore(t, filepath.Join(dir, "coredump"), elf.EM_AARCH64)

	log, recorder := logtest.New(t)
	target := readCoreTarget(log, dir)
	assert.Equal(t, "3.9.0", target.Version.Release)
	assert.Equal(t, "aarch64", target.Arch)
	assert.Empty(t, recorder.Records())

	// The core is not read: the tarantool tells.
	dir = t.TempDir()
	writeArtefacts(t, dir)
	writeCore(t, filepath.Join(dir, "tarantool"), elf.EM_X86_64)

	log, recorder = logtest.New(t)
	target = readCoreTarget(log, dir)
	assert.Equal(t, "x86_64", target.Arch)
	assert.Empty(t, recorder.Records())

	// A machine of no name of uname -m is named as debug/elf names it.
	writeCore(t, filepath.Join(dir, "coredump"), elf.EM_RISCV)

	log, _ = logtest.New(t)
	assert.Equal(t, "EM_RISCV", readCoreTarget(log, dir).Arch)

	// Neither the version nor the architecture can be told.
	dir = t.TempDir()
	writeArtefacts(t, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "version"), []byte("Tarantool\n"), 0o644))

	log, recorder = logtest.New(t)
	target = readCoreTarget(log, dir)
	assert.Empty(t, target.Version.Release)
	assert.Empty(t, target.Arch)

	records := recorder.Records()
	require.Len(t, records, 2)

	for _, record := range records {
		assert.Equal(t, slog.LevelDebug, record.Level, record.Message)
	}

	assert.Equal(t, "cannot tell the version of the core, the extensions are not checked "+
		`against it: cannot tell the version of Tarantool from "Tarantool"`, records[0].Message)
	assert.Contains(t, records[1].Message, "cannot tell the architecture of the core, the "+
		"extensions are not checked against it: cannot read "+filepath.Join(dir, "coredump")+
		": ")
	assert.Contains(t, records[1].Message, "; cannot read "+filepath.Join(dir, "tarantool")+": ")

	// No version file.
	require.NoError(t, os.Remove(filepath.Join(dir, "version")))

	log, recorder = logtest.New(t)
	readCoreTarget(log, dir)
	require.Len(t, recorder.Records(), 2)
	assert.Contains(t, recorder.Records()[0].Message, "cannot tell the version of the core")
}

// TestWarnUnchecked checks the warning of an extension the core is out of
// the range of the versions or of the architectures of, one for both, and
// that there is none for an extension of a core it was checked on, of any
// patch of the minors of its range, for what cannot be told of the core,
// and for an extension whose manifest says nothing, as no upstream one
// does.
func TestWarnUnchecked(t *testing.T) {
	t.Parallel()

	exts := []extension{
		{
			Name:      "lj_bt.py",
			Tarantool: versionRange{Min: "3.3.5", Max: "3.9.0"},
			Arch:      []string{"x86_64", "aarch64"},
		},
		*upstreamExtension(luajitGDBName, nil),
	}

	target := func(line, arch string) coreTarget {
		return coreTarget{Version: mustParseVersion(t, line), Arch: arch}
	}

	const (
		v390 = "Tarantool 3.9.0-0-g8b9a0f59a95b"
		v211 = "Tarantool 2.11.1-0-g96877bd"
	)

	cases := []struct {
		name   string
		target coreTarget
		want   []string
	}{
		{name: "checked", target: target(v390, "x86_64"), want: nil},
		{
			name:   "the first checked",
			target: target("Tarantool 3.3.5-0-g1234567", "aarch64"),
			want:   nil,
		},
		{
			name:   "an older patch of the first minor",
			target: target("Tarantool 3.3.0-0-g1234567", "x86_64"),
			want:   nil,
		},
		{
			name:   "a newer patch of the last minor",
			target: target("Tarantool 3.9.1-0-g1234567", "x86_64"),
			want:   nil,
		},
		{
			name:   "older",
			target: target("Tarantool 3.2.1-0-g1234567", "x86_64"),
			want: []string{"lj_bt.py is checked on Tarantool 3.3 to 3.9, the core is of " +
				"3.2.1: it may not work"},
		},
		{
			name:   "newer, a pre-release",
			target: target("Tarantool 3.10.0-entrypoint-36-gdf2e3013e1", "aarch64"),
			want: []string{"lj_bt.py is checked on Tarantool 3.3 to 3.9, the core is of " +
				"3.10.0-entrypoint: it may not work"},
		},
		{
			name:   "another architecture",
			target: target(v390, "EM_RISCV"),
			want: []string{"lj_bt.py is checked on x86_64 and aarch64, the core is of " +
				"EM_RISCV: it may not work"},
		},
		{
			name:   "both",
			target: target(v211, "EM_RISCV"),
			want: []string{"lj_bt.py is checked on Tarantool 3.3 to 3.9 on x86_64 and " +
				"aarch64, the core is of 2.11.1 on EM_RISCV: it may not work"},
		},
		{
			name:   "nothing told",
			target: coreTarget{Version: tarantoolVersion{}, Arch: ""},
			want:   nil,
		},
	}

	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()

			log, recorder := logtest.New(t)

			warnUnchecked(log, testCase.target, exts)
			assert.Equal(t, testCase.want, warnings(recorder))
			assert.Len(t, recorder.Records(), len(testCase.want), "no line but the warnings")
		})
	}
}

// TestWarnUncheckedBadRange checks that a range of versions an end of which
// is no release x.y.z checks no core, of any version, and says so in one
// line at the debug level, and that the architectures are checked all the
// same.
func TestWarnUncheckedBadRange(t *testing.T) {
	t.Parallel()

	target := coreTarget{
		Version: mustParseVersion(t, "Tarantool 2.11.1-0-g96877bd"), Arch: "EM_RISCV",
	}

	for _, checked := range []versionRange{
		{Min: "3.3.5", Max: "3.9"}, {Min: "3.3", Max: "3.9.0"}, {Min: "", Max: "3.9.0"},
		{Min: "3.3.5", Max: ""}, {Min: "3.3.5", Max: "3.9.0-rc1"},
	} {
		log, recorder := logtest.New(t)

		warnUnchecked(log, target, []extension{
			{Name: "lj_bt.py", Tarantool: checked, Arch: []string{"x86_64"}},
		})

		records := recorder.Records()
		require.Len(t, records, 2, checked)
		assert.Equal(t, slog.LevelDebug, records[0].Level, checked)
		assert.Equal(t, fmt.Sprintf("cannot tell the versions of Tarantool lj_bt.py is "+
			"checked on from its manifest, %q to %q, not releases x.y.z: the core is not "+
			"checked against them", checked.Min, checked.Max), records[0].Message)
		assert.Equal(t, []string{"lj_bt.py is checked on x86_64, the core is of EM_RISCV: it " +
			"may not work"}, warnings(recorder), checked)
	}
}

func TestJoinAnd(t *testing.T) {
	t.Parallel()

	assert.Empty(t, joinAnd(nil))
	assert.Equal(t, "a", joinAnd([]string{"a"}))
	assert.Equal(t, "a and b", joinAnd([]string{"a", "b"}))
	assert.Equal(t, "a, b and c", joinAnd([]string{"a", "b", "c"}))
}

// TestInspectGDBToolsUnchecked checks that inspect with GDBTools warns once
// of each extension it hands the debugger whose manifest does not cover the
// core, in the order it hands them, of none it does not hand it, and hands
// it them all the same; and that without GDBTools there is no such warning.
// The core is of RISC-V and of the minor after the last one of every range
// of the manifest, which none of them has, whatever commit of
// tarantool-gdb-tools tt embeds.
func TestInspectGDBToolsUnchecked(t *testing.T) {
	t.Parallel()

	exts, err := gdbToolsSet.all()
	require.NoError(t, err)

	var last release

	for _, ext := range exts {
		minors := mustMinors(t, ext.Tarantool)
		if minors.last.compare(last) > 0 {
			last = minors.last
		}
	}

	version := fmt.Sprintf("%d.%d.0", last[0], last[1]+1)

	dir := t.TempDir()
	writeArtefacts(t, dir)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "version"),
		[]byte("Tarantool "+version+"-0-g96877bd\n"), 0o644))
	writeCore(t, filepath.Join(dir, "coredump"), elf.EM_RISCV)

	run := func(t *testing.T, backend debugger, gdbTools bool) *logtest.Recorder {
		t.Helper()

		_, server := newFakeGitHub(t)
		res, recorder := testResolver(t, server, t.TempDir())

		var stdout bytes.Buffer

		err := inspectWith(t.Context(), output.Streams{
			In: strings.NewReader(""), Out: &stdout, Err: &stdout,
		}, res, backend, InspectOptions{Path: dir, GDBTools: gdbTools})
		require.NoError(t, err, stdout.String())

		return recorder
	}

	// The warnings of the extensions the debugger name is handed, in the
	// order inspect hands them in, said as warnUnchecked says them.
	expected := func(name string) []string {
		loaded, _, err := forDebugger(exts, name)
		require.NoError(t, err)

		want := make([]string, 0, len(loaded))

		for _, ext := range loaded {
			want = append(want, fmt.Sprintf("%s is checked on Tarantool %s on %s, the core is "+
				"of %s on EM_RISCV: it may not work", ext.Name, mustMinors(t, ext.Tarantool),
				joinAnd(ext.Arch), version))
		}

		return want
	}

	gdb := newRecordingDebugger(debuggerGDB)
	recorder := run(t, gdb, true)
	assert.Len(t, gdb.exts, 4)
	assert.Equal(t, expected(debuggerGDB), warnings(recorder))

	lldb := newRecordingDebugger(debuggerLLDB)

	recorder = run(t, lldb, true)
	assert.Equal(t, []string{gdbToolsLuaJIT}, lldb.exts)
	assert.Equal(t, expected(debuggerLLDB), warnings(recorder))
	assert.Len(t, warnings(recorder), 1)

	// The copies of tt are loaded for a version GitHub has no extensions
	// of, with warnings of their own.
	recorder = run(t, newRecordingDebugger(debuggerGDB), false)
	require.NotEmpty(t, warnings(recorder))

	for _, warning := range warnings(recorder) {
		assert.NotContains(t, warning, "it may not work")
	}
}
