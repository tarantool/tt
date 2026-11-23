package coredump

import (
	"bytes"
	"debug/elf"
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tarantool/tt/sdk/log/logtest"
	"github.com/tarantool/tt/sdk/output"
)

// The byte offsets of the fields of the notes of a core of a 64-bit process
// of Linux, as include/linux/elfcore.h lays them out, written out here
// rather than taken from the constants of the parser, so that a wrong
// constant fails the tests: the pid of struct elf_prstatus after its
// siginfo, its signal and the masks of its signals; pr_reg after the pids
// and the times, 112, and in it rsp of x86_64, the 20th word, 112 + 19 * 8,
// and sp of aarch64 after the 31 general registers, 112 + 31 * 8; the pid
// of struct elf_prpsinfo after its state, its flags and its ids.
const (
	testPrstatusPID = 32
	testPrpsinfoPID = 24
)

// testStackPointers are the byte offsets of the stack pointer in struct
// elf_prstatus by the machine.
var testStackPointers = map[elf.Machine]int{elf.EM_X86_64: 264, elf.EM_AARCH64: 360}

// prstatusNote returns the NT_PRSTATUS note of the thread pid of a core of
// machine whose stack pointer is stackPointer: a struct elf_prstatus of a
// 64-bit process of the size of that of aarch64, the largest, zeros but for
// its pid and the stack pointer among its registers. A machine whose stack
// pointer is not known has none.
func prstatusNote(machine elf.Machine, pid uint32, stackPointer uint64) []byte {
	desc := make([]byte, 392)

	binary.LittleEndian.PutUint32(desc[testPrstatusPID:], pid)

	offset, ok := testStackPointers[machine]
	if ok {
		binary.LittleEndian.PutUint64(desc[offset:], stackPointer)
	}

	return note("CORE", ntPrstatus, desc)
}

// prpsinfoNote returns the NT_PRPSINFO note of the process pid: a struct
// elf_prpsinfo of a 64-bit process, zeros but for its pid.
func prpsinfoNote(pid uint32) []byte {
	desc := make([]byte, 136)

	binary.LittleEndian.PutUint32(desc[testPrpsinfoPID:], pid)

	return note("CORE", ntPrpsinfo, desc)
}

// The stacks of the threads of the test cores: a segment each, but for the
// one of the main thread; the stack pointer of each is in its middle.
const (
	mainStack   = 0x7ffd00000000
	zeroStack   = 0x7f0000000000
	zeroStack2  = 0x7f0000100000
	dataStack   = 0x7f1000000000
	shortStack  = 0x7f2000000000
	noStack     = 0x7f3000000000
	stackSize   = 0x2000
	stackMiddle = 0x1000
)

// stackSegment returns a segment of a stack at vaddr: zeros, or with a
// return address a word above its middle.
func stackSegment(vaddr uint64, data bool) testSegment {
	segment := testSegment{vaddr: vaddr, data: make([]byte, stackSize)}
	if data {
		binary.LittleEndian.PutUint64(segment.data[stackMiddle+8:], 0x5555deadbeef)
	}

	return segment
}

// stacksCore returns a test core of machine of the process 100, its main
// thread on a stack with data, and the threads 101 to 105: two on stacks of
// zeros, as gcore of an affected gdb writes them, one on a stack with
// data, as the kernel writes it, one whose stack pointer is 512 bytes
// below the end of its segment, and one whose stack pointer no segment
// holds. Without process the core has no NT_PRPSINFO note.
func stacksCore(t *testing.T, machine elf.Machine, process bool) string {
	t.Helper()

	notes := [][]byte{
		prstatusNote(machine, 101, zeroStack+stackMiddle),
		prstatusNote(machine, 100, mainStack+stackMiddle),
		prstatusNote(machine, 102, zeroStack2+stackMiddle),
		prstatusNote(machine, 103, dataStack+stackMiddle),
		prstatusNote(machine, 104, shortStack+stackSize-512),
		prstatusNote(machine, 105, noStack+stackMiddle),
	}
	if process {
		notes = append(notes, prpsinfoNote(100))
	}

	path := testCore{
		segments: []testSegment{
			stackSegment(zeroStack, false), stackSegment(zeroStack2, false),
			stackSegment(dataStack, true), stackSegment(shortStack, true),
			stackSegment(mainStack, true),
		},
		mappings: nil, auxv: nil, notes: bytes.Join(notes, nil),
	}.write(t, filepath.Join(t.TempDir(), "core"))

	setMachine(t, path, machine)

	return path
}

