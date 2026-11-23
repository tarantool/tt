# luajit_backtrace_gdb.py -- one stitched C + Lua backtrace for LuaJIT
# (Tarantool) in gdb.
#
#   (gdb) source luajit_gdb.py
#   (gdb) source tarantool_gdb.py                       (optional: fibers)
#   (gdb) source luajit_backtrace_gdb.py
#   (gdb) lj-bt [-v] [-n N] [<lua_State *>]
#   (gdb) lj-bt --all-fibers
#
# The LuaJIT extension may also be the one of the LuaJIT sources of the
# build: src/luajit_dbg.py (src/luajit-gdb.py before 5db8c217), and the
# Tarantool one tools/tarantool-gdb.py of the release.
#
# gdb `source` runs this file in the same __main__ namespace as the
# LuaJIT extension, so every top-level name here is prefixed with
# ljbt_/LJBT to avoid clobbering the helpers we reuse.  We take from it
# only what luajit-gdb.py (Tarantool 3.3-3.5), luajit_dbg.py (3.6+) and
# luajit_gdb.py all have: mref, gcref, gcval, G, J, funcproto, frame_ftsz,
# typenames, itypemap, vm_state, LJ_GC64, LJ_FR2.  Bytecode and IR names,
# which differ between LuaJIT versions, come from the enums in DWARF (or,
# if a build has none, from the BYTECODES and IRS tables of luajit_gdb.py
# and luajit_dbg.py).
#
# How the two stacks are matched (see lj_err.c:err_unwind, lj_frame.h):
#  * Every entry into the VM from C (lj_vm_call/pcall/cpcall/resume,
#    ffi callbacks) is one physical C frame: the VM keeps rsp == cframe
#    while it runs (VM CFI: CFA = rsp + CFRAME_SIZE).  So a gdb frame
#    whose pc is in the VM code and whose sp is a cframe of some
#    lua_State is a "VM band"; cframe_L(sp) tells whose Lua stack runs
#    there (lj_frame.h CFRAME_OFS_L), cframe_prev links the entries of
#    that lua_State (CFRAME_OFS_PREV).
#  * The Lua frames of a lua_State are split between its cframes
#    exactly as err_unwind does: walk the frames from L->base-1 with
#    cf = L->cframe; a FRAME_C/FRAME_CP link closes the segment of cf
#    and moves to cframe_prev(cf); a cframe with nres < 0 (lua_cpcall
#    without a Lua frame) owns no frames below its saved top.
#  * A Lua C function on top of a segment is the C frame right above
#    the band; Lua functions and fast functions have no C frame.
#  * JIT traces: gdb cannot unwind mcode, so an unwinder below maps a
#    pc inside a trace to the band (cframe = sp + T->spadjust); the
#    inlined Lua frames come from the snapshot covering the pc
#    (lj_trace.c:lj_trace_unwind, lj_snap.c:lj_snap_restore).

import functools
import json
import re
import struct

import gdb

try:
    import gdb.unwinder
    LJBT_HAVE_UNWINDER = True
except ImportError:
    LJBT_HAVE_UNWINDER = False

import __main__ as ljbt_lj

# What lj-bt uses of the LuaJIT extension (luajit_gdb.py, luajit_dbg.py
# and luajit-gdb.py have all of it).
LJBT_NEEDED = ('mref', 'gcref', 'gcval', 'G', 'J', 'funcproto', 'frame_ftsz',
               'typenames', 'itypemap', 'vm_state', 'LJ_GC64', 'LJ_FR2')
if not all(hasattr(ljbt_lj, n) for n in LJBT_NEEDED):
    raise gdb.GdbError('luajit_backtrace_gdb.py: source luajit_gdb.py (or '
                       'the luajit_dbg.py or luajit-gdb.py of the LuaJIT) '
                       'first; missing: {}'.format(', '.join(
                           n for n in LJBT_NEEDED
                           if not hasattr(ljbt_lj, n))))

def ljbt_lang_is_c():
    try:
        return gdb.current_language() == 'c'
    except (AttributeError, gdb.error):     # gdb < 12: no way to tell.
        return False


class LJBTLangC(object):
    '''with LJBTLangC(): gdb parses expressions and looks types up in the
    current language, which follows the selected frame: in a Rust frame
    "(unsigned long)&lj_vm_asm_begin" does not parse, "void" is no type
    and "char" is the 4-byte char of Rust.  The block runs with the
    language C and gives the previous setting back ("auto" too) on the
    way out, on an exception as well.  Nested blocks switch nothing.'''
    depth = 0
    prev = None

    def __enter__(self):
        cls = LJBTLangC
        if cls.depth == 0 and not ljbt_lang_is_c():
            prev = gdb.parameter('language')
            gdb.execute('set language c', False, True)
            cls.prev = prev
        cls.depth += 1
        return self

    def __exit__(self, *exc):
        cls = LJBTLangC
        cls.depth -= 1
        if cls.depth == 0 and cls.prev is not None:
            prev, cls.prev = cls.prev, None
            try:
                gdb.execute('set language ' + prev, False, True)
            except gdb.error:
                pass
        return False


def ljbt_in_c(fn):
    '''Decorator: fn runs in a LJBTLangC block.'''
    @functools.wraps(fn)
    def wrapper(*args, **kwargs):
        with LJBTLangC():
            return fn(*args, **kwargs)
    return wrapper


def ljbt_eval(expr):
    '''gdb.parse_and_eval of a C expression, whatever the frame.'''
    with LJBTLangC():
        return gdb.parse_and_eval(expr)


LJBT_TYPES = {}


def ljbt_type(typestr):
    '''gdb type by name; "T *" is a pointer to T (as the extensions do).'''
    t = LJBT_TYPES.get(typestr)
    if t is None:
        m = re.match(r'((?:(?:struct|union) )?\S*)\s*[*]', typestr)
        with LJBTLangC():
            t = gdb.lookup_type(typestr) if m is None else \
                gdb.lookup_type(m.group(1)).pointer()
        LJBT_TYPES[typestr] = t
    return t


def ljbt_sizeof(typestr):
    return ljbt_type(typestr).sizeof


def ljbt_offsetof(typestr, field):
    return ljbt_type(typestr)[field].bitpos // 8


def ljbt_enum_or_table(example, table, what, info):
    '''The enum of DWARF (see ljbt_enum) or, if the build has none (an
    enum whose members are only used as constants may be left out,
    tarantool/tarantool#13094), the name table of the LuaJIT extension,
    noted; with neither, a clear refusal.'''
    try:
        return ljbt_enum(example)
    except gdb.error:
        pass
    names = getattr(ljbt_lj, table, None)
    if names:
        info.append('{} names from the {} table of the LuaJIT extension: '
                    'no enum with {} in DWARF'.format(what, table, example))
        return dict((n, i) for i, n in enumerate(names))
    raise gdb.GdbError('lj-bt: no {} names: DWARF has no enum with {} and '
                       'the LuaJIT extension no {} table'.format(
                           what, example, table))


def ljbt_enum(example):
    '''{name without prefix: value} of the enum the constant belongs to,
    from DWARF: ljbt_enum('BC_CALL') is the BCOp enum (lj_bc.h).'''
    pfx = example.split('_', 1)[0] + '_'
    tp = ljbt_eval(example).type.strip_typedefs()
    return dict((f.name[len(pfx):], int(f.enumval)) for f in tp.fields()
                if f.name.startswith(pfx))


# Layout. ########################################################

# Transcribed from lj_frame.h:139-180 (x64, non-Windows) and
# lj_frame.h:194-202 (arm64), keyed by (arch, LJ_GC64).
#  JITX   what BC_JLOOP subtracts from sp before jumping into a trace:
#         CFRAME_SIZE_JIT - CFRAME_SIZE on x64 (vm_x86.dasc:5387,
#         lj_frame.h:178); on arm64 SPS_FIXED, `sub sp, sp, #16`
#         (vm_arm64.dasc BC_JLOOP, undone by lj_asm_arm64.h
#         asm_tail_fixup) though lj_frame.h has no CFRAME_SIZE_JIT there.
#  FP     where the VM's frame pointer points, if its CFI is fp-based:
#         arm64 `add fp, sp, # SAVE_FP_LR_` and "def_cfa fp 16"
#         (vm_arm64.dasc saveregs, emit_asm_debug); None on x64, whose
#         VM CFI is sp-based.
#  PCSTEP how far into the VM the synthetic pc of a trace's caller goes
#         (gdb looks the CFI up at pc - 1; arm64 wants it aligned).
LJBT_CFRAME_LAYOUTS = {
    ('x64', False): dict(PREV=6 * 8, PC=7 * 4, L=6 * 4, ERRF=5 * 4,
                         NRES=4 * 4, SIZE=12 * 8, JITX=16, FP=None,
                         PCSTEP=1),
    ('x64', True): dict(PREV=6 * 8, PC=3 * 8, L=2 * 8, ERRF=3 * 4,
                        NRES=2 * 4, SIZE=12 * 8, JITX=16, FP=None,
                        PCSTEP=1),
    ('arm64', True): dict(PREV=0, PC=8, L=16, ERRF=36, NRES=40, SIZE=208,
                          JITX=16, FP=192, PCSTEP=4),
}

# Functions in the VM's code that are plain C-ABI leaf helpers, called
# from C or from traces, with no cframe of their own (vm_x64.dasc
# "Math helper functions" and lj_vm_cpuid, vm_*.dasc tail).
LJBT_VM_LEAF = re.compile(r'^(lj_vm_(floor|ceil|trunc)(_sse)?|lj_vm_mod|'
                          r'lj_vm_cpuid|lj_assert_bad_for_arg_type)$')

# The outermost frames of a thread or a fiber stack: gdb stops there with
# "previous frame identical" on arm64 (libcoro's coro_startup), which is
# no inconsistency.
LJBT_STACK_BOTTOMS = ('coro_startup', 'coro_init', 'start_thread', 'clone',
                      'clone3', '__clone', '_start', '__libc_start_main',
                      '__libc_start_call_main', 'thread_start')

# The context libcoro's coro_create makes for a new fiber returns to a
# null address from one of these: a fiber that has not run yet unwinds to
# them and a frame with pc 0 (gdb stops there with "previous frame
# identical" on arm64).
LJBT_CORO_FRESH = ('coro_transfer', 'coro_init', 'coro_startup')

LJBT_TV = 8                     # sizeof(TValue)
LJBT_CFRAME_RESUME = 1          # lj_frame.h:275
LJBT_CFRAME_RAWMASK = ~3        # lj_frame.h:277
LJBT_GCT_THREAD = 6             # gct = ~LJ_TTHREAD, lj_obj.h
LJBT_GCT_FUNC = 8               # ~LJ_TFUNC
LJBT_LUA_YIELD = 1              # LUA_YIELD, lua.h

# Walk limits against a looping or garbage stack; reaching one is marked.
# A real Lua stack has fewer links (LJ_STACK_MAX slots) and cframes (the
# C stack overflows first).
LJBT_LIMITS = {'cframes': 1000, 'links': 100000}
LJBT_CONT_TAILCALL = 0          # LJ_CONT_TAILCALL, lj_frame.h:87
LJBT_CONT_FFI_CALLBACK = 1      # LJ_CONT_FFI_CALLBACK
LJBT_SNAP_FRAME = 0x010000      # lj_jit.h:201
LJBT_SNAP_CONT = 0x020000

# Frame types, lj_frame.h:24-27.
LJBT_FT_LUA, LJBT_FT_C, LJBT_FT_CONT, LJBT_FT_VARG = 0, 1, 2, 3
LJBT_FT_LUAP, LJBT_FT_CP, LJBT_FT_PCALL, LJBT_FT_PCALLH = 4, 5, 6, 7
LJBT_FT_NAMES = {0: 'L', 1: 'C', 2: 'M', 3: 'V', 4: 'LP', 5: 'CP',
                 6: 'PP', 7: 'PP'}


