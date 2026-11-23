# gcore_pages_gdb.py: gcore that keeps the thread stacks of glibc 2.42+
# with gdb before 18.1.
#
#   (gdb) source gcore_pages_gdb.py
#   (gdb) gcore-pages [FILE]
#
# gcore of gdb before 18.1 (gcore_copy_callback() in gdb/gcore.c) copies
# each mapping of the process in reads of 1 MiB, and at the first read
# that fails it stops copying that mapping: the rest of it stays zeroes
# in the core.  Since glibc 2.42 the guard page of a thread stack is no
# PROT_NONE mapping of its own: it is installed with
# madvise(MADV_GUARD_INSTALL) (Linux 6.13+) inside the stack mapping, at
# its start, and a read of it fails (EIO from /proc/PID/mem).  So the
# stack of every thread but the main one is zeroes in the core, its TLS
# and struct pthread too, and several stacks often share one mapping.
# This is gdb bug 33855, fixed in gdb 18.1 (commit c1da013915: such a
# read is retried page by page).
#
# gcore-pages FILE runs `gcore FILE`, then does what gdb 18.1 does on
# the core gdb wrote: it reads every mapping of FILE again in reads of
# 1 MiB and, from the first read that fails on, writes into FILE what it
# reads, page by page where a whole read fails.  The pages that cannot
# be read (the guard pages) stay zeroes.  The process is not changed;
# with all-stop (the default) it does not run in between.  With gdb 18.1
# and later it is plain gcore.
import struct

import gdb

CHUNK = 1 << 20     # MAX_COPY_BYTES of gdb/gcore.c
PAGE = 4096         # the smallest page size: a read of less is not needed


def gdb_major():
    try:
        return int(gdb.VERSION.split('.')[0])
    except ValueError:
        return 0


def load_segments(fh):
    '''[(vaddr, offset, filesz)] of the PT_LOAD segments of an ELF file
    that have file bytes.'''
    ident = fh.read(16)
    if len(ident) < 16 or ident[:4] != b'\x7fELF':
        raise gdb.GdbError('gcore-pages: not an ELF file')
    end = '<' if ident[5] == 1 else '>'
    if ident[4] == 2:                       # ELFCLASS64
        hdr = struct.unpack(end + 'HHIQQQIHHHHHH', fh.read(48))
        phdr, shinfo = end + 'IIQQQQQQ', 44
    else:
        hdr = struct.unpack(end + 'HHIIIIIHHHHHH', fh.read(36))
        phdr, shinfo = end + 'IIIIIIII', 28
    phoff, shoff, phentsize, phnum = hdr[4], hdr[5], hdr[8], hdr[9]
    if phnum == 0xffff:                     # PN_XNUM: in sh_info of [0]
        fh.seek(shoff + shinfo)
        phnum, = struct.unpack(end + 'I', fh.read(4))
    segs = []
    for i in range(phnum):
        fh.seek(phoff + i * phentsize)
        f = struct.unpack(phdr, fh.read(struct.calcsize(phdr)))
        if ident[4] == 2:
            p_type, off, vaddr, filesz = f[0], f[2], f[3], f[5]
        else:
            p_type, off, vaddr, filesz = f[0], f[1], f[2], f[4]
        if p_type == 1 and filesz:          # PT_LOAD
            segs.append((vaddr, off, filesz))
    return segs


def read_pages(inf, addr, size):
    '''The bytes at addr, zeroes where a page cannot be read; and the
    number of such pages.'''
    buf = bytearray(size)
    bad = 0
    for p in range(0, size, PAGE):
        n = min(PAGE, size - p)
        try:
            buf[p:p + n] = bytes(inf.read_memory(addr + p, n))
        except gdb.MemoryError:
            bad += 1
    return buf, bad


def refill(path, inf):
    '''Write into the core at path what gcore of gdb before 18.1 left
    out: every mapping from its first 1 MiB that cannot be read whole.
    Returns (mappings, bytes written, pages that cannot be read).'''
    maps = written = bad = 0
    with open(path, 'r+b') as fh:
        for vaddr, off, filesz in load_segments(fh):
            failed = wrote = False
            for pos in range(0, filesz, CHUNK):
                n = min(CHUNK, filesz - pos)
                try:
                    buf = bytes(inf.read_memory(vaddr + pos, n))
                    if not failed:
                        continue            # gcore wrote it
                except gdb.MemoryError:
                    failed = True           # gcore stopped here
                    buf, nbad = read_pages(inf, vaddr + pos, n)
                    bad += nbad
                    if nbad == (n + PAGE - 1) // PAGE:
                        continue            # its zeroes stand
                fh.seek(off + pos)
                fh.write(buf)
                written += n
                wrote = True
            maps += wrote
    return maps, written, bad


class GcorePages(gdb.Command):
    '''gcore-pages [FILE]: gcore FILE (core.PID by default) with the
    pages that gcore of gdb before 18.1 leaves out: every mapping is read
    again from its first 1 MiB that cannot be read whole, page by page
    there, as gdb 18.1 does (a thread stack of glibc 2.42+ starts with
    its guard page).  See gcore_pages_gdb.py.'''

    def __init__(self):
        super(GcorePages, self).__init__('gcore-pages', gdb.COMMAND_FILES,
                                         gdb.COMPLETE_FILENAME)

    def invoke(self, arg, from_tty):
        inf = gdb.selected_inferior()
        path = arg.strip() or 'core.{}'.format(inf.pid)
        gdb.execute('gcore ' + path, from_tty, False)
        if gdb_major() >= 18:
            gdb.write('gcore-pages: gdb {} reads such pages itself\n'
                      .format(gdb.VERSION))
            return
        maps, written, bad = refill(path, inf)
        if maps:
            gdb.write('gcore-pages: read again {} bytes of {} mappings, '
                      '{} pages unreadable\n'.format(written, maps, bad))
        else:
            gdb.write('gcore-pages: gcore read every mapping\n')


GcorePages()