// kernelCore returns a test core of x86_64 of the process 100, its main
// thread and the threads 101 and 102 on stacks with data, as the kernel
// writes them.
func kernelCore(t *testing.T, path string) string {
	t.Helper()

	return testCore{
		segments: []testSegment{
			stackSegment(mainStack, true), stackSegment(dataStack, true),
			stackSegment(zeroStack, true),
		},
		mappings: nil, auxv: nil, notes: bytes.Join([][]byte{
			prstatusNote(elf.EM_X86_64, 100, mainStack+stackMiddle),
			prstatusNote(elf.EM_X86_64, 101, dataStack+stackMiddle),
			prstatusNote(elf.EM_X86_64, 102, zeroStack+stackMiddle),
			prpsinfoNote(100),
		}, nil),
	}.write(t, path)
}

// setMachine sets the machine of the ELF header of the file at path, which
// testCore writes as x86_64.
func setMachine(t *testing.T, path string, machine elf.Machine) {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)

	binary.LittleEndian.PutUint16(data[18:], uint16(machine))
	require.NoError(t, os.WriteFile(path, data, 0o600))
}

func TestThreadStacks(t *testing.T) {
	t.Parallel()

	for _, machine := range []elf.Machine{elf.EM_X86_64, elf.EM_AARCH64} {
		stacks := readThreadStacks(stacksCore(t, machine, true))
		assert.Equal(t, 5, stacks.others, machine)
		assert.Equal(t, 3, stacks.read, machine)
		assert.Equal(t, 2, stacks.zeroed, machine)
		require.Len(t, stacks.unread, 2, machine)
		assert.Contains(t, stacks.unread[0], "thread 104: ")
		assert.Contains(t, stacks.unread[0], "holds 512 bytes above it in the file")
		assert.Contains(t, stacks.unread[1], "thread 105: ")
		assert.Contains(t, stacks.unread[1], "no segment of the core holds its stack pointer "+
			"0x7f3000001000 in the file")
	}

	stacks := readThreadStacks(kernelCore(t, filepath.Join(t.TempDir(), "core")))
	assert.Equal(t, threadStacks{others: 2, read: 2, zeroed: 0, unread: nil}, stacks)

	// The main thread is told by the pid of the process alone.
	stacks = readThreadStacks(stacksCore(t, elf.EM_X86_64, false))
	assert.Equal(t, threadStacks{others: 0, read: 0, zeroed: 0, unread: []string{
		"the core has no NT_PRPSINFO note to tell its main thread by, or one of 0 bytes",
	}}, stacks)

	stacks = readThreadStacks(stacksCore(t, elf.EM_RISCV, true))
	assert.Equal(t, threadStacks{others: 0, read: 0, zeroed: 0, unread: []string{
		"the stack pointers of a core of EM_RISCV of ELFCLASS64 are not read",
	}}, stacks)

	stacks = readThreadStacks(filepath.Join(t.TempDir(), "core"))
	require.Len(t, stacks.unread, 1)
	assert.Contains(t, stacks.unread[0], "cannot read the core ")
}

// stacksHint is the hint of the warning of a core with threads on stacks
// of zeros.
const stacksHint = "hint: gcore of gdb older than 18.1 writes such stacks on glibc 2.42 or " +
	"later with Linux 6.13 or later; take the core the kernel writes, or write it with " +
	"gcore-pages of tarantool-gdb-tools"

