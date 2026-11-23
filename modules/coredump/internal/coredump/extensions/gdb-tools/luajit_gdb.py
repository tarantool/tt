# Debug extension for LuaJIT post-mortem analysis.
# The universal one of github.com/buyemune-sys/tarantool-gdb-tools: for
# every Tarantool 3.x.  From src/luajit_dbg.py of tarantool/luajit, MIT
# license (Copyright (C) 2005-2017 Mike Pall, COPYRIGHT of LuaJIT); see
# NOTICE of that repository.
# To use in GDB:
# `source <path-to-repo>/luajit_gdb.py'
# To use in LLDB (the file name is the module name: no dots or dashes):
# `command script import <path-to-repo>/luajit_gdb.py'

import abc
import heapq
import re
import struct
import sys

from importlib import import_module

# Make the script compatible with ancient Python.
LEGACY = re.match(r'^2\.', sys.version)

if LEGACY:
    int = long  # noqa: F821
    range = xrange  # noqa: F821


# Debugger. ######################################################


lldb = None
gdb = None

# XXX: While the `gdb` library is only available inside a debug
# session, the `lldb` library can be loaded in any Python script.
# To address that, we need to perform an additional check to
# ensure a debug session is actually running.
debuggers = {
    'gdb': lambda lib: True,
    'lldb': lambda lib: lib.debugger is not None,
}
for name, healthcheck in debuggers.items():
    lib = None
    try:
        lib = import_module(name)
        if healthcheck(lib):
            globals()[name] = lib
            break
    except Exception:
        continue

assert (not not lldb) != (not not gdb), 'Debugger must be either LLDB or GDB.'


class Debugger(object):
    def __init__(self):
        self.dbgtype_cache = {}

    def __new__(self):
        if gdb:
            self.GDB = True
            return super(Debugger, self).__new__(_GDBDebugger)
        elif lldb:
            self.LLDB = True
            return super(Debugger, self).__new__(_LLDBDebugger)

    def parse_flags(self, raw_flags, permitted_flags):
        flags = {}
        for flag in raw_flags:
            if flag not in permitted_flags:
                raise self.error('Unrecognized option: "{}"'.format(flag))
            flags[flag] = True
        return flags

    def extract_flags(self, arg, permitted_flags):
        if not arg:
            return None, None
        flags = {}
        if arg.startswith('/'):
            match = re.match(r'/(\S*)\s+(.*)$', arg)
            if not match:
                return arg, flags
            raw_flags, arg = match.group(1, 2)
            flags = self.parse_flags(raw_flags, permitted_flags)
        return arg, flags

    def configure(self):
        global PADDING, LJ_TISNUM, LJ_64, LJ_FR2, LJ_GC64
        if not self.check_libluajit():
            return False
        try:
            self.init_luajit_arch()
            if LJ_64 is None or LJ_GC64 is None:
                # No IRType in the debug info (see ENUM_FALLBACK): the
                # sizes of a pointer and of a GC reference tell the same.
                LJ_64 = self.sizeof('GCobj *') == 8
                LJ_FR2 = LJ_GC64 = self.sizeof('GCRef') == 8
            PADDING = ' ' * len(':' + hex((1 << (47 if LJ_GC64 else 32)) - 1))
            LJ_TISNUM = 0xfffeffff if LJ_64 and not LJ_GC64 else LJ_T['NUMX']
        except Exception:
            self.write('luajit_gdb.py failed to load: '
                       'no debugging symbols found for libluajit\n')
            return False

        # Setup arch.
        try:
            self.arch = str(self.eval('LJ_ARCH_NAME')).split('"')[1]
        except Exception:
            try:
                self.arch = self.detect_arch()
            except Exception:
                # Setup on demand if necessary.
                pass

        return True

    def initialize_extension(self, commands):
        if self.configure():
            for name, command in commands.items():
                if hasattr(command, 'pre_register'):
                    command.pre_register()
                self.register_command(command, name)
                self.write('{} command initialized\n'.format(name))
            self.write('LuaJIT debug extension is successfully loaded\n')

    def print(self, msg):
        '''Print the message with the new line.'''
        self.write(msg + '\n')

    @abc.abstractmethod
    def cast(self, typestr, val):
        '''Cast the value to the required C type.'''
        pass

    @abc.abstractmethod
    def cast_typeof(self, ref_val, val):
        '''Cast the value to the type of the given object
        It is used mostly for casting when the string name of the type isn't
        accessible (for example, anonymous enums).'''
        pass

    @abc.abstractmethod
    def sizeof(self, typestr):
        '''Return the size of the given type in bytes.'''
        pass

    @abc.abstractmethod
    def offsetof(self, typestr, fieldstr):
        '''Return the offset of the given field in the type in bytes.'''
        pass

    @abc.abstractmethod
    def cstr(self, strptr):
        '''Return the content of the string by the given pointer.'''
        pass

    @abc.abstractmethod
    def address(self, obj):
        '''Return the address in memory of the given object.'''
        pass

    @abc.abstractmethod
    def lookup_global(self, symbol):
        '''Look up the global C symbol by the given name.'''
        pass

    @abc.abstractmethod
    def member_by_offset(self, typename, offset, prev_name=None):
        '''Look up the global C symbol by the given name.'''
        pass

    @abc.abstractmethod
    def eval(self, expr):
        '''Parse and evaluate the given debugger expression.'''
        pass

    @abc.abstractmethod
    def detect_arch(self):
        '''Detect the CPU architecture and canonicalize it to the LuaJIT
        notation.'''
        pass

    @abc.abstractmethod
    def write(self, msg):
        '''Print the message.'''
        pass

    @abc.abstractmethod
    def error(self, msg):
        '''Create the error object with message.'''
        pass

    @abc.abstractmethod
    def check_libluajit(self):
        '''Check that libluajit is loaded.
        Check that the object file with libluajit symbols is loaded.
        Postpone loading of the extension if needed.
        '''
        pass

    @abc.abstractmethod
    def init_luajit_arch(self):
        '''Initialize LuaJIT architecture-specific globals.
        Initialize build-dependent global constants.
        If no debugging symbols are found raise an error.
        '''
        pass

    @abc.abstractmethod
    def register_command(self, command, name):
        '''Register the command with the corresponding name.'''
        pass

    # Creating the value of the given enum member is quite
    # different in GDB and LLDB, so this method is needed to hide
    # the implementation details.
    @abc.abstractmethod
    def create_enum_value(self, enum_member_name, enum_name=None):
        '''Return debugger-specific value object that represents
        the given enum member.
        '''
        pass

    @abc.abstractproperty
    def LJBase(self):
        '''Base command class.
        Provides the base class for the extension commands.
        '''


