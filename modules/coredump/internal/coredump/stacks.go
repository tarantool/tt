package coredump

import (
	"debug/elf"
	"fmt"
	"log/slog"
	"slices"
)

// The offsets of the fields of the notes of a core of a 64-bit process of
// Linux the check of the stacks reads (see include/linux/elfcore.h of
// Linux), which gcore of gdb writes as Linux does. struct elf_prstatus, of
// a thread, has the pid of the thread after its siginfo, its signal and the
// masks of its signals, and its registers, pr_reg, after the pids and the
// times; struct elf_prpsinfo, of the process, has the pid of the process,
// that of its main thread, after its state, its flags and its ids.
const (
	prstatusPID = 32
	prstatusReg = 112
	prpsinfoPID = 24
)

// pidSize is the size of a pid_t.
const pidSize = 4

// The indexes of the stack pointer among the registers of pr_reg of a
// 64-bit process, in words: rsp after 19 others in struct user_regs_struct
// of x86_64, and sp after the 31 general registers of struct user_pt_regs of
// aarch64.
const (
	x86StackPointer   = 19
	arm64StackPointer = 31
)

// stackPointers are the indexes of the stack pointer in pr_reg by the
// machine of a core.
var stackPointers = map[elf.Machine]uint64{
	elf.EM_X86_64:  x86StackPointer,
	elf.EM_AARCH64: arm64StackPointer,
}

// stackCheckSize is how much of the stack of a thread above its stack
// pointer is read: a frame of any function that ran there holds a return
// address, and zeros all the way are a stack written as zeros.
const stackCheckSize = 1 << 10

// threadStacks is what the core tells of the stacks of its threads but the
// main one: gcore of gdb older than 18.1 writes those of a process of glibc
// 2.42 or later on Linux 6.13 or later as zeros, since glibc guards them with
// MADV_GUARD_INSTALL, which makes the read of the whole mapping fail (fixed
// by c1da013915 of gdb). The main thread runs on the stack of the kernel,
// which no guard is installed in, and a core the kernel writes has every
// stack.
type threadStacks struct {
	// others is the number of the threads but the main one.
	others int
	// read is the number of those whose stacks were read.
	read int
	// zeroed is the number of those whose stacks are zeros above their
	// stack pointers.
	zeroed int
	// unread are why the stacks of the others, or of the core, cannot be
	// told of.
	unread []string
}

// threadStacks reads the stacks of the threads of c but the main one, the
// thread whose pid is that of the process: the stackCheckSize bytes above
// the stack pointer of each, in the file of the segment of the core that
// holds the stack pointer. A stack whose bytes the file does not hold, as a
// segment of the core has none of its memory or fewer bytes in the file
// than in memory, cannot be told of.
func (c *coreDump) threadStacks() threadStacks {
	stacks := threadStacks{others: 0, read: 0, zeroed: 0, unread: nil}

	reg, ok := stackPointers[c.file.Machine]
	if !ok || c.file.Class != elf.ELFCLASS64 {
		stacks.unread = append(stacks.unread, fmt.Sprintf("the stack pointers of a core of "+
			"%s of %s are not read", c.file.Machine, c.file.Class))

		return stacks
	}

	if len(c.process) < prpsinfoPID+pidSize {
		stacks.unread = append(stacks.unread, fmt.Sprintf("the core has no NT_PRPSINFO note "+
			"to tell its main thread by, or one of %d bytes", len(c.process)))

		return stacks
	}

	order := c.file.ByteOrder
	pid := order.Uint32(c.process[prpsinfoPID:])
	spOffset := prstatusReg + reg*c.wordSize()

	for _, desc := range c.threads {
		if uint64(len(desc)) < spOffset+c.wordSize() {
			stacks.unread = append(stacks.unread, fmt.Sprintf("an NT_PRSTATUS note of %d bytes",
				len(desc)))

			continue
		}

		thread := order.Uint32(desc[prstatusPID:])
		if thread == pid {
			continue
		}

		stacks.others++

		zeros, err := c.stackZeros(order.Uint64(desc[spOffset:]))
		if err != nil {
			stacks.unread = append(stacks.unread, fmt.Sprintf("thread %d: %v", thread, err))

			continue
		}

		stacks.read++

		if zeros {
			stacks.zeroed++
		}
	}

	return stacks
}