// stacksWarnings are the warning and the hint of a core with two threads
// besides the main one on stacks of zeros of five.
var stacksWarnings = []string{
	"2 of the 5 threads of the core besides the main one have nothing but zeros in the 1024 " +
		"bytes above the stack pointer: the backtraces of those threads will be empty, and " +
		"libthread_db may fail on the core",
	stacksHint,
}

func TestWarnThreadStacks(t *testing.T) {
	t.Parallel()

	log, recorder := logtest.New(t)

	warnThreadStacks(log, threadStacks{
		others: 5, read: 4, zeroed: 2, unread: []string{"thread 104: x"},
	})
	assert.Equal(t, stacksWarnings, warnings(recorder))
	assert.Contains(t, recorder.Messages(), "cannot tell whether the stacks of the threads of "+
		"the core are zeros: thread 104: x")
	assert.Contains(t, recorder.Messages(), "read the stacks of 4 of the 5 threads of the core "+
		"besides the main one, 2 of them zeros")

	log, recorder = logtest.New(t)

	warnThreadStacks(log, threadStacks{
		others: 2, read: 1, zeroed: 0, unread: []string{"thread 3: x"},
	})
	assert.Empty(t, warnings(recorder))

	for _, testCase := range []struct {
		others, zeroed      int
		subject, backtraces string
	}{
		{
			1, 1, "the thread of the core besides the main one has",
			"the backtrace of that thread will be empty",
		},
		{
			3, 3, "all 3 threads of the core besides the main one have",
			"the backtraces of those threads will be empty",
		},
		{
			4, 1, "1 of the 4 threads of the core besides the main one has",
			"the backtrace of that thread will be empty",
		},
	} {
		log, recorder = logtest.New(t)

		warnThreadStacks(log, threadStacks{
			others: testCase.others, zeroed: testCase.zeroed, unread: nil,
		})
		assert.Equal(t, []string{
			testCase.subject + " nothing but zeros in the 1024 bytes above the stack pointer: " +
				testCase.backtraces + ", and libthread_db may fail on the core",
			stacksHint,
		}, warnings(recorder))
	}
}

// TestProbeArtefactsStacks checks that the probe of pack tells of the stacks
// of the threads of the core it opens for the rest, which pack warns of.
func TestProbeArtefactsStacks(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tarantool := testExecutable(t, 0x400000, 0x123, testBuildID).write(t,
		filepath.Join(dir, "tarantool"))

	artefacts, err := probeArtefacts(io.Discard, PackOptions{
		Core: stacksCore(t, elf.EM_X86_64, true), Executable: tarantool, Dir: "", PID: 0,
		Time: "",
	}, nil)
	require.NoError(t, err)
	assert.Equal(t, 5, artefacts.stacks.others)
	assert.Equal(t, 2, artefacts.stacks.zeroed)
	assert.Len(t, artefacts.stacks.unread, 2)
}

// TestInspectWarnsOfZeroStacks checks that inspect warns, before the
// debugger runs, of a core whose threads besides the main one have stacks
// of zeros, and of none of a core the kernel writes.
func TestInspectWarnsOfZeroStacks(t *testing.T) {
	t.Parallel()

	run := func(t *testing.T, core string) []string {
		t.Helper()

		dir := t.TempDir()
		writeArtefacts(t, dir)

		data, err := os.ReadFile(core)
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(dir, "coredump"), data, 0o600))

		_, server := newFakeGitHub(t)
		res, recorder := testResolver(t, server, t.TempDir())

		var stdout bytes.Buffer

		err = inspectWith(t.Context(), output.Streams{
			In: strings.NewReader(""), Out: &stdout, Err: &stdout,
		}, res, newRecordingDebugger(debuggerGDB), InspectOptions{
			Path: dir, SourceDir: "", GDBTools: false,
		})
		require.NoError(t, err, stdout.String())

		return warnings(recorder)
	}

	assert.Equal(t, stacksWarnings, run(t, stacksCore(t, elf.EM_X86_64, true)))
	assert.Empty(t, run(t, kernelCore(t, filepath.Join(t.TempDir(), "core"))))
}