class _GDBDebugger(Debugger):
    def _dbgtype(self, typestr):
        if typestr in self.dbgtype_cache:
            return self.dbgtype_cache[typestr]

        m = re.match(r'((?:(?:struct|union) )?\S*)\s*[*]', typestr)

        dbgtype = gdb.lookup_type(typestr) if m is None \
            else gdb.lookup_type(m.group(1)).pointer()

        self.dbgtype_cache[typestr] = dbgtype
        return dbgtype

    def __init__(self):
        super(_GDBDebugger, self).__init__()
        self.CONNECTED = False

    def cast(self, typestr, val):
        return gdb.Value(val).cast(self._dbgtype(typestr))

    def cast_typeof(self, ref_val, val):
        return gdb.Value(val).cast(ref_val.type)

    def sizeof(self, typestr):
        return self._dbgtype(typestr).sizeof

    def offsetof(self, typestr, fieldstr):
        return int(self._dbgtype(typestr)[fieldstr].bitpos / 8)

    def cstr(self, strptr):
        # A string is printed with a pointer to it. Just strip it.
        return re.sub(r'^0x[a-f0-9]+\s+(?=")', '', str(strptr))

    def address(self, obj):
        return obj.address

    def lookup_global(self, symbol):
        variable, _ = gdb.lookup_symbol(symbol)
        return variable.value() if variable else None

    def member_by_offset(self, tp, offset, prev_name=None):
        if isinstance(tp, str):
            tp = self._dbgtype(tp)
        assert offset < tp.sizeof, 'offset is bigger than object size'
        if tp.code == gdb.TYPE_CODE_TYPEDEF:
            tp = tp.strip_typedefs()
        if tp.code == gdb.TYPE_CODE_STRUCT:
            fields = tp.fields()
            for n_field in range(len(fields)):
                islast = n_field == (len(fields) - 1)
                field = fields[n_field]
                start_field = field.bitpos / 8
                end_field = fields[n_field + 1].bitpos / 8 if not islast \
                    else tp.sizeof
                if start_field <= offset and offset < end_field:
                    next_name = self.member_by_offset(
                        field.type,
                        offset - start_field,
                        prev_name=field.name
                    )
                    return '.{field}{suffix}'.format(
                        field=field.name,
                        suffix=next_name if next_name else ''
                    )
        elif tp.code == gdb.TYPE_CODE_ARRAY:
            # Get array field type.
            target = tp.target()
            tsize = target.sizeof
            idx = int(offset // tsize)
            next_name = self.member_by_offset(target, offset - idx * tsize)
            idxname = idx_name(prev_name)
            if idxname and idx in idxname:
                idx = idxname[idx]
            return '[{}]{}'.format(idx, next_name if next_name else '')
        else:
            return None

    def eval(self, expr):
        return gdb.parse_and_eval(expr)

    def detect_arch(self):
        if hasattr(self, 'arch'):
            return self.arch
        target = str(gdb.execute('info target', False, True))
        if re.match('.*x86-64.*', target, flags=re.DOTALL):
            return 'x64'
        elif re.match('.*aarch64.*', target, flags=re.DOTALL):
            return 'arm64'
        else:
            return ''

    def write(self, msg):
        gdb.write(msg)

    def error(self, errmsg):
        return gdb.GdbError(errmsg)

    def check_libluajit(self):
        # XXX Fragile: Though connecting the callback looks bad,
        # it respects both Python 2 and Python 3 (see #4828).
        def connect(callback):
            if LEGACY:
                self.CONNECTED = True
            gdb.events.new_objfile.connect(callback)

        # XXX Fragile: Though disconnecting the callback looks
        # bad, it respects both Python 2 and Python 3 (see #4828).
        def disconnect(callback):
            if LEGACY:
                if not self.CONNECTED:
                    return
                self.CONNECTED = False
            gdb.events.new_objfile.disconnect(callback)

        try:
            # Try to remove the callback at first to not append
            # duplicates to gdb.events.new_objfile internal list.
            disconnect(load)
        except Exception:
            # Callback is not connected.
            pass

        try:
            # Detect whether libluajit objfile is loaded.
            gdb.parse_and_eval('luaJIT_setmode')
        except Exception:
            gdb.write('luajit_gdb.py initialization is postponed '
                      'until libluajit objfile is loaded\n')
            # Add a callback to be executed when the next objfile
            # is loaded.
            connect(load)
            return False
        return True

    def init_luajit_arch(self):
        global LJ_64, LJ_DUALNUM, LJ_FR2, LJ_GC64
        try:
            LJ_64 = str(gdb.parse_and_eval('IRT_PTR')) == 'IRT_P64'
            LJ_FR2 = LJ_GC64 = str(gdb.parse_and_eval('IRT_PGC')) == 'IRT_P64'
        except gdb.error:
            pass  # No IRType in the debug info, see configure().
        LJ_DUALNUM = gdb.lookup_global_symbol('lj_lib_checknumber') is not None

    def register_command(self, command, name):
        command(name)

    def create_enum_value(self, enum_member_name, enum_name=None):
        val = self.eval(enum_member_name)
        if val.type.code != gdb.TYPE_CODE_ENUM:
            raise self.error('{}: expected enum, got type {}'.format(
                enum_member_name,
                val.type.code,
            ))
        return val

    class LJBase(gdb and gdb.Command or object):
        def __init__(ljbase, name):
            # XXX Fragile: Though the command initialization looks
            # bad, it respects both Python 2 and Python 3.
            gdb.Command.__init__(ljbase, name, gdb.COMMAND_DATA)

        def invoke(ljbase, args, from_tty):
            return ljbase.execute(args)

        @abc.abstractmethod
        def execute(ljbase, args):
            '''Implementation of the command.
            Subclasses override this method to implement the logic of a given
            command, e.g. printing a stack.
            '''

    LJBase = LJBase


class _LLDBDebugger(Debugger):
    def _lldb_tp_isfp(self, tp):
        return tp.GetCanonicalType().GetBasicType() in [
            lldb.eBasicTypeFloat,
            lldb.eBasicTypeDouble,
            lldb.eBasicTypeLongDouble
        ]

    def _lldb_tp_issigned(self, tp):
        return tp.GetCanonicalType().GetBasicType() in [
            lldb.eBasicTypeChar,
            lldb.eBasicTypeSignedChar,
            lldb.eBasicTypeShort,
            lldb.eBasicTypeInt,
            lldb.eBasicTypeLong,
            lldb.eBasicTypeLongLong,
            lldb.eBasicTypeInt128
        ]

    def _lldb_tp_isenum(self, tp):
        return tp.GetCanonicalType().GetTypeClass() == \
            lldb.eTypeClassEnumeration

    def _lldb_value_from_raw(self, raw_value, size, tp):
        isfp = self._lldb_tp_isfp(tp)
        if isfp:
            pack_flag = '<d'
        elif self._lldb_tp_issigned(tp):
            pack_flag = '<q'
        else:
            pack_flag = '<Q'
            # Cast to 64-bit unsigned value in Python.
            raw_value &= 0xffffffffffffffff
        raw_data = struct.pack(pack_flag, raw_value)
        sbdata = lldb.SBData()
        sbdata.SetData(
            lldb.SBError(),
            raw_data,
            lldb.eByteOrderLittle,
            size
        )
        sbval_res = self.target.CreateValueFromData(
            # XXX: Name is required, let's make it meaningful.
            '({tp}){val}'.format(
                tp=tp.name,
                val=raw_value if isfp else hex(raw_value)
            ),
            sbdata,
            tp
        )
        return lldb.value(sbval_res)

    def __init__(self):
        def lldb__add__(lldbval, other):
            other = int(other)
            sbvalue = lldbval.sbvalue
            if sbvalue.TypeIsPointerType():
                tp = sbvalue.GetType()
                sz = sbvalue.deref.size
                addr = sbvalue.GetValueAsUnsigned() + other * sz
                return self._lldb_value_from_raw(
                    addr, sbvalue.GetByteSize(), tp
                )
            else:
                return int(lldbval) + other

        def lldb__bool__(lldbval):
            return int(lldbval) != 0

        def lldb__ge__(lldbval, other):
            return int(lldbval) >= int(other)

        def lldb__getitem__(lldbval, key):
            if type(key) is lldb.value:
                key = int(key)
            if type(key) is bool:
                key = int(key)
            if type(key) is int:
                # Allow array access.
                ltp = lldbval.sbvalue.GetType()
                # XXX: LLDB in versions 17 - 19 can't use an array
                # object as the initializer for `lldb.value` since
                # `GetValue()` for it returns `None` leading to
                # the invalid result. See
                # https://github.com/llvm/llvm-project/pull/90144.
                if (self.version < 17 or self.version > 19) or \
                   ltp.GetTypeClass() != lldb.eTypeClassArray:
                    pass
                elif lldbval.sbvalue.GetLoadAddress() == \
                        lldb.LLDB_INVALID_ADDRESS:
                    # Data, not memory: a table of the file on a core
                    # (_from_file()).
                    return lldb.value(lldbval.sbvalue.GetChildAtIndex(key))
                else:
                    ptr_tp = ltp.GetArrayElementType().GetPointerType()
                    lldbval = self._lldb_value_from_raw(
                        lldbval.sbvalue.GetLoadAddress(),
                        ptr_tp.GetByteSize(),
                        ptr_tp
                    )
                if key >= 0 and not lldbval.sbvalue.TypeIsPointerType():
                    return lldb.value(
                        lldbval.sbvalue.GetValueForExpressionPath('[%i]' % key)
                    )
                else:
                    # GetValueForExpressionPath doesn't work for
                    # negative offsets.
                    sbvalue = lldbval.sbvalue
                    assert sbvalue.TypeIsPointerType(), \
                        'attempt to get index of non-pointer type'
                    tp = sbvalue.GetType().GetPointeeType()
                    sz = sbvalue.deref.size
                    addr = sbvalue.GetValueAsUnsigned() + key * sz
                    return lldb.value(self.target.CreateValueFromAddress(
                            '({tp}){addr}'.format(tp=tp, addr=addr),
                            lldb.SBAddress(addr, self.target),
                            tp,
                    ))
            elif type(key) is str:
                return lldb.value(lldbval.sbvalue.GetChildMemberWithName(key))
            raise Exception(TypeError('No item of type %s' % str(type(key))))

        def lldb__gt__(lldbval, other):
            return int(lldbval) > int(other)

        def lldb__index__(lldbval):
            return int(lldbval)

        def lldb__le__(lldbval, other):
            return int(lldbval) <= int(other)

        def lldb__lt__(lldbval, other):
            return int(lldbval) < int(other)

        def lldb__or__(lldbval, other):
            return int(lldbval) | int(other)

        def lldb__str__(lldbval):
            # Instead of default GetSummary.
            if not lldbval.sbvalue.TypeIsPointerType():
                tp = lldbval.sbvalue.GetType()
                if self._lldb_tp_isfp(tp) or self._lldb_tp_isenum(tp):
                    return lldbval.sbvalue.GetValue()
                else:
                    return str(int(lldbval))

            s = lldbval.sbvalue.GetValue()
            if s[:2] == '0x':
                # Strip useless leading zeros.
                res = s[2:].lstrip('0')
                return '0x' + (res if res else '0')
            return s

        def lldb__sub__(lldbval, other):
            if type(other) is not lldb.value or \
               type(other) is lldb.value and \
               not other.sbvalue.TypeIsPointerType():
                other = int(other)
            if type(other) is int:
                return lldb__add__(lldbval, -other)
            elif lldbval.sbvalue.TypeIsPointerType():
                sbval = lldbval.sbvalue
                osbval = other.sbvalue
                lldbval_tp = sbval.GetType()
                other_tp = osbval.GetType()
                # Subtract pointers of the same size only.
                elsz = lldbval_tp.GetPointeeType().size
                if other_tp.GetPointeeType().size != elsz:
                    raise Exception(
                        'Attempt to substruct {otp} from {stp}'.format(
                            stp=lldbval_tp.name,
                            otp=other_tp.name
                        )
                    )
                diff = sbval.GetValueAsUnsigned() - osbval.GetValueAsUnsigned()
                return int(diff / elsz)
            else:
                return int(lldbval) - int(other)

        super(_LLDBDebugger, self).__init__()
        self.target = lldb.debugger.GetSelectedTarget()
        self._primed = set()    # processes, see _prime_allocations()
        # Monkey-patch the lldb.value class.
        lldb.value.__add__ = lldb__add__
        lldb.value.__bool__ = lldb__bool__
        lldb.value.__ge__ = lldb__ge__
        lldb.value.__getitem__ = lldb__getitem__
        lldb.value.__gt__ = lldb__gt__
        lldb.value.__index__ = lldb__index__
        lldb.value.__le__ = lldb__le__
        lldb.value.__lt__ = lldb__lt__
        lldb.value.__or__ = lldb__or__
        lldb.value.__ror__ = lldb__or__  # Same semantics.
        lldb.value.__str__ = lldb__str__
        lldb.value.__sub__ = lldb__sub__

        def lldb_major_version():
            version_string = lldb.SBDebugger.GetVersionString()
            match = re.search(r'(\d+)', version_string)
            if match:
                return int(match.group(1))
            return None

        # Needed for features detection.
        self.version = lldb_major_version()

    def _dbgtype(self, typestr):
        if typestr in self.dbgtype_cache:
            return self.dbgtype_cache[typestr]

        m = re.match(r'((?:(?:struct|union) )?\S*)\s*[*]', typestr)

        dbgtype = self.target.FindFirstType(typestr) if m is None \
            else self.target.FindFirstType(m.group(1)).GetPointerType()

        self.dbgtype_cache[typestr] = dbgtype
        return dbgtype

    def _cast(self, tp, val):
        if isinstance(val, lldb.value):
            val = val.sbvalue
        elif type(val) is int:
            return self._lldb_value_from_raw(val, tp.GetByteSize(), tp)
        elif not isinstance(val, lldb.SBValue):
            raise Exception(
                'Unexpected cast from type: {t}.'.format(t=type(val))
            )

        # XXX: Simply SBValue.Cast() works incorrectly since it
        # may take the 8 bytes of memory instead of 4, before the
        # cast. Construct the value on the fly.
        if self._lldb_tp_isfp(tp):
            rawval = float(val.GetValue())
        elif self._lldb_tp_issigned(tp):
            rawval = val.GetValueAsSigned()
        else:
            rawval = val.GetValueAsUnsigned()
        return self._lldb_value_from_raw(rawval, val.GetByteSize(), tp)

    def cast(self, typestr, val):
        return self._cast(self._dbgtype(typestr), val)

    def cast_typeof(self, ref_val, val):
        return self._cast(ref_val.sbvalue.type, val)

    def sizeof(self, typestr):
        return self._dbgtype(typestr).GetByteSize()

    def offsetof(self, typestr, fieldstr):
        def _type_member(type_obj, name):
            return next((x for x in type_obj.members if x.name == name), None)

        type_obj = self._dbgtype(typestr)
        member = _type_member(type_obj, fieldstr)
        assert member is not None, 'There is no field {f} in {t}'.format(
            f=fieldstr,
            t=typestr,
        )
        return member.GetOffsetInBytes()

    def cstr(self, strptr):
        return strptr.sbvalue.summary

    def address(self, obj):
        return lldb.value(obj.sbvalue.address_of)

    def _from_file(self, symbol, sbvalue):
        '''A const table (lj_bc_mode, lj_ir_mode) on a core, read from
        the executable.  A core has no read-only file-backed pages,
        neither the kernel's nor gcore's; gdb reads them from the file,
        lldb only when asked (SBTarget.ReadMemory, which still sets the
        error of the core).  None if the core has the table.'''
        err = lldb.SBError()
        sbvalue.GetData().GetUnsignedInt8(err, 0)
        if err.Success():
            return None
        # The type is often `const uint16_t []` of a header, with no
        # size: the symbol has it.
        el = sbvalue.GetType().GetArrayElementType()
        size = sbvalue.GetAddress().GetSymbol().GetSize()
        if not el.GetByteSize() or not size:
            return None
        tp = el.GetArrayType(size // el.GetByteSize())
        raw = self.target.ReadMemory(sbvalue.GetAddress(), tp.GetByteSize(),
                                     err)
        if not raw or len(raw) != tp.GetByteSize():
            return None
        data = lldb.SBData()
        data.SetData(err, raw, self.target.GetByteOrder(),
                     self.target.GetAddressByteSize())
        return lldb.value(self.target.CreateValueFromData(symbol, data, tp))

    def lookup_global(self, symbol):
        sbvalue = self.target.FindFirstGlobalVariable(symbol)
        if sbvalue.IsValid() and sbvalue.GetError().Fail():
            # A __thread variable (tarantool_L since Tarantool 3.7):
            # the target has no thread to read it in ("no thread to
            # evaluate TLS within"), a frame has.  In a frame of
            # assembly (lj_BC_FUNCC, a syscall stub) or of a library
            # without debug info it is "out of scope": the first frame
            # of the thread that has it.
            process = self.target.GetProcess()
            self._prime_allocations(process)
            thread = process.GetSelectedThread()
            for frame in [thread.GetSelectedFrame()] + list(thread):
                v = frame.EvaluateExpression(symbol)
                if v.IsValid() and not v.GetError().Fail():
                    sbvalue = v
                    break
        tp = sbvalue.GetType()
        if tp.GetTypeClass() == lldb.eTypeClassArray and \
           self.target.GetProcess().GetPluginName() != 'gdb-remote':
            rodata = self._from_file(symbol, sbvalue)
            if rodata is not None:
                return rodata
        # XXX: LLDB in versions 17 - 19 can't use an array object
        # as the initializer for `lldb.value` since `GetValue()`
        # for it returns `None` leading to the invalid result.
        # See https://github.com/llvm/llvm-project/pull/90144.
        if (self.version < 17 or self.version > 19) or \
           tp.GetTypeClass() != lldb.eTypeClassArray:
            return lldb.value(sbvalue)
        else:
            ptr_tp = tp.GetArrayElementType().GetPointerType()
            return self._lldb_value_from_raw(
                sbvalue.GetLoadAddress(),
                ptr_tp.GetByteSize(),
                ptr_tp
            )

    def member_by_offset(self, tp, offset, prev_name=None):
        if isinstance(tp, str):
            tp = self._dbgtype(tp)
        assert offset < tp.GetByteSize(), 'offset is bigger than object size'
        tp = tp.GetCanonicalType()
        if tp.GetTypeClass() == lldb.eTypeClassStruct:
            len_fields = tp.GetNumberOfFields()
            for n_field in range(len_fields):
                islast = n_field == (len_fields - 1)
                field = tp.GetFieldAtIndex(n_field)
                start_field = field.GetOffsetInBytes()
                if not islast:
                    end_field = tp.GetFieldAtIndex(
                        n_field + 1
                    ).GetOffsetInBytes()
                else:
                    end_field = tp.GetByteSize()
                if start_field <= offset and offset < end_field:
                    next_name = self.member_by_offset(
                        field.GetType(),
                        offset - start_field,
                        prev_name=field.GetName()
                    )
                    return '.{field}{suffix}'.format(
                        field=field.GetName(),
                        suffix=next_name if next_name else ''
                    )
        elif tp.GetTypeClass() == lldb.eTypeClassArray:
            # Get array field type.
            target = tp.GetArrayElementType()
            tsize = target.GetByteSize()
            idx = int(offset // tsize)
            next_name = self.member_by_offset(target, offset - idx * tsize)
            idxname = idx_name(prev_name)
            if idxname and idx in idxname:
                idx = idxname[idx]
            return '[{}]{}'.format(idx, next_name if next_name else '')
        else:
            return None

    # lldb-server gets the memory of an expression with an mmap that it
    # runs in the inferior, and takes the stop of any pending signal for
    # its end (NativeProcessLinux::Syscall): the address is then the
    # syscall number.  Tarantool's low-resolution clock (an ITIMER_REAL
    # of 10 ms, SIGALRM) keeps one pending in a stopped process.  lldb
    # keeps every page it got, per permission set, for good: allocate
    # until a page is mapped, leave the bad ones reserved in full so
    # that nothing lands there, free the good one for lldb to use.
    _PRIME = (
        (0x1000, 'rw'), (0x1000, 'rx'), (0x1000, 'rwx'), (0x1000, 'r'),
        # IRMemoryMap::FindSpace takes 0x81000 bytes per expression.
        (0x200000, 'rw'),
    )

    def _prime_allocations(self, process):
        if process.GetPluginName() != 'gdb-remote' or \
           process.GetState() != lldb.eStateStopped or \
           process.GetUniqueID() in self._primed:
            return
        self._primed.add(process.GetUniqueID())
        perms = {'r': lldb.ePermissionsReadable,
                 'w': lldb.ePermissionsWritable,
                 'x': lldb.ePermissionsExecutable}
        for size, p in self._PRIME:
            perm = 0
            for c in p:
                perm |= perms[c]
            for _ in range(20):
                err = lldb.SBError()
                addr = process.AllocateMemory(size, perm, err)
                if err.Fail():
                    break
                info = lldb.SBMemoryRegionInfo()
                if process.GetMemoryRegionInfo(addr, info).Success() and \
                   info.IsMapped():
                    process.DeallocateMemory(addr)
                    break

    def eval(self, expr):
        process = self.target.GetProcess()
        self._prime_allocations(process)
        thread = process.GetSelectedThread()
        frame = thread.GetSelectedFrame()
        val = frame.EvaluateExpression(expr)
        # LLDB doesn't throw an exception if it fails to
        # evaluate an expression. In order to simplify handling
        # of `Debugger.eval()` it is convenient to make it work
        # similar to GDB, i.e. throw an exception in case of
        # failure.
        # Be aware that SBValue.IsValid() is necessary, but not
        # sufficient as it only indicates that the value object
        # does contain the relevant data (including the potential
        # error), so it is necessary to check the `error` property
        # as well.
        if not val.IsValid():
            raise self.error('{}: invalid expression'.format(expr))
        if val.error.fail:
            raise self.error(val.error)
        return lldb.value(val)

    def detect_arch(self):
        if hasattr(self, 'arch'):
            return self.arch
        target = self.target.GetTriple().split('-')[0]
        if target == 'x86_64':
            return 'x64'
        elif target == 'arm64' or target == 'aarch64':
            return 'arm64'
        else:
            return ''

    def write(self, msg):
        sys.stdout.write(msg)

    class DbgError(Exception):
        '''Raised to indicate an error during command execution.
        Doesn't cause a traceback to be printed (like gdb.GdbError for GDB).'''
        pass

    def error(self, errmsg):
        return self.DbgError(errmsg)

    def check_libluajit(self):
        # TODO: Implement postpone loading for LLDB too.
        return True

    def init_luajit_arch(self):
        global LJ_64, LJ_DUALNUM, LJ_FR2, LJ_GC64
        IRT_P64 = 9
        module = self.target.modules[0]
        dualnum_sym = module.FindSymbol('lj_lib_checknumber')
        LJ_DUALNUM = dualnum_sym is not None and dualnum_sym.IsValid()
        irtype_enum = self.target.FindFirstType('IRType').enum_members
        for member in irtype_enum:
            if member.name == 'IRT_PTR':
                LJ_64 = member.unsigned & 0x1f == IRT_P64
            if member.name == 'IRT_PGC':
                LJ_FR2 = LJ_GC64 = member.unsigned & 0x1f == IRT_P64

    def register_command(self, command, name):
        command.name = name
        # lldb names the module after the file it imports.
        lldb.debugger.HandleCommand(
            'command script add {o} --class {mod}.{cls} {cmd}'.format(
                o='--overwrite' if self.version >= 14 else '',
                mod=__name__,
                cls=command.__name__,
                cmd=name,
            )
        )

    def create_enum_value(self, enum_member_name, enum_name=None):
        if enum_name is not None:
            # In LLDB an enum member has to be specified in the
            # form: <enum_name>::<enum_member_name>
            expr = enum_name + "::" + enum_member_name
            try:
                val = self.eval(expr)
            except Exception:
                val = None

            if val is not None:
                if not self._lldb_tp_isenum(val.sbvalue.GetType()):
                    raise self.error('{}: expected enum, got type {}'.format(
                        expr,
                        val.sbvalue.GetType().GetTypeClass(),
                    ))
                return val

        # LLDB needs the enum name in the expression above but
        # 1. Enum might be anonymous (like LJ_VMST__MAX)
        # 2. Debugging information about the enum name might be
        #    optimized out if no variable of the given enum type
        #    is declared and its members are only used as the
        #    predefined constants (like in case of the enum
        #    IRFieldID)

        # In these cases the above method doesn't work so trying
        # to discover the enum type by the given enum member.

        def find_enum_type_member(enum_type, enum_member_name):
            # SBTypeEnumMemberList supports members iteration and
            # [] access (both by index and by member name) only
            # starting from lldb-12 so this implementation is used
            # to handle earlier versions.
            members = enum_type.GetEnumMembers()
            for i in range(members.GetSize()):
                item = members.GetTypeEnumMemberAtIndex(i)
                if item.name == enum_member_name:
                    return item
            return None

        for m in self.target.modules:
            for et in m.GetTypes(lldb.eTypeClassEnumeration):
                et_member = find_enum_type_member(et, enum_member_name)
                if et_member is not None:
                    return self._cast(et, et_member.unsigned)

        raise self.error('{}: not found'.format(enum_member_name))

    class LJBase(object):
        # Ignore given parameters by LLDB.
        def __init__(ljbase, debugger, unused):
            pass

        def get_short_help(ljbase):
            return ljbase.__doc__.splitlines()[1]

        def get_long_help(ljbase):
            return ljbase.__doc__

        def __call__(ljbase, debugger, args, exe_ctx, result):
            try:
                ljbase.execute(args)
            except _LLDBDebugger.DbgError as e:
                msg = 'Failed to execute command `{}`: {}'.format(
                    ljbase.name,
                    e
                )
                result.SetError(msg)
                # `result` is an output argument of LLDB command
                # handler. If error message is set, LLDB displays
                # it in session. But such a message is missing in
                # output when the same problem occurs in batch
                # mode, so in order to be able to catch this kind
                # of error in batch mode it is needed to put it
                # into stderr explicitly.
                # Also to avoid duplication of the same error
                # message in interactive mode, it should be done
                # in batch mode only. There is no official way to
                # check if LLDB is in the batch mode so one of the
                # recommended workarounds is used below that
                # checks if tty is not used for standard input.
                if not sys.stdin.isatty():
                    result.PutError(lldb.SBFile.Create(sys.stderr, True))

        @abc.abstractmethod
        def execute(ljbase, args):
            '''Implementation of the command.
            Subclasses override this method to implement the logic of a given
            command, e.g. printing a stack. Any unhandled exception will be
            automatically transformed into proper errors.
            '''

    LJBase = LJBase


dbg = Debugger()


# LuaJIT. ########################################################


# Constants.


LJ_64 = None
LJ_DUALNUM = None
LJ_FR2 = None
LJ_GC64 = None

LJ_GCVMASK = ((1 << 47) - 1)
LJ_TISNUM = None
PADDING = None

# These constants are meaningful only for 'LJ_64' mode.
LJ_LIGHTUD_BITS_SEG = 8
LJ_LIGHTUD_BITS_LO = 47 - LJ_LIGHTUD_BITS_SEG
LIGHTUD_SEG_MASK = (1 << LJ_LIGHTUD_BITS_SEG) - 1
LIGHTUD_LO_MASK = (1 << LJ_LIGHTUD_BITS_LO) - 1


# Simple converters.


def tou64(val):
    return dbg.cast('uint64_t', val) & 0xFFFFFFFFFFFFFFFF


def tou32(val):
    return int(val) & 0xFFFFFFFF


def i2notu32(val):
    return ~int(val) & 0xFFFFFFFF


def strx64(val):
    return re.sub('L?$', '', hex(int(tou64(val))))


# The members of the enums of EnumBasedList by the max member, for a
# build whose debug info has not the enum (one used only through its
# constants may be left out): the same in the LuaJIT of every
# Tarantool from 3.0 to 3.9.
ENUM_FALLBACK = {
    'BCM_max': '''
        BCMnone BCMdst BCMbase BCMvar BCMrbase BCMuv BCMlit BCMlits BCMpri
        BCMnum BCMstr BCMtab BCMfunc BCMjump BCMcdata
    ''',
    'BC__MAX': '''
        BC_ISLT BC_ISGE BC_ISLE BC_ISGT BC_ISEQV BC_ISNEV BC_ISEQS BC_ISNES
        BC_ISEQN BC_ISNEN BC_ISEQP BC_ISNEP BC_ISTC BC_ISFC BC_IST BC_ISF
        BC_ISTYPE BC_ISNUM BC_MOV BC_NOT BC_UNM BC_LEN BC_ADDVN BC_SUBVN
        BC_MULVN BC_DIVVN BC_MODVN BC_ADDNV BC_SUBNV BC_MULNV BC_DIVNV
        BC_MODNV BC_ADDVV BC_SUBVV BC_MULVV BC_DIVVV BC_MODVV BC_POW BC_CAT
        BC_KSTR BC_KCDATA BC_KSHORT BC_KNUM BC_KPRI BC_KNIL BC_UGET BC_USETV
        BC_USETS BC_USETN BC_USETP BC_UCLO BC_FNEW BC_TNEW BC_TDUP BC_GGET
        BC_GSET BC_TGETV BC_TGETS BC_TGETB BC_TGETR BC_TSETV BC_TSETS BC_TSETB
        BC_TSETM BC_TSETR BC_CALLM BC_CALL BC_CALLMT BC_CALLT BC_ITERC
        BC_ITERN BC_VARG BC_ISNEXT BC_RETM BC_RET BC_RET0 BC_RET1 BC_FORI
        BC_JFORI BC_FORL BC_IFORL BC_JFORL BC_ITERL BC_IITERL BC_JITERL
        BC_LOOP BC_ILOOP BC_JLOOP BC_JMP BC_FUNCF BC_IFUNCF BC_JFUNCF BC_FUNCV
        BC_IFUNCV BC_JFUNCV BC_FUNCC BC_FUNCCW
    ''',
    'FF__MAX': '''
        FF_LUA_ FF_C_ FF_assert FF_type FF_next FF_pairs FF_ipairs_aux
        FF_ipairs FF_getmetatable FF_setmetatable FF_getfenv FF_setfenv
        FF_rawget FF_rawset FF_rawequal FF_unpack FF_select FF_tonumber
        FF_tostring FF_error FF_pcall FF_xpcall FF_loadfile FF_load
        FF_loadstring FF_dofile FF_gcinfo FF_collectgarbage FF_newproxy
        FF_print FF_coroutine_status FF_coroutine_running
        FF_coroutine_isyieldable FF_coroutine_create FF_coroutine_yield
        FF_coroutine_resume FF_coroutine_wrap_aux FF_coroutine_wrap
        FF_math_abs FF_math_floor FF_math_ceil FF_math_sqrt FF_math_log10
        FF_math_exp FF_math_sin FF_math_cos FF_math_tan FF_math_asin
        FF_math_acos FF_math_atan FF_math_sinh FF_math_cosh FF_math_tanh
        FF_math_frexp FF_math_modf FF_math_log FF_math_atan2 FF_math_pow
        FF_math_fmod FF_math_ldexp FF_math_min FF_math_max FF_math_random
        FF_math_randomseed FF_bit_tobit FF_bit_bnot FF_bit_bswap FF_bit_lshift
        FF_bit_rshift FF_bit_arshift FF_bit_rol FF_bit_ror FF_bit_band
        FF_bit_bor FF_bit_bxor FF_bit_tohex FF_string_byte FF_string_char
        FF_string_sub FF_string_rep FF_string_reverse FF_string_lower
        FF_string_upper FF_string_dump FF_string_find FF_string_match
        FF_string_gmatch_aux FF_string_gmatch FF_string_gsub FF_string_format
        FF_table_maxn FF_table_insert FF_table_concat FF_table_sort
        FF_table_new FF_table_clear FF_io_method_close FF_io_method_read
        FF_io_method_write FF_io_method_flush FF_io_method_seek
        FF_io_method_setvbuf FF_io_method_lines FF_io_method___gc
        FF_io_method___tostring FF_io_open FF_io_popen FF_io_tmpfile
        FF_io_close FF_io_read FF_io_write FF_io_flush FF_io_input
        FF_io_output FF_io_lines FF_io_type FF_os_execute FF_os_remove
        FF_os_rename FF_os_tmpname FF_os_getenv FF_os_exit FF_os_clock
        FF_os_date FF_os_time FF_os_difftime FF_os_setlocale
        FF_debug_getregistry FF_debug_getmetatable FF_debug_setmetatable
        FF_debug_getfenv FF_debug_setfenv FF_debug_getinfo FF_debug_getlocal
        FF_debug_setlocal FF_debug_getupvalue FF_debug_setupvalue
        FF_debug_upvalueid FF_debug_upvaluejoin FF_debug_sethook
        FF_debug_gethook FF_debug_debug FF_debug_traceback FF_jit_on
        FF_jit_off FF_jit_flush FF_jit_status FF_jit_attach
        FF_jit_util_funcinfo FF_jit_util_funcbc FF_jit_util_funck
        FF_jit_util_funcuvname FF_jit_util_traceinfo FF_jit_util_traceir
        FF_jit_util_tracek FF_jit_util_tracesnap FF_jit_util_tracemc
        FF_jit_util_traceexitstub FF_jit_util_ircalladdr FF_jit_opt_start
        FF_jit_profile_start FF_jit_profile_stop FF_jit_profile_dumpstack
        FF_ffi_meta___index FF_ffi_meta___newindex FF_ffi_meta___eq
        FF_ffi_meta___len FF_ffi_meta___lt FF_ffi_meta___le
        FF_ffi_meta___concat FF_ffi_meta___call FF_ffi_meta___add
        FF_ffi_meta___sub FF_ffi_meta___mul FF_ffi_meta___div
        FF_ffi_meta___mod FF_ffi_meta___pow FF_ffi_meta___unm
        FF_ffi_meta___tostring FF_ffi_meta___pairs FF_ffi_meta___ipairs
        FF_ffi_clib___index FF_ffi_clib___newindex FF_ffi_clib___gc
        FF_ffi_callback_free FF_ffi_callback_set FF_ffi_cdef FF_ffi_new
        FF_ffi_cast FF_ffi_typeof FF_ffi_typeinfo FF_ffi_istype FF_ffi_sizeof
        FF_ffi_alignof FF_ffi_offsetof FF_ffi_errno FF_ffi_string FF_ffi_copy
        FF_ffi_fill FF_ffi_abi FF_ffi_metatype FF_ffi_gc FF_ffi_load
        FF_misc_getmetrics FF_misc_sysprof_start FF_misc_sysprof_stop
        FF_misc_sysprof_report FF_misc_memprof_start FF_misc_memprof_stop
    ''',
    'IRCALL__MAX': '''
        IRCALL_lj_str_cmp IRCALL_lj_str_find IRCALL_lj_str_new
        IRCALL_lj_strscan_num IRCALL_lj_strfmt_int IRCALL_lj_strfmt_num
        IRCALL_lj_strfmt_char IRCALL_lj_strfmt_putint IRCALL_lj_strfmt_putnum
        IRCALL_lj_strfmt_putquoted IRCALL_lj_strfmt_putfxint
        IRCALL_lj_strfmt_putfnum_int IRCALL_lj_strfmt_putfnum_uint
        IRCALL_lj_strfmt_putfnum IRCALL_lj_strfmt_putfstr
        IRCALL_lj_strfmt_putfchar IRCALL_lj_buf_putmem IRCALL_lj_buf_putstr
        IRCALL_lj_buf_putchar IRCALL_lj_buf_putstr_reverse
        IRCALL_lj_buf_putstr_lower IRCALL_lj_buf_putstr_upper
        IRCALL_lj_buf_putstr_rep IRCALL_lj_buf_puttab IRCALL_lj_buf_tostr
        IRCALL_lj_tab_new_ah IRCALL_lj_tab_new1 IRCALL_lj_tab_dup
        IRCALL_lj_tab_clear IRCALL_lj_tab_newkey IRCALL_lj_tab_len
        IRCALL_lj_gc_step_jit IRCALL_lj_gc_barrieruv IRCALL_lj_mem_newgco
        IRCALL_lj_math_random_step IRCALL_lj_vm_modi IRCALL_log10 IRCALL_exp
        IRCALL_sin IRCALL_cos IRCALL_tan IRCALL_asin IRCALL_acos IRCALL_atan
        IRCALL_sinh IRCALL_cosh IRCALL_tanh IRCALL_fputc IRCALL_fwrite
        IRCALL_fflush IRCALL_lj_vm_floor IRCALL_lj_vm_ceil IRCALL_lj_vm_trunc
        IRCALL_sqrt IRCALL_log IRCALL_lj_vm_log2 IRCALL_pow IRCALL_atan2
        IRCALL_ldexp IRCALL_lj_vm_tobit IRCALL_softfp_add IRCALL_softfp_sub
        IRCALL_softfp_mul IRCALL_softfp_div IRCALL_softfp_cmp
        IRCALL_softfp_i2d IRCALL_softfp_d2i IRCALL_lj_vm_sfmin
        IRCALL_lj_vm_sfmax IRCALL_lj_vm_tointg IRCALL_softfp_ui2d
        IRCALL_softfp_f2d IRCALL_softfp_d2ui IRCALL_softfp_d2f
        IRCALL_softfp_i2f IRCALL_softfp_ui2f IRCALL_softfp_f2i
        IRCALL_softfp_f2ui IRCALL_fp64_l2d IRCALL_fp64_ul2d IRCALL_fp64_l2f
        IRCALL_fp64_ul2f IRCALL_fp64_d2l IRCALL_fp64_d2ul IRCALL_fp64_f2l
        IRCALL_fp64_f2ul IRCALL_lj_carith_divi64 IRCALL_lj_carith_divu64
        IRCALL_lj_carith_modi64 IRCALL_lj_carith_modu64
        IRCALL_lj_carith_powi64 IRCALL_lj_carith_powu64 IRCALL_lj_cdata_newv
        IRCALL_lj_cdata_setfin IRCALL_strlen IRCALL_memcpy IRCALL_memset
        IRCALL_lj_vm_errno IRCALL_lj_carith_mul64 IRCALL_lj_carith_shl64
        IRCALL_lj_carith_shr64 IRCALL_lj_carith_sar64 IRCALL_lj_carith_rol64
        IRCALL_lj_carith_ror64
    ''',
    'IRFL__MAX': '''
        IRFL_STR_LEN IRFL_FUNC_ENV IRFL_FUNC_PC IRFL_FUNC_FFID IRFL_THREAD_ENV
        IRFL_TAB_META IRFL_TAB_ARRAY IRFL_TAB_NODE IRFL_TAB_ASIZE
        IRFL_TAB_HMASK IRFL_TAB_NOMM IRFL_UDATA_META IRFL_UDATA_UDTYPE
        IRFL_UDATA_FILE IRFL_CDATA_CTYPEID IRFL_CDATA_PTR IRFL_CDATA_INT
        IRFL_CDATA_INT64 IRFL_CDATA_INT64_4
    ''',
    'IRFPM__MAX': '''
        IRFPM_FLOOR IRFPM_CEIL IRFPM_TRUNC IRFPM_SQRT IRFPM_LOG IRFPM_LOG2
        IRFPM_OTHER
    ''',
    'IRT__MAX': '''
        IRT_NIL IRT_FALSE IRT_TRUE IRT_LIGHTUD IRT_STR IRT_P32 IRT_THREAD
        IRT_PROTO IRT_FUNC IRT_P64 IRT_CDATA IRT_TAB IRT_UDATA IRT_FLOAT
        IRT_NUM IRT_I8 IRT_U8 IRT_I16 IRT_U16 IRT_INT IRT_U32 IRT_I64 IRT_U64
        IRT_SOFTFP
    ''',
    'IR__MAX': '''
        IR_LT IR_GE IR_LE IR_GT IR_ULT IR_UGE IR_ULE IR_UGT IR_EQ IR_NE IR_ABC
        IR_RETF IR_NOP IR_BASE IR_PVAL IR_GCSTEP IR_HIOP IR_LOOP IR_USE IR_PHI
        IR_RENAME IR_PROF IR_KPRI IR_KINT IR_KGC IR_KPTR IR_KKPTR IR_KNULL
        IR_KNUM IR_KINT64 IR_KSLOT IR_BNOT IR_BSWAP IR_BAND IR_BOR IR_BXOR
        IR_BSHL IR_BSHR IR_BSAR IR_BROL IR_BROR IR_ADD IR_SUB IR_MUL IR_DIV
        IR_MOD IR_POW IR_NEG IR_ABS IR_LDEXP IR_MIN IR_MAX IR_FPMATH IR_ADDOV
        IR_SUBOV IR_MULOV IR_AREF IR_HREFK IR_HREF IR_NEWREF IR_UREFO IR_UREFC
        IR_FREF IR_STRREF IR_LREF IR_ALOAD IR_HLOAD IR_ULOAD IR_FLOAD IR_XLOAD
        IR_SLOAD IR_VLOAD IR_ASTORE IR_HSTORE IR_USTORE IR_FSTORE IR_XSTORE
        IR_SNEW IR_XSNEW IR_TNEW IR_TDUP IR_CNEW IR_CNEWI IR_BUFHDR IR_BUFPUT
        IR_BUFSTR IR_TBAR IR_OBAR IR_XBAR IR_CONV IR_TOBIT IR_TOSTR IR_STRTO
        IR_CALLN IR_CALLA IR_CALLL IR_CALLS IR_CALLXS IR_CARG
    ''',
    'LJ_VMST__MAX': '''
        LJ_VMST_INTERP LJ_VMST_LFUNC LJ_VMST_FFUNC LJ_VMST_CFUNC LJ_VMST_GC
        LJ_VMST_EXIT LJ_VMST_RECORD LJ_VMST_OPT LJ_VMST_ASM
    ''',
    'MM__MAX': '''
        MM_index MM_newindex MM_gc MM_mode MM_eq MM_len MM_lt MM_le MM_concat
        MM_call MM_add MM_sub MM_mul MM_div MM_mod MM_pow MM_unm MM_metatable
        MM_tostring MM_new MM_pairs MM_ipairs
    ''',
}


class EnumBasedList(object):
    def __init__(self, max_enum_member, enum_name, map_func=None,
                 *map_func_args):
        self.__max_enum_member = max_enum_member
        self.__enum_name = enum_name
        self.__map_func = map_func
        self.__map_func_args = map_func_args
        # Lazy initialization (see __get_items) as the required
        # information might be unavailable at this moment.
        self.__items = None

    def __iter__(self):
        return iter(self.__get_items())

    def __getitem__(self, key):
        return self.__get_items()[key]

    def __len__(self):
        return len(self.__get_items())

    def __get_items(self):
        if self.__items is None:
            try:
                max_enum_value = dbg.create_enum_value(
                    self.__max_enum_member, self.__enum_name
                )
            except Exception as e:
                msg = 'failed to resolve {} enum members using {}'.format(
                    self.__enum_name or 'anonymous',
                    self.__max_enum_member,
                )
                if self.__max_enum_member not in ENUM_FALLBACK:
                    raise dbg.error('{}: {}'.format(msg, e))
                # The debug info may leave out an enum used only through
                # its constants: take the built-in members.
                dbg.write('luajit_gdb.py: {}, taking the built-in '
                          'list\n'.format(msg))
                names = ENUM_FALLBACK[self.__max_enum_member].split()
            else:
                names = [str(dbg.cast_typeof(max_enum_value, i))
                         for i in range(max_enum_value)]
            items = []
            for item in names:
                if self.__map_func:
                    item = self.__map_func(item, *self.__map_func_args)
                items.append(item)
            self.__items = items
        return self.__items

    def index(self, item):
        return self.__get_items().index(item)


def cut_prefix(s, prefix):
    return s[len(prefix):] if s.startswith(prefix) else s


# Types and TValues.


LJ_T = {
    'NIL':     i2notu32(0),
    'FALSE':   i2notu32(1),
    'TRUE':    i2notu32(2),
    'LIGHTUD': i2notu32(3),
    'STR':     i2notu32(4),
    'UPVAL':   i2notu32(5),
    'THREAD':  i2notu32(6),
    'PROTO':   i2notu32(7),
    'FUNC':    i2notu32(8),
    'TRACE':   i2notu32(9),
    'CDATA':   i2notu32(10),
    'TAB':     i2notu32(11),
    'UDATA':   i2notu32(12),
    'NUMX':    i2notu32(13),
}


def typenames(value):
    return {
        LJ_T[k]: 'LJ_T' + k for k in LJ_T.keys()
    }.get(int(value), 'LJ_TINVALID')


def itype(o):
    return tou32(o['it64'] >> 47) if LJ_GC64 else o['it']


def tvisint(o):
    return LJ_DUALNUM and itype(o) == LJ_TISNUM


def tvisnumber(o):
    return itype(o) <= LJ_TISNUM


def tvislightud(o):
    if LJ_64 and not LJ_GC64:
        return (dbg.cast('int32_t', itype(o)) >> 15) == -2
    else:
        return itype(o) == LJ_T['LIGHTUD']


def itypemap(o):
    if LJ_64 and not LJ_GC64:
        return LJ_T['NUMX'] if tvisnumber(o)       \
            else LJ_T['LIGHTUD'] if tvislightud(o) \
            else itype(o)
    else:
        return LJ_T['NUMX'] if tvisnumber(o) else itype(o)


# Bytecode.

def bc_op(ins):
    return int(ins) & 0xff


def bc_a(ins):
    return (int(ins) >> 8) & 0xff


def bc_b(ins):
    return int(ins) >> 24


def bc_c(ins):
    return (int(ins) >> 16) & 0xff


def bc_d(ins):
    return int(ins) >> 16


BCMODE = EnumBasedList('BCM_max', 'BCMode', cut_prefix, 'BCM')


def lj_bc_mode():
    # Looked up on every use, not kept: the value holds the address of
    # the array in the process it was looked up in, and a PIE is at
    # another address in another process (another core, an attach).
    return dbg.lookup_global('lj_bc_mode')


def bcmode_a(op):
    return int(lj_bc_mode()[op] & 7)


def bcmode_b(op):
    return int((lj_bc_mode()[op] >> 3) & 15)


def bcmode_cd(op):
    return int((lj_bc_mode()[op] >> 7) & 15)


BYTECODES = EnumBasedList('BC__MAX', 'BCOp', cut_prefix, 'BC_')


def proto_bc(proto):
    return dbg.cast('BCIns *',
                    dbg.cast('char *', proto) + dbg.sizeof('GCproto'))


def proto_kgc(pt, idx):
    return gcref(mref('GCRef *', pt['k'])[idx])


def proto_knumtv(pt, idx):
    return mref('TValue *', pt['k'])[idx]


# Frames.


FRAME_TYPE = 0x3
FRAME_P = 0x4
FRAME_TYPEP = FRAME_TYPE | FRAME_P

FRAME = {
    'LUA':    0x0,
    'C':      0x1,
    'CONT':   0x2,
    'VARG':   0x3,
    'LUAP':   0x4,
    'CP':     0x5,
    'PCALL':  0x6,
    'PCALLH': 0x7,
}


def frametypes(ft):
    return {
        FRAME['LUA']:  'L',
        FRAME['C']:    'C',
        FRAME['CONT']: 'M',
        FRAME['VARG']: 'V',
    }.get(ft, '?')


def frame_ftsz(framelink):
    return dbg.cast('ptrdiff_t', framelink['ftsz'] if LJ_FR2
                    else framelink['fr']['tp']['ftsz'])


def frame_pc(framelink):
    return dbg.cast('BCIns *', frame_ftsz(framelink)) if LJ_FR2 \
        else mref('BCIns *', framelink['fr']['tp']['pcr'])


def frame_prevl(framelink):
    return framelink - (1 + LJ_FR2 + bc_a(frame_pc(framelink)[-1]))


def frame_ispcall(framelink):
    return (frame_ftsz(framelink) & FRAME['PCALL']) == FRAME['PCALL']


def frame_sized(framelink):
    return (frame_ftsz(framelink) & ~FRAME_TYPEP)


def frame_prevd(framelink):
    return dbg.cast('TValue *',
                    dbg.cast('char *', framelink) - frame_sized(framelink))


def frame_type(framelink):
    return frame_ftsz(framelink) & FRAME_TYPE


def frame_typep(framelink):
    return frame_ftsz(framelink) & FRAME_TYPEP


def frame_islua(framelink):
    return frametypes(int(frame_type(framelink))) == 'L' \
        and int(frame_ftsz(framelink)) > 0


def frame_prev(framelink):
    return frame_prevl(framelink) if frame_islua(framelink) \
        else frame_prevd(framelink)


def frame_sentinel(L):
    return mref('TValue *', L['stack']) + LJ_FR2


# The generator that implements frame iterator.
# Every frame is represented as a tuple of framelink and frametop.
def frames(L):
    frametop = L['top']
    framelink = L['base'] - 1
    framelink_sentinel = frame_sentinel(L)
    while True:
        yield framelink, frametop
        frametop = framelink - (1 + LJ_FR2)
        if framelink <= framelink_sentinel:
            break
        framelink = frame_prev(framelink)


# LuaJIT macro implementations and structure access.


# Get FastFunc enum members and replace any single '_' with '.'.
FF_NAMES = EnumBasedList('FF__MAX', 'FastFunc', lambda x:
                         re.sub('(?<!_)_', '.', cut_prefix(x, 'FF_')))


def mref(typename, obj):
    return dbg.cast(typename, obj['ptr64'] if LJ_GC64 else obj['ptr32'])


def gcref(obj):
    return dbg.cast('GCobj *', obj['gcptr64'] if LJ_GC64
                    else dbg.cast('uintptr_t', obj['gcptr32']))


def gcval(obj):
    return dbg.cast('GCobj *', obj['gcptr64'] & LJ_GCVMASK if LJ_GC64
                    else dbg.cast('uintptr_t', obj['gcptr32']))


def gcnext(obj):
    return gcref(obj)['gch']['nextgc']


def lookup_main_coro(symbol):
    try:
        return dbg.lookup_global(symbol)
    except Exception:
        # A thread-local one and no thread to read it in.
        return None


def L(L=None):
    # Look up a symbol for the main coroutine considering the host
    # application.
    # XXX Fragile: Though the loop initialization looks bad, it
    # respects both Python 2 and Python 3.
    for lstate in [L] + list(map(lambda main: lookup_main_coro(main), (
        # LuaJIT main coro (see luajit/src/luajit.c).
        'globalL',
        # Tarantool main coro (see tarantool/src/lua/init.h).
        'tarantool_L',
        # TODO: Add more.
    ))):
        if lstate:
            return dbg.cast('lua_State *', lstate)
    # Both are NULL before the Lua is set up; tarantool_L is __thread
    # since Tarantool 3.7, NULL in the threads with no Lua.
    raise dbg.error('no lua_State: neither globalL nor tarantool_L '
                    'is set in this thread')


def G(L):
    return mref('global_State *', L['glref'])


def J(g):
    g_offset = dbg.offsetof('GG_State', 'g')
    J_offset = dbg.offsetof('GG_State', 'J')
    return dbg.cast('jit_State *', dbg.cast('char *', g) - g_offset + J_offset)


# Matched `MMDEF(_)`.
MM_NAMES = EnumBasedList('MM__MAX', 'MMS', cut_prefix, 'MM_')


# Get the name of the index in the predefined arrays.
def idx_name(field_name):
    GCROOT_MMNAME = 0
    GCROOT_BASEMT = GCROOT_MMNAME + len(MM_NAMES)
    GCROOT_IO_INPUT = GCROOT_BASEMT + i2notu32(LJ_T['NUMX']) + 1
    GCROOT_IO_OUTPUT = GCROOT_IO_INPUT + 1

    # Don't use **{ to be compatible with Python 2.
    gcroot = {}
    gcroot.update({
        i: 'GCROOT_MMNAME_' + MM_NAMES[i] for i in range(len(MM_NAMES))
    })
    gcroot.update({
        i2notu32(LJ_T[k]) + GCROOT_BASEMT: 'GCROOT_BASEMT_' + k
        for k in LJ_T.keys()
    })
    gcroot.update({
        GCROOT_IO_INPUT:  'GCROOT_IO_INPUT',
        GCROOT_IO_OUTPUT: 'GCROOT_IO_OUTPUT',
    })
    return {
        # May be one of 2 slots depending on the result address.
        'ksimd': {
            0 * 2 + 0: 'LJ_KSIMD_ABS',
            0 * 2 + 1: 'LJ_KSIMD_ABS',
            1 * 2 + 0: 'LJ_KSIMD_NEG',
            1 * 2 + 1: 'LJ_KSIMD_NEG',
        },
        'gcroot': gcroot,
    }.get(field_name, None)


ggfname_cache = {}


# Get GG field name by given offset. Use in JIT dump.
def ggfname_by_offset(offset):
    if offset in ggfname_cache:
        return ggfname_cache[offset]

    field_path = dbg.member_by_offset('GG_State', offset)
    if not field_path:
        return None

    # Remove first '.'.
    ggfname = 'offsetof(GG, {})'.format(field_path[1:])
    ggfname_cache[offset] = ggfname
    return ggfname


VM_STATES = EnumBasedList('LJ_VMST__MAX', None, cut_prefix, 'LJ_VMST_')


def vm_state(g):
    index = i2notu32(g['vmstate'])
    return VM_STATES[index] if index < len(VM_STATES) \
        else vm_state_trace(~index)


def vm_state_trace(traceno):
    return 'TRACE(#{})'.format(traceno)


def gc_state(g):
    return {
        0: 'PAUSE',
        1: 'PROPAGATE',
        2: 'ATOMIC',
        3: 'SWEEPSTRING',
        4: 'SWEEP',
        5: 'FINALIZE',
    }.get(int(g['gc']['state']), 'INVALID')


def jit_state(g):
    return {
        0:    'IDLE',
        0x10: 'ACTIVE',
        0x11: 'RECORD',
        0x12: 'START',
        0x13: 'END',
        0x14: 'ASM',
        0x15: 'ERR',
    }.get(int(J(g)['state']), 'INVALID')


def strdata(obj):
    try:
        return dbg.cstr(dbg.cast('char *', dbg.cast('GCstr *', obj) + 1))
    except UnicodeEncodeError:
        return "<luajit_gdb: error occurred while rendering non-ascii slot>"


def funcproto(func):
    assert func['ffid'] == 0, 'Attempt to take a prototype of non-Lua function'
    return dbg.cast('GCproto *',
                    mref('char *', func['pc']) - dbg.sizeof('GCproto'))


def gclistlen(root, end=0x0):
    count = 0
    while (gcref(root) != end):
        count += 1
        root = gcnext(root)
    return count


def gcringlen(root):
    if not gcref(root):
        return 0
    elif gcref(root) == gcref(gcnext(root)):
        return 1
    else:
        return 1 + gclistlen(gcnext(root), gcref(root))


gclen = {
    'root':      gclistlen,
    'gray':      gclistlen,
    'grayagain': gclistlen,
    'weak':      gclistlen,
    # XXX: gc.mmudata is a ring-list.
    'mmudata':   gcringlen,
}


def lightudV(tv):
    if LJ_64:
        u = int(tv['u64'])
        # `lightudseg()' macro expanded.
        seg = (u >> LJ_LIGHTUD_BITS_LO) & LIGHTUD_SEG_MASK
        segmap = mref('uint32_t *', G(L(None))['gc']['lightudseg'])
        # `lightudlo()' macro expanded.
        return (int(segmap[seg]) << 32) | (u & LIGHTUD_LO_MASK)
    else:
        return gcval(tv['gcr'])


# FFI.


# Externally visible types.
CT_NUM = 0  # Integer or floating-point numbers.
CT_STRUCT = 1  # Struct or union.
CT_PTR = 2  # Pointer or reference.
CT_ARRAY = 3  # Array or complex type.
CT_MAYCONVERT = CT_ARRAY
CT_VOID = 4  # Void type.
CT_ENUM = 5  # Enumeration.
CT_HASSIZE = CT_ENUM  # Last type where ct->size holds the actual size.
CT_FUNC = 6  # Function.
CT_TYPEDEF = 7  # Typedef.
CT_ATTRIB = 8  # Miscellaneous attributes.

# Common types.
CTID_CTYPEID = 21

# C type info flags.
CTF_BOOL = 0x08000000  # Boolean: NUM, BITFIELD.
CTF_FP = 0x04000000  # Floating-point: NUM.
CTF_CONST = 0x02000000  # Const qualifier.
CTF_VOLATILE = 0x01000000  # Volatile qualifier.
CTF_UNSIGNED = 0x00800000  # Unsigned: NUM, BITFIELD.
CTF_LONG = 0x00400000  # Long: NUM.
CTF_VLA = 0x00100000  # Variable-length: ARRAY, STRUCT.
CTF_REF = 0x00800000  # Reference: PTR.
CTF_VECTOR = 0x08000000  # Vector: ARRAY.
CTF_COMPLEX = 0x04000000  # Complex: ARRAY.
CTF_UNION = 0x00800000  # Union: STRUCT.
CTF_VARARG = 0x00800000  # Vararg: FUNC.
CTF_SSEREGPARM = 0x00400000  # SSE register parameters: FUNC.

CTF_UCHAR = CTF_UNSIGNED if int(dbg.cast('char', -1)) > 0 else 0

CTMASK_ATTRIB = 255  # Max. 256 attributes.
CTSHIFT_ATTRIB = 16

# Attribute numbers.
CTA_QUAL = 1  # Unmerged qualifiers.

CTSHIFT_NUM = 28
CTMASK_CID = 0x0000ffff
CTMASK_NUM = 0xf0000000  # Max. 16 type numbers.

# Special sizes.
CTSIZE_INVALID = 0xffffffff
DWORDSZ = 4
QWORDSZ = 8


# Implementation of the `CTINFO()` macro.
def ctinfo(ct, flags):
    return (tou32(ct) << CTSHIFT_NUM) + flags


def ctype_type(info):
    return info >> CTSHIFT_NUM


def ctype_cid(info):
    return info & CTMASK_CID


def ctype_attrib(info):
    return (info >> CTSHIFT_ATTRIB) & CTMASK_ATTRIB


def ctype_isptr(info):
    return ctype_type(info) == CT_PTR


def ctype_isinteger(info):
    return (info & (CTMASK_NUM | CTF_BOOL | CTF_FP)) == ctinfo(CT_NUM, 0)


def ctype_iscomplex(info):
    return (info & (CTMASK_NUM | CTF_COMPLEX)) == ctinfo(CT_ARRAY, CTF_COMPLEX)


def ctype_isrefarray(info):
    return (info & (CTMASK_NUM | CTF_VECTOR | CTF_COMPLEX)) == \
           ctinfo(CT_ARRAY, 0)


def ctype_child(cts, ctype):
    return ctype_get(cts, ctype_cid(ctype['info']))


def ctype_ctsG(g):
    return mref('CTState *', g['ctype_state'])


def ctype_get(cts, id):
    return dbg.address(cts['tab'][id])


# Get C type ID for a C type.
def ctype_typeid(cts, ct):
    return ct - cts['tab']


def cdata_getptr(p, size):
    if (LJ_64 and size == 4) or not LJ_64:
        return dbg.cast('void *', dbg.cast('uint32_t *', p)[0])
    else:
        assert size == 8, 'incorrect pointer size'
        return dbg.cast('void *', dbg.cast('uint64_t *', p)[0])


def cdataptr(cd):
    return dbg.cast('void *', (cd + 1))


# JIT engine.


IRS = EnumBasedList('IR__MAX', 'IROp', cut_prefix, 'IR_')


# Mode bits: Commutative, {Normal/Ref, Alloc, Load, Store},
# Non-weak guard.
IRM_BITS_W = 0x80
IRM_BITS = {
    0x00: 'N',
    0x10: 'C',
    0x20: 'A',
    0x40: 'L',
    0x60: 'S',
}
IRM_BITS_MASK = 0x70


# IR operand mode (2 bit).
IRM = [
  'ref',
  'lit',
  'cst',
  '',  # none
]


def lj_ir_mode():
    # Looked up on every use, as lj_bc_mode().
    return dbg.lookup_global('lj_ir_mode')


def ir_left(op):
    return IRM[int(lj_ir_mode()[op] & 3)]


def ir_right(op):
    return IRM[int(lj_ir_mode()[op] >> 2 & 3)]


def ir_mode(op):
    irmode = int((lj_ir_mode()[op]))
    isweak = not bool(irmode & IRM_BITS_W)
    mode = IRM_BITS[irmode & IRM_BITS_MASK]
    mode += 'W' if isweak else ''
    return mode


IRTYPES = EnumBasedList('IRT__MAX', 'IRType', lambda x: {
                            'IRT_CDATA': 'cdt',
                            'IRT_FLOAT': 'flt',
                            'IRT_LIGHTUD': 'lud',
                            'IRT_SOFTFP': 'sfp',
                            'IRT_UDATA': 'udt',
                        }.get(x, cut_prefix(x, 'IRT_')[:3].ljust(3).lower()))


IRT_NUM = None


def irt_num():
    global IRT_NUM
    if IRT_NUM is None:
        IRT_NUM = IRTYPES.index('num')
    return IRT_NUM


IRFIELDS = EnumBasedList('IRFL__MAX', 'IRFieldID', lambda x:
                         cut_prefix(x, 'IRFL_').lower().replace('_', '.', 1))


IRFPMS = EnumBasedList('IRFPM__MAX', 'IRFPMathOp',
                       lambda x: cut_prefix(x, 'IRFPM_').lower())


# Don't use *[ to be compatible with Python 2.
REGISTERS = {
    'x64': [
        'rax',
        'rcx',
        'rdx',
        'rbx',
        'rsp',
        'rbp',
        'rsi',
        'rdi',
    ] + [
        'r{}'.format(i) for i in range(8, 16)  # r8 .. r15
    ] + [
        'xmm{}'.format(i) for i in range(0, 16)  # xmm0 .. xmm15
    ],
    'arm64': [
        'x{}'.format(i) for i in range(0, 31)  # x0 .. x30
    ] + [
        'sp'  # x31
    ] + [
        'd{}'.format(i) for i in range(0, 32)  # d0 .. d31
    ]
}


IR_CALLS = EnumBasedList('IRCALL__MAX', 'IRCallID', cut_prefix, 'IRCALL_')


def regname(reg_number):
    if not hasattr(dbg, 'arch'):
        dbg.arch = dbg.detect_arch()
    return REGISTERS[dbg.arch][reg_number]


def litname_sload(mode):
    modes_str = ''
    modes_str += 'P' if mode & 0x1 else ''
    modes_str += 'F' if mode & 0x2 else ''
    modes_str += 'T' if mode & 0x4 else ''
    modes_str += 'C' if mode & 0x8 else ''
    modes_str += 'R' if mode & 0x10 else ''
    modes_str += 'I' if mode & 0x20 else ''
    return modes_str


def litname_xload(mode):
    flags = ['-', 'R', 'V', 'RV', 'U', 'RU', 'VU', 'RVU']
    return flags[mode]


def litname_conv(mode):
    IRCONV_DSH = 5
    IRCONV_CSH = 12
    IRCONV_SEXT = 0x800
    IRCONV_SRCMASK = 0x1f
    conv_str = '{to}.{frm}'.format(
        to=IRTYPES[(mode >> IRCONV_DSH) & IRCONV_SRCMASK],
        frm=IRTYPES[mode & IRCONV_SRCMASK]
    )
    conv_str += ' sext' if mode & IRCONV_SEXT else ''
    num2int_mode = mode >> IRCONV_CSH
    if num2int_mode == 2:
        conv_str += ' index'
    elif num2int_mode == 3:
        conv_str += ' check'
    return conv_str


def litname_irfield(mode):
    if mode >= len(IRFIELDS):
        return 'unknown irfield'
    return IRFIELDS[mode]


def litname_fpm(mode):
    if mode >= len(IRFPMS):
        return 'unknown irfpm'
    return IRFPMS[mode]


def litname_bufhdr(mode):
    modes = ['RESET', 'APPEND']
    if mode >= len(modes):
        return 'unknown bufhdr mode'
    return modes[mode]


def litname_tostr(mode):
    modes = ['INT', 'NUM', 'CHAR']
    if mode >= len(modes):
        return 'unknown tostr mode'
    return modes[mode]


IR_LITNAMES = {
    'SLOAD':  litname_sload,
    'XLOAD':  litname_xload,
    'CONV':   litname_conv,
    'FLOAD':  litname_irfield,
    'FREF':   litname_irfield,
    'FPMATH': litname_fpm,
    'BUFHDR': litname_bufhdr,
    'TOSTR':  litname_tostr
}

# Additional flags.
IRT_MARK = 0x20  # Marker for misc. purposes.
IRT_ISPHI = 0x40  # Instruction is left or right PHI operand.
IRT_GUARD = 0x80  # Instruction is a guard.
# Masks.
IRT_TYPE = 0x1f

RID_NONE = 0x80
RID_MASK = 0x7f
RID_INIT = (RID_NONE | RID_MASK)
RID_SINK = (RID_INIT - 1)
RID_SUNK = (RID_INIT - 2)
# Spill slot 0 means no spill slot has been allocated.
SPS_NONE = 0

REF_BIAS = 0x8000
REF_NIL = REF_BIAS - 1

TREF_SHIFT = 24

TREF_REFMASK = 0x0000ffff
TREF_FRAME = 0x00010000
TREF_CONT = 0x00020000
# Snapshot flags and masks.
SNAP_FRAME = 0x010000
SNAP_NORESTORE = 0x040000
SNAP_SOFTFPNUM = 0x080000

SNAP_FR2_SLOT = (1 << TREF_SHIFT) | SNAP_FRAME | SNAP_NORESTORE + REF_NIL


def irt_type(t):
    return dbg.cast('IRType', t['irt'] & IRT_TYPE)


def tref_type(tr):
    return dbg.cast('IRType', (tr >> TREF_SHIFT) & IRT_TYPE)


def tref_ref(tr):
    return int(tr & TREF_REFMASK)


def irt_ismarked(t):
    return bool(t['irt'] & IRT_MARK)


def irt_isphi(t):
    return bool(t['irt'] & IRT_ISPHI)


def irt_isguard(t):
    return bool(t['irt'] & IRT_GUARD)


def irt_toitype(irt):
    t = irt_type(irt)
    if LJ_DUALNUM and t > irt_num():
        return LJ_T['NUMX']
    else:
        return i2notu32(t)


def ir_kptr(ir):
    irname = IRS[ir['o']]
    assert irname == 'KPTR' or irname == 'KKPTR', 'wrong IR for ir_kptr()'
    return mref('void *', dbg.cast('IRIns *', dbg.address(ir))[LJ_GC64]['ptr'])


def ir_kgc(ir):
    irname = IRS[ir['o']]
    assert irname == 'KGC', 'wrong IR for ir_kgc()'
    return gcref(dbg.cast('IRIns *', dbg.address(ir))[LJ_GC64]['gcr'])


def ir_knum(ir):
    irname = IRS[ir['o']]
    assert irname == 'KNUM', 'wrong IR for ir_knum()'
    return dbg.address(dbg.cast('IRIns *', dbg.address(ir))[1]['tv'])


def ir_kint64(ir):
    irname = IRS[ir['o']]
    assert irname == 'KINT64', 'wrong IR for ir_kint64()'
    return dbg.address(dbg.cast('IRIns *', dbg.address(ir))[1]['tv'])


# Dumpers.

def dump_fast_function(ffid):
    ffname = FF_NAMES[ffid] if ffid < len(FF_NAMES) else 'unknown'
    return 'fast function {} (#{})'.format(ffname, ffid)


# GCobj dumpers.

def dump_lj_gco_str(gcobj):
    return 'string {body} @ {address}'.format(
        body=strdata(gcobj),
        address=strx64(gcobj)
    )


def dump_lj_gco_upval(gcobj):
    return 'upvalue @ {}'.format(strx64(gcobj))


def dump_lj_gco_thread(gcobj):
    return 'thread @ {}'.format(strx64(gcobj))


def dump_lj_gco_proto(gcobj):
    return 'proto @ {}'.format(strx64(gcobj))


def dump_lj_gco_func(gcobj):
    func = dbg.cast('struct GCfuncC *', gcobj)
    ffid = int(func['ffid'])

    if ffid == 0:
        pt = funcproto(func)
        return 'Lua function @ {addr}, {nups} upvalues, {chunk}:{line}'.format(
            addr=strx64(func),
            nups=int(func['nupvalues']),
            chunk=strdata(dbg.cast('GCstr *', gcval(pt['chunkname']))),
            line=pt['firstline']
        )
    elif ffid == 1:
        return 'C function @ {}'.format(strx64(func['f']))
    else:
        return dump_fast_function(ffid)


def dump_lj_gco_trace(gcobj):
    trace = dbg.cast('struct GCtrace *', gcobj)
    return 'trace {traceno} @ {addr}'.format(
        traceno=strx64(trace['traceno']),
        addr=strx64(trace)
    )


def dump_lj_gco_cdata(gcobj):
    cdata = dbg.cast('struct GCcdata *', gcobj)
    cts = ctype_ctsG(G(L()))
    cid = cdata['ctypeid']
    ctype = ctype_get(cts, cid)
    info = ctype['info']
    size = ctype['size']
    value = ''
    if ctype_iscomplex(info):
        value = dump_cdata_val_complex(cdata, ctype)
    elif size == 8 and ctype_isinteger(info):
        value = dump_cdata_val_int64(cdata, ctype)
    else:
        value = cdataptr(cdata)
        if ctype_isptr(info):
            value = cdata_getptr(value, size)
    return 'cdata @ {addr} {ctype} {value}'.format(
        addr=strx64(gcobj),
        ctype=dump_ctype(ctype),
        value=value,
    )


def dump_lj_gco_tab(gcobj):
    table = dbg.cast('GCtab *', gcobj)
    return 'table @ {gcr} (asize: {asize}, hmask: {hmask})'.format(
        gcr=strx64(table),
        asize=table['asize'],
        hmask=strx64(table['hmask']),
    )


def dump_lj_gco_udata(gcobj):
    return 'userdata @ {}'.format(strx64(gcobj))


def dump_lj_gco_invalid(gcobj):
    return 'not valid type @ {}'.format(strx64(gcobj))


# TValue dumpers

def dump_lj_tv_nil(tv):
    return 'nil'


def dump_lj_tv_false(tv):
    return 'false'


def dump_lj_tv_true(tv):
    return 'true'


def dump_lj_tv_lightud(tv):
    return 'light userdata @ {}'.format(strx64(lightudV(tv)))


# Generate wrappers for TValues containing GCobj.
gco_fn_dumpers = [
    fn for fn in globals().keys() if fn.startswith('dump_lj_gco')
]
for fn_name in gco_fn_dumpers:
    wrapped_fn_name = fn_name.replace('gco', 'tv')
    # Lambda takes `fn_name` as a reference, so the additional
    # lambda is needed to fixate the correct wrapper.
    globals()[wrapped_fn_name] = (lambda f: (
        lambda tv: globals()[f](gcval(tv['gcr']))
    ))(fn_name)


def dump_lj_tv_numx(tv):
    if tvisint(tv):
        return 'integer {}'.format(dbg.cast('int32_t', tv['i']))
    else:
        return 'number {}'.format(dbg.cast('double', tv['n']))


gco_dumpers = {
    'LJ_TSTR':     dump_lj_gco_str,
    'LJ_TUPVAL':   dump_lj_gco_upval,
    'LJ_TTHREAD':  dump_lj_gco_thread,
    'LJ_TPROTO':   dump_lj_gco_proto,
    'LJ_TFUNC':    dump_lj_gco_func,
    'LJ_TTRACE':   dump_lj_gco_trace,
    'LJ_TCDATA':   dump_lj_gco_cdata,
    'LJ_TTAB':     dump_lj_gco_tab,
    'LJ_TUDATA':   dump_lj_gco_udata,
}


tv_dumpers = {
    'LJ_TNIL':     dump_lj_tv_nil,
    'LJ_TFALSE':   dump_lj_tv_false,
    'LJ_TTRUE':    dump_lj_tv_true,
    'LJ_TLIGHTUD': dump_lj_tv_lightud,
    'LJ_TSTR':     dump_lj_tv_str,  # noqa: F821 # Generated.
    'LJ_TUPVAL':   dump_lj_tv_upval,  # noqa: F821 # Generated.
    'LJ_TTHREAD':  dump_lj_tv_thread,  # noqa: F821 # Generated.
    'LJ_TPROTO':   dump_lj_tv_proto,  # noqa: F821 # Generated.
    'LJ_TFUNC':    dump_lj_tv_func,  # noqa: F821 # Generated.
    'LJ_TTRACE':   dump_lj_tv_trace,  # noqa: F821 # Generated.
    'LJ_TCDATA':   dump_lj_tv_cdata,  # noqa: F821 # Generated.
    'LJ_TTAB':     dump_lj_tv_tab,  # noqa: F821 # Generated.
    'LJ_TUDATA':   dump_lj_tv_udata,  # noqa: F821 # Generated.
    'LJ_TNUMX':    dump_lj_tv_numx,
}


def dump_gcobj(gcobj):
    return gco_dumpers.get(
        typenames(i2notu32(gcobj['gch']['gct'])), dump_lj_gco_invalid
    )(gcobj)


def dump_tvalue(tvalue):
    return tv_dumpers.get(
        typenames(itypemap(tvalue)),
        dump_lj_tv_invalid  # noqa: F821 # Generated.
    )(tvalue)


def dump_framelink_slot_address(fr):
    return '{}:{}'.format(fr - 1, fr) if LJ_FR2 \
        else '{}'.format(fr) + PADDING


def dump_framelink(L, fr):
    if fr == frame_sentinel(L):
        return '{addr} [S   ] FRAME: dummy L'.format(
            addr=dump_framelink_slot_address(fr),
        )
    return '{addr} [    ] FRAME: [{pp}] delta={d}, {f}'.format(
        addr=dump_framelink_slot_address(fr),
        pp='PP' if frame_ispcall(fr) else '{frname}{p}'.format(
            frname=frametypes(int(frame_type(fr))),
            # The link of a Lua frame is the return PC: its FRAME_P bit
            # is a bit of the address.
            p='P' if frame_type(fr) != FRAME['LUA'] and
            frame_typep(fr) & FRAME_P else ''
        ),
        d=dbg.cast('TValue *', fr) - dbg.cast('TValue *', frame_prev(fr)),
        f=dump_lj_tv_func(fr - LJ_FR2),  # noqa: F821 # Generated.
    )


def dump_stack_slot(L, slot, base=None, top=None):
    base = base or L['base']
    top = top or L['top']

    return '{addr}{padding} [ {B}{T}{M}] VALUE: {value}'.format(
        addr=strx64(slot),
        padding=PADDING,
        B='B' if slot == base else ' ',
        T='T' if slot == top else ' ',
        M='M' if slot == mref('TValue *', L['maxstack']) else ' ',
        value=dump_tvalue(slot),
    )


def dump_stack(L, base=None, top=None):
    base = base or L['base']
    top = top or L['top']
    stack = mref('TValue *', L['stack'])
    maxstack = mref('TValue *', L['maxstack'])
    # The red zone is LJ_STACK_EXTRA slots, 5 + 2 * LJ_FR2 in some
    # LuaJIT versions and 5 + 3 * LJ_FR2 in others: take it from the
    # stack itself (lj_state.c: stacksize = n + 1 + LJ_STACK_EXTRA,
    # maxstack = stack + n).
    red = int(L['stacksize']) - 1 - int(maxstack - stack)

    dump = [
        '{padding} Red zone: {nredslots: >2} slots {padding}'.format(
            padding='-' * len(PADDING),
            nredslots=red,
        ),
    ]
    dump.extend([
        dump_stack_slot(L, maxstack + offset, base, top)
            for offset in range(red, 0, -1)  # noqa: E131
    ])
    dump.extend([
        '{padding} Stack: {nstackslots: >5} slots {padding}'.format(
            padding='-' * len(PADDING),
            nstackslots=int((tou64(maxstack) - tou64(stack)) >> 3),
        ),
        dump_stack_slot(L, maxstack, base, top),
        '{start}:{end} [    ] {nfreeslots} slots: Free stack slots'.format(
            start=strx64(top + 1),
            end=strx64(maxstack - 1),
            nfreeslots=int((tou64(maxstack) - tou64(top) - 8) >> 3),
        ),
    ])

    for framelink, frametop in frames(L):
        # Dump all data slots in the (framelink, top) interval.
        dump.extend([
            dump_stack_slot(L, framelink + offset, base, top)
                for offset in range(frametop - framelink, 0, -1)  # noqa: E131
        ])
        # Dump frame slot (2 slots in case of GC64).
        dump.append(dump_framelink(L, framelink))

    return '\n'.join(dump)


def dump_gc(g):
    gc = g['gc']
    stats = ['{key}: {value}'.format(key=f, value=gc[f]) for f in (
        'total', 'threshold', 'debt', 'estimate', 'stepmul', 'pause'
    )]

    stats += ['sweepstr: {sweepstr}/{strmask}'.format(
        sweepstr=gc['sweepstr'],
        # String hash mask (size of hash table - 1).
        strmask=g['strmask'] + 1,
    )]

    stats += ['{key}: {number} objects'.format(
        key=stat,
        number=handler(gc[stat])
    ) for stat, handler in gclen.items()]

    return '\n'.join(map(lambda s: '\t' + s, stats))


# The size of a GC object: what the free function of its type frees
# (gc_freefunc of lj_gc.c).

def gco_size_str(gcobj):
    # sizestring() of lj_obj.h.
    return dbg.sizeof('GCstr') + int(dbg.cast('GCstr *', gcobj)['len']) + 1


def gco_size_upval(gcobj):
    return dbg.sizeof('GCupval')


def gco_size_thread(gcobj):
    # lj_state_free(): the stack and the lua_State.
    stacksize = int(dbg.cast('lua_State *', gcobj)['stacksize'])
    return dbg.sizeof('lua_State') + stacksize * dbg.sizeof('TValue')


def gco_size_proto(gcobj):
    return int(dbg.cast('GCproto *', gcobj)['sizept'])


def gco_size_func(gcobj):
    # sizeLfunc() and sizeCfunc() of lj_obj.h.
    func = dbg.cast('GCfuncC *', gcobj)
    nupvalues = int(func['nupvalues'])
    if int(func['ffid']) == 0:
        return dbg.sizeof('GCfuncL') + \
            (nupvalues - 1) * dbg.sizeof('GCRef')
    return dbg.sizeof('GCfuncC') + (nupvalues - 1) * dbg.sizeof('TValue')


def gco_size_trace(gcobj):
    # lj_trace_free().
    trace = dbg.cast('GCtrace *', gcobj)
    return ((dbg.sizeof('GCtrace') + 7) & ~7) + \
        (int(trace['nins']) - int(trace['nk'])) * dbg.sizeof('IRIns') + \
        int(trace['nsnap']) * dbg.sizeof('SnapShot') + \
        int(trace['nsnapmap']) * dbg.sizeof('SnapEntry')


def gco_size_cdata(gcobj):
    # lj_cdata_free(): a variable-length one (cdataisv()) or the size of
    # its C type.
    cdata = dbg.cast('GCcdata *', gcobj)
    if int(cdata['marked']) & 0x80:
        cdatav = dbg.cast('GCcdataVar *', dbg.cast('char *', cdata) -
                          dbg.sizeof('GCcdataVar'))
        return int(cdatav['len']) + int(cdatav['extra'])
    cts = ctype_ctsG(G(L()))
    ctype = ctype_get(cts, cdata['ctypeid'])
    while ctype_type(ctype['info']) == CT_ATTRIB:
        ctype = ctype_child(cts, ctype)
    if ctype_type(ctype['info']) <= CT_HASSIZE:
        size = int(ctype['size'])
    else:
        size = 8 if LJ_64 else 4  # CTSIZE_PTR.
    return dbg.sizeof('GCcdata') + size


def gco_size_tab(gcobj):
    # lj_tab_free(): the hash part, the array part unless colocated, the
    # GCtab with the colocated array.
    table = dbg.cast('GCtab *', gcobj)
    asize, hmask = int(table['asize']), int(table['hmask'])
    colo = int(table['colo'])
    size = dbg.sizeof('Node') * (hmask + 1) if hmask > 0 else 0
    if asize > 0 and colo <= 0:
        size += dbg.sizeof('TValue') * asize
    return size + dbg.sizeof('GCtab') + dbg.sizeof('TValue') * (colo & 0x7f)


def gco_size_udata(gcobj):
    # sizeudata() of lj_obj.h.
    return dbg.sizeof('GCudata') + int(dbg.cast('GCudata *', gcobj)['len'])


gco_sizes = {
    'LJ_TSTR':    gco_size_str,
    'LJ_TUPVAL':  gco_size_upval,
    'LJ_TTHREAD': gco_size_thread,
    'LJ_TPROTO':  gco_size_proto,
    'LJ_TFUNC':   gco_size_func,
    'LJ_TTRACE':  gco_size_trace,
    'LJ_TCDATA':  gco_size_cdata,
    'LJ_TTAB':    gco_size_tab,
    'LJ_TUDATA':  gco_size_udata,
}


def gco_size(gcobj):
    return gco_sizes[typenames(i2notu32(gcobj['gch']['gct']))](gcobj)


def gc_objects(g):
    '''Every GC object: the root list (with the userdata after the main
    thread), the ring of the ones waiting for their finalizers and the
    strings, which are in the chains of the string hash only.'''
    obj = gcref(g['gc']['root'])
    while obj:
        yield obj
        obj = gcref(obj['gch']['nextgc'])
    last = gcref(g['gc']['mmudata'])
    obj = last
    while obj:
        obj = gcref(obj['gch']['nextgc'])
        yield obj
        if obj == last:
            break
    strhash = g['strhash']
    for i in range(int(g['strmask']) + 1):
        obj = gcref(strhash[i])
        while obj:
            yield obj
            obj = gcref(obj['gch']['nextgc'])


def dump_gctop(g, amount):
    count, total, top = 0, 0, []
    for gcobj in gc_objects(g):
        size = gco_size(gcobj)
        count += 1
        total += size
        # The order of the visit breaks ties.
        heapq.heappush(top, (size, -count, gcobj))
        if len(top) > amount:
            heapq.heappop(top)
    dump = ['{count} GC objects, {total} bytes; the {n} largest:'.format(
        count=count, total=total, n=len(top))]
    dump += ['{size} bytes: {obj}'.format(size=size, obj=dump_gcobj(gcobj))
             for size, _, gcobj in sorted(top, reverse=True)]
    return '\n'.join(dump)


def proto_loc(proto):
    return '{chunk}:{firstline}'.format(
        chunk=strdata(dbg.cast('GCstr *', gcval(proto['chunkname']))),
        firstline=proto['firstline'],
    )


def funck(pt, idx):
    if idx >= 0:
        assert idx < pt['sizekn'], 'invalid idx for numeric constant in proto'
        tv = proto_knumtv(pt, idx)
        return dump_tvalue(tv)
    else:
        assert ~idx < pt['sizekgc'], 'invalid idx for GC constant in proto'
        gcobj = proto_kgc(pt, idx)
        if typenames(i2notu32(gcobj['gch']['gct'])) == 'LJ_TPROTO':
            return proto_loc(dbg.cast('GCproto *', gcobj))
        return dump_gcobj(gcobj)


def funcuvname(pt, idx):
    assert idx < pt['sizeuv'], 'invalid idx for upvalue in proto'
    uvinfo = mref('uint8_t *', pt['uvinfo'])
    if not uvinfo:
        return ''

    # if (idx) while (*uvinfo++ || --idx);
    while idx > 0:
        while uvinfo[0]:
            uvinfo += 1
        uvinfo += 1
        idx -= 1

    return 'upvalue {name} @ {addr}'.format(
        name=dbg.cstr(dbg.cast('char *', uvinfo)),
        addr=strx64(uvinfo)
    )


def dump_reg(rtype, value, jmp_format=None, jmp_ctx=None):
    if rtype == 'jump':
        # Destination of jump instruction encoded as offset from
        # BCBIAS_J.
        delta = value - 0x7fff
        if jmp_format:
            value = jmp_format(jmp_ctx, delta)
        else:
            prefix = '+' if delta >= 0 else ''
            value = prefix + str(delta)
    else:
        if rtype == 'lits' and value > 0x7fff:
            # A signed 16-bit literal (BCMlits), e.g. KSHORT -1.
            value -= 0x10000
        value = '{:3d}'.format(value)

    return '{rtype:6} {value}'.format(
        rtype=rtype + ':',
        value=value,
    )


def dump_kc(rtype, value, proto):
    kc = ''
    if proto:
        if rtype == 'str' or rtype == 'func':
            kc = funck(proto, ~value)
        elif rtype == 'num':
            kc = funck(proto, value)
        elif rtype == 'uv':
            kc = funcuvname(proto, value)

        if kc != '':
            kc = ' ; ' + kc
    return kc


def dump_bc(ins, jmp_format=None, jmp_ctx=None, proto=None):
    op = bc_op(ins)
    if op >= len(BYTECODES):
        return 'INVALID'

    bcname = BYTECODES[op]
    bcma = bcmode_a(op)
    bcmb = bcmode_b(op)
    bcmcd = bcmode_cd(op)

    kca = dump_kc(BCMODE[bcma], bc_a(ins), proto) if bcma else ''
    kcc = dump_kc(
        BCMODE[bcmcd], bc_c(ins) if bcmb else bc_d(ins), proto
    ) if bcmcd else ''

    return '{name:6} {ra}{rb}{rcd}{kc}'.format(
        name=bcname,
        ra=dump_reg(BCMODE[bcma], bc_a(ins)) + ' ' if bcma else '',
        rb=dump_reg(BCMODE[bcmb], bc_b(ins)) + ' ' if bcmb else '',
        rcd=dump_reg(
            BCMODE[bcmcd], bc_c(ins) if bcmb else bc_d(ins),
            jmp_format=jmp_format, jmp_ctx=jmp_ctx
        ) if bcmcd else '',
        kc=kca + kcc
    )


def dump_proto(proto):
    startbc = proto_bc(proto)
    func_loc = proto_loc(proto)
    # Location has the following format: '{chunk}:{firstline}'.
    dump = '{func_loc}-{lastline}'.format(
        func_loc=func_loc,
        lastline=proto['firstline'] + proto['numline'],
    )

    def jmp_format(npc_from, delta):
        return '=> ' + str(npc_from + delta).zfill(4)

    for bcnum in range(0, int(proto['sizebc'])):
        dump += ('\n' + str(bcnum).zfill(4) + ' ' + dump_bc(
            startbc[bcnum], jmp_format=jmp_format, jmp_ctx=bcnum, proto=proto,
        ))
    return dump


def dump_func(func):
    ffid = int(func['ffid'])

    if ffid == 0:
        pt = funcproto(func)
        return dump_proto(pt)
    elif ffid == 1:
        return 'C function @ {}'.format(strx64(func['f']))
    else:
        return dump_fast_function(ffid)


# FFI dumpers.


def dump_cdata_val_int64(cdata, ctype):
    info = ctype['info']
    isunsigned = info & CTF_UNSIGNED
    cdataval = cdataptr(cdata)
    valueptr = None
    usuffix = ''
    if isunsigned:
        usuffix = 'U'
        valueptr = dbg.cast('uint64_t *', cdataval)
    else:
        valueptr = dbg.cast('int64_t *', cdataval)
    return str(valueptr[0]) + usuffix + 'LL'


def dump_cdata_val_complex(cdata, ctype):
    size = ctype['size']
    cdataval = cdataptr(cdata)
    casttype = None
    if size == QWORDSZ * 2:
        casttype = 'double *'
    else:
        assert size == DWORDSZ * 2, 'bad (complex float) size'
        casttype = 'float *'
    re = dbg.cast(casttype, cdataval)[0]
    im = dbg.cast(casttype, cdataval)[1]
    sign = '+' if im > 0 else ''
    return '{re}{sign}{im}i'.format(re=re, im=im, sign=sign)


def ctype_preplit(ctypestr, lit):
    # Prevent extra space in the end of the string.
    space = ' ' if ctypestr != '' else ''
    return lit + space + ctypestr


def ctype_prepqual(ctypestr, info):
    if (info & CTF_VOLATILE):
        ctypestr = ctype_preplit(ctypestr, 'volatile')
    if (info & CTF_CONST):
        ctypestr = ctype_preplit(ctypestr, 'const')
    return ctypestr


def ctype_preptype(cts, ctypestr, ctype, qual, tp):
    nameref = gcref(ctype['name'])
    if nameref:
        ctypestr = ctype_preplit(ctypestr, re.sub('"', '', strdata(nameref)))
    else:
        ctypestr = ctype_preplit(ctypestr, str(ctype_typeid(cts, ctype)))
    ctypestr = ctype_preplit(ctypestr, tp)
    ctypestr = ctype_prepqual(ctypestr, qual)
    return ctypestr


# Partially moved the code from `ctype_repr()` here to make it
# more readable.
def ctype_prepnum(ctypestr, info, size):
    if info & CTF_BOOL:
        ctypestr = ctype_preplit(ctypestr, 'bool')
    elif info & CTF_FP:
        if size == QWORDSZ:
            ctypestr = ctype_preplit(ctypestr, 'double')
        elif size == DWORDSZ:
            ctypestr = ctype_preplit(ctypestr, 'float')
        else:
            assert size == QWORDSZ * 2, 'bad (long double) size'
            ctypestr = ctype_preplit(ctypestr, 'long double')
    elif size == 1:
        if not ((info ^ CTF_UCHAR) & CTF_UNSIGNED):
            ctypestr = ctype_preplit(ctypestr, 'char')
        elif CTF_UCHAR:
            ctypestr = ctype_preplit(ctypestr, 'signed char')
        else:
            ctypestr = ctype_preplit(ctypestr, 'unsigned char')
    elif size < 8:
        if size == 4:
            ctypestr = ctype_preplit(ctypestr, 'int')
        else:
            assert size == DWORDSZ // 2, 'bad (short) size'
            ctypestr = ctype_preplit(ctypestr, 'short')
        if info & CTF_UNSIGNED:
            ctypestr = ctype_preplit(ctypestr, 'unsigned')
    else:
        size_t = '{u}int{sz}_t'.format(
            u='u' if info & CTF_UNSIGNED else '',
            sz=size * 8,
        )
        ctypestr = ctype_preplit(ctypestr, size_t)
    return ctypestr


def ctype_repr(cts, id):
    ctype = ctype_get(cts, id)
    ctypestr = ''
    qual = 0
    ptrto = 0
    while True:
        info = ctype['info']
        size = ctype['size']
        ctp = ctype_type(info)
        if ctp == CT_NUM:
            ctypestr = ctype_prepnum(ctypestr, info, size)
            return ctype_prepqual(ctypestr, qual | info)
        elif ctp == CT_VOID:
            ctypestr = ctype_preplit(ctypestr, 'void')
            return ctype_prepqual(ctypestr, qual | info)
        elif ctp == CT_STRUCT:
            tp = 'union' if (info & CTF_UNION) else 'struct'
            return ctype_preptype(cts, ctypestr, ctype, qual, tp)
        elif ctp == CT_ENUM:
            if id == CTID_CTYPEID:
                return ctype_preplit(ctypestr, 'ctype')
            return ctype_preptype(cts, ctypestr, ctype, qual, 'enum')
        elif ctp == CT_ATTRIB:
            if ctype_attrib(info) == CTA_QUAL:
                qual |= size
        elif ctp == CT_PTR:
            if info & CTF_REF:
                ctypestr = ctype_preplit(ctypestr, '&')
            else:
                ctypestr = ctype_prepqual(ctypestr, qual | info)
                if LJ_64 and size == 4:
                    ctypestr = ctype_preplit(ctypestr, '__ptr32')
                ctypestr = ctype_preplit(ctypestr, '*')
            qual = 0
            ptrto = 1
        elif ctp == CT_ARRAY:
            if ctype_isrefarray(info):
                if ptrto:
                    ptrto = 0
                    ctypestr = '(' + ctypestr + ')'
                arrsize = ''
                if size != CTSIZE_INVALID:
                    child_size = ctype_child(cts, ctype)['size']
                    arrsize = str(int(size / child_size) if child_size > 0
                                  else 0)
                elif info & CTF_VLA:
                    arrsize = '?'
                ctypestr = ctypestr + '[{}]'.format(arrsize)
            elif ctype_iscomplex(info):
                if size == DWORDSZ * 2:
                    ctypestr = ctype_preplit(ctypestr, 'float')
                else:
                    assert size == QWORDSZ * 2, 'bad (complex double) size'
                return ctype_preplit(ctypestr, 'complex')
            else:
                ctypestr = ctype_preplit(
                    ctypestr,
                    '__attribute__((vector_size({})))'.format(size)
                )
        elif ctp == CT_FUNC:
            if ptrto:
                ptrto = 0
                ctypestr = '(' + ctypestr + ')'
            ctypestr += '()'
        ctype = ctype_child(cts, ctype)


def dump_ctype(ct):
    cts = ctype_ctsG(G(L()))
    cid = ctype_typeid(cts, ct)
    name = ctype_repr(cts, cid)
    return '[{id}] <{name}>'.format(
        id=cid,
        name=name,
    )


# JIT dumpers.


def dump_call_func(trace, callop):
    ctype = ''
    if callop > 0:
        ir = trace['ir'][REF_BIAS + callop]
        if IRTYPES[irt_type(ir['t'])] == 'nil':  # nil == CARG(func, ctype)
            callop = int(ir['op1']) - REF_BIAS
            cdt_idx_irk = trace['ir'][ir['op2']]
            assert IRS[cdt_idx_irk['o']] == 'KINT', \
                   'unexpected IR for ctype storage'
            ctype_idx = cdt_idx_irk['i']
            cts = ctype_ctsG(G(L()))
            ctype = 'ctype: {}'.format(dump_ctype(ctype_get(cts, ctype_idx)))

    func_str = ''
    if callop < 0:
        irk = trace['ir'][REF_BIAS + callop]
        assert IRS[irk['o']] == 'KINT64', \
               'unexpected IR for FFI function storage'
        func_addr = int(ir_kint64(irk)['u64'])
        # TODO: Symbol demangling.
        func_str = '[{:#x}]'.format(func_addr)
    else:
        func_str = '[{:04d}]'.format(callop)

    return func_str, ctype


def dump_call_args(trace, ins):
    if ins < 0:
        return '{{{}}}'.format(dump_irk(trace, ins))
    else:
        ir = trace['ir'][REF_BIAS + ins]
        irname = IRS[ir['o']]
        if irname == 'CARG':
            last_arg = ''
            args = dump_call_args(trace, int(ir['op1']) - REF_BIAS)
            op2 = int(ir['op2']) - REF_BIAS
            if op2 < 0:
                last_arg = '{{{}}}'.format(dump_irk(trace, op2))
            else:
                last_arg = '{{{:04d}}}'.format(op2)
            return args + ', ' + last_arg
        else:
            return '{{{:04d}}}'.format(ins)


# Special FP constant.
CONST_BIAS = 2 ** 52 + 2 ** 51


def dump_irk(trace, idx):
    ref = idx + REF_BIAS
    assert ref >= trace['nk'] and ref < REF_BIAS, 'bad constant in IR dump'
    irins = trace['ir'][ref]
    irname = IRS[irins['o']]
    slot = ''
    if irname == 'KSLOT':
        slot = ' KSLOT: @{}'.format(int(irins['op2']))
        irins = trace['ir'][irins['op1']]
        irname = IRS[irins['o']]

    irtype = irins['t']
    if irname == 'KPRI':
        typename = typenames(irt_toitype(irtype))
        # Trivial dump for primitives.
        irk = tv_dumpers.get(
            typename, dump_lj_tv_invalid  # noqa: F821 # Generated.
        )(0)
    elif irname == 'KINT':
        irk = 'integer {}'.format(dbg.cast('int32_t', irins['i']))
    elif irname == 'KGC':
        typename = typenames(irt_toitype(irtype))
        irk = gco_dumpers.get(typename, dump_lj_gco_invalid)(ir_kgc(irins))
    elif irname == 'KKPTR':
        addr = ir_kptr(irins)
        if addr == dbg.address(G(L())['nilnode']):
            return '[g->nilnode]' + slot
        irk = '[{}]'.format(strx64(addr))
    elif irname == 'KPTR':
        irk = '[{}]'.format(strx64(ir_kptr(irins)))
    elif irname == 'KNULL':
        irk = 'NULL'
    elif irname == 'KNUM':
        tv_num = ir_knum(irins)
        if float(tv_num['n']) == CONST_BIAS:
            return 'bias'
        irk = dump_lj_tv_numx(tv_num)
    elif irname == 'KINT64':
        irk = 'int64_t {}'.format(dbg.cast(
            'int64_t', int(ir_kint64(irins)['u64'])
        ))
    else:
        return 'Unknown IRK: ' + irname
    return irk + slot


def dump_irins(irins, trace=None):
    irop = int(irins['o'])
    if irop >= len(IRS):
        return 'INVALID'

    irname = IRS[irop]
    leftop = ir_left(irop)
    rightop = ir_right(irop)
    irt = irins['t']
    is_sinksunk = irins['r'] == RID_SINK or irins['r'] == RID_SUNK
    flags = '{is_sinksunk}{is_marked}{is_guard}{is_phi}'.format(
        # Sink flag should be the first to match sink slots during
        # the dump of registers.
        is_sinksunk='}' if is_sinksunk else ' ',
        is_marked='!' if irt_ismarked(irt) else ' ',
        is_guard='>' if irt_isguard(irt) else ' ',
        is_phi='+' if irt_isphi(irt) else ' '
    )

    if not trace:
        g = G(L(None))
        compiling = jit_state(g) != 'IDLE'
        assert compiling, 'attempt to dump IR for J.cur trace in bad VM state'
        trace = J(g)['cur']

    left = ''
    right = ''
    lisref = leftop == 'ref'
    risref = rightop == 'ref'
    op1 = int((irins['op1'] - REF_BIAS) if lisref else irins['op1'])
    op2 = int((irins['op2'] - REF_BIAS) if risref else irins['op2'])

    skip_right = False
    if re.match('CALL', irname):
        ctype = ''
        args = ''
        if rightop == 'lit':
            func = IR_CALLS[op2]
        else:
            func, ctype = dump_call_func(trace, op2)

        if op1 != -1:
            args = dump_call_args(trace, int(op1))

        return '{flags} {type} {name:6} [{mode:2}] {f}({args}) {ct}'.format(
            flags=flags,
            name=irname,
            mode=ir_mode(irop),
            type=IRTYPES[irt_type(irt)],
            ct=ctype,
            args=args,
            f=func,
        )
    elif irname == 'CNEW' and op2 == -1:
        left = dump_irk(trace, op1)
        skip_right = True
    elif leftop:
        if op1 < 0:
            left = dump_irk(trace, op1)
        elif leftop == 'cst':
            idx = irins - dbg.address(trace['ir'][REF_BIAS])
            left = dump_irk(trace, idx)
        else:
            left = ('{:04d}' if lisref else '#{:<3d}').format(op1)

        if rightop:
            if rightop == 'lit':
                litname = IR_LITNAMES.get(irname, None)
                if litname:
                    # Try to handle `lj_ir_ggfload()`.
                    ggfname = None
                    if irname == 'FLOAD' and left == 'nil' \
                       and op2 >= len(IRFIELDS):
                        ggfname = ggfname_by_offset(op2 << 2)

                    if ggfname:
                        right = ggfname
                    else:
                        right = litname(op2)
                elif irname == 'UREFO' or irname == 'UREFC':
                    right = '#{:<3d}'.format(op2 >> 8)
                else:
                    right = '#{:<3d}'.format(op2)
            elif op2 < 0:
                right = dump_irk(trace, op2)
            else:
                right = ('{:04d}').format(op2)

    typename = ''
    if irname == 'LOOP':
        typename = '---'
    elif irname == 'NOP':
        typename = '   '
    else:
        typename = IRTYPES[irt_type(irt)]

    return '{flags} {type} {name:6} [{mode:2}] {left:<9s} {right}'.format(
        flags=flags,
        name=irname,
        mode=ir_mode(irop),
        type=typename,
        left=(leftop + ': ' + left) if leftop else '',
        right=(rightop + ': ' + right) if rightop and not skip_right else '',
    )


def dump_snap(trace, snapno, snap):
    dump = 'SNAP   #{:<3d} ['.format(snapno)
    snap_map = dbg.address(trace['snapmap'][snap['mapofs']])
    snap_entry_num = 0
    for slot in range(0, snap['nslots']):
        dump += ' '
        snap_entry = int(snap_map[snap_entry_num])
        if snap_entry_num < snap['nent'] and snap_entry >> TREF_SHIFT == slot:
            snap_entry_num += 1
            ref = int((snap_entry & TREF_REFMASK) - REF_BIAS)
            if ref < 0:
                if int(snap_entry) == SNAP_FR2_SLOT:
                    dump += '----'
                    continue
                elif (snap_entry & TREF_CONT):
                    dump += 'contpc'
                elif (snap_entry & TREF_FRAME):
                    dump += 'ftsz '
                else:
                    dump += '{{{const}}}'.format(const=dump_irk(trace, ref))
            elif snap_entry & SNAP_SOFTFPNUM:
                dump += '{:04d}/{:04d}'.format(ref, ref + 1)
            else:
                dump += '{:04d}'.format(ref)

            if snap_entry & SNAP_FRAME:
                dump += '|'
        else:
            dump += '----'

    dump += ' ]\n'
    return dump


def dump_sink_slot(rid, spill, ins_number):
    assert rid == RID_SINK or rid == RID_SUNK, 'incorrect rid in sink dump'
    tp = 'sink' if rid == RID_SINK else 'sunk'
    return '{{{}'.format(tp) if spill == RID_INIT or spill == SPS_NONE \
           else '{{{:04d}'.format(int(ins_number - spill))


def dump_regsp(irins, ins_number):
    rid = irins['r']
    spill = irins['s']
    if rid == RID_SINK or rid == RID_SUNK:
        return dump_sink_slot(rid, spill, ins_number)
    elif irins['prev'] > 255:
        return '[{:#05x}]'.format(int(spill * 4))
    elif rid < 128:
        return regname(rid)
    else:
        return ''


def dump_trace(trace, flags):
    dump = 'Trace {num} start\n\tproto: {start_pt}\n\tBC: {start_bc}'.format(
        num=trace['traceno'],
        start_pt=gcref(trace['startpt']),
        start_bc=mref('BCIns *', trace['startpc']),
    )

    nins = trace['nins'] - REF_BIAS
    dump += '\n---- TRACE IR'
    nsnap = 0
    snap = trace['snap'][nsnap]
    snapref = snap['ref']
    for irnum in range(1, nins):
        dump += '\n'
        irref = REF_BIAS + irnum
        if 's' in flags and irref >= snapref and nsnap < trace['nsnap']:
            dump += '....          '
            if 'r' in flags:
                dump += ' ' * 7
            dump += dump_snap(trace, nsnap, snap)
            nsnap += 1
            snap = trace['snap'][nsnap]
            snapref = snap['ref']
        dump += '{:04d} '.format(irnum)
        if 'r' in flags:
            dump += '{:>7}'.format(dump_regsp(trace['ir'][irref], irnum))
        dump += dump_irins(trace['ir'][irref], trace)
    return dump


def dump_tref(tref):
    return '[{F}{C}] {tp} {ref:#x}'.format(
        F='F' if tref & TREF_FRAME else ' ',
        C='C' if tref & TREF_CONT else ' ',
        tp=IRTYPES[tref_type(tref)],
        ref=tref_ref(tref)
    )


def dump_jslots(J):
    dump = []
    maxslot = J['baseslot'] + J['maxslot']
    first_base_slot = 1 + LJ_FR2
    for n in reversed(range(first_base_slot, maxslot)):
        tref = J['slot'][n]
        ref = tref_ref(tref)
        address = dbg.address(tref)
        dump.append('{addr} {nslot:04d} {base:1s} {tref}{const}'.format(
            addr=address,
            base='B' if address == J['base'] else ' ',
            nslot=n,
            tref=dump_tref(tref),
            const=' ' + dump_irk(J['cur'], ref - REF_BIAS)
                  if ref != 0 and ref < REF_BIAS else ''
        ))
    return '\n'.join(dump)


# Extension commands helpers. ####################################


def cast_command_arg(typestr, arg):
    if not arg:
        raise dbg.error('Argument is required')
    return dbg.cast(typestr, dbg.eval(arg))


# Extension commands. ############################################


class LJDumpArch(dbg.LJBase):
    '''
lj-arch

The command requires no args and dumps values of LJ_64 and LJ_GC64
compile-time flags. These values define the sizes of host and GC
pointers, respectively. Also, it dumps the value for the LJ_DUALNUM
compile-time flag to inspect if LuaJIT is built in dual-number mode.
    '''

    def execute(self, arg):
        dbg.print(
            'LJ_64: {LJ_64}, LJ_GC64: {LJ_GC64}, LJ_DUALNUM: {LJ_DUALNUM}'
            .format(
                LJ_64=LJ_64,
                LJ_GC64=LJ_GC64,
                LJ_DUALNUM=LJ_DUALNUM
            )
        )


class LJDumpBC(dbg.LJBase):
    '''
lj-bc <BCIns *>

The command receives a pointer to a bytecode instruction and dumps
the type of the instruction and the values of RA, RB, and RC (or RD)
virtual registers and their modes (operand types):

<BCNAME>  <modeA>: <RA>
<BCNAME>  <modeA>: <RA>  <modeB>: <RB>  <modeC>: <RC> ; <const> ; <uvname>
<BCNAME>  <modeA>: <RA>  <modeD>: <RD>

<BCNAME>: Name of the bytecode instruction
<R[ABCD]>: The value of the R[ABCD] virtual register operand
<mode[ABCD]>: The operand type for the R[ABCD] register
<const>: The value of the constant associated with the operand, if any
<uvname>: The name of the upvalue, if any

For the list of bytecode names and modes (operand types), see:
https://github.com/tarantool/tarantool/wiki/LuaJIT-Bytecodes.
    '''

    def execute(self, arg):
        dbg.print(dump_bc(cast_command_arg('BCIns *', arg)[0]))


class LJDumpCType(dbg.LJBase):
    '''
lj-ctype <CType *>

The command receives a pointer <ctype> of the corresponding CType
and dumps the ID and the name for this C data type.
    '''

    def execute(self, arg):
        dbg.print(dump_ctype(cast_command_arg('CType *', arg)))


class LJDumpFunc(dbg.LJBase):
    '''
lj-func <GCfunc *>

The command receives a <gcr> of the corresponding GCfunc object and dumps
the chunk name, where the corresponding function is defined, the
corresponding range of lines, and a list of bytecodes related to this
function:

<file>:<start>-<end>
<bcnum>  <BC>
...
<bcnum>  <BC>

<file>: The location of the corresponding function definition
<start>: The number of the line where the function starts
<end>: The number of the line where the function ends
<bcnum>: The sequential number of the bytecode instruction
<BC>: The encoded bytecode instruction. Type "help lj-bc" for details.
    '''

    def execute(self, arg):
        dbg.print(dump_func(cast_command_arg('GCfuncC *', arg)))


class LJGC(dbg.LJBase):
    '''
lj-gc

The command requires no args and dumps current GC stats:
* total: <total number of allocated bytes in GC area>
* threshold: <limit when gc step is triggered>
* debt: <how much GC is behind schedule>
* estimate: <estimate of memory actually in use>
* stepmul: <incremental GC step granularity>
* pause: <pause between successive GC cycles>
* sweepstr: <sweep position in string table>
* root: <number of all collectable objects>
* gray: <number of gray objects>
* grayagain: <number of objects for atomic traversal>
* weak: <number of weak tables (to be cleared)>
* mmudata: <number of udata|cdata to be finalized>
    '''

    def execute(self, arg):
        g = G(L(None))
        dbg.print('GC stats: {state}\n{stats}'.format(
            state=gc_state(g),
            stats=dump_gc(g)
        ))


class LJGCTop(dbg.LJBase):
    '''
lj-gctop [<amount>]

The command walks every GC object (the root list, the objects waiting
for finalizers and the strings) and dumps the number of objects, their
total size and the <amount> (10 by default) largest of them, the
largest first:

<size> bytes: <GCobj>

<size>: The size of the object as the GC frees it, with its stack,
        array and hash parts, upvalues, payload
<GCobj>: See help lj-gco for more info

The walk takes a while on a big heap: some seconds per million objects.
    '''

    def execute(self, arg):
        try:
            amount = int(arg) if arg else 10
        except ValueError:
            raise dbg.error('Amount must be a number: "{}"'.format(arg))
        if amount < 1:
            raise dbg.error('Amount must be positive: "{}"'.format(arg))
        dbg.print(dump_gctop(G(L(None)), amount))


class LJDumpGCobj(dbg.LJBase):
    '''
lj-gco <GCobj *>

The command receives a pointer to <GCobj> (GCobj address) and dumps
the type and some info related to it.

* LJ_TSTR: string <string payload> @ <gcr>
* LJ_TUPVAL: upvalue @ <gcr>
* LJ_TTHREAD: thread @ <gcr>
* LJ_TPROTO: proto @ <gcr>
* LJ_TFUNC: <LFUNC|CFUNC|FFUNC>
  <LFUNC>: Lua function @ <gcr>, <nupvals> upvalues, <chunk:line>
  <CFUNC>: C function <mcode address>
  <FFUNC>: fast function <ffname> (#<ffid>)
* LJ_TTRACE: trace <traceno> @ <gcr>
* LJ_TCDATA: cdata @ <gcr>
* LJ_TTAB: table @ <gcr> (asize: <asize>, hmask: <hmask>)
* LJ_TUDATA: userdata @ <gcr>

Whether the type of the given address differs from the listed above, then
error message occurs.
    '''

    def execute(self, arg):
        dbg.print(dump_gcobj(cast_command_arg('GCobj *', arg)))


class LJDumpIR(dbg.LJBase):
    '''
lj-ir <IRIns *>

The command receives a pointer to <ir> (IRIns address) and dumps
the IR type and some info related to it. The format is similar to
the `jit.dump` tool but also provides information about IR mode and
operands modes.

For the list of IR names and modes (operand types), see:
https://github.com/tarantool/tarantool/wiki/LuaJIT-SSA-IR.
    '''

    def execute(self, arg):
        dbg.print(dump_irins(cast_command_arg('IRIns *', arg)))


class LJDumpJSlots(dbg.LJBase):
    '''
lj-jslots [<lua_State *>]

The command receives an optional lua_State address and dumps the
slots of JIT stack map:

<slot ptr> <slot number> [<FRAME|CONTINUATION>] <IR reference>

The lua_State pointer is optional to help in finding the VM's JIT state
when there is no coroutine to be inspected in the debugged frame.
    '''

    def execute(self, arg):
        lstate = L(dbg.eval(arg) if arg else None)
        dbg.print(dump_jslots(J(G(lstate))))


class LJDumpProto(dbg.LJBase):
    '''
lj-proto <GCproto *>

The command receives a <gcr> of the corresponding GCproto object and dumps
the chunk name, where the corresponding function is defined, the
corresponding range of lines, and a list of bytecodes related to this
function:

<file>:<start>-<end>
<bcnum>  <BC>
...
<bcnum>  <BC>

<file>: The location of the corresponding function definition
<start>: The number of the line where the function starts
<end>: The number of the line where the function ends
<bcnum>: The sequential number of the bytecode instruction
<BC>: The encoded bytecode instruction. Type "help lj-bc" for details.
    '''

    def execute(self, arg):
        dbg.print(dump_proto(cast_command_arg('GCproto *', arg)))


class LJDumpStack(dbg.LJBase):
    '''
lj-stack [<lua_State *>]

The command receives a lua_State address and dumps the given Lua
coroutine guest stack:

<slot ptr> [<slot attributes>] <VALUE|FRAME>

* <slot ptr>: guest stack slot address
* <slot attributes>:
  - S: Bottom of the stack (the slot L->stack points to)
  - B: Base of the current guest frame (the slot L->base points to)
  - T: Top of the current guest frame (the slot L->top points to)
  - M: Last slot of the stack (the slot L->maxstack points to)
* <VALUE>: See help lj-tv for more info
* <FRAME>: Framelink slot differs from the value slot: it contains info
  related to the function being executed within this guest frame, its
  type, and a link to the parent guest frame
  [<frame type>] delta=<slots in frame>, <lj-tv for LJ_TFUNC slot>
  - <frame type>:
    + L:  VM performs a call as a result of bytecode execution
    + C:  VM performs a call as a result of lj_vm_call
    + M:  VM performs a call to a metamethod as a result of bytecode
          execution
    + V:  Variable-length frame for storing arguments of a variadic
          function
    + CP: Protected C frame
    + PP: VM performs a call as a result of executing pcall or xpcall

If L is omitted, the main coroutine is used.
    '''

    def execute(self, arg):
        lstate = L(dbg.eval(arg) if arg else None)
        dbg.print(dump_stack(lstate))


class LJState(dbg.LJBase):
    @classmethod
    def pre_register(cls):
        cls.__doc__ = '''
lj-state
The command requires no args and dumps current VM and GC states:
* VM state: <{}>
* GC state: <PAUSE|PROPAGATE|ATOMIC|SWEEPSTRING|SWEEP|FINALIZE>
* JIT state: <IDLE|ACTIVE|RECORD|START|END|ASM|ERR>
    '''.format('|'.join(list(VM_STATES) + [vm_state_trace('TRACENO')]))

    def execute(self, arg):
        g = G(L(None))
        dbg.print('\n'.join(
            map(lambda t: '{} state: {}'.format(*t), {
                'VM':  vm_state(g),
                'GC':  gc_state(g),
                'JIT': jit_state(g),
            }.items())
        ))


class LJDumpString(dbg.LJBase):
    '''
lj-str <GCstr *>

The command receives a <gcr> of the corresponding GCstr object and dumps
the payload, size in bytes and hash.

*Caveat*: Since Python 2 provides no native Unicode support, the payload
is replaced with the corresponding error when decoding fails.
    '''

    def execute(self, arg):
        string = cast_command_arg('GCstr *', arg)
        dbg.print("String: {body} [{len} bytes] with hash {hash}".format(
            body=strdata(string),
            hash=strx64(string['hash']),
            len=string['len'],
        ))


class LJDumpTable(dbg.LJBase):
    '''
lj-tab <GCtab *>

The command receives a GCtab address and dumps the table contents:
* Metatable address whether the one is set
* Array part <asize> slots:
  <aslot ptr>: [<index>]: <tv>
* Hash part <hsize> nodes:
  <hnode ptr>: { <tv> } => { <tv> }; next = <next hnode ptr>
    '''

    def execute(self, arg):
        t = cast_command_arg('GCtab *', arg)
        array = mref('TValue *', t['array'])
        nodes = mref('struct Node *', t['node'])
        mt = gcval(t['metatable'])
        capacity = {
            'apart': int(t['asize']),
            'hpart': int(t['hmask'] + 1) if t['hmask'] > 0 else 0
        }

        if mt != 0:
            dbg.print('Metatable detected: {}'.format(strx64(mt)))

        dbg.print('Array part: {} slots'.format(capacity['apart']))
        for i in range(capacity['apart']):
            slot = array + i
            dbg.print('{ptr}: [{index}]: {value}'.format(
                ptr=slot,
                index=i,
                value=dump_tvalue(slot)
            ))

        dbg.print('Hash part: {} nodes'.format(capacity['hpart']))
        # See hmask comment in lj_obj.h
        for i in range(capacity['hpart']):
            node = nodes + i
            dbg.print('{ptr}: {{ {key} }} => {{ {val} }}; next = {n}'.format(
                ptr=node,
                key=dump_tvalue(node['key']),
                val=dump_tvalue(node['val']),
                n=mref('struct Node *', node['next'])
            ))


class LJDumpTValue(dbg.LJBase):
    '''
lj-tv <TValue *>

The command receives a pointer to <tv> (TValue address) and dumps
the type and some info related to it.

* LJ_TNIL: nil
* LJ_TFALSE: false
* LJ_TTRUE: true
* LJ_TLIGHTUD: light userdata @ <gcr>
* LJ_TSTR: string <string payload> @ <gcr>
* LJ_TUPVAL: upvalue @ <gcr>
* LJ_TTHREAD: thread @ <gcr>
* LJ_TPROTO: proto @ <gcr>
* LJ_TFUNC: <LFUNC|CFUNC|FFUNC>
  <LFUNC>: Lua function @ <gcr>, <nupvals> upvalues, <chunk:line>
  <CFUNC>: C function <mcode address>
  <FFUNC>: fast function <ffname> (#<ffid>)
* LJ_TTRACE: trace <traceno> @ <gcr>
* LJ_TCDATA: cdata @ <gcr>
* LJ_TTAB: table @ <gcr> (asize: <asize>, hmask: <hmask>)
* LJ_TUDATA: userdata @ <gcr>
* LJ_TNUMX: <number|integer> <numeric payload>

Whether the type of the given address differs from the listed above, then
error message occurs.
    '''

    def execute(self, arg):
        dbg.print(dump_tvalue(cast_command_arg('TValue *', arg)))


class LJDumpTrace(dbg.LJBase):
    '''
lj-trace [/FLAGS] <GCtrace *>

The command receives a pointer to <trace> (IRIns address) and dumps
its number, IRs, and information about start location. The format is
similar to the `jit.dump` tool but also provides information about
IR mode and operands modes.

Trace may be preceded with /FLAGS:
* r: Dump registers associated with IR, if any.
* s: Dump snapshots for the trace.
    '''

    def execute(self, arg):
        arg, flags = dbg.extract_flags(arg, 'rs')
        dbg.print(dump_trace(cast_command_arg('GCtrace *', arg), flags))


def load(event=None):
    dbg.initialize_extension({
        'lj-arch':   LJDumpArch,
        'lj-bc':     LJDumpBC,
        'lj-ctype':  LJDumpCType,
        'lj-func':   LJDumpFunc,
        'lj-gc':     LJGC,
        'lj-gco':    LJDumpGCobj,
        'lj-gctop':  LJGCTop,
        'lj-ir':     LJDumpIR,
        'lj-jslots': LJDumpJSlots,
        'lj-proto':  LJDumpProto,
        'lj-stack':  LJDumpStack,
        'lj-state':  LJState,
        'lj-str':    LJDumpString,
        'lj-tab':    LJDumpTable,
        'lj-trace':  LJDumpTrace,
        'lj-tv':     LJDumpTValue,
    })


if gdb:
    load()
elif lldb:
    def __lldb_init_module(debugger, internal_dictionary):
        load()