class LJBTConf(object):
    '''Build-dependent constants, resolved once per stop.'''

    @ljbt_in_c
    def __init__(self):
        if ljbt_lj.LJ_GC64 is None or ljbt_lj.LJ_FR2 is None:
            # The extension postpones its setup until libluajit symbols
            # are loaded; "None" must not read as "not GC64".
            raise gdb.GdbError('lj-bt: the LuaJIT extension is not '
                               'initialized yet (no LuaJIT symbols loaded?)')
        self.thread = ljbt_thread_key()
        self.fr2 = int(ljbt_lj.LJ_FR2)
        self.gc64 = bool(ljbt_lj.LJ_GC64)
        arch = gdb.selected_inferior().architecture().name().lower()
        if 'x86-64' in arch:
            self.arch, self.spreg, self.pcreg = 'x64', 'rsp', 'rip'
            self.fpreg = 'rbp'
        elif 'aarch64' in arch:
            self.arch, self.spreg, self.pcreg = 'arm64', 'sp', 'pc'
            self.fpreg = 'x29'
        else:
            raise gdb.GdbError('lj-bt: unsupported arch ' + arch)
        lay = LJBT_CFRAME_LAYOUTS.get((self.arch, self.gc64))
        if lay is None:
            raise gdb.GdbError('lj-bt: no cframe layout for {} GC64={}'
                               .format(self.arch, self.gc64))
        self.cf = lay
        self.refsz = 8 if self.gc64 else 4      # GCRef/MRef size
        self.sym = {}
        for name in ('lj_vm_asm_begin', 'lj_vm_ffi_call', 'lj_BC_JLOOP',
                     'lj_BC_JFUNCF', 'lj_BC_FUNCC'):
            try:
                self.sym[name] = int(ljbt_eval('(unsigned long)&' + name))
            except gdb.error:
                pass
        self.vm_begin = self.sym.get('lj_vm_asm_begin')
        self.vm_end = self.sym.get('lj_vm_ffi_call')
        self.ffnames = ljbt_ffnames()
        self.G = ljbt_find_G()
        self.proto_size = ljbt_sizeof('GCproto')
        self.str_size = ljbt_sizeof('GCstr')
        self.mcode_size = ljbt_sizeof('MCode')      # snap->mcofs unit
        # Bytecodes and IR ops of this very LuaJIT (lj_bc.h, lj_ir.h).
        self.info = []
        self.bc = ljbt_enum_or_table('BC_CALL', 'BYTECODES', 'bytecode',
                                     self.info)
        self.bcname = dict((v, k) for k, v in self.bc.items())
        self.ir = ljbt_enum_or_table('IR_KGC', 'IRS', 'IR', self.info)
        self.funcf_ops = set(self.bc[n] for n in ('FUNCF', 'IFUNCF',
                                                  'JFUNCF') if n in self.bc)
        # lj_bc_mode[] of DWARF, read on first use; a build may lack it as
        # it may lack the enums: then the modes by name, noted.
        self.bcmode = None
        try:
            self.bcmode_arr = ljbt_eval('lj_bc_mode')
        except gdb.error:
            self.bcmode = [ljbt_bcmode_a_by_name(self.bcname.get(op))
                           for op in range(max(self.bc.values()) + 1)]
            self.info.append('bytecode operand modes from the table of '
                             'lj-bt: no lj_bc_mode in DWARF')

    def bcmode_a(self, op):
        '''bcmode_a() of lj_bc.h: the mode of operand A.'''
        if self.bcmode is None:
            arr = self.bcmode_arr
            self.bcmode = [int(arr[i]) for i in range(max(self.bc.values())
                                                      + 1)]
        return self.bcmode[op] & 7


# conf, traces, owners: the state of the thread conf.thread; failed: the
# threads whose conf could not be built since the last reset.
LJBT_STATE = {'conf': None, 'traces': None, 'fiber': None, 'failed': {}}


def ljbt_thread_key():
    '''The selected thread: G comes from tarantool_L, which is __thread
    (src/lua/init.c, since 3.7), so the state is per thread, not per
    stop.'''
    try:
        th = gdb.selected_thread()
        return th.ptid if th is not None else None
    except gdb.error:
        return None


def ljbt_conf():
    key = ljbt_thread_key()
    c = LJBT_STATE['conf']
    if c is not None and c.thread != key:
        ljbt_drop()                 # `thread N` since the stop.
        c = None
    if c is None:
        try:
            c = LJBTConf()
        except Exception:
            LJBT_STATE['failed'][key] = True
            raise
        LJBT_STATE['conf'] = c
        LJBT_STATE['failed'].pop(key, None)
    return c


def ljbt_drop():
    '''Forget the state of the thread it was built for.'''
    LJBT_STATE['conf'] = None
    LJBT_STATE['traces'] = None
    LJBT_STATE['owners'] = None


def ljbt_reset(event=None):
    '''A stop, a new objfile, an exit: what was read may be stale.'''
    ljbt_drop()
    LJBT_STATE['failed'] = {}


def ljbt_running_fiber():
    '''The fiber running in the selected thread, cord_ptr->fiber, as
    fiber() of tarantool_gdb.py reads it (but without it loaded); None
    where there is none.  By symbol, no expression: the stop handler
    calls it, see ljbt_on_stop.'''
    try:
        sym = gdb.lookup_global_symbol('cord_ptr') or \
            gdb.lookup_static_symbol('cord_ptr')
        if sym is None:
            return None
        return int(sym.value(gdb.selected_frame())['fiber'])
    except Exception:
        return None


def ljbt_on_stop(event):
    '''No gdb.execute here, nor anything that may call it (LJBTLangC,
    the conf): gdb.execute runs the commands of the breakpoint stopped
    at, and from a stop handler that is before the other handlers and
    before gdb itself would; a `continue` there nests the next stop in
    this one.  The conf is built on first use.'''
    ljbt_reset()
    # The Tarantool extension (tarantool_gdb.py, tools/tarantool-gdb.py)
    # keeps the fiber picked by `tt-fiber` (or the one running when its
    # unwinder was created) across `continue`: after a stop in another
    # fiber `bt` and lj-bt would unwind a stale saved context.  So when
    # the running fiber changed since the last stop, the selection goes
    # back to it; a stepi within one fiber keeps it.  The running fiber
    # is recorded at every stop, the Tarantool extension loaded or
    # not: it is usually loaded at a stop, and its selection is of that
    # stop, not of the next one.
    cur = ljbt_running_fiber()
    FU = getattr(ljbt_lj, 'FiberUnwinder', None)
    # None: not known at the last stop, nothing is known to be stale.
    if FU is not None and LJBT_STATE['fiber'] is not None and \
            cur != LJBT_STATE['fiber']:
        try:
            FU.instance().reset_fiber()
            gdb.invalidate_cached_frames()
        except Exception:
            pass
    LJBT_STATE['fiber'] = cur


# Memory. ########################################################


def ljbt_mem(addr, size):
    return bytes(gdb.selected_inferior().read_memory(addr, size))


def ljbt_u8(addr):
    return ljbt_mem(addr, 1)[0]


def ljbt_u32(addr):
    return struct.unpack('<I', ljbt_mem(addr, 4))[0]


def ljbt_i32(addr):
    return struct.unpack('<i', ljbt_mem(addr, 4))[0]


def ljbt_u64(addr):
    return struct.unpack('<Q', ljbt_mem(addr, 8))[0]


def ljbt_ref(addr):
    '''GCRef/MRef stored at addr.'''
    return ljbt_u64(addr) if ljbt_conf().gc64 else ljbt_u32(addr)


def ljbt_cast(typestr, addr):
    return gdb.Value(addr).cast(ljbt_type(typestr))


def ljbt_proto_kgc(pt, idx):
    '''proto_kgc() of lj_obj.h: GC constant idx (< 0) of a GCproto.'''
    return ljbt_lj.gcref(ljbt_lj.mref('GCRef *', pt['k'])[idx])


def ljbt_ir_kgc(ins):
    '''ir_kgc() of lj_ir.h: the object of a KGC instruction (GC64 keeps it
    in the next IRIns).'''
    ir = ljbt_cast('IRIns *', ins.address)
    return ljbt_lj.gcref(ir[1 if ljbt_conf().gc64 else 0]['gcr'])


def ljbt_ir_k64(ins):
    '''The 64-bit payload of KNUM/KINT64 (ir_k64(): the next IRIns).'''
    return ljbt_u64(int(ljbt_cast('IRIns *', ins.address)[1]['tv'].address))


# cframes, lj_frame.h:279-295.

def ljbt_cf_raw(cf):
    return cf & LJBT_CFRAME_RAWMASK


def ljbt_cf_nres(cf):
    return ljbt_i32(cf + ljbt_conf().cf['NRES'])


def ljbt_cf_prev(cf):
    return ljbt_u64(cf + ljbt_conf().cf['PREV'])


def ljbt_cf_L(cf):
    c = ljbt_conf()
    v = ljbt_ref(cf + c.cf['L'])
    return v & ((1 << 47) - 1) if c.gc64 else v


def ljbt_cf_pc(cf):
    return ljbt_ref(cf + ljbt_conf().cf['PC'])


def ljbt_is_gct(addr, gct):
    if not addr:
        return False
    try:
        return int(ljbt_cast('GCobj *', addr)['gch']['gct']) == gct
    except gdb.error:
        return False


# Names. #########################################################

LJBT_LIBPFX = [
    ('coroutine_', 'coroutine.'), ('math_', 'math.'), ('bit_', 'bit.'),
    ('string_', 'string.'), ('table_', 'table.'), ('io_method_', 'file:'),
    ('io_', 'io.'), ('os_', 'os.'), ('debug_', 'debug.'),
    ('jit_util_', 'jit.util.'), ('jit_opt_', 'jit.opt.'),
    ('jit_profile_', 'jit.profile.'), ('jit_', 'jit.'),
    ('ffi_meta_', 'ffi.meta.'), ('ffi_clib_', 'ffi.clib.'),
    ('ffi_callback_', 'ffi.callback.'), ('ffi_', 'ffi.'),
    ('buffer_method_', 'buffer:'), ('buffer_', 'buffer.'),
    ('misc_sysprof_', 'misc.sysprof.'), ('misc_memprof_', 'misc.memprof.'),
    ('misc_', 'misc.'),
]


def ljbt_ffnames():
    '''ffid -> builtin name, from the FF_* enum (lj_ffdef.h via DWARF).'''
    names = {}
    try:
        tp = ljbt_eval('FF_pcall').type.strip_typedefs()
        for fld in tp.fields():
            n = fld.name[3:] if fld.name.startswith('FF_') else fld.name
            for pfx, repl in LJBT_LIBPFX:
                if n.startswith(pfx):
                    n = repl + n[len(pfx):]
                    break
            names[int(fld.enumval)] = n
    except gdb.error:
        pass
    return names


def ljbt_symname(addr):
    if not addr:
        return None
    try:
        blk = gdb.block_for_pc(addr)
        while blk is not None and blk.function is None:
            blk = blk.superblock
        if blk is not None and blk.function is not None:
            return blk.function.name
    except RuntimeError:
        pass
    try:
        # "NAME [+ OFS] in section S [of FILE]"; a demangled NAME may have
        # spaces: <ljbt_plain::Call as ljbt_plain::LuaEntry>::entry.
        out = gdb.execute('info symbol 0x{:x}'.format(addr), False, True)
        m = re.match(r'(.+?)( \+ \d+)? in section ', out)
        if m:
            return m.group(1)
    except gdb.error:
        pass
    return None


def ljbt_gcstr(addr):
    c = ljbt_conf()
    try:
        n = int(ljbt_cast('GCstr *', addr)['len'])
        return ljbt_mem(addr + c.str_size, min(n, 4096)).decode(
            'utf-8', 'replace')
    except gdb.error:
        return '<bad string 0x{:x}>'.format(addr)


def ljbt_chunkid(src):
    '''The name of a chunk as LuaJIT's short_src has it
    (lj_debug_shortname, LUA_IDSIZE 60): "=name" without the "=", cut
    at 59 characters; a string source as [string "..."], up to its first
    control character and at most 48 characters, cut to 45 and "..." if
    there is more.  But a file ("@") with its whole path, not the end.'''
    if src.startswith('@'):
        return src[1:]
    if src.startswith('='):
        return src[1:60]
    n = 0
    while n < 48 and n < len(src) and src[n] >= ' ':
        n += 1
    if n < len(src):
        return '[string "{}..."]'.format(src[:min(n, 45)])
    return '[string "{}"]'.format(src)