// stackZeros reports whether the stackCheckSize bytes at stackPointer, the
// stack pointer of a thread, are all zeros in the file of the core, read
// from the segment that holds it. The segment is to hold them all in the
// file.
func (c *coreDump) stackZeros(stackPointer uint64) (bool, error) {
	prog := c.loadAt(stackPointer)
	if prog == nil {
		return false, fmt.Errorf("%w: no segment of the core holds its stack pointer %#x in the "+
			"file", errNotInCore, stackPointer)
	}

	offset := stackPointer - prog.Vaddr
	if prog.Filesz-offset < stackCheckSize {
		return false, fmt.Errorf("%w: the segment of its stack pointer %#x holds %d bytes above "+
			"it in the file", errNotInCore, stackPointer, prog.Filesz-offset)
	}

	data := make([]byte, stackCheckSize)

	//nolint:gosec // An offset within the segment.
	_, err := prog.ReadAt(data, int64(offset))
	if err != nil {
		return false, fmt.Errorf("cannot read its stack at %#x: %w", stackPointer, err)
	}

	return !slices.ContainsFunc(data, func(b byte) bool { return b != 0 }), nil
}

// readThreadStacks tells of the stacks of the threads of the core at path
// (see coreDump.threadStacks). A core that cannot be read tells nothing,
// and says why.
func readThreadStacks(path string) threadStacks {
	core, err := openCore(path)
	if err != nil {
		return threadStacks{others: 0, read: 0, zeroed: 0, unread: []string{
			fmt.Sprintf("cannot read the core %s: %v", path, err),
		}}
	}

	defer func() { _ = core.Close() }()

	return core.threadStacks()
}

// warnThreadStacks warns, once, of the threads of stacks whose stacks are
// zeros: their backtraces will be empty, and libthread_db, which reads the
// descriptors glibc keeps at the top of those stacks, may fail on the core,
// as "Cannot find new threads: debugger service failed". The hint says
// what writes such stacks, which the core likely is of, and how to take
// another. How many stacks were read, and what cannot be told, is said at
// the debug level, and warned of not.
func warnThreadStacks(log *slog.Logger, stacks threadStacks) {
	for _, reason := range stacks.unread {
		log.Debug("cannot tell whether the stacks of the threads of the core are zeros: " +
			reason)
	}

	log.Debug(fmt.Sprintf("read the stacks of %d of the %d threads of the core besides the "+
		"main one, %d of them zeros", stacks.read, stacks.others, stacks.zeroed))

	if stacks.zeroed == 0 {
		return
	}

	backtraces := "the backtraces of those threads will be empty"
	if stacks.zeroed == 1 {
		backtraces = "the backtrace of that thread will be empty"
	}

	warnWithHint(log, fmt.Sprintf("%s nothing but zeros in the %d bytes above the stack "+
		"pointer: %s, and libthread_db may fail on the core", stacks.zeroedSubject(),
		stackCheckSize, backtraces),
		"gcore of gdb older than 18.1 writes such stacks on glibc 2.42 or later with Linux 6.13 "+
			"or later; take the core the kernel writes, or write it with gcore-pages of "+
			"tarantool-gdb-tools")
}

// zeroedSubject names the threads of s with stacks of zeros as the warning
// of them begins: "2 of the 5 threads of the core besides the main one
// have", "all 3 threads ... have", or "the thread ... has" of the one.
func (s threadStacks) zeroedSubject() string {
	const threads = "threads of the core besides the main one"

	switch {
	case s.zeroed == s.others && s.others == 1:
		return "the thread of the core besides the main one has"
	case s.zeroed == s.others:
		return fmt.Sprintf("all %d %s have", s.others, threads)
	case s.zeroed == 1:
		return fmt.Sprintf("1 of the %d %s has", s.others, threads)
	default:
		return fmt.Sprintf("%d of the %d %s have", s.zeroed, s.others, threads)
	}
}