class LJBTProto(object):
    def __init__(self, addr):
        c = ljbt_conf()
        pt = ljbt_cast('GCproto *', addr)
        self.addr = addr
        self.chunk = ljbt_chunkid(ljbt_gcstr(int(ljbt_lj.gcref(
            pt['chunkname']))))
        self.firstline = int(pt['firstline'])
        self.numline = int(pt['numline'])
        self.sizebc = int(pt['sizebc'])
        self.bc = addr + c.proto_size
        self.lineinfo = int(ljbt_lj.mref('void *', pt['lineinfo']))
        self.varinfo = int(ljbt_lj.mref('void *', pt['varinfo']))
        self.uvinfo = int(ljbt_lj.mref('void *', pt['uvinfo']))
        self.sizeuv = int(pt['sizeuv'])
        self.pt = pt

    def ins(self, pos):
        return ljbt_u32(self.bc + 4 * pos)

    def kgc(self, d):
        '''GC constant #d as used by GGET/TGETS/FNEW (proto_kgc(pt, ~d)).'''
        return int(ljbt_proto_kgc(self.pt, ~d))

    def has_pc(self, pc):
        return self.bc <= pc <= self.bc + 4 * self.sizebc

    def line(self, pos):
        '''lj_debug.c:lj_debug_line.'''
        if pos is None or pos < 0 or pos > self.sizebc or not self.lineinfo:
            return None
        if pos == self.sizebc:
            return self.firstline + self.numline
        if pos == 0:
            return self.firstline
        pos -= 1
        if self.numline < 256:
            d = ljbt_u8(self.lineinfo + pos)
        elif self.numline < 65536:
            d = struct.unpack('<H', ljbt_mem(self.lineinfo + 2 * pos, 2))[0]
        else:
            d = ljbt_u32(self.lineinfo + 4 * pos)
        return self.firstline + d

    def line_retpc(self, pc):
        '''Line of the call whose return PC is pc (lj_debug.c:currentpc).'''
        if not self.has_pc(pc):
            return None
        return self.line((pc - self.bc) // 4 - 1)

    def line_pc(self, pc):
        '''Line of the instruction at pc (a snapshot PC).'''
        if not self.has_pc(pc):
            return None
        return self.line((pc - self.bc) // 4)

    def where(self):
        if self.firstline == 0:
            return 'main chunk of {}'.format(self.chunk)
        return 'function <{}:{}>'.format(self.chunk, self.firstline)


class LJBTFunc(object):
    def __init__(self, addr):
        self.addr = addr
        self.kind = None
        self.proto = None
        self.f = 0
        self.name = None
        if not ljbt_is_gct(addr, LJBT_GCT_FUNC):
            return
        fn = ljbt_cast('GCfuncC *', addr)
        self.ffid = int(fn['ffid'])
        if self.ffid == 0:
            self.kind = 'lua'
            pt = int(ljbt_lj.funcproto(fn))
            self.proto = LJBTProto(pt)
        else:
            self.f = int(fn['f'])
            if self.ffid == 1:
                self.kind = 'c'
                self.name = ljbt_symname(self.f) or '0x{:x}'.format(self.f)
            else:
                self.kind = 'ff'
                self.name = ljbt_conf().ffnames.get(
                    self.ffid, 'ffid#{}'.format(self.ffid))

    def describe(self, line=None, linenote=None):
        if self.kind == 'lua':
            if self.proto.firstline == 0:
                fwhere = 'main chunk'
            else:
                fwhere = 'function <{}:{}>'.format(self.proto.chunk,
                                                    self.proto.firstline)
            return '[lua] {}:{} in {}{}'.format(
                self.proto.chunk, '?' if line is None else line, fwhere,
                '  ({})'.format(linenote) if linenote else '')
        if self.kind == 'c':
            return '[lua] C function {}'.format(self.name)
        if self.kind == 'ff':
            return '[lua] builtin {}'.format(self.name)
        return '[lua] <not a function @ 0x{:x}>'.format(self.addr)


# Lua frames. ####################################################


class LJBTLink(object):
    '''One frame link (lj_frame.h:14-73) of a lua_State.'''

    def __init__(self, addr):
        c = ljbt_conf()
        self.addr = addr
        fr = ljbt_cast('TValue *', addr)
        self.ftsz = int(ljbt_lj.frame_ftsz(fr))
        self.islua = (self.ftsz & 3) == LJBT_FT_LUA
        # A Lua link is a 4-byte aligned PC: bit 2 is not FRAME_P there.
        self.typep = LJBT_FT_LUA if self.islua else self.ftsz & 7
        self.fnaddr = int(ljbt_lj.gcval((fr - c.fr2)['gcr']))
        self.fn = LJBTFunc(self.fnaddr)

    def pc(self):
        '''frame_pc() of a Lua link: the return PC into the caller.'''
        return self.ftsz & (0xffffffffffffffff if ljbt_conf().gc64
                            else 0xffffffff)

    def prev(self):
        if self.islua:
            ins = ljbt_u32(self.pc() - 4)
            return self.addr - LJBT_TV * (1 + ljbt_conf().fr2 +
                                          ((ins >> 8) & 0xff))
        return self.addr - (self.ftsz & ~7)

    def contv(self):
        '''frame_contv() of a CONT link (lj_frame.h:90-94): FR2 keeps a
        64-bit value three slots down, the 32-bit layout the low word of
        the slot below.  LJ_CONT_TAILCALL (0), LJ_CONT_FFI_CALLBACK (1),
        else the continuation: an address under FR2 (frame_contf), an
        offset from lj_vm_asm_begin otherwise.'''
        if ljbt_conf().fr2:
            return ljbt_u64(self.addr - 3 * LJBT_TV)
        return ljbt_i32(self.addr - LJBT_TV)

    def contf(self):
        v = self.contv()
        c = ljbt_conf()
        return v if c.fr2 else (c.vm_begin + v if c.vm_begin else None)

    def contpc(self):
        return ljbt_ref(self.addr - LJBT_TV * (1 + ljbt_conf().fr2) +
                        (4 if not ljbt_conf().gc64 else 0))

    def tname(self):
        if self.islua:
            return 'L'
        return LJBT_FT_NAMES.get(self.typep, '?')


class LJBTSegment(object):
    def __init__(self, L, cf):
        self.L = L
        self.cf = cf            # cframe as stored (with flag bits) or None
        self.links = []
        self.shown = False


class LJBTState(object):
    '''A lua_State with its frames split between its cframes.'''

    def __init__(self, addr, top=None, topnote=None):
        c = ljbt_conf()
        self.addr = addr
        self.notes = []         # Discrepancies ("!!").
        self.info = []          # Facts worth showing ("--").
        L = ljbt_cast('lua_State *', addr)
        self.base = int(L['base'])
        self.top = int(L['top'])
        self.stack = int(ljbt_lj.mref('TValue *', L['stack']))
        self.cframe = int(L['cframe'])
        self.status = int(L['status'])
        # A yielded coroutine: no cframe, its frames are on no C stack
        # until it is resumed (lj_state.c, LUA_YIELD).
        self.suspended = self.cframe == 0 and self.status == LJBT_LUA_YIELD
        self.topframe = top if top is not None else self.base - LJBT_TV
        self.topnote = topnote
        self.segments = []
        self.chain = []         # raw cframes, newest first
        self.flags = {}         # raw cframe -> CFRAME_RESUME etc.
        cf = self.cframe
        seen = set()
        while cf:
            if len(self.chain) >= LJBT_LIMITS['cframes']:
                self.notes.append('more than {} cframes, the rest is not '
                                  'walked'.format(LJBT_LIMITS['cframes']))
                break
            r = ljbt_cf_raw(cf)
            if r in seen:
                self.notes.append('cframe chain loops at 0x{:x}'.format(r))
                break
            seen.add(r)
            self.chain.append(r)
            self.flags[r] = cf & 3
            try:
                cf = ljbt_cf_prev(r)
            except gdb.error:
                self.notes.append('cannot read cframe 0x{:x}'.format(r))
                break
        if self.chain:
            try:
                if ljbt_cf_L(self.chain[0]) != addr:
                    self.notes.append(
                        'cframe_L(L->cframe) != L: wrong cframe layout?')
            except gdb.error:
                pass
        self.empty_top = None
        self.broken = None      # First unreadable frame link.
        self.split(self.cframe)
        self.check_shift()

    def check_shift(self):
        '''Every cframe with nres >= 0 owns at least its entry frame (the
        FRAME_C/FRAME_CP link that lj_vm_call/pcall/resume push).  If the
        oldest such cframe got nothing while the newest got frames, the
        newest cframe is being entered or left (lj_vm_return ..
        lj_vm_leave_cp, or lj_vm_call before the link is written): its own
        frames are not on the Lua stack.  Shift the split by one cframe.'''
        if len(self.chain) < 2 or not self.segments:
            return
        last = None
        for s in reversed(self.segments):
            if not s.cf:
                continue
            try:
                if ljbt_cf_nres(ljbt_cf_raw(s.cf)) >= 0:
                    last = s
                    break
            except gdb.error:
                return
        first = self.segments[0]
        if last is None or last.links or last is first or not first.links:
            return
        top = self.chain[0]
        self.segments = []
        self.broken = None
        self.split(ljbt_cf_prev(top))
        self.segments.insert(0, LJBTSegment(self, self.cframe))
        self.empty_top = top
        self.notes.append(
            'newest cframe 0x{:x} owns no Lua frame (VM entry or exit in '
            'progress); its would-be frames belong to the older cframes'
            .format(top))

    def split(self, cf):
        '''Mirror of lj_err.c:err_unwind with errcode == 0.'''
        c = ljbt_conf()
        frame = self.topframe
        bot = self.stack + LJBT_TV * c.fr2
        seg = LJBTSegment(self, cf)
        steps = 0
        while True:
            steps += 1
            if steps > LJBT_LIMITS['links']:
                self.notes.append('more than {} frame links, the rest is '
                                  'not walked'.format(LJBT_LIMITS['links']))
                break
            if cf:
                r = ljbt_cf_raw(cf)
                try:
                    nres = ljbt_cf_nres(r)
                except gdb.error:
                    self.notes.append('cannot read cframe 0x{:x}'.format(r))
                    break
                if nres < 0 and frame < self.stack + (-nres):
                    # C frame without Lua frame: lj_vm_cpcall.
                    self.segments.append(seg)
                    cf = ljbt_cf_prev(r)
                    seg = LJBTSegment(self, cf)
                    continue
            if frame <= bot:
                break
            link, nxt = self.read_link(frame, bot)
            if link is None:
                # Overwritten link (e.g. lj_vm_return moving results
                # down): resync on the nearest plausible link below.
                good = self.resync(frame, bot)
                if self.broken is None:
                    self.broken = frame
                if good is None:
                    self.notes.append('broken frame link at 0x{:x}, no '
                                      'plausible link below'.format(frame))
                    break
                self.notes.append(
                    'broken frame link at 0x{:x}: skipped {} slot(s) down '
                    'to the plausible link at 0x{:x}'.format(
                        frame, (frame - good) // LJBT_TV, good))
                frame = good
                continue
            seg.links.append(link)
            t = link.typep
            closes = False
            if link.islua:
                pass
            elif t == LJBT_FT_C:
                closes = True
            elif t == LJBT_FT_CP:
                closes = True
            elif t == LJBT_FT_CONT:
                # An FFI callback continuation is a C frame
                # (lj_err.c:167-169, frame_iscont_fficb), FR2 or not.
                try:
                    if link.contv() == LJBT_CONT_FFI_CALLBACK:
                        closes = True
                except gdb.error:
                    pass
            if closes:
                self.segments.append(seg)
                if cf is None or cf == 0:
                    if not self.suspended:
                        self.notes.append(
                            '{} frame at 0x{:x} without a cframe'.format(
                                link.tname(), frame))
                    cf = 0
                elif t == LJBT_FT_CP and cf & LJBT_CFRAME_RESUME:
                    cf = 0      # Bottom of a resumed coroutine.
                else:
                    cf = ljbt_cf_prev(ljbt_cf_raw(cf))
                seg = LJBTSegment(self, cf or None)
            frame = nxt
        if seg.links or seg.cf:
            self.segments.append(seg)

    def read_link(self, frame, bot):
        '''(link, prev) if frame is a plausible frame link, else (None,
        None): the function slot holds a function and the link goes down
        to the bottom or to another function slot.'''
        try:
            link = LJBTLink(frame)
            if link.fn.kind is None:
                return None, None
            nxt = link.prev()
            if nxt >= frame or nxt < bot:
                return None, None
            if link.islua:
                caller = LJBTLink(nxt)
                if caller.fn.kind != 'lua' or \
                        not caller.fn.proto.has_pc(link.pc()):
                    return None, None
            elif nxt > bot and LJBTLink(nxt).fn.kind is None:
                return None, None
            return link, nxt
        except gdb.error:
            return None, None

    def resync(self, frame, bot):
        f = frame - LJBT_TV
        while f > bot and frame - f <= 64 * LJBT_TV:
            if self.read_link(f, bot)[0] is not None:
                return f
            f -= LJBT_TV
        return None

    def topseg(self):
        for s in self.segments:
            if s.links:
                return s
        return None

    def seg_by_cf(self, raw):
        for s in self.segments:
            if s.cf and ljbt_cf_raw(s.cf) == raw:
                return s
        return None


# Traces. ########################################################


class LJBTTrace(object):
    def __init__(self, no, addr):
        T = ljbt_cast('GCtrace *', addr)
        self.no = no
        self.addr = addr
        self.T = T
        self.mcode = int(T['mcode'])
        self.szmcode = int(T['szmcode'])
        self.spadjust = int(T['spadjust'])
        self.root = int(T['root'])
        self.startins = int(T['startins'])

    @ljbt_in_c                  # Also called late, by the frame decorator.
    def start(self):
        try:
            pt = LJBTProto(int(ljbt_lj.gcref(self.T['startpt'])))
            pc = int(ljbt_lj.mref('BCIns *', self.T['startpc']))
            return '{}:{}'.format(pt.chunk, pt.line_pc(pc))
        except gdb.error:
            return '?'

    def snapshot(self, pc):
        '''lj_trace.c:lj_trace_unwind: rightmost snapshot with mcofs <=
        pc - mcode, in MCode units: bytes on x86, 4-byte instructions on
        arm64 (lj_target_*.h), where a byte offset picks a later one.'''
        ofs = (pc - self.mcode) // ljbt_conf().mcode_size
        nsnap = int(self.T['nsnap'])
        lo, hi = 0, nsnap
        snap = self.T['snap']
        while lo < hi:
            mid = (lo + hi) >> 1
            if ofs < int(snap[mid]['mcofs']):
                hi = mid
            else:
                lo = mid + 1
        return hi - 1 if hi > 0 else None

    def frames(self, snapno):
        '''Inlined frames of snapshot snapno (lj_snap.c:lj_snap_restore).
        Returns (pc, [(slot, func, link, iscont)]) bottom-up.'''
        if ljbt_conf().fr2:
            return self.frames_fr2(snapno)
        T = self.T
        snap = T['snap'][snapno]
        mapofs = int(snap['mapofs'])
        nent = int(snap['nent'])
        nsnap = int(T['nsnap'])
        if snapno + 1 < nsnap:
            nextofs = int(T['snap'][snapno + 1]['mapofs'])
        else:
            nextofs = int(T['nsnapmap'])
        smap = T['snapmap']
        ents = [int(smap[mapofs + i]) for i in range(nent)]
        pc = int(smap[mapofs + nent])
        flinks = [int(smap[i]) for i in range(mapofs + nent + 1, nextofs)]
        fi = len(flinks) - 1
        out = []
        ir = T['ir']
        for sn in ents:
            if not sn & (LJBT_SNAP_FRAME | LJBT_SNAP_CONT):
                continue
            slot = sn >> 24
            ref = sn & 0xffff
            link = None
            if slot != 0:
                link = flinks[fi] if fi >= 0 else None
                fi -= 1
            func = None
            if sn & LJBT_SNAP_FRAME:
                try:
                    func = self.kgc(ref)
                except gdb.error:
                    pass
            out.append((slot, func, link, bool(sn & LJBT_SNAP_CONT)))
        return pc, out

    def kgc(self, ref):
        '''The function of a KGC instruction of the trace, else None.'''
        ins = self.T['ir'][ref]
        if int(ins['o']) == ljbt_conf().ir['KGC']:
            return int(ljbt_ir_kgc(ins))
        return None

    def frames_fr2(self, snapno):
        '''LJ_FR2: a frame is two slots, [func][link].  The link slot is
        the SNAP_FRAME entry, its IR is a KNUM holding the raw 64-bit link
        (lj_snap.c:snapshot_slots), the function is the entry of the slot
        below it; slot 1 (the link of the trace's base frame) is skipped.
        The PC is the u64 after the entries: pc << 8 | baseslot - 2
        (snapshot_framelinks, snap_pc).'''
        T = self.T
        snap = T['snap'][snapno]
        mapofs = int(snap['mapofs'])
        nent = int(snap['nent'])
        smap = T['snapmap']
        ents = [int(smap[mapofs + i]) for i in range(nent)]
        pc = (int(smap[mapofs + nent]) |
              int(smap[mapofs + nent + 1]) << 32) >> 8
        byslot = dict((sn >> 24, sn) for sn in ents)
        out = []
        for sn in ents:
            slot = sn >> 24
            if slot == 0:
                # The function of the base frame, if the trace changed it
                # (a tail call inside the trace).
                try:
                    func = self.kgc(sn & 0xffff)
                except (gdb.error, IndexError):
                    func = None
                if func:
                    out.append((0, func, None, False))
                continue
            if slot == 1 or not sn & (LJBT_SNAP_FRAME | LJBT_SNAP_CONT):
                continue
            link = None
            try:
                ins = T['ir'][sn & 0xffff]
                c = ljbt_conf()
                if int(ins['o']) in (c.ir['KNUM'], c.ir.get('KINT64')):
                    link = ljbt_ir_k64(ins)
            except gdb.error:
                pass
            func = None
            fsn = byslot.get(slot - 1)
            if fsn is not None and sn & LJBT_SNAP_FRAME:
                try:
                    func = self.kgc(fsn & 0xffff)
                except (gdb.error, IndexError):
                    pass
            out.append((slot, func, link, bool(sn & LJBT_SNAP_CONT)))
        return pc, out


def ljbt_traces():
    if LJBT_STATE['traces'] is not None:
        return LJBT_STATE['traces']
    with LJBTLangC():           # The unwinder may be the first to ask.
        return ljbt_read_traces()


def ljbt_read_traces():
    res = []
    LJBT_STATE['traces'] = res
    c = ljbt_conf()
    if not c.G:
        return res
    try:
        J = ljbt_lj.J(ljbt_cast('global_State *', c.G))
        n = int(J['sizetrace'])
        tr = J['trace']
        for i in range(1, n):
            a = int(ljbt_lj.gcref(tr[i]))
            if a:
                res.append(LJBTTrace(i, a))
    except gdb.error:
        pass
    return res


def ljbt_trace_by_pc(pc):
    for t in ljbt_traces():
        if t.mcode <= pc < t.mcode + t.szmcode:
            return t
    return None


if LJBT_HAVE_UNWINDER:
    try:
        LJBTFrameId = gdb.unwinder.FrameId
    except AttributeError:
        class LJBTFrameId(object):
            def __init__(self, sp, pc):
                self.sp = sp
                self.pc = pc

    class LJBTTraceUnwinder(gdb.unwinder.Unwinder):
        '''A frame with pc in trace mcode: its caller is the VM frame of
        the cframe the trace runs on (sp + spadjust).  We return a
        synthetic VM pc there, so gdb's own VM CFI unwinds further.'''

        def __init__(self):
            super(LJBTTraceUnwinder, self).__init__('lj-bt-trace')

        busy = False

        @staticmethod
        def conf():
            '''The conf of the thread being unwound.  Built here when no
            stop event built it (a core file just opened: the first `bt`
            unwinds before the frame filter runs) or it is of another
            thread (`thread N`: in 3.7+ a thread without Lua has no G).
            Not again for a thread where it failed since the last reset:
            gdb asks the unwinder thousands of times between two stops.
            The build reads globals and TLS, no frames, but guard against
            reentry.'''
            c = LJBT_STATE['conf']
            key = ljbt_thread_key()
            if c is not None and c.thread == key:
                return c
            if LJBTTraceUnwinder.busy or key in LJBT_STATE['failed']:
                return None
            LJBTTraceUnwinder.busy = True
            try:
                return ljbt_conf()
            except Exception:
                return None
            finally:
                LJBTTraceUnwinder.busy = False

        @staticmethod
        def in_objfile(pc):
            '''Machine code of a trace is in no objfile.  Asked first, so
            that the conf is built only for a frame that may be one: the
            build may switch the language with gdb.execute, which at a
            stop runs the breakpoint's commands (see ljbt_on_stop), and gdb
            asks the unwinder about frame #0 there.'''
            try:
                return gdb.current_progspace().objfile_for_address(pc) \
                    is not None
            except AttributeError:      # gdb < 13: by debug info only.
                return gdb.block_for_pc(pc) is not None
            except (gdb.error, RuntimeError):
                return False

        def __call__(self, pf):
            try:
                if self.in_objfile(int(pf.pc() if hasattr(pf, 'pc') else
                                       pf.read_register('pc'))):
                    return None
            except (gdb.error, ValueError):
                pass
            c = self.conf()
            if c is None or not c.G:
                return None
            try:
                pcv = pf.read_register(c.pcreg)
                pc = int(pcv)
                if c.vm_begin and c.vm_begin <= pc < (c.vm_end or 0):
                    return None
                t = ljbt_trace_by_pc(pc)
                if t is None:
                    return None
                spv = pf.read_register(c.spreg)
                sp = int(spv)
                entry = c.sym.get('lj_BC_JLOOP')
                if (t.startins & 0xff) in c.funcf_ops:
                    entry = c.sym.get('lj_BC_JFUNCF', entry)
                if not entry:
                    return None
                cf = sp + t.spadjust + c.cf['JITX']
                # A gdb.Value, not an int: gdb before 14 takes no int for
                # the pc of a frame id ("The value of the 'pc' attribute
                # is not a pointer."), and gdb 12 then had no caller of
                # the trace frame but its own guess.
                ui = pf.create_unwind_info(LJBTFrameId(
                    spv, gdb.Value(t.mcode).cast(pcv.type)))
                ui.add_saved_register(c.pcreg, gdb.Value(
                    entry + c.cf['PCSTEP']).cast(pcv.type))
                ui.add_saved_register(c.spreg, gdb.Value(cf).cast(spv.type))
                if c.cf['FP'] is not None:
                    # The VM's CFI is fp-based there: give the VM frame
                    # the fp it keeps while it runs.
                    fpv = pf.read_register(c.fpreg)
                    ui.add_saved_register(c.fpreg, gdb.Value(
                        cf + c.cf['FP']).cast(fpv.type))
                return ui
            except Exception:
                return None


# G, L and fibers. ###############################################


def ljbt_find_G():
    '''G of the selected thread.  tarantool_L is __thread since 3.7: in
    a thread of Tarantool with no Lua (iproto, WAL, ...) it is NULL, and
    then there is no G here (before 3.7 it is a plain global, every
    thread sees the G of tx); sysprof.g, a plain global, is only a
    fallback for hosts without tarantool_L or globalL.'''
    for name in ('tarantool_L', 'globalL'):
        sym = gdb.lookup_global_symbol(name) or \
            gdb.lookup_static_symbol(name)
        if sym is None:
            continue
        try:
            v = sym.value()
            if int(v):
                return int(ljbt_lj.G(ljbt_cast('lua_State *', v)))
        except gdb.error:
            continue
        return None
    sym = gdb.lookup_static_symbol('sysprof')
    if sym is not None:
        try:
            g = int(sym.value()['g'])
            if g:
                return g
        except gdb.error:
            pass
    return None


def ljbt_sysprof_running():
    '''sysprof is profiling in a mode that walks the Lua stack (L or C;
    mode D, LUAM_SYSPROF_DEFAULT = 0 in lmisclib.h, only counts).'''
    sym = gdb.lookup_static_symbol('sysprof')
    if sym is None:
        return False
    try:
        prof = int(ljbt_eval('SPS_PROFILE'))
    except gdb.error:
        prof = 1                # lj_sysprof.c: enum { SPS_IDLE, SPS_PROFILE
    try:
        sp = sym.value()
        return int(sp['state']) == prof and int(sp['opt']['mode']) != 0
    except gdb.error:
        return False


def ljbt_cur_L(g):
    return int(ljbt_lj.gcref(ljbt_cast('global_State *', g)['cur_L']))


def ljbt_fiber_selection():
    '''(selected fiber, running fiber) if tarantool_gdb.py is loaded.'''
    FU = getattr(ljbt_lj, 'FiberUnwinder', None)
    fib = getattr(ljbt_lj, 'fiber', None)
    if FU is None or fib is None:
        return None, None
    try:
        return FU.instance().fiber(), fib()
    except gdb.error:
        return None, None


def ljbt_fiber_L(f):
    try:
        return int(f['storage']['lua']['stack'])
    except gdb.error:
        return 0


def ljbt_fiber_desc(f):
    try:
        return 'fiber {} "{}"'.format(int(f['fid']), f['name'].string())
    except gdb.error:
        return 'fiber @ {}'.format(f)


# C stack. #######################################################


class LJBTCFrame(object):
    def __init__(self, frame):
        c = ljbt_conf()
        self.frame = frame
        self.level = frame.level()
        self.pc = frame.pc()
        self.name = frame.name()
        self.type = frame.type()
        try:
            self.sp = int(frame.read_register(c.spreg))
        except (gdb.error, ValueError):
            self.sp = None
        self.kind = 'c'
        self.trace = None
        self.L = None
        self.cf = None
        self.inchain = False
        self.annot = []
        self.lua = []           # Lua entries (dicts) run in this frame
        self.folded = None      # Lua C function entry folded into it
        self.coroutine = False
        if self.type == gdb.SIGTRAMP_FRAME:
            self.kind = 'sig'
            return
        if self.name is None:
            t = ljbt_trace_by_pc(self.pc)
            if t is not None:
                self.kind = 'trace'
                self.trace = t
                return
        invm = c.vm_begin is not None and c.vm_begin <= self.pc and \
            (c.vm_end is None or self.pc < c.vm_end)
        if not invm and self.name and c.vm_end is None:
            invm = re.match(r'lj_(BC|ff|fff|cont|vm)_', self.name) \
                is not None
        if invm and self.name and LJBT_VM_LEAF.match(self.name):
            invm = False        # Plain C-ABI helpers in the VM's code.
        if invm and self.sp is not None and \
                self.type in (gdb.NORMAL_FRAME, gdb.SIGTRAMP_FRAME,
                              gdb.ARCH_FRAME):
            self.kind = 'vm?'
            try:
                L = ljbt_cf_L(self.sp)
            except gdb.error:
                L = 0
            if ljbt_is_gct(L, LJBT_GCT_THREAD):
                self.kind = 'vm'
                self.L = L
                self.cf = self.sp

    def text(self):
        if self.kind == 'sig':
            return '<signal handler called>'
        name = self.name or '??'
        if self.kind == 'trace':
            t = self.trace
            return '0x{:x} in [trace {}{}] started at {}'.format(
                self.pc, t.no,
                ', side of {}'.format(t.root) if t.root else '', t.start())
        loc = ''
        if self.kind in ('vm', 'vm?'):
            # All VM code maps to one line of buildvm_*.dasc.
            return '{} ()'.format(name)
        try:
            sal = self.frame.find_sal()
            if sal.symtab is not None:
                loc = ' at {}:{}'.format(ljbt_shortpath(
                    sal.symtab.filename), sal.line)
        except gdb.error:
            pass
        pfx = ''
        if not loc:
            pfx = '0x{:x} in '.format(self.pc)
        inl = ' [inlined]' if self.type == gdb.INLINE_FRAME else ''
        return '{}{} (){}{}'.format(pfx, name, loc, inl)


LJBT_OPTS = {'full': False}


def ljbt_shortpath(path):
    if LJBT_OPTS['full']:
        return path
    parts = path.split('/')
    return '/'.join(parts[-2:]) if len(parts) > 2 else path


def ljbt_walk_cstack(skip0, limit):
    '''gdb frames, newest first, and notes ("!!"/"--") on how it ended.'''
    out = []
    notes = []
    try:
        f = gdb.newest_frame()
    except gdb.error as e:
        return out, ['!! no stack: {}'.format(e)]
    newer = None
    while f is not None and len(out) < limit:
        if not (skip0 and f.level() == 0):
            try:
                out.append(LJBTCFrame(f))
            except gdb.error as e:
                notes.append('!! frame {}: {}'.format(f.level(), e))
        try:
            nf = f.older()
        except gdb.error as e:
            notes.append('!! unwinding stopped: {}'.format(e))
            break
        if nf is None:
            r = f.unwind_stop_reason()
            if r not in (gdb.FRAME_UNWIND_NO_REASON,
                         gdb.FRAME_UNWIND_OUTERMOST):
                notes.append(ljbt_unwind_end(f, newer,
                                             gdb.frame_stop_reason_string(r)))
        newer, f = f, nf
    if f is not None and len(out) >= limit:
        notes.append(LJBT_MORE_FRAMES)
    return out, notes


def ljbt_unwind_end(f, newer, why):
    '''The note on a walk that gdb ended with a reason (why): "--" at the
    bottom of a stack, "!!" anywhere else.'''
    name = f.name()
    if name in LJBT_STACK_BOTTOMS:
        return '-- unwinding stopped at {}, the bottom of the stack: {}'.format(
            name, why)
    newer_name = newer.name() if newer is not None else None
    if f.pc() == 0 and newer_name in LJBT_CORO_FRESH:
        return ('-- unwinding stopped at pc 0 below {}, the bottom of the '
                'context coro_create makes: a fiber that has not run yet: '
                '{}'.format(newer_name, why))
    return '!! unwinding stopped: {}'.format(why)
    if f is not None and len(out) >= limit:
        notes.append(LJBT_MORE_FRAMES)
    return out, notes


# Not a mismatch: the walk stopped at the -n limit on purpose.
LJBT_MORE_FRAMES = '-- more frames follow (use -n)'


# Tail calls (heuristic). ########################################
# BC_CALLT reuses the frame of the caller, LuaJIT keeps no trace of it.
# But the frame below still sits on its CALL: like lj_debug_funcname
# (lj_debug.c:298) -> lj_debug_slotname we find what that CALL meant to
# call and, where the source is still alive (a global of the caller's
# environment, its upvalue, a field of such a table, a caller's slot
# below the call base), its value.  If that is another function than
# the one running in the frame, frames were tail-called in between.

LJBT_BCM_DST, LJBT_BCM_BASE = 1, 2      # BCMdst, BCMbase (lj_bc.h)
LJBT_VARNAME_MAX = 7                    # VARNAME__MAX, lj_debug.h:62-67
LJBT_GCROOT_MMNAME = 0                  # GCROOT_MMNAME, lj_obj.h; MM_index

# Operand A of the bytecodes (lj_bc.h BCDEF) where lj_bc_mode is not in
# DWARF: the heuristic tells only dst and base from the rest.
LJBT_BCM_A_DST = frozenset((
    'ISTC ISFC MOV NOT UNM LEN ADDVN SUBVN MULVN DIVVN MODVN ADDNV SUBNV '
    'MULNV DIVNV MODNV ADDVV SUBVV MULVV DIVVV MODVV POW CAT KSTR KCDATA '
    'KSHORT KNUM KPRI UGET FNEW TNEW TDUP GGET TGETV TGETS TGETB TGETR')
    .split())
LJBT_BCM_A_BASE = frozenset((
    'KNIL TSETM CALLM CALL CALLMT CALLT ITERC ITERN VARG ISNEXT RETM FORI '
    'JFORI FORL IFORL JFORL ITERL IITERL JITERL').split())


def ljbt_bcmode_a_by_name(name):
    if name in LJBT_BCM_A_DST:
        return LJBT_BCM_DST
    if name in LJBT_BCM_A_BASE:
        return LJBT_BCM_BASE
    return 0


def ljbt_bc(name):
    return ljbt_conf().bc[name]


def ljbt_tv(addr):
    '''(type name, GC object or None) of the TValue at addr.'''
    tv = ljbt_cast('TValue *', addr)
    tn = ljbt_lj.typenames(ljbt_lj.itypemap(tv))
    if tn in ('LJ_TFUNC', 'LJ_TTAB', 'LJ_TSTR', 'LJ_TUDATA', 'LJ_TCDATA',
              'LJ_TTHREAD', 'LJ_TPROTO'):
        return tn, int(ljbt_lj.gcval(tv['gcr']))
    return tn, None


def ljbt_tab_rawgetstr(taddr, saddr):
    '''t[s] for an interned string s, hash part only (lj_tab.c:hashstr).'''
    t = ljbt_cast('GCtab *', taddr)
    hmask = int(t['hmask'])
    node = int(ljbt_lj.mref('Node *', t['node']))
    nsz = ljbt_sizeof('Node')
    kofs = ljbt_offsetof('Node', 'key')
    n = node + (int(ljbt_cast('GCstr *', saddr)['hash']) & hmask) * nsz
    for _ in range(hmask + 1):
        if ljbt_tv(n + kofs) == ('LJ_TSTR', saddr):
            return ljbt_tv(n)
        n = int(ljbt_lj.mref('Node *', ljbt_cast('Node *', n)['next']))
        if not n:
            break
    return 'LJ_TNIL', None


def ljbt_tab_getstr(taddr, saddr):
    '''t[s] following __index tables; None if an __index is a function,
    or if a metatable is met with no G known: the string "__index" is in
    G (gdb may not read tarantool_L, a thread-local variable, e.g. gdb 15
    with `symbol-file` of a PIE).'''
    G = ljbt_conf().G
    sindex = None
    if G:
        g = ljbt_cast('global_State *', G)
        sindex = int(ljbt_lj.gcref(g['gcroot'][LJBT_GCROOT_MMNAME]))
    for _ in range(16):
        v = ljbt_tab_rawgetstr(taddr, saddr)
        if v[0] != 'LJ_TNIL':
            return v
        mt = int(ljbt_lj.gcref(ljbt_cast('GCtab *', taddr)['metatable']))
        if not mt:
            return v
        if sindex is None:
            return None
        idx = ljbt_tab_rawgetstr(mt, sindex)
        if idx[0] == 'LJ_TNIL':
            return v
        if idx[0] != 'LJ_TTAB':
            return None
        taddr = idx[1]
    return None


def ljbt_upvalue(fnaddr, idx):
    fn = ljbt_cast('GCfuncL *', fnaddr)
    uv = ljbt_cast('GCupval *', ljbt_lj.gcref(fn['uvptr'][idx]))
    return ljbt_tv(int(ljbt_lj.mref('TValue *', uv['v'])))


def ljbt_cstring(addr, limit=256):
    data = ljbt_mem(addr, limit)
    return data.split(b'\0', 1)[0].decode('utf-8', 'replace')


def ljbt_uvname(pt, idx):
    '''lj_debug.c:lj_debug_uvname.'''
    if not pt.uvinfo or idx >= pt.sizeuv:
        return '?'
    data = ljbt_mem_until(pt.uvinfo, lambda d: d.count(b'\0') > idx)
    return data.split(b'\0')[idx].decode('utf-8', 'replace')


def ljbt_mem_until(addr, done, chunk=4096, limit=1 << 24):
    '''Bytes from addr, read in chunks until done(data) holds; uvinfo and
    varinfo have no stored size (lj_debug.c walks them to a terminator).
    A chunk that cannot be read ends the data (the last one may run past
    the mapping).'''
    data = b''
    while len(data) < limit and not done(data):
        try:
            data += ljbt_mem(addr + len(data), chunk)
        except gdb.MemoryError:
            if chunk <= 64:
                break
            chunk //= 4
    return data


def ljbt_varinfo_end(data):
    '''True once data holds the whole varinfo: VARNAME_END outside of a
    name or a uleb128 (lj_debug.c:debug_varname, the same walk).'''
    p = 0
    try:
        while True:
            vn = data[p]
            if vn == 0:
                return True
            if vn < LJBT_VARNAME_MAX:
                p += 1
            else:
                p = data.index(b'\0', p) + 1
            for _ in range(2):          # startpc, endpc deltas
                while data[p] >= 0x80:
                    p += 1
                p += 1
    except (IndexError, ValueError):
        return False


def ljbt_varname(pt, pos, slot):
    '''lj_debug.c:debug_varname: the local living in slot at pos.'''
    if not pt.varinfo:
        return None
    data = ljbt_mem_until(pt.varinfo, ljbt_varinfo_end)
    p, lastpc = 0, 0

    def uleb():
        nonlocal p
        v, sh = 0, 0
        while True:
            b = data[p]
            p += 1
            v |= (b & 0x7f) << sh
            sh += 7
            if b < 0x80:
                return v
    while p < len(data):
        vn = data[p]
        if vn < LJBT_VARNAME_MAX:
            if vn == 0:
                break
            name = None             # (for index) etc.
            p += 1
        else:
            end = data.index(b'\0', p)
            name = data[p:end].decode('utf-8', 'replace')
            p = end + 1
        lastpc = startpc = lastpc + uleb()
        if startpc > pos:
            break
        endpc = startpc + uleb()
        if pos < endpc:
            if slot == 0:
                return name
            slot -= 1
    return None


class LJBTSlotEval(object):
    '''What a slot of a Lua function holds before instruction pos, like
    lj_debug.c:lj_debug_slotname, plus its value where still alive.
    base: frame base of a live frame (slots < live are intact) or None
    for a static look at the bytecode of a closure.'''

    def __init__(self, fn, base, live):
        self.fn = fn
        self.pt = fn.proto
        self.base = base
        self.live = live

    def value(self, pos, slot, depth=0):
        '''(kind, name, (type, gcobj) or None) or None if unknown.'''
        if depth > 12:
            return None
        pt = self.pt
        if self.base is not None and slot < self.live:
            name = ljbt_varname(pt, pos, slot)
            return ('local', name or 'slot {}'.format(slot),
                    ljbt_tv(self.base + LJBT_TV * slot))
        k = pos - 1
        while k > 0:
            ins = pt.ins(k)
            op, a = ins & 0xff, (ins >> 8) & 0xff
            mode = ljbt_conf().bcmode_a(op)
            if mode == LJBT_BCM_BASE:
                if slot >= a and (op != ljbt_bc('KNIL') or
                                  slot <= ins >> 16):
                    return None     # Written by a call/vararg/... result.
            elif mode == LJBT_BCM_DST and a == slot:
                return self.define(k, ins, op, depth)
            k -= 1
        return None

    def define(self, k, ins, op, depth):
        pt = self.pt
        name = ljbt_conf().bcname.get(op)
        b, c, d = ins >> 24, (ins >> 16) & 0xff, ins >> 16
        if name == 'MOV':
            return self.value(k, d, depth + 1)
        if name == 'GGET':
            key = pt.kgc(d)
            env = int(ljbt_lj.gcref(ljbt_cast('GCfuncL *',
                                              self.fn.addr)['env']))
            return ('global', ljbt_gcstr(key), ljbt_tab_getstr(env, key))
        if name == 'UGET':
            return ('upvalue', ljbt_uvname(pt, d),
                    ljbt_upvalue(self.fn.addr, d))
        if name == 'TGETS':
            key = pt.kgc(c)
            kind = 'field'
            if k > 1:
                p = pt.ins(k - 1)
                if p & 0xff == ljbt_bc('MOV') and \
                        (p >> 8) & 0xff == ((ins >> 8) & 0xff) + 1 + \
                        ljbt_conf().fr2 and p >> 16 == b:
                    kind = 'method'
            tab = self.value(k, b, depth + 1)
            val = None
            if tab is not None and tab[2] is not None and \
                    tab[2][0] == 'LJ_TTAB':
                val = ljbt_tab_getstr(tab[2][1], key)
            return (kind, ljbt_gcstr(key), val)
        if name == 'FNEW':
            return ('function', None, ('LJ_TPROTO', pt.kgc(d)))
        return None


def ljbt_func_where(fn):
    if fn.kind == 'lua':
        return '{}:{}'.format(fn.proto.chunk, fn.proto.firstline)
    return fn.name or '?'


def ljbt_callsite(link):
    '''(caller link, CALL position, callee slot) of the call that
    created this frame, for frames called from Lua (L or PP links).'''
    if link.islua:
        calllink, extra = link, 0
    elif link.typep in (LJBT_FT_PCALL, LJBT_FT_PCALLH):
        calllink = LJBTLink(link.prev())    # The pcall/xpcall frame.
        if not calllink.islua:
            return None
        extra = 1 + ljbt_conf().fr2         # f is pcall's first argument.
    else:
        return None
    caller = LJBTLink(calllink.prev())
    if caller.fn.kind != 'lua' or not caller.fn.proto.has_pc(calllink.pc()):
        return None
    pt = caller.fn.proto
    pos = (calllink.pc() - pt.bc) // 4 - 1
    ins = pt.ins(pos)
    op, a = ins & 0xff, (ins >> 8) & 0xff
    if op == ljbt_bc('ITERC'):
        return caller, pos, a - 3, a
    if op not in (ljbt_bc('CALL'), ljbt_bc('CALLM')):
        return None
    return caller, pos, a + extra, a


def ljbt_tailcall_lines(link, fn=None):
    '''Lines to insert right after the frame of link (newest first); fn
    is the function running in it if it is not the one in the slot.'''
    site = ljbt_callsite(link)
    if site is None:
        return [], None
    caller, pos, slot, a = site
    ev = LJBTSlotEval(caller.fn, caller.addr + LJBT_TV, a)
    r = ev.value(pos, slot)
    if r is None:
        return [], 'callee at the call site not resolvable'
    kind, name, val = r
    called = "{} '{}'".format(kind, name) if name else kind
    if val is None:
        return [], "called as {}, not verifiable".format(called)
    tn, obj = val
    fn = fn or link.fn
    if tn == 'LJ_TPROTO':
        if fn.kind == 'lua' and fn.proto.addr == obj:
            return [], None
        exp = None
        where = '{}:{}'.format(LJBTProto(obj).chunk, LJBTProto(obj).firstline)
    elif tn == 'LJ_TFUNC':
        if obj == fn.addr:
            return [], None
        exp = LJBTFunc(obj)
        if exp.kind != 'lua':
            return [], "called as {} = {}, not verifiable".format(
                called, ljbt_func_where(exp))
        where = ljbt_func_where(exp)
    else:
        # __call of a table/cdata, or not a function at all.
        return [], "called as {} ({}), not verifiable".format(
            called, tn[5:].lower())
    line = '[lua] (tail call from {} = {})'.format(called, where)
    chain = []
    if LJBT_OPTS.get('tail_chain') and exp is not None:
        chain, note = ljbt_tail_chain(exp, fn)
        if note:
            line = line[:-1] + '; its CALLT: {})'.format(note)
    return chain + [ljbt_heur_entry(line + '  [heuristic]', called, where,
                                    False)], None


def ljbt_heur_entry(text, called, where, probably):
    chunk, _, defline = where.rpartition(':')
    return {'kind': 'heuristic', 'text': text, 'called': called,
            'where': where, 'chunk': chunk or None,
            'defline': int(defline) if defline.isdigit() else None,
            'probably': probably}


def ljbt_tail_callees(fn):
    '''Statically: what the CALLTs of a Lua closure call.'''
    res = []
    pt = fn.proto
    ev = LJBTSlotEval(fn, None, 0)
    for pos in range(1, pt.sizebc):
        ins = pt.ins(pos)
        if ins & 0xff not in (ljbt_bc('CALLT'), ljbt_bc('CALLMT')):
            continue
        r = ev.value(pos, (ins >> 8) & 0xff)
        if r is None:
            res.append(('?', None))
            continue
        kind, name, val = r
        called = "{} '{}'".format(kind, name) if name else kind
        if val is not None and val[0] == 'LJ_TFUNC':
            res.append((called, val[1]))
        else:
            res.append((called, None))
    return res


def ljbt_tail_chain(exp, fn):
    '''--tail-chain: frames between exp (the expected callee) and fn,
    from the CALLTs in the bytecode; newest first.'''
    # Breadth-first search exp -> ... -> fn over static CALLT edges.
    paths = [[(None, exp.addr)]]
    seen = set([exp.addr])
    for _ in range(4):
        nxt = []
        for path in paths:
            f = LJBTFunc(path[-1][1])
            if f.kind != 'lua':
                continue
            for called, obj in ljbt_tail_callees(f):
                if obj == fn.addr:
                    return [ljbt_heur_entry(
                        '[lua] (probably tail call from {} = {})  '
                        '[heuristic, static CALLT]'.format(
                            cl, ljbt_func_where(LJBTFunc(o))),
                        cl, ljbt_func_where(LJBTFunc(o)), True)
                        for cl, o in reversed(path[1:])], None
                if obj is not None and obj not in seen:
                    seen.add(obj)
                    nxt.append(path + [(called, obj)])
        paths = nxt
    # Not closed: one of these may be the running function itself, so
    # they are a note, not frames.
    return [], ', '.join('{}{}'.format(
        called, ' = ' + ljbt_func_where(LJBTFunc(obj)) if obj is not None
        else ' (unresolved)') for called, obj in ljbt_tail_callees(exp)[:3])


# Stitching. #####################################################


def ljbt_vmregs_top(cfr, st):
    '''For an interrupted VM frame, the real top frame from BASE and PC
    registers (x64 non-Windows: rdx and rbx in both vm_x86.dasc:28-46 and
    vm_x64.dasc:21-32; arm64: x19 and x21, vm_arm64.dasc:31-33), if they
    make sense.  Only inside a bytecode handler (lj_BC_*) do they hold
    the frame being run.'''
    c = ljbt_conf()
    regs = {'x64': ('rdx', 'rbx'), 'arm64': ('x19', 'x21')}.get(c.arch)
    if regs is None:
        return None
    if not (cfr.name and cfr.name.startswith('lj_BC_')):
        return None
    mask = 0xffffffffffffffff if c.gc64 else 0xffffffff
    try:
        base = int(cfr.frame.read_register(regs[0])) & mask
        pc = int(cfr.frame.read_register(regs[1])) & mask
    except (gdb.error, ValueError):
        return None
    if not (st.stack < base < st.stack + 0x10000000):
        return None
    try:
        link = LJBTLink(base - LJBT_TV)
    except gdb.error:
        return None
    if link.fn.kind != 'lua' or not link.fn.proto.has_pc(pc):
        return None
    return base - LJBT_TV, pc


def ljbt_render_links(seg, top_pc, out):
    '''Lua lines of a segment, newest first.  top_pc is (line, note) for
    the newest Lua function, if known from elsewhere.  The current line of
    a Lua function comes from the link of the frame it called, as in
    lj_debug.c:currentpc.'''
    prev = None
    skip_next = False
    for i, link in enumerate(seg.links):
        if skip_next:
            # The original link of a vararg function, hidden behind its
            # FRAME_VARG link (lj_debug.c:lj_debug_frame).
            skip_next = False
            prev = link
            continue
        if link.fnaddr == seg.L.addr:
            prev = link
            continue        # Dummy frame, lj_err_optype_call().
        fn = link.fn
        override = None
        if prev is None and top_pc is not None and len(top_pc) > 2 and \
                top_pc[2] is not None:
            override, fn = fn, top_pc[2]
        line, note = None, None
        if fn.kind == 'lua':
            if prev is None:
                if top_pc is not None:
                    line, note = top_pc[:2]
                else:
                    line, note = ljbt_cframe_line(seg, fn)
            elif prev.islua:
                line = fn.proto.line_retpc(prev.pc())
            elif prev.typep == LJBT_FT_CONT:
                try:
                    line = fn.proto.line_retpc(prev.contpc())
                except gdb.error:
                    pass
            elif prev.typep in (LJBT_FT_PCALL, LJBT_FT_PCALLH,
                                LJBT_FT_VARG):
                line, note = None, 'pc of caller not found'
            else:
                line, note = ljbt_cframe_line(seg, fn)
        how = []
        # The link that says how fn was called: of a vararg function, the
        # original one under its FRAME_VARG link, the next in the segment.
        call = link
        if link.typep == LJBT_FT_VARG and not link.islua:
            skip_next = True
            if i + 1 < len(seg.links):
                call = seg.links[i + 1]
        if call.typep == LJBT_FT_C and not call.islua:
            how.append('called from C: lua_call')
        elif call.typep == LJBT_FT_CP and not call.islua:
            if seg.cf and seg.cf & LJBT_CFRAME_RESUME:
                how.append('coroutine body')
            else:
                how.append('called from C: lua_pcall/cpcall')
        elif call.typep == LJBT_FT_CONT and not call.islua:
            how.append(ljbt_cont_kind(call))
        elif call.typep in (LJBT_FT_PCALL, LJBT_FT_PCALLH) and \
                not call.islua:
            how.append('protected by pcall/xpcall')
        if override is not None:
            how.append('tail-called inside the trace, the frame slot still '
                       'holds {}'.format(ljbt_func_where(override)))
        s = fn.describe(line, note)
        if how:
            s += '  <' + ', '.join(how) + '>'
        tail, why = [], None
        if seg.L.broken is None and not LJBT_OPTS.get('no_tail'):
            # Silent on an inconsistent stack: any read error -> nothing.
            # The call site is that of the call link (a vararg frame's).
            try:
                tail, why = ljbt_tailcall_lines(call, fn)
            except (gdb.error, ValueError, IndexError, KeyError,
                    RuntimeError):
                tail, why = [], None
        if LJBT_OPTS.get('verbose'):
            s += '  [{} link 0x{:x}{}]'.format(
                call.tname(), call.addr, '' if call is link else
                ' under V link 0x{:x}'.format(link.addr))
            if why:
                s += '  ({})'.format(why)
        extra = {} if call is link else {'vararg': True}
        out.append(ljbt_lua_entry(fn, line, note, how, link, seg.L.addr, s,
                                  call=call, **extra))
        out.extend(tail)
        prev = link


def ljbt_lua_entry(fn, line, note, how, link, L, text, call=None, **extra):
    '''A Lua frame of the output: text plus the fields of --json.  ftype
    is the type of the link that called the frame (call, if not link).'''
    call = call or link
    e = {
        'kind': 'lua', 'text': text, 'fkind': fn.kind,
        'func': fn.name if fn.kind in ('c', 'ff') else None,
        'chunk': fn.proto.chunk if fn.kind == 'lua' else None,
        'defline': fn.proto.firstline if fn.kind == 'lua' else None,
        'currentline': line, 'note': note, 'how': how,
        'ftype': call.tname() if call is not None else None,
        'link': '0x{:x}'.format(link.addr) if link is not None else None,
        'L': '0x{:x}'.format(L) if L else None,
    }
    e.update(extra)
    return e


def ljbt_warn(text):
    return {'kind': 'warn',
            'text': text if text.startswith('!!') else '!! ' + text}


def ljbt_info(text):
    return {'kind': 'info',
            'text': text if text.startswith('--') else '-- ' + text}


def ljbt_cont_kind(link):
    '''What a continuation frame is (lj_frame.h:87-106).'''
    try:
        contv = link.contv()
        addr = link.contf()
    except gdb.error:
        return 'continuation'
    if contv == LJBT_CONT_TAILCALL:
        return 'LJ_CONT_TAILCALL: tail call from C (lj_meta.c:lj_meta_tailcall)'
    if contv == LJBT_CONT_FFI_CALLBACK:
        return 'FFI callback'
    name = ljbt_symname(addr) if addr else None
    if name == 'lj_cont_stitch':
        return 'called from a trace, stitched (lj_cont_stitch)'
    return 'metamethod, continuation {}'.format(name or hex(contv))


def ljbt_cframe_line(seg, fn):
    '''PC of a Lua function on top of a segment: the cframe's saved PC
    (lj_debug.c:currentpc, "Lua function below errfunc/gc/hook").'''
    if not seg.cf:
        return None, 'top frame: current pc unknown'
    r = ljbt_cf_raw(seg.cf)
    try:
        pc = ljbt_cf_pc(r)
    except gdb.error:
        return None, 'top frame: current pc unknown'
    if pc == ljbt_cf_L(r) or not fn.proto.has_pc(pc):
        return None, 'top frame: current pc unknown'
    return fn.proto.line_retpc(pc), 'pc saved in cframe, may be stale'


def ljbt_stitch(cframes, defL, out, truncated=False):
    c = ljbt_conf()
    states = {}
    order = {}

    def state(L, top=None, note=None):
        if L not in states:
            states[L] = LJBTState(L, top, note)
            order[L] = 0
        return states[L]

    g = ljbt_cast('global_State *', c.G) if c.G else None
    jit_base = 0                # Base of the running trace, lj_jit.h.
    if g is not None:
        try:
            jit_base = int(ljbt_lj.mref('TValue *', g['jit_base']))
        except gdb.error:
            pass

    # Pass 1: decide the top frame of every lua_State seen in a band.
    first_band = {}
    for i, cfr in enumerate(cframes):
        if cfr.kind == 'vm' and cfr.L not in first_band:
            first_band[cfr.L] = i
    pending_top = {}
    stale_top = set()
    for L, i in first_band.items():
        cfr = cframes[i]
        prevf = cframes[i - 1] if i > 0 else None
        if prevf is not None and prevf.kind == 'trace' and jit_base:
            pending_top[L] = (jit_base - LJBT_TV, 'trace')
        elif prevf is None or prevf.kind == 'sig':
            # The VM itself was interrupted here, not a C function it
            # called: L->base is synced only when the VM leaves.
            r = None
            try:
                tmp = LJBTState(L)
                r = ljbt_vmregs_top(cfr, tmp)
                if r is not None:
                    pending_top[L] = (r[0], ('regs', r[1]))
            except gdb.error:
                pass
            if r is None:
                stale_top.add(L)

    for L, (top, why) in pending_top.items():
        st = state(L, top, why)
        if why == 'trace':
            # Not a discrepancy: L->base is not kept in sync in a trace.
            st.info.append('running a trace: Lua frames from g->jit_base '
                           '0x{:x}{}'.format(jit_base, '' if
                                             st.base == jit_base else
                                             ', L->base 0x{:x} is behind'
                                             .format(st.base)))
        elif st.base - LJBT_TV != top:
            # Neither: the interpreter syncs L->base only when it leaves
            # the VM; lj-bt shows the frames from the register.
            st.info.append('VM BASE register says top frame 0x{:x}, '
                            'L->base-1 is 0x{:x}: L->base is stale'.format(
                                top, st.base - LJBT_TV))
    for L in stale_top:
        state(L).info.append(
            'stopped in the VM ({}) with no frame in its registers: the '
            'top frame is L->base-1, which the VM syncs only when it '
            'leaves, so newer frames may be missing'.format(
                cframes[first_band[L]].name))

    # Pass 2: bands.
    for i, cfr in enumerate(cframes):
        if cfr.kind == 'vm?':
            cfr.annot.append('!! VM code, but sp=0x{:x} is not a cframe'
                             .format(cfr.sp))
        if cfr.kind != 'vm':
            continue
        st = state(cfr.L)
        raw = cfr.cf
        cfr.inchain = raw in st.chain
        if not cfr.inchain:
            cfr.annot.append('!! VM frame sp=0x{:x} is not a cframe of '
                             'L=0x{:x} (VM entry/exit in progress?)'.format(
                                 raw, cfr.L))
            continue
        idx = st.chain.index(raw)
        if idx < order[cfr.L]:
            cfr.annot.append('!! cframe 0x{:x} seen twice on the C stack'
                             .format(raw))
            continue
        # Segments of this L skipped on the C stack.
        for j in range(order[cfr.L], idx):
            seg = st.seg_by_cf(st.chain[j])
            cfr.lua.append(ljbt_warn(
                'cframe 0x{:x} of L=0x{:x} not found on the C stack '
                '(missing VM entry):'.format(st.chain[j], cfr.L)))
            if seg is not None:
                ljbt_render_links(seg, None, cfr.lua)
                seg.shown = True
        order[cfr.L] = idx + 1
        seg = st.seg_by_cf(raw)
        cfr.coroutine = bool(st.flags.get(raw, 0) & LJBT_CFRAME_RESUME)
        cfr.annot.append('[{} L=0x{:x} cframe=0x{:x}{}]'.format(
            'coroutine' if cfr.coroutine else 'VM entry', cfr.L, raw,
            ljbt_L_owner(cfr.L)))
        if raw == st.empty_top:
            cfr.annot.append('!! owns no Lua frame: VM entry/exit in '
                             'progress')
        elif idx == 0 and st.broken is not None and \
                st.broken == st.topframe:
            cfr.annot.append('!! top frame link 0x{:x} is overwritten'
                             .format(st.broken))
        if seg is None:
            continue
        seg.shown = True
        links = seg.links
        top_pc = None
        prevf = cframes[i - 1] if i > 0 else None
        istop = seg is st.topseg()
        # Trace on top of this band: inlined frames from the snapshot.
        if prevf is not None and prevf.kind == 'trace' and \
                st.topnote == 'trace' and istop:
            top_pc = ljbt_render_trace(prevf, st, seg)
        elif isinstance(st.topnote, tuple) and istop:
            pc = st.topnote[1]
            if links and links[0].fn.kind == 'lua':
                top_pc = (links[0].fn.proto.line_retpc(pc),
                          'pc from VM registers')
        # A C function (or a builtin with a C body) on top of the
        # segment is the C frame right above the band.
        fold = False
        if links and links[0].fn.kind in ('c', 'ff') and links[0].fn.f:
            fn = links[0].fn
            target = None
            for k in range(i - 1, -1, -1):
                pf = cframes[k]
                if pf.kind != 'c':
                    break
                if pf.type == gdb.INLINE_FRAME:
                    continue
                target = pf
                break
            fname = ljbt_symname(fn.f)
            fdesc = fname or '0x{:x}'.format(fn.f)
            if target is not None and fname and target.name == fname:
                fold = target
            elif fn.kind == 'c' and (i == 0 or cframes[i - 1].kind == 'sig'):
                # Stopped in the VM right around the call.
                cfr.lua.append(ljbt_warn(
                    'no C frame of {} above this VM frame: the call is in '
                    'progress'.format(fdesc)))
            elif fn.kind == 'c' and fname is None and target is not None \
                    and target.name is None:
                # A stripped module: nothing to match the two by.
                cfr.lua.append(ljbt_info(
                    'C function {} and the C frame #{} right above this VM '
                    'frame have no names: not matched'.format(
                        fdesc, target.level)))
            elif fn.kind == 'c':
                # C frames above, but not this one: it tail-called (jmp).
                cfr.lua.append(ljbt_info(
                    'no C frame of {} right above this VM frame: a tail '
                    'call in C?'.format(fdesc)))
        lines = []
        ljbt_render_links(seg, top_pc, lines)
        if fold:
            fold.annot.append('<- ' + lines[0]['text'][len('[lua] '):])
            fold.folded = lines[0]
            lines = lines[1:]
        cfr.lua.extend(lines)

    # Leftovers.
    tail = []
    # No VM on this C stack at all (the scheduler, a C fiber, a thread of
    # its own): the coroutine G->cur_L names last ran elsewhere.
    elsewhere = not any(cfr.kind in ('vm', 'vm?', 'trace')
                        for cfr in cframes)
    if defL and defL not in states:
        st = state(defL)
        if st.suspended:
            tail.append(ljbt_info('L=0x{:x} is a suspended coroutine: its '
                                  'frames are on no C stack until resumed:'
                                  .format(defL)))
        elif truncated:
            tail.append(ljbt_info('L=0x{:x} has no VM entry above the -n '
                                  'limit:'.format(defL)))
        elif elsewhere:
            st.suspended = True     # Shown as such, see below.
            tail.append(ljbt_info('no Lua runs on this C stack; L=0x{:x} '
                                  'last ran on another one:'.format(defL)))
        else:
            tail.append(ljbt_warn('L=0x{:x} has no VM entry on this C '
                                  'stack:'.format(defL)))
    # What sysprof would walk (lj_sysprof.c:stream_backtrace_lua).
    curL = ljbt_cur_L(c.G) if c.G else 0
    if curL in states and ljbt_sysprof_running():
        st = states[curL]
        try:
            tf = int(g['top_frame_info']['top_frame']) - LJBT_TV
            if tf != st.topframe:
                out.append('!! sysprof walks cur_L from top_frame_info.'
                           'top_frame-1 = 0x{:x}, the top frame is 0x{:x} '
                           '({:+d} slots)'.format(
                               tf, st.topframe,
                               (tf - st.topframe) // LJBT_TV))
            elif st.broken == tf:
                out.append('!! sysprof walks cur_L from top_frame_info.'
                           'top_frame-1 = 0x{:x}, a frame link that is '
                           'overwritten'.format(tf))
        except gdb.error:
            pass
    for L, st in states.items():
        for n in st.info:
            out.append('-- L=0x{:x}: {}'.format(L, n))
        for n in st.notes:
            out.append('!! L=0x{:x}: {}'.format(L, n))
        for seg in st.segments:
            if seg.shown or not seg.links:
                continue
            if st.suspended:
                ljbt_render_links(seg, None, tail)
                continue
            owner = ljbt_cframe_owner(ljbt_cf_raw(seg.cf)) if seg.cf \
                else None
            mark = ljbt_info if truncated else ljbt_warn
            tail.append(mark(
                'Lua frames of L=0x{:x} cframe={} not matched to this C '
                'stack{}:'.format(
                    L, '0x{:x}'.format(ljbt_cf_raw(seg.cf))
                    if seg.cf else 'none',
                    ' (the cframe is on the C stack of {})'.format(owner)
                    if owner else ' (its VM entry is below the -n limit)'
                    if truncated else '')))
            ljbt_render_links(seg, None, tail)
    LJBT_STATE['states'] = states
    return tail


def ljbt_render_trace(tfr, st, seg):
    '''Inlined frames of the trace at tfr; returns the (line, note) for
    the trace's base frame (the newest frame of seg).'''
    t = tfr.trace
    sn = t.snapshot(tfr.pc)
    if sn is None:
        tfr.annot.append('!! no snapshot covers this pc')
        return None
    tfr.annot.append('snapshot #{}'.format(sn))
    try:
        res = t.frames(sn)
    except gdb.error as e:
        tfr.annot.append('!! cannot decode snapshot: {}'.format(e))
        return None
    pc, fr = res
    base_fn = seg.links[0].fn if seg.links else None
    # Bottom-up list of (func, link) with the trace base frame first.
    chain = [(base_fn, None)]
    for slot, func, link, iscont in fr:
        if slot == 0 and func:
            chain[0] = (LJBTFunc(func), None)
            continue
        chain.append((LJBTFunc(func) if func else None, link, iscont))
    lines = []
    # Newest first: the innermost frame runs at the snapshot pc.
    cur_pc = pc
    cur_exact = True
    for k in range(len(chain) - 1, -1, -1):
        ent = chain[k]
        fn = ent[0]
        if k == 0:
            break
        iscont = ent[2]
        if iscont:
            # Continuation slot: its link is the cont PC into the caller.
            cur_pc, cur_exact = ent[1], False
            continue
        if fn is None:
            lines.append({
                'kind': 'lua', 'fkind': None, 'func': None, 'chunk': None,
                'defline': None, 'currentline': None, 'ftype': None,
                'how': [], 'note': 'function not a constant',
                'trace': t.no, 'snapshot': sn, 'inlined': True,
                'L': '0x{:x}'.format(st.addr), 'link': None,
                'text': '[lua] <inlined frame, function not a constant>'
                        '  (trace {}, snapshot #{})'.format(t.no, sn)})
        else:
            line = None
            if fn.kind == 'lua' and cur_pc is not None:
                line = fn.proto.line_pc(cur_pc) if cur_exact else \
                    fn.proto.line_retpc(cur_pc)
            note = 'inlined in trace {}, snapshot #{}'.format(t.no, sn)
            lines.append(ljbt_lua_entry(
                fn, line, note, [], None, st.addr, fn.describe(line, note),
                trace=t.no, snapshot=sn, inlined=True))
        link = ent[1]
        if link is not None and (link & 3) == 0:
            cur_pc, cur_exact = link, False
        else:
            cur_pc = None
    tfr.lua.extend(lines)
    # The trace tail-called inside itself: the frame slot in memory still
    # holds the old function, the snapshot has the running one.
    base = chain[0][0]
    override = base if base is not None and base_fn is not None and \
        base.addr != base_fn.addr else None
    if base is not None and base.kind == 'lua' and cur_pc is not None:
        line = base.proto.line_pc(cur_pc) if cur_exact else \
            base.proto.line_retpc(cur_pc)
        return (line, 'trace {} base frame, snapshot #{}'.format(t.no, sn),
                override)
    return (None, 'trace {} base frame, snapshot #{}'.format(t.no, sn),
            override)


def ljbt_main_L():
    sym = gdb.lookup_global_symbol('tarantool_L') or \
        gdb.lookup_static_symbol('globalL')
    try:
        return int(sym.value()) if sym else 0
    except gdb.error:
        return 0


def ljbt_cframe_owner(addr):
    '''The fiber whose C stack holds addr (needs tarantool_gdb.py).'''
    Cord = getattr(ljbt_lj, 'Cord', None)
    if Cord is None:
        return None
    try:
        for f in Cord().fibers():
            s = int(f['stack'])
            if s and s <= addr < s + int(f['stack_size']):
                return ljbt_fiber_desc(f)
    except gdb.error:
        pass
    return None


def ljbt_L_owner(L):
    '''", main L" / ", fiber 104 "name"" for a lua_State, if known.'''
    if L == ljbt_main_L():
        return ', main L'
    owners = LJBT_STATE.get('owners')
    if owners is None:
        owners = {}
        Cord = getattr(ljbt_lj, 'Cord', None)
        if Cord is not None:
            try:
                for f in Cord().fibers():
                    fl = ljbt_fiber_L(f)
                    if fl:
                        owners[fl] = ljbt_fiber_desc(f)
            except gdb.error:
                pass
        LJBT_STATE['owners'] = owners
    if L in owners:
        return ', ' + owners[L]
    return ''


# Command. #######################################################


def ljbt_default_L(arg_L):
    '''(L, header, is another fiber selected) for the Lua side.'''
    c = ljbt_conf()
    sel, run = ljbt_fiber_selection()
    other_fiber = sel is not None and run is not None and \
        int(sel) != int(run)
    defL, src = None, None
    if arg_L:
        defL, src = arg_L, 'argument'
        if not c.G:
            c.G = int(ljbt_lj.G(ljbt_cast('lua_State *', defL)))
            LJBT_STATE['traces'] = None
    elif other_fiber:
        # A fiber made from C or Rust has none: what Lua it runs is in the
        # VM entries of its C stack.
        defL = ljbt_fiber_L(sel)
        src = ('storage.lua.stack of ' if defL else
               'no storage.lua.stack in ') + ljbt_fiber_desc(sel)
    elif c.G:
        defL, src = ljbt_cur_L(c.G), 'G->cur_L'
    hdr = 'lj-bt: L=0x{:x} ({})'.format(defL or 0, src or 'none')
    if c.G:
        g = ljbt_cast('global_State *', c.G)
        hdr += ', G=0x{:x}, vmstate={}'.format(c.G, ljbt_vmstate(g))
    if sel is not None:
        hdr += ', ' + ljbt_fiber_desc(sel) + (
            ' (not running)' if other_fiber else ' (running)')
    return defL, hdr, other_fiber


class LJBTKeepFrame(object):
    '''with LJBTKeepFrame(): gdb.invalidate_cached_frames drops the frame
    selection (frame #0 is selected again); the frame selected before the
    block, by its level, is selected again on the way out.  Inside
    LJBTLangC, so that `set language auto` at its end goes by that frame.'''

    def __enter__(self):
        try:
            self.level = gdb.selected_frame().level()
        except gdb.error:
            self.level = None
        return self

    def __exit__(self, *exc):
        if not self.level:
            return False
        try:
            f = gdb.newest_frame()
            for _ in range(self.level):
                f = f.older() if f is not None else None
            if f is not None:
                f.select()
        except gdb.error:
            pass
        return False


@ljbt_in_c
def ljbt_backtrace(arg_L, limit, out, as_json=False):
    with LJBTKeepFrame():
        ljbt_render(arg_L, limit, out, as_json)


def ljbt_render(arg_L, limit, out, as_json):
    defL, hdr, other_fiber = ljbt_default_L(arg_L)
    gdb.invalidate_cached_frames()
    cframes, unotes = ljbt_walk_cstack(other_fiber, limit)
    notes = ['-- ' + n for n in ljbt_conf().info]
    tail = ljbt_stitch(cframes, defL, notes,
                       truncated=LJBT_MORE_FRAMES in unotes)
    notes += unotes
    if as_json:
        out.append(ljbt_json(defL, hdr, cframes, notes, tail))
        return
    out.append(hdr)
    out.extend(n for n in notes if n not in unotes)
    for cfr in cframes:
        s = '#{:<4} {}'.format(cfr.level, cfr.text())
        if cfr.annot:
            s += '  ' + '  '.join(cfr.annot)
        out.append(s)
        n = 0
        for e in cfr.lua:
            if e['kind'] in ('warn', 'info'):
                out.append('      ' + e['text'])
                continue
            n += 1
            out.append('#{:<4} {}'.format('{}.{}'.format(cfr.level, n),
                                          e['text']))
    out.extend(unotes)
    out.extend(e['text'] for e in tail)


def ljbt_json(defL, hdr, cframes, notes, tail):
    '''The --json document: C frames in gdb order, each with the Lua
    frames that run in it ("lua"), the folded Lua C function ("folded")
    and its "!!" marks ("warnings").'''
    c = ljbt_conf()
    frames = []
    prev = None
    for cfr in cframes:
        f = {'kind': cfr.kind, 'level': cfr.level, 'name': cfr.name,
             'pc': '0x{:x}'.format(cfr.pc),
             'inline': cfr.type == gdb.INLINE_FRAME,
             'text': cfr.text(), 'file': None, 'line': None}
        if cfr.kind == 'c':
            try:
                sal = cfr.frame.find_sal()
                if sal.symtab is not None:
                    f['file'], f['line'] = sal.symtab.filename, sal.line
            except gdb.error:
                pass
        if cfr.kind == 'vm':
            f.update(L='0x{:x}'.format(cfr.L), cframe='0x{:x}'.format(cfr.cf),
                     coroutine=getattr(cfr, 'coroutine', False),
                     synthetic=prev is not None and prev.kind == 'trace')
        if cfr.kind == 'trace':
            f.update(trace=cfr.trace.no, root=cfr.trace.root,
                     start=cfr.trace.start())
        f['folded'] = getattr(cfr, 'folded', None)
        f['warnings'] = [a for a in cfr.annot if a.startswith('!!')] + \
            [e['text'] for e in cfr.lua if e['kind'] == 'warn']
        f['info'] = [e['text'] for e in cfr.lua if e['kind'] == 'info']
        f['lua'] = [e for e in cfr.lua if e['kind'] not in ('warn', 'info')]
        frames.append(f)
        prev = cfr
    states = []
    for L, st in (LJBT_STATE.get('states') or {}).items():
        links, hidden = 0, 0
        for seg in st.segments:
            skip = False
            for link in seg.links:
                links += 1
                if skip or link.fnaddr == L:
                    hidden += 1
                skip = not link.islua and link.typep == LJBT_FT_VARG
        states.append({
            'L': '0x{:x}'.format(L), 'links': links, 'hidden': hidden,
            'top': '0x{:x}'.format(st.topframe),
            'base': '0x{:x}'.format(st.base),
            'topnote': st.topnote if isinstance(st.topnote, str) else
            ('regs' if st.topnote else None),
            'broken': st.broken is not None,
            'empty_top': st.empty_top is not None,
            'notes': st.notes})
    doc = {'header': hdr, 'L': '0x{:x}'.format(defL) if defL else None,
           'G': '0x{:x}'.format(c.G) if c.G else None,
           'vmstate': ljbt_vmstate(ljbt_cast('global_State *', c.G))
           if c.G else None,
           'notes': notes, 'frames': frames, 'tail': tail, 'states': states}
    return json.dumps(doc)


def ljbt_vmstate(g):
    try:
        v = int(g['vmstate'])
    except gdb.error:
        return '?'
    if v >= 0:
        if ljbt_conf().arch == 'arm64':
            # BC_JLOOP stores 0: arm64 traces keep no number in vmstate.
            return 'TRACE'
        return 'TRACE {}'.format(v)
    try:
        return ljbt_lj.vm_state(g)
    except Exception:
        return str(v)


class LJBTCommand(gdb.Command):
    '''lj-bt [-v] [-f] [-n N] [--all-fibers] [--tail-chain] [--no-tail] [<L>]

Print one backtrace with C and Lua frames interleaved in the order of
the real call nesting.  C frames keep gdb's frame numbers; the Lua
frames that run inside a VM frame #N are numbered #N.1, #N.2, ...

By default the Lua side is the current coroutine (G->cur_L); after
`tt-fiber <id>` (tarantool_gdb.py) it is the selected fiber.
Lines starting with "!!" mark inconsistencies between the two stacks.

  -v             show frame link addresses and types
  -f             full source paths
  -n N           at most N C frames (default 256)
  --all-fibers   lj-bt for every fiber of the current cord
  --tail-chain   guess the frames between a tail-calling function and the
                 running one from the CALLT instructions of its bytecode
  --no-tail      no tail call heuristic
  --json         one JSON document instead of text (see ljbt_json)
  -o FILE        append the output to FILE instead of printing it

Tail calls (BC_CALLT) leave no frame.  Where the CALL below a frame
called something still known (a global, an upvalue, a field of such a
table, a live local) and that is not the running function, a line
"(tail call from <name> = <chunk:line>)  [heuristic]" follows the frame.
With -v the frames whose callee cannot be verified say so.
'''

    def __init__(self):
        super(LJBTCommand, self).__init__('lj-bt', gdb.COMMAND_STACK)

    @ljbt_in_c
    def invoke(self, arg, from_tty):
        argv = gdb.string_to_argv(arg)
        limit = 256
        allf = False
        as_json = False
        outfile = None
        LJBT_OPTS['verbose'] = False
        LJBT_OPTS['full'] = False
        LJBT_OPTS['tail_chain'] = False
        LJBT_OPTS['no_tail'] = False
        rest = []
        i = 0
        while i < len(argv):
            a = argv[i]
            if a == '-v':
                LJBT_OPTS['verbose'] = True
            elif a == '--tail-chain':
                LJBT_OPTS['tail_chain'] = True
            elif a == '--no-tail':
                LJBT_OPTS['no_tail'] = True
            elif a == '-f':
                LJBT_OPTS['full'] = True
            elif a == '-n':
                i += 1
                if i >= len(argv) or not argv[i].isdigit() or \
                        int(argv[i]) < 1:
                    raise gdb.GdbError('lj-bt: -n needs a positive number')
                limit = int(argv[i])
            elif a == '-o' and i + 1 >= len(argv):
                raise gdb.GdbError('lj-bt: -o needs a file')
            elif a == '--all-fibers':
                allf = True
            elif a == '--json':
                as_json = True
            elif a == '-o':
                i += 1
                outfile = argv[i]
            else:
                rest.append(a)
            i += 1
        ljbt_reset()
        arg_L = int(ljbt_eval(' '.join(rest))) if rest else None
        out = []
        if allf:
            self.all_fibers(limit, out, as_json)
        else:
            ljbt_backtrace(arg_L, limit, out, as_json)
        text = '\n'.join(out) + '\n'
        if outfile:
            with open(outfile, 'a') as fh:
                fh.write(text)
        else:
            gdb.write(text)

    def all_fibers(self, limit, res, as_json):
        FU = getattr(ljbt_lj, 'FiberUnwinder', None)
        Cord = getattr(ljbt_lj, 'Cord', None)
        if FU is None or Cord is None:
            raise gdb.GdbError('lj-bt --all-fibers: source '
                               'tarantool_gdb.py first')
        u = FU.instance()
        orig = u.fiber()
        docs = []
        keep = LJBTKeepFrame().__enter__()
        try:
            # Every fiber, also one with no storage.lua.stack: a fiber made
            # from C or Rust may run Lua in a lua_State of its own (or in
            # the one of a stored procedure), and it has a C stack anyway.
            for f in list(Cord().fibers()):
                u.set_fiber(f)
                gdb.invalidate_cached_frames()
                if as_json:
                    out = []
                    try:
                        ljbt_backtrace(None, limit, out, True)
                        doc = out[0]
                    except gdb.error as e:
                        doc = 'null, "error": {}'.format(json.dumps(str(e)))
                    docs.append('{{"fiber": {}, "bt": {}}}'.format(
                        json.dumps(ljbt_fiber_desc(f)), doc))
                    continue
                res.extend(['', '=== ' + ljbt_fiber_desc(f)])
                try:
                    ljbt_backtrace(None, limit, res)
                except gdb.error as e:
                    res.append('!! {}'.format(e))
        finally:
            u.set_fiber(orig)
            gdb.invalidate_cached_frames()
            keep.__exit__()
        if as_json:
            res.append('[' + ', '.join(docs) + ']')


# Frame filter: the same stitched view in plain `bt`. ##############
# Lua frames are printed as elided frames under the VM frame they run
# in (gdb prints them indented, with the level of that VM frame).
# `disable frame-filter global lj-bt` turns it off.

try:
    from gdb.FrameDecorator import FrameDecorator as LJBTFrameDecorator
except ImportError:
    LJBTFrameDecorator = None


if LJBTFrameDecorator is not None:
    class LJBTLuaFrame(LJBTFrameDecorator):
        def __init__(self, frame, text):
            super(LJBTLuaFrame, self).__init__(frame)
            self.ljbt_text = text

        def function(self):
            return self.ljbt_text

        def address(self):
            return None

        def filename(self):
            return None

        def line(self):
            return None

        def frame_args(self):
            return None

        def frame_locals(self):
            return None

        def elided(self):
            return None

    class LJBTDecorated(LJBTFrameDecorator):
        def __init__(self, base, cfr):
            super(LJBTDecorated, self).__init__(base)
            self.ljbt_base = base
            self.ljbt_cfr = cfr

        def function(self):
            cfr = self.ljbt_cfr
            if cfr.kind == 'trace':
                return cfr.text().split(' in ', 1)[1]
            return self.ljbt_base.function()

        def elided(self):
            cfr = self.ljbt_cfr
            sub = [LJBTLuaFrame(cfr.frame, t if isinstance(t, str)
                                else t['text'])
                   for t in cfr.annot + cfr.lua]
            orig = self.ljbt_base.elided()
            if orig is not None:
                sub.extend(orig)
            return iter(sub) if sub else None

    class LJBTFrameFilter(object):
        def __init__(self):
            self.name = 'lj-bt'
            self.priority = 50      # After TtFiberUnwinderFrameFilter.
            self.enabled = True
            gdb.frame_filters[self.name] = self

        @ljbt_in_c
        def filter(self, frame_iter):
            decos = []
            truncated = False
            for d in frame_iter:
                decos.append(d)
                if len(decos) >= 512:
                    truncated = True
                    break
            saved = dict(LJBT_OPTS)
            LJBT_OPTS.update(verbose=False, full=False, tail_chain=False,
                             no_tail=False)
            try:
                c = ljbt_conf()
                if not c.G:
                    return iter(decos)
                defL, hdr, other = ljbt_default_L(None)
                cframes = [LJBTCFrame(d.inferior_frame()) for d in decos]
                notes = ['-- ' + n for n in c.info]
                tail = ljbt_stitch(cframes, defL, notes, truncated)
                res = [LJBTDecorated(d, cfr)
                       for d, cfr in zip(decos, cframes)]
                if res:
                    res[0].ljbt_cfr.annot[:0] = notes
                    res[-1].ljbt_cfr.lua.extend(tail)
                res.extend(frame_iter)
                return iter(res)
            except Exception as e:
                # Say it once (no symbols, say, would repeat it on every
                # bt), then leave bt as it is.
                msg = 'lj-bt frame filter: {}'.format(e)
                if msg != LJBT_STATE.get('filter_error'):
                    gdb.write(msg + ' (plain bt follows)\n')
                    LJBT_STATE['filter_error'] = msg
                return iter(decos + list(frame_iter))
            finally:
                LJBT_OPTS.clear()
                LJBT_OPTS.update(saved)

    LJBTFrameFilter()


LJBTCommand()
# Sourced at a stop: that stop's fiber is the one a later stop compares to.
LJBT_STATE['fiber'] = ljbt_running_fiber()
gdb.events.stop.connect(ljbt_on_stop)
gdb.events.exited.connect(ljbt_reset)
gdb.events.new_objfile.connect(ljbt_reset)
if LJBT_HAVE_UNWINDER:
    gdb.unwinder.register_unwinder(None, LJBTTraceUnwinder(), replace=True)
gdb.write('lj-bt command initialized\n')
