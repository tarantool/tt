"""
GDB extension for Tarantool post-mortem analysis.
To use, just put 'source <path-to-this-file>' in gdb.
"""
# The universal one of github.com/buyemune-sys/tarantool-gdb-tools: for
# every Tarantool 3.x.  From tools/tarantool-gdb.py of tarantool/tarantool,
# BSD 2-Clause license (Copyright 2010-2020 Tarantool AUTHORS, LICENSE of
# tarantool/tarantool); see NOTICE of that repository.

import argparse
import base64
import logging
import struct
import itertools
import re
import sys
from collections import namedtuple

import gdb
import gdb.printing
import gdb.types

if sys.version_info[0] == 2:
    map = itertools.imap
    filter = itertools.ifilter
    zip = itertools.izip
elif sys.version_info[0] == 3:
    unicode = str


enumauto = itertools.count().__next__
arch_id_x86_64 = enumauto()
arch_id_aarch64 = enumauto()


def map_to_arch_id(arch: gdb.Architecture):
    arch_name = arch.name().lower()
    if "x86-64" in arch_name:
        return arch_id_x86_64
    elif "aarch64" in arch_name:
        return arch_id_aarch64
    return None


logger = logging.getLogger('gdb.tarantool')
logger.setLevel(logging.WARNING)

log_depth = 0

def log(msg):
    global log_depth
    # gdb.write('    '*log_depth + msg + '\n')
    pass
def log_enter(title):
    global log_depth
    log(title)
    log_depth += 1
def log_leave():
    global log_depth
    log_depth -= 1
    log('end')

def dump_type(type):
    return 'tag={} code={} sizeof={}'.format(type.tag, type.code, type.sizeof)

def equal_types(type1, type2):
    return type1.code == type2.code and type1.tag == type2.tag

def equal_to_any_types(type, types):
    for t in types:
        if equal_types(type, t):
            return True
    return False

def int_from_address(address):
    return int(address.cast(gdb.lookup_type('uint64_t')))

# couple of functions below don't raise an exception and should be used
# when type/value may or may not exist

def find_type(type):
    try:
        return gdb.lookup_type(type)
    except Exception:
        return None

def find_value(sym):
    try:
        return gdb.parse_and_eval(sym)
    except Exception:
        return None

def dwarf_has(what, values=(), types=(), symbols=()):
    """The debug info has every value (an enum member, a variable), type
    and symbol a printer needs; if not, say once that the printer is off,
    instead of failing the whole file (an enum used only through its
    constants may be left out of the DWARF, tarantool/tarantool#13094)."""
    for kind, names, found in (('value', values, find_value),
                               ('type', types, find_type),
                               ('symbol', symbols, symbol_exists)):
        for name in names:
            res = found(name)       # an enum member may be 0: no bool()
            if res is None or res is False:
                gdb.write('tarantool_gdb.py: no {} {} in the debug info, '
                          'the {} printer is off\n'.format(kind, name, what))
                return False
    return True

def symbol_exists(name):
    if gdb.lookup_global_symbol(name) is not None:
        return True
    if hasattr(gdb, 'lookup_static_symbol') and \
            gdb.lookup_static_symbol(name) is not None:
        return True
    try:
        return gdb.lookup_symbol(name)[0] is not None
    except gdb.error:
        return False

def find(predicate, iterable, default=None):
    return next(filter(predicate, iterable), default)

def enumval_name(type, val, default):
    assert type.code == gdb.TYPE_CODE_ENUM, "enumval_name expects enum type \
        but type of code {} was given".format(type.code)
    enumval_attr = 'enumval'
    if not hasattr(type.fields()[0], 'enumval'):
        enumval_attr = 'bitpos'
    field = find(lambda x: getattr(x, enumval_attr) == val, type.fields())
    return default if field is None else field.name

def cast_ptr(dest_type, ptr, offset):
    dest_ptr = ptr.cast(gdb.lookup_type('char').pointer()) + offset
    return dest_ptr.cast(dest_type.pointer())

def offset_ptr(ptr, offset):
    return cast_ptr(ptr.type.target(), ptr, offset)

def container_of(ptr, container_type, field):
    return cast_ptr(container_type, ptr, -(container_type[field].bitpos // 8)).dereference()

def struct_to_dict(val, fields):
    """The fields of a struct value as Python ints (lists for arrays): the
    printers of decimal, uuid and datetime do Python arithmetic and
    formatting, which a gdb.Value does not support ('//', '{:x}')."""
    res = {}
    for name in fields:
        v = val[name]
        if v.type.strip_typedefs().code == gdb.TYPE_CODE_ARRAY:
            low, high = v.type.strip_typedefs().range()
            res[name] = [int(v[i]) for i in range(low, high + 1)]
        else:
            res[name] = int(v)
    return res

INT32_MAX = 2**31 - 1
INT32_MIN = -2**31
INT_MAX = INT32_MAX
INT_MIN = INT32_MIN

pp = gdb.printing.RegexpCollectionPrettyPrinter("tarantool")
gdb.printing.register_pretty_printer(gdb.current_objfile(), pp, True)


class MsgPackError(Exception):
    def __init__(self, address):
        self.address = address

class MsgPackInvalidFormatError(MsgPackError): pass

class MsgPackExtError(MsgPackError):
    def __init__(self, address, ext_type):
        super(MsgPackExtError, self).__init__(address)
        self.ext_type = ext_type

class MsgPackExtLengthMismatchError(MsgPackExtError):
    def __init__(self, address, ext_type, len_decoded, len_expected):
        super(MsgPackExtLengthMismatchError, self).__init__(address, ext_type)
        self.len_decoded = len_decoded
        self.len_expected = len_expected


class MsgPack(object):
    # types
    enumauto = itertools.count().__next__
    TYPE_INVALID = enumauto()
    TYPE_NIL = enumauto()
    TYPE_BOOL = enumauto()
    TYPE_INT = enumauto()
    TYPE_UINT = enumauto()
    TYPE_FLOAT = enumauto()
    TYPE_STR = enumauto()
    TYPE_BIN = enumauto()
    TYPE_ARRAY = enumauto()
    TYPE_MAP = enumauto()
    TYPE_EXT = enumauto()

    # number types
    enumauto = itertools.count().__next__
    NUM_INT = enumauto()
    NUM_UINT = enumauto()
    NUM_FLOAT = enumauto()

    def decode_invalid(self):
        raise MsgPackInvalidFormatError(int_from_address(self.__val))

    @classmethod
    def read(cls, val, offset, size):
        tmp = val.cast(gdb.lookup_type('uint8_t').pointer())
        buf = bytearray(size)
        for i in range(size):
            buf[i] = int(tmp[offset + i])
        return buf

    @classmethod
    def decode_num(cls, val, offset, num_type, num_len, big_endian=True):
        packfmt = ('>' if big_endian else '<') + {
            cls.NUM_UINT: {
                1: 'B',
                2: 'H',
                4: 'I',
                8: 'Q',
            },
            cls.NUM_INT: {
                1: 'b',
                2: 'h',
                4: 'i',
                8: 'q',
            },
            cls.NUM_FLOAT: {
                4: 'f',
                8: 'd',
            },
        }[num_type][num_len]
        return struct.unpack_from(packfmt, cls.read(val, offset, num_len))[0]

    # UINT
    def decode_uint(self):
        return self.decode_num(self.__val, 1, self.NUM_UINT, len(self) - 1)

    # INT
    def decode_int(self):
        return self.decode_num(self.__val, 1, self.NUM_INT, len(self) - 1)

    # FLOAT
    def decode_float(self):
        return self.decode_num(self.__val, 1, self.NUM_FLOAT, len(self) - 1)

    # STR
    def len_str(self, len_size):
        return len_size + self.decode_num(self.__val, 1, self.NUM_UINT, len_size)
    def decode_str(self, len_size=0):
        offset = 1 + len_size
        return self.read(self.__val, offset, len(self) - offset)

    # BIN
    len_bin = len_str
    decode_bin = decode_str

    # Collection helpers
    def decode_and_advance(self, offset):
        mp = self.decode(self.__val + offset)
        return mp, offset + len(mp)

    def len_collection(self, len_size, len_item_func):
        return len_size + sum(map(len_item_func, self.value))
    def decode_collection_items(self, num_items, offset, decode_item_func):
        items = []
        for _ in range(num_items):
            item, offset = decode_item_func(offset)
            yield item
            items.append(item)
        self.__decode = items
    def decode_fixcollection(self, num_items, decode_item_func):
        return self.decode_collection_items(num_items, 1, decode_item_func)
    def decode_collection(self, len_size, decode_item_func):
        num_items = self.decode_num(self.__val, 1, self.NUM_UINT, len_size)
        return self.decode_collection_items(num_items, 1 + len_size, decode_item_func)

    # ARRAY
    decode_array_item = decode_and_advance
    def decode_fixarray(self, num_items):
        return self.decode_fixcollection(num_items, self.decode_array_item)
    def decode_array(self, len_size):
        return self.decode_collection(len_size, self.decode_array_item)
    def len_array(self, len_size=0):
        return self.len_collection(len_size, len)

    # MAP
    def decode_map_item(self, offset):
        k, offset = self.decode_and_advance(offset)
        v, offset = self.decode_and_advance(offset)
        return (k, v), offset
    def decode_fixmap(self, num_items):
        return self.decode_fixcollection(num_items, self.decode_map_item)
    def decode_map(self, len_size):
        return self.decode_collection(len_size, self.decode_map_item)
    def len_map(self, len_size=0):
        return self.len_collection(len_size, lambda item: len(item[0]) + len(item[1]))

    # EXT
    __ext_decoders = {}
    __ext_default_decoder = lambda ext_type, ext_len, val, offset: \
        MsgPack.read(val, offset, ext_len)

    @classmethod
    def set_ext_default_decoder(cls, decoder):
        cls.__ext_default_decoder = decoder

    @classmethod
    def register_ext_decoder(cls, type, decoder):
        cls.__ext_decoders[type] = decoder

    @classmethod
    def ext_decoder(cls, type):
        return cls.__ext_decoders.get(type, cls.__ext_default_decoder)

    def len_ext(self, len_size):
        return len_size + 1 + self.decode_num(self.__val, 1, self.NUM_UINT, len_size)
    def decode_ext(self, len_size=0):
        ext_type = self.decode_num(self.__val, 1 + len_size, self.NUM_INT, 1)
        offset = 1 + len_size + 1 # fmt | [ext_len] | ext_type
        ext_len = len(self) - offset
        ext_decoder = self.ext_decoder(ext_type)(ext_type, ext_len, self.__val, offset)
        if len(ext_decoder) != ext_len:
            raise MsgPackExtLengthMismatchError(
                int_from_address(self.val), ext_type, len(ext_decoder), ext_len)
        return ext_decoder

    class FmtTraits(namedtuple('FmtTraits', [
        'type',
        'len', # number of bytes beyond the first byte (either number or function)
        'decode', # function that decodes value or the value itself
    ])):
        __slots__ = ()

        def __new__(cls, type, len, decode, len_size=None):
            if len_size is None:
                return cls._make((type, len, decode))
            else:
                return cls._make((
                    type,
                    lambda self: len(self, len_size),
                    lambda self: decode(self, len_size),
                ))

    # prepare LUT for quick decoding
    __lut = []
    for i in range(0x80): # 0x00 - 0x7f
        __lut.append(FmtTraits(TYPE_UINT, 0, i))
    for i in range(0x10): # 0x80 - 0x8f
        __lut.append(FmtTraits(TYPE_MAP, len_map,
                                lambda self, n=i: self.decode_fixmap(n)))
    for i in range(0x10): # 0x90 - 0x9f
        __lut.append(FmtTraits(TYPE_ARRAY, len_array,
                                lambda self, n=i: self.decode_fixarray(n)))
    for i in range(0x20): # 0xa0 - 0xbf
        __lut.append(FmtTraits(TYPE_STR, i, decode_str))
    __lut.extend([
        FmtTraits(TYPE_NIL, 0, None), # 0xc0
        FmtTraits(TYPE_INVALID, 0, decode_invalid), # 0xc1
        FmtTraits(TYPE_BOOL, 0, False), # 0xc2
        FmtTraits(TYPE_BOOL, 0, True), # 0xc3
        FmtTraits(TYPE_BIN, len_bin, decode_bin, 1), # 0xc4
        FmtTraits(TYPE_BIN, len_bin, decode_bin, 2), # 0xc5
        FmtTraits(TYPE_BIN, len_bin, decode_bin, 4), # 0xc6
        FmtTraits(TYPE_EXT, len_ext, decode_ext, 1), # 0xc7
        FmtTraits(TYPE_EXT, len_ext, decode_ext, 2), # 0xc8
        FmtTraits(TYPE_EXT, len_ext, decode_ext, 4), # 0xc9
        FmtTraits(TYPE_FLOAT, 4, decode_float), # 0xca
        FmtTraits(TYPE_FLOAT, 8, decode_float), # 0xcb
        FmtTraits(TYPE_UINT, 1, decode_uint), # 0xcc
        FmtTraits(TYPE_UINT, 2, decode_uint), # 0xcd
        FmtTraits(TYPE_UINT, 4, decode_uint), # 0xce
        FmtTraits(TYPE_UINT, 8, decode_uint), # 0xcf
        FmtTraits(TYPE_INT, 1, decode_int), # 0xd0
        FmtTraits(TYPE_INT, 2, decode_int), # 0xd1
        FmtTraits(TYPE_INT, 4, decode_int), # 0xd2
        FmtTraits(TYPE_INT, 8, decode_int), # 0xd3
        FmtTraits(TYPE_EXT, 1+1, decode_ext), # 0xd4
        FmtTraits(TYPE_EXT, 1+2, decode_ext), # 0xd5
        FmtTraits(TYPE_EXT, 1+4, decode_ext), # 0xd6
        FmtTraits(TYPE_EXT, 1+8, decode_ext), # 0xd7
        FmtTraits(TYPE_EXT, 1+16, decode_ext), # 0xd8
        FmtTraits(TYPE_STR, len_str, decode_str, 1), # 0xd9
        FmtTraits(TYPE_STR, len_str, decode_str, 2), # 0xda
        FmtTraits(TYPE_STR, len_str, decode_str, 4), # 0xdb
        FmtTraits(TYPE_ARRAY, len_array, decode_array, 2), # 0xdc
        FmtTraits(TYPE_ARRAY, len_array, decode_array, 4), # 0xdd
        FmtTraits(TYPE_MAP, len_map, decode_map, 2), # 0xde
        FmtTraits(TYPE_MAP, len_map, decode_map, 4), # 0xdf
    ])
    for i in range(0x20): # 0xe0 - 0xff
        __lut.append(FmtTraits(TYPE_INT, 0, i-0x20))

    @classmethod
    def decode(cls, val):
        fmt_traits = cls.__lut[cls.decode_num(val, 0, cls.NUM_UINT, 1)]
        return cls(val, fmt_traits)

    def __init__(self, val, fmt_traits):
        self.__val = val
        self.__type = fmt_traits.type
        self.__len = fmt_traits.len
        self.__decode = fmt_traits.decode

    @property
    def val(self):
        return self.__val

    @property
    def type(self):
        return self.__type

    @property
    def value(self):
        if callable(self.__decode):
            self.__decode = self.__decode(self)
        return self.__decode

    def __len__(self):
        if callable(self.__len):
            self.__len = self.__len(self)
        return 1 + self.__len


class ExplicitPrinter(gdb.printing.PrettyPrinter):
    def __init__(self, printer):
        super(ExplicitPrinter, self).__init__(printer.name + ' (explicit)')
        self.activated = False
        self.printer = printer

    def __call__(self, val):
        return self.printer(val) if self.activated else None


class MsgPackExtUnexpectedTypeError(MsgPackExtError):
    def __init__(self, address, ext_type, offset, actual, *expected):
        super(MsgPackExtUnexpectedTypeError, self).__init__(address, ext_type)
        self.offset = offset
        self.actual = actual
        self.expected = expected


class MsgPackPrinter(gdb.printing.PrettyPrinter):
    """Print a MsgPack."""

    gdb_type = gdb.lookup_type('char').pointer()
    mp_gdb_type = gdb.lookup_type('uint8_t').pointer()

    class Printer(object):
        __mappers = []

        @classmethod
        def push_mapper(cls, mapper):
            cls.__mappers.append(mapper)

        @classmethod
        def pop_mapper(cls):
            return cls.__mappers.pop() if len(cls.__mappers) > 0 else None

        __ext_names = {}

        @classmethod
        def ext_name(cls, ext_type):
            return cls.__ext_names.get(ext_type, "UNKNOWN({})".format(ext_type))

        @classmethod
        def register_ext_printer(cls, ext_type, ext_name, printer):
            MsgPack.set_ext_default_decoder(cls.ExtPrinterDefault)
            MsgPack.register_ext_decoder(ext_type, printer)
            cls.__ext_names[ext_type] = ext_name

        def __init__(self, val):
            self.mp = MsgPack.decode(val)
            self.mapper = self.pop_mapper()

            # Setup pretty-printer methods according to MsgPack type

            self.display_hint = {
                MsgPack.TYPE_ARRAY: self.display_hint_array,
                MsgPack.TYPE_MAP: self.display_hint_map,
                MsgPack.TYPE_STR: self.display_hint_str,
                MsgPack.TYPE_EXT: self.display_hint_ext,
            }.get(self.mp.type, self.display_hint_default)

            self.to_string = {
                MsgPack.TYPE_INVALID: self.to_string_invalid,
                MsgPack.TYPE_NIL: self.to_string_nil,
                MsgPack.TYPE_BOOL: self.to_string_bool,
                MsgPack.TYPE_INT: self.to_string_num,
                MsgPack.TYPE_UINT: self.to_string_unsigned,
                MsgPack.TYPE_FLOAT: self.to_string_num,
                MsgPack.TYPE_STR: self.to_string_str,
                MsgPack.TYPE_BIN: self.to_string_bin,
                MsgPack.TYPE_ARRAY: self.to_string_array,
                MsgPack.TYPE_MAP: self.to_string_map,
                MsgPack.TYPE_EXT: self.to_string_ext,
            }[self.mp.type]

            self.children = {
                MsgPack.TYPE_ARRAY: self.children_array,
                MsgPack.TYPE_MAP: self.children_map,
                MsgPack.TYPE_EXT: self.children_ext,
            }.get(self.mp.type, None)
            if self.children is None:
                del self.children

            # NOTE: MP_STR might contain binary data that should be
            # displayed differently. It might be done by affecting
            # display_hint method within to_string_str, so we should:
            # 1. call to_string at the end of this printer c-tor
            # 2. replace current to_string method with the one that
            #    just returns the string calculated at #1
            if self.mp.type == MsgPack.TYPE_STR:
                self.to_string = lambda s=self.to_string(): s

        def display_hint_default(self): return None
        def display_hint_array(self): return 'array'
        def display_hint_map(self): return 'map'
        def display_hint_str(self): return 'string'
        def display_hint_ext(self): return self.mp_value().display_hint()
        display_hint_bin_as_str = display_hint_default

        def to_string_invalid(self): return self.mp_value()
        def to_string_nil(self): return 'null'
        def to_string_bool(self): return 'true' if self.mp_value() else 'false'
        def to_string_num(self): return str(self.mp_value())
        def to_string_unsigned(self): return str(self.mp_value()) + 'U'
        def to_string_str(self):
            # MP_STR may actually contain a binary string, in which case
            # we encode the value in base64, like we do for MP_BIN.
            try:
                s = unicode(self.mp_value(), 'utf-8')
            except UnicodeDecodeError:
                self.display_hint = self.display_hint_bin_as_str
                s = 'str:' + unicode(base64.b64encode(self.mp_value()), 'utf-8')
            return s
        def to_string_bin(self): return 'bin:' + unicode(base64.b64encode(self.mp_value()), 'utf-8')
        def to_string_array(self): return None
        def to_string_map(self): return None
        def to_string_ext(self): return self.mp_value().to_string()

        def children_array(self):
            mapper = self.mapper if self.mapper is not None else lambda item: item.val
            for i, item in enumerate(map(mapper, self.mp_value())):
                yield str(i), item

        def children_map(self):
            mapper = self.mapper if self.mapper is not None else lambda item: (item[0].val, item[1].val)
            for i, (k, v) in enumerate(map(mapper, self.mp_value())):
                yield str(2 * i), k
                yield str(2 * i + 1), v

        def children_ext(self):
            return self.mp_value().children()

        def mp_value(self):
            def mp_type_str(mp_type):
                return {
                    MsgPack.TYPE_INVALID: 'INVALID',
                    MsgPack.TYPE_NIL: 'NIL',
                    MsgPack.TYPE_BOOL: 'BOOL',
                    MsgPack.TYPE_INT: 'INT',
                    MsgPack.TYPE_UINT: 'UINT',
                    MsgPack.TYPE_FLOAT: 'FLOAT',
                    MsgPack.TYPE_STR: 'STR',
                    MsgPack.TYPE_BIN: 'BIN',
                    MsgPack.TYPE_ARRAY: 'ARRAY',
                    MsgPack.TYPE_MAP: 'MAP',
                    MsgPack.TYPE_EXT: 'EXT',
                }[mp_type]
            # MsgPack implements lazy decoding. It is done
            # when mp.value is accessed the first time, thus here
            # is the best place to catch all the problems
            try:
                return self.mp.value
            except MsgPackInvalidFormatError as e:
                raise gdb.GdbError("MsgPack: invalid format at {:#x}".format(e.address))
            except MsgPackExtLengthMismatchError as e:
                raise gdb.GdbError("MsgPack: Ext:{} at {:#x}: decoded length {} doesn't match the expected {}".format(
                    self.ext_name(e.ext_type),
                    e.address,
                    e.len_decoded,
                    e.len_expected,
                ))
            except MsgPackExtUnexpectedTypeError as e:
                raise gdb.GdbError("MsgPack: Ext:{} at {:#x}: got type {} at offset {} (expected - {})".format(
                    self.ext_name(e.ext_type),
                    e.address,
                    mp_type_str(e.actual),
                    e.offset,
                    ', '.join(map(mp_type_str, e.expected)),
                ))

        class ExtPrinter(object):
            """This class is to be used to support MsgPack extensions.
            In order to support the new extension you need to subclass it
            and implement the 'decode' method. This method is used to decode
            extension data bytes (that follows extension type).
            After decoding is done the number of the decoded bytes is checked
            against the expected length, which is specified either implicitly
            (over one of the FIXEXT formats) or explicitly (with the number
            that follows EXT format), so there are 2 requirements for subclass:
            1. All decoding must be done within the 'decode'
            2. __len__ method must return the decoded length

            It is highly recommended to use the following methods:
               decode_mp, decode_num, read, seek
            because they handle decoded length automatically, and you wouldn't
            need to override __len__ method

            Please, refer to already implemented extensions to see its usage"""

            def __init__(self, ext_type, ext_len, val, offset):
                self.__ext_type = ext_type
                self.__ext_len = ext_len
                self.__len = 0
                self.__val = val
                self.__offset = offset
                self.decode()

            def decode(self):
                raise NotImplementedError("{}.decode not implemented".format(self.__class__.__name__))

            def display_hint(self): return None
            def to_string(self): return None
            def children(self): return iter(())

            def seek(self, offset):
                self.__len += offset

            def read(self, size):
                data = MsgPack.read(self.val, self.offset, size)
                self.seek(size)
                return data

            def decode_num(self, num_type, num_len, big_endian=True):
                num = MsgPack.decode_num(self.val, self.offset, num_type, num_len, big_endian)
                self.seek(num_len)
                return num

            def assert_type(self, mp, *expected_types):
                if len(expected_types) > 0 and mp.type not in expected_types:
                    raise MsgPackExtUnexpectedTypeError(
                        int_from_address(self.val),
                        self.ext_type,
                        int_from_address(mp.val) - int_from_address(self.val),
                        mp.type,
                        *expected_types
                    )

            def decode_mp(self, *expected_types):
                mp = MsgPack.decode(self.val + self.offset)
                self.assert_type(mp, *expected_types)
                self.seek(len(mp))
                return mp

            def __len__(self):
                return self.__len

            @property
            def ext_type(self):
                return self.__ext_type

            @property
            def ext_len(self):
                return self.__ext_len

            @property
            def ext_len_undecoded(self):
                return self.__ext_len - len(self)

            @property
            def val(self):
                return self.__val

            @property
            def offset(self):
                return self.__offset + self.__len

        class ExtPrinterDefault(ExtPrinter):
            def decode(self):
                self.seek(self.ext_len)

            def to_string(self):
                return 'ext:{} of len {}'.format(self.ext_type, self.ext_len)

    ExtPrinter = Printer.ExtPrinter

    @classmethod
    def register_ext_printer(cls, ext_type, ext_name, printer):
        cls.Printer.register_ext_printer(ext_type, ext_name, printer)

    @classmethod
    def to_print(cls, val, mapper_func):
        cls.Printer.push_mapper(mapper_func)
        return val

    def __call__(self, val):
        if val.type.code not in [gdb.TYPE_CODE_PTR, gdb.TYPE_CODE_ARRAY] \
            or val.type.target() != self.gdb_type.target():
            return None
        if val.type.code == gdb.TYPE_CODE_ARRAY:
            val = val.cast(self.gdb_type)
        return self.Printer(val)

pp_msgpack = ExplicitPrinter(MsgPackPrinter('MsgPack'))
gdb.printing.register_pretty_printer(gdb.current_objfile(), pp_msgpack, True)


class MsgPackPrint(gdb.Command):
    """
Decode and print MsgPack referred by EXP in a human-readable form
Usage: tt-mp [[OPTION]... --] EXP

Options:
  Any print option (see 'help print').
    """
    def __init__(self):
        super(MsgPackPrint, self).__init__('tt-mp', gdb.COMMAND_DATA)

    def invoke(self, arg, from_tty):
        pp_msgpack.activated = True
        try:
            gdb.execute('print {}'.format(arg))
        except Exception as e:
            raise e
        finally:
            pp_msgpack.activated = False

MsgPackPrint()


################################################################
# MsgPack extension: MP_DECIMAL

MP_DECIMAL = find_value('MP_DECIMAL')
if MP_DECIMAL is not None:
    class MsgPackExtPrinterDecimal(MsgPackPrinter.ExtPrinter):
        def decode(self):
            scale = self.decode_mp(MsgPack.TYPE_UINT, MsgPack.TYPE_INT)
            bcd = self.read(self.ext_len_undecoded)

            self.dn = DecNumber.from_bcd(bcd, scale.value)

        def to_string(self):
            return 'dec:' + str(self.dn)

    MsgPackPrinter.register_ext_printer(int(MP_DECIMAL), 'DECIMAL', MsgPackExtPrinterDecimal)

    class DecNumber:
        # DECIMAL_MAX_DIGITS of decimal.h, 38 and 76 since 3.5: a macro,
        # but DECIMAL_MAX_STR_LEN = DECIMAL_MAX_DIGITS + 14 is in the DWARF.
        DECIMAL_MAX_DIGITS = 38
        if find_value('DECIMAL_MAX_STR_LEN') is not None:
            DECIMAL_MAX_DIGITS = int(find_value('DECIMAL_MAX_STR_LEN')) - 14
        DECNUMDIGITS = DECIMAL_MAX_DIGITS

        DECDPUN = 4
        DECNUMUNITS = (DECNUMDIGITS + DECDPUN - 1) // DECDPUN

        DECPOWERS = [ 10**i for i in range(0, DECDPUN) ]

        DECNEG = 0x80
        DECINF = 0x40
        DECNAN = 0x20
        DECSNAN = 0x10
        DECSPECIAL = DECINF | DECNAN | DECSNAN

        DECPMINUS = 0x0D
        DECPMINUSALT = 0x0B
        DECNUMMAXE = 999999999

        @classmethod
        def d2u(cls, d): # D2U
            return (d + cls.DECDPUN - 1) // cls.DECDPUN

        @classmethod
        def msu_digits(cls, d): # MSUDIGITS
            return d - (cls.d2u(d) - 1) * cls.DECDPUN

        @classmethod
        def to_digit(cls, u, cut): # TODIGIT
            if cut + 1 < cls.DECDPUN: # drop higher digits, if any
                u %= cls.DECPOWERS[cut + 1]
            c = 0
            pow = cls.DECPOWERS[cut] * 2
            if u > pow:
                pow *= 4
                if u >= pow: u -= pow; c += 8
                pow = pow // 2
                if u >= pow: u -= pow; c += 4
                pow = pow // 2
            if u >= pow: u -= pow; c += 2
            pow = pow // 2
            if u >= pow: u -= pow; c += 1
            return chr(ord('0') + c)

        def __init__(self, val):
            self.val = val

        @classmethod
        def from_bcd(cls, bcd, scale): # decPackedToNumber
            if scale > cls.DECIMAL_MAX_DIGITS or scale <= -cls.DECIMAL_MAX_DIGITS:
                raise RuntimeError("DecNumber.from_bcd: unexpected scale ({})".format(scale))

            last = len(bcd) - 1
            first = 0
            up = 0
            cut = 0

            dn = cls(dict( # decNumberZero
                bits = 0,
                exponent = 0,
                digits = 1,
                lsu = [ 0 for _ in range(0, cls.DECNUMUNITS) ],
            ))

            nib = bcd[last] & 0x0f
            if nib == cls.DECPMINUS or nib == cls.DECPMINUSALT:
                dn.val['bits'] = cls.DECNEG
            elif nib <= 9:
                raise RuntimeError("DecNumber.from_bcd: not a sign nibble")

            while bcd[first] == 0:
                first += 1

            digits = (last - first) * 2 + 1
            if (bcd[first] & 0xf0) == 0:
                digits -= 1
            if digits != 0:
                dn.val['digits'] = digits

            dn.val['exponent'] = -scale
            if scale >= 0:
                if (dn.val['digits'] - scale - 1) < -cls.DECNUMMAXE:
                    raise RuntimeError("DecNumber.from_bcd: underflow")

            else:
                if scale < -cls.DECNUMMAXE or (dn.val['digits'] - scale - 1) > cls.DECNUMMAXE:
                    raise RuntimeError("DecNumber.from_bcd: overflow")

            if digits == 0: return dn

            while True:
                nib = (bcd[last] & 0xf0) >> 4
                if nib > 9:
                    raise RuntimeError("DecNumber.from_bcd: unexpected digit in high nibble: \
                                       bcd[{}]={:02x}".format(last, bcd[last]))

                if cut == 0: dn.val['lsu'][up] = nib
                else: dn.val['lsu'][up] += nib * cls.DECPOWERS[cut]
                digits -= 1
                if digits == 0: break
                cut += 1
                if cut == cls.DECDPUN:
                    up += 1
                    cut = 0

                last -= 1
                nib = bcd[last] & 0x0f
                if nib > 9:
                    raise RuntimeError("DecNumber.from_bcd: unexpected digit in low nibble:\
                                       bcd[{}]={:02x}".format(last, bcd[last]))

                if cut == 0: dn.val['lsu'][up] = nib
                else: dn.val['lsu'][up] += nib * cls.DECPOWERS[cut]
                digits -= 1
                if digits == 0: break
                cut += 1
                if cut == cls.DECDPUN:
                    up += 1
                    cut = 0

            return dn

        def is_negative(self): # decNumberIsNegative
            return (self.val['bits'] & self.DECNEG) != 0

        def is_infinite(self): # decNumberIsInfinite
            return (self.val['bits'] & self.DECINF) != 0

        def is_snan(self): # decNumberIsSNaN
            return (self.val['bits'] & self.DECSNAN) != 0

        def is_special(self): # decNumberIsSpecial
            return (self.val['bits'] & self.DECSPECIAL) != 0

        def __str__(self): # decToString
            self_exp = self.val['exponent']
            self_digits = self.val['digits']
            self_lsu = self.val['lsu']

            num_units = self.d2u(self_digits)
            up = num_units - 1

            s = ''
            if self.is_negative():
                s += '-'

            if self.is_special():
                if self.is_infinite():
                    s += "Infinity"
                    return s

                if self.is_snan():
                    s += 's'
                s += "NaN"
                if self_exp != 0 or (self_lsu[0] == 0 and self_digits == 1):
                    return s

            cut = self.msu_digits(self_digits)
            cut -= 1

            if self_exp == 0:
                for i in range(num_units):
                    u = self_lsu[num_units - 1 - i]
                    for j in range(cut, -1, -1):
                        s += self.to_digit(u, j)
                    cut = self.DECDPUN - 1
                return s

            pre = self_digits + self_exp

            u = self_lsu[up]
            if pre > 0:
                n = pre
                for i in range(pre, 0, -1):
                    if cut < 0:
                        if up == 0: break
                        up -= 1
                        cut = self.DECDPUN - 1
                        u = self_lsu[up]
                    s += self.to_digit(u, cut)
                    cut -= 1

                if n < self_digits:
                    s += '.'
                    while True:
                        if cut < 0:
                            if up == 0: break
                            up -= 1
                            cut = self.DECDPUN - 1
                            u = self_lsu[up]
                        s += self.to_digit(u, cut)
                        cut -= 1
                else:
                    for _ in range(self_exp):
                        s += '0'

            else:
                s += '0.'
                for i in range(pre, 0):
                    s += '0'
                while True:
                    if cut < 0:
                        if up == 0: break
                        up -= 1
                        cut = self.DECDPUN - 1
                        u = self_lsu[up]
                    s += self.to_digit(u, cut)
                    cut -= 1

            return s

    class DecNumberInMemory(DecNumber):
        """A decNumber of the inferior.  Its units hold DECDPUN digits as
        tarantool builds decNumber, 3 (the default of decNumber.h), not
        the 4 of the numbers from_bcd() makes: it is the count of digits
        that gives the count of units of decNumber.lsu, else 3."""
        DECDPUN = 3
        __type = find_type('decNumber')
        if __type is not None:
            __units = __type['lsu'].type.range()[1] + 1
            for __d in range(1, 10):
                if (DecNumber.DECIMAL_MAX_DIGITS + __d - 1) // __d == __units:
                    DECDPUN = __d
                    break
        DECPOWERS = [10**i for i in range(0, DECDPUN)]

    class DecNumberPrinter:
        def __init__(self, val):
            self.val = DecNumberInMemory(struct_to_dict(
                val, ('digits', 'exponent', 'bits', 'lsu')))

        def to_string(self):
            return str(self.val)

    # decNumber is a typedef of an anonymous struct: in a C++ unit the
    # struct takes the name, in a C one it has none and only the typedef
    # (decNumber or decimal_t) is there to match; which one gdb finds
    # depends on the symtabs it expanded first.
    pp.add_printer('DecNumber', '^(decNumber|decimal_t)$', DecNumberPrinter)


################################################################
# MsgPack extension: MP_UUID

MP_UUID = find_value('MP_UUID')
if MP_UUID is not None:
    class MsgPackExtPrinterUuid(MsgPackPrinter.ExtPrinter):
        def decode(self): # uuid_unpack
            self.uuid = Uuid(dict(
                time_low = self.decode_num(MsgPack.NUM_UINT, 4),
                time_mid = self.decode_num(MsgPack.NUM_UINT, 2),
                time_hi_and_version = self.decode_num(MsgPack.NUM_UINT, 2),
                clock_seq_hi_and_reserved = self.decode_num(MsgPack.NUM_UINT, 1),
                clock_seq_low = self.decode_num(MsgPack.NUM_UINT, 1),
                node = [ self.decode_num(MsgPack.NUM_UINT, 1) for _ in range(6) ],
            ))

        def to_string(self):
            return str(self.uuid)

    MsgPackPrinter.register_ext_printer(int(MP_UUID), 'UUID', MsgPackExtPrinterUuid)

    class Uuid:
        def __init__(self, val):
            self.val = val

        # No validation: since 3.2 any 128 bits are a UUID (b0b32bff02,
        # "uuid: relax UUID value validation"), the variant included.
        def __str__(self): # tt_uuid_to_string
            return '{:08x}-{:04x}-{:04x}-{:02x}{:02x}-{:02x}{:02x}{:02x}{:02x}{:02x}{:02x}'.format(
                self.val['time_low'], self.val['time_mid'], self.val['time_hi_and_version'],
                self.val['clock_seq_hi_and_reserved'], self.val['clock_seq_low'], self.val['node'][0],
                self.val['node'][1], self.val['node'][2], self.val['node'][3], self.val['node'][4], self.val['node'][5])

    class UuidPrinter:
        def __init__(self, val):
            self.val = Uuid(struct_to_dict(val, (
                'time_low', 'time_mid', 'time_hi_and_version',
                'clock_seq_hi_and_reserved', 'clock_seq_low', 'node')))

        def to_string(self):
            return str(self.val)

    pp.add_printer('Uuid', '^tt_uuid$', UuidPrinter)


################################################################
# MsgPack extension: MP_DATETIME

MP_DATETIME = find_value('MP_DATETIME')
if MP_DATETIME is not None and \
        dwarf_has('datetime', values=('zones_unsorted',)):
    class MsgPackExtPrinterDatetime(MsgPackPrinter.ExtPrinter):
        def decode(self): # datetime_unpack
            epoch = self.decode_num(MsgPack.NUM_INT, 8, False)
            if self.ext_len_undecoded == 0:
                self.date = Datetime(dict(
                    epoch = epoch,
                    nsec = 0,
                    tzoffset = 0,
                    tzindex = 0,
                ))
            else:
                self.date = Datetime(dict(
                    epoch = epoch,
                    nsec = self.decode_num(MsgPack.NUM_INT, 4, False),
                    tzoffset = self.decode_num(MsgPack.NUM_INT, 2, False),
                    tzindex = self.decode_num(MsgPack.NUM_INT, 2, False),
                ))

        def to_string(self):
            return str(self.date)

    MsgPackPrinter.register_ext_printer(int(MP_DATETIME), 'DATETIME', MsgPackExtPrinterDatetime)

    class Datetime:
        ######################################################
        # this section corresponds to c-dt library

        DT_EPOCH_OFFSET = 0
        DT1901 = 693961 + DT_EPOCH_OFFSET
        DT2099 = 766644 + DT_EPOCH_OFFSET
        days_preceding_month = [
            [ 0, 0, 31, 59, 90, 120, 151, 181, 212, 243, 273, 304, 334 ],
            [ 0, 0, 31, 60, 91, 121, 152, 182, 213, 244, 274, 305, 335 ], # leap year
        ]

        @staticmethod
        def leap_year(y):
            return (y & 3) == 0 and (y % 100 != 0 or y % 400 == 0)

        @classmethod
        def dt_from_rdn(cls, rdn): # dt_from_rdn
            return rdn + cls.DT_EPOCH_OFFSET

        @classmethod
        def dt_to_yd(cls, d): # dt_to_yd
            y = 0

            if d >= cls.DT1901 and d <= cls.DT2099:
                d -= cls.DT1901 - 1
                y += (4 * d - 1) // 1461
                d -= (1461 * y) // 4
                y += 1901

            else:
                d -= cls.DT_EPOCH_OFFSET
                if d < 1:
                    n400 = 1 - d // 146097
                    y -= n400 * 400
                    d += n400 * 146097
                d -= 1
                y += 400 * (d // 146097)
                d %= 146097

                n100 = d // 36524
                y += 100 * n100
                d %= 36524

                y += 4 * (d // 1461)
                d %= 1461

                n1 = d // 365
                y += n1
                d %= 365

                if n100 == 4 or n1 == 4:
                    d = 366
                else:
                    y += 1
                    d += 1

            return y, d

        @classmethod
        def dt_to_ymd(cls, dt): # dt_to_ymd
            y, doy = cls.dt_to_yd(dt)
            l = cls.leap_year(y)
            m = 1 if doy < 32 else 1 + (5 * (doy - 59 - l) + 303) // 153

            if m < 1 or m > 12:
                raise gdb.GdbError("Datetime.st_to_ymd: unexpected month ({})".format(m))

            return y, m, doy - cls.days_preceding_month[l][m]

        ######################################################
        # this section corresponds to tzcode library

        zones_unsorted_size = gdb.parse_and_eval('sizeof(zones_unsorted) / sizeof(zones_unsorted[0])')

        @classmethod
        def timezone_name(cls, index): # timezone_name
            if index >= cls.zones_unsorted_size:
                raise gdb.GdbError("Datetime.timezone_name: index {} is out of bound".format(index))
            # Looked up on use, not on load: a value keeps the address of
            # the variable at its lookup, and that of a PIE moves when the
            # program starts (or another core is loaded).
            return gdb.parse_and_eval('zones_unsorted')[index]['name'].string()

        ######################################################
        # this section corresponds to tarantool core library

        SECS_PER_DAY = 86400
        DT_EPOCH_1970_OFFSET = 719163
        MAX_NANOS_PER_SEC = 2000000000

        MAX_DT_DAY_VALUE = INT_MAX
        MIN_DT_DAY_VALUE = INT_MIN
        SECS_EPOCH_1970_OFFSET = DT_EPOCH_1970_OFFSET * SECS_PER_DAY
        MAX_EPOCH_SECS_VALUE = MAX_DT_DAY_VALUE * SECS_PER_DAY - SECS_EPOCH_1970_OFFSET
        MIN_EPOCH_SECS_VALUE = MIN_DT_DAY_VALUE * SECS_PER_DAY - SECS_EPOCH_1970_OFFSET

        TZOFFSET_MAX = 14 * 60
        TZOFFSET_MIN = -12 * 60
        MAX_TZINDEX = 1024

        def __init__(self, val):
            self.val = val

        def is_valid(self): # datetime_validate
            if self.val['epoch'] < self.MIN_EPOCH_SECS_VALUE or self.val['epoch'] > self.MAX_EPOCH_SECS_VALUE:
                return False
            if self.val['nsec'] < 0 or self.val['nsec'] >= self.MAX_NANOS_PER_SEC:
                return False
            if self.val['tzoffset'] < self.TZOFFSET_MIN or self.val['tzoffset'] > self.TZOFFSET_MAX:
                return False
            if self.val['tzindex'] < 0 or self.val['tzindex'] > self.MAX_TZINDEX:
                return False
            return True

        def __str__(self): # datetime_snprint
            offset = self.val['tzoffset']
            tzindex = self.val['tzindex']
            rd_seconds = self.val['epoch'] + offset * 60 + self.SECS_EPOCH_1970_OFFSET
            rd_number = rd_seconds // self.SECS_PER_DAY
            if rd_number < INT_MIN or rd_number > INT_MAX:
                raise gdb.GdbError('{} is out of bound'.format(rd_number))
            dt = self.dt_from_rdn(rd_number)

            year, month, day = self.dt_to_ymd(dt)

            rd_seconds = rd_seconds % self.SECS_PER_DAY
            hour = (rd_seconds // 3600) % 24
            minute = (rd_seconds // 60) % 60
            second = rd_seconds % 60
            nanosec = self.val['nsec']

            s = str()
            s += '{:04d}-{:02d}-{:02d}T{:02d}:{:02d}:{:02d}'.format(
                year, month, day, hour, minute, second)
            if nanosec != 0:
                if nanosec % 1000000 == 0:
                    s += '.{:03d}'.format(nanosec // 1000000)
                elif nanosec % 1000 == 0:
                    s += '.{:06d}'.format(nanosec // 1000)
                else:
                    s += '.{:09d}'.format(nanosec)

            if tzindex != 0:
                tz_name = self.timezone_name(tzindex)
                if tz_name is None:
                    raise gdb.GdbError("Datetime::__str__: timezone name is missing")
                s += ('{}' if len(tz_name) <= 1 else ' {}').format(tz_name)

            elif offset == 0:
                s += 'Z'

            else:
                if offset < 0:
                    sign = '-'
                    offset = -offset
                else:
                    sign = '+'
                s += '{}{:02d}{:02d}'.format(sign, offset // 60, offset % 60)

            if not self.is_valid():
                s += '[invalid]'
            return s

    class DatetimePrinter:
        def __init__(self, val):
            self.val = Datetime(struct_to_dict(
                val, ('epoch', 'nsec', 'tzoffset', 'tzindex')))

        def to_string(self):
            return str(self.val)

    pp.add_printer('Datetime', '^datetime$', DatetimePrinter)


################################################################
# MsgPack extension: MP_ERROR

MP_ERROR = find_value('MP_ERROR')
if MP_ERROR is not None and dwarf_has('MsgPack error', values=(
        'MP_ERROR_MAX', 'mp_error_field_to_json_key', 'MP_ERROR_STACK')):
    class MsgPackExtPrinterError(MsgPackPrinter.ExtPrinter):
        MP_ERROR_MAX = int(gdb.parse_and_eval('MP_ERROR_MAX'))

        MP_ERROR_STACK = int(gdb.parse_and_eval('MP_ERROR_STACK'))

        def decode(self):
            self.mp = self.decode_mp(MsgPack.TYPE_MAP)

        def to_string(self):
            # Looked up on use, not on load (see Datetime.timezone_name).
            json_keys = gdb.parse_and_eval('mp_error_field_to_json_key')

            def map_single_error(item):
                k, v = item
                self.assert_type(k, MsgPack.TYPE_UINT)
                if k.value < self.MP_ERROR_MAX:
                    key = json_keys[k.value].string()
                    key = key.strip().split(':', 1)[0]
                else:
                    key = k.val
                return key, v.val

            def map_error_stack(item):
                self.assert_type(item, MsgPack.TYPE_MAP)
                return MsgPackPrinter.to_print(item.val, map_single_error)

            def map_error(item):
                k, v = item
                self.assert_type(k, MsgPack.TYPE_UINT)
                if k.value == MsgPackExtPrinterError.MP_ERROR_STACK:
                    self.assert_type(v, MsgPack.TYPE_ARRAY)
                    return '"stack"', MsgPackPrinter.to_print(v.val, map_error_stack)
                return k.val, v.val

            return MsgPackPrinter.to_print(self.mp.val, map_error)

    MsgPackPrinter.register_ext_printer(int(MP_ERROR), 'ERROR', MsgPackExtPrinterError)


################################################################
# MsgPack extension: MP_COMPRESSION

MP_COMPRESSION = find_value('MP_COMPRESSION')
if MP_COMPRESSION is not None and dwarf_has('MsgPack compression', values=(
        'COMPRESSION_TYPE_NONE', 'compression_type_MAX',
        'compression_type_strs')):
    class MsgPackExtPrinterCompression(MsgPackPrinter.ExtPrinter):
        def decode(self):
            cmpr_type = self.decode_mp(MsgPack.TYPE_UINT)
            raw_size = self.decode_mp(MsgPack.TYPE_UINT)

            self.cmpr = Compression(dict(
                type = cmpr_type.value,
                raw_size = raw_size.value,
                size = self.ext_len_undecoded,
            ))

            self.seek(self.ext_len_undecoded)

        def to_string(self):
            return str(self.cmpr)

    MsgPackPrinter.register_ext_printer(int(MP_COMPRESSION), 'COMPRESSION', MsgPackExtPrinterCompression)

    class Compression:
        COMPRESSION_TYPE_NONE = gdb.parse_and_eval('COMPRESSION_TYPE_NONE')
        COMPRESSION_TYPE_MAX = gdb.parse_and_eval('compression_type_MAX')

        def __init__(self, val):
            self.val = val

        def __str__(self):
            type = self.val['type']
            if type >= self.COMPRESSION_TYPE_NONE and type < self.COMPRESSION_TYPE_MAX:
                # Looked up on use, not on load (see Datetime.timezone_name).
                type = gdb.parse_and_eval('compression_type_strs')[type].string()
            return 'compression({}):[{}]->[{}]'.format(type, self.val['raw_size'], self.val['size'])


################################################################
# MsgPack extension: MP_INTERVAL

MP_INTERVAL = find_value('MP_INTERVAL')
if MP_INTERVAL is not None and dwarf_has('MsgPack interval', values=(
        'FIELD_YEAR', 'FIELD_MONTH', 'FIELD_WEEK', 'FIELD_DAY', 'FIELD_HOUR',
        'FIELD_MINUTE', 'FIELD_SECOND', 'FIELD_NANOSECOND', 'FIELD_ADJUST',
        'DT_EXCESS', 'DT_LIMIT', 'DT_SNAP')):
    class MsgPackExtPrinterInterval(MsgPackPrinter.ExtPrinter):
        FIELD_YEAR = int(gdb.parse_and_eval('FIELD_YEAR'))
        FIELD_MONTH = int(gdb.parse_and_eval('FIELD_MONTH'))
        FIELD_WEEK = int(gdb.parse_and_eval('FIELD_WEEK'))
        FIELD_DAY = int(gdb.parse_and_eval('FIELD_DAY'))
        FIELD_HOUR = int(gdb.parse_and_eval('FIELD_HOUR'))
        FIELD_MINUTE = int(gdb.parse_and_eval('FIELD_MINUTE'))
        FIELD_SECOND = int(gdb.parse_and_eval('FIELD_SECOND'))
        FIELD_NANOSECOND = int(gdb.parse_and_eval('FIELD_NANOSECOND'))
        FIELD_ADJUST = int(gdb.parse_and_eval('FIELD_ADJUST'))

        DT_EXCESS = int(gdb.parse_and_eval('DT_EXCESS'))
        DT_LIMIT = int(gdb.parse_and_eval('DT_LIMIT'))
        DT_SNAP = int(gdb.parse_and_eval('DT_SNAP'))

        def decode(self):
            field_units = {
                self.FIELD_YEAR: 'years',
                self.FIELD_MONTH: 'months',
                self.FIELD_WEEK: 'weeks',
                self.FIELD_DAY: 'days',
                self.FIELD_HOUR: 'hours',
                self.FIELD_MINUTE: 'minutes',
                self.FIELD_SECOND: 'seconds',
                self.FIELD_NANOSECOND: 'nanoseconds',
            }

            val = {}
            num_fields = self.decode_num(MsgPack.NUM_UINT, 1)
            for _ in range(num_fields):
                field_id = self.decode_num(MsgPack.NUM_UINT, 1)
                field_value = self.decode_mp(MsgPack.TYPE_UINT, MsgPack.TYPE_INT)
                val[field_id] = field_value.value

            s = []
            field_fmt = '{:+} {}'
            for field in (
                self.FIELD_YEAR,
                self.FIELD_MONTH,
                self.FIELD_WEEK,
                self.FIELD_DAY,
                self.FIELD_HOUR,
                self.FIELD_MINUTE,
                self.FIELD_SECOND,
                self.FIELD_NANOSECOND,
            ):
                if field in val and val[field] != 0:
                    s.append(field_fmt.format(val[field], field_units[field]))
                    field_fmt = '{} {}'
            if len(s) == 0:
                s.append(field_fmt.format(0, field_units[self.FIELD_SECOND]))
            if self.FIELD_ADJUST in val:
                adjust = {
                    self.DT_EXCESS: 'excess',
                    self.DT_LIMIT: 'none',
                    self.DT_SNAP: 'last',
                }.get(val[self.FIELD_ADJUST], 'UNKNOWN')
                s.append('({})'.format(adjust))
            self.str = ' '.join(s)

        def to_string(self):
            return self.str

    MsgPackPrinter.register_ext_printer(int(MP_INTERVAL), 'INTERVAL', MsgPackExtPrinterInterval)


################################################################
# MsgPack extension: MP_TUPLE

MP_TUPLE = find_value('MP_TUPLE')
if MP_TUPLE is not None:
    class MsgPackExtPrinterTuple(MsgPackPrinter.ExtPrinter):
        def decode(self):
            self.format_id = self.decode_mp(MsgPack.TYPE_UINT)
            self.payload = self.decode_mp(MsgPack.TYPE_ARRAY)

        def to_string(self):
            return None

        def children(self):
            yield str(0), "format_id"
            yield str(0), self.format_id.val
            yield str(1), "payload"
            yield str(1), self.payload.val

        def display_hint(self):
            return 'map'

    MsgPackPrinter.register_ext_printer(int(MP_TUPLE), 'TUPLE', MsgPackExtPrinterTuple)


class JsonTokenPrinter:
    """Print a json_token object."""

    # Registered below only if the debug info has them all.
    JSON_TOKEN_NUM = find_value('JSON_TOKEN_NUM')
    JSON_TOKEN_STR = find_value('JSON_TOKEN_STR')
    JSON_TOKEN_ANY = find_value('JSON_TOKEN_ANY')
    JSON_TOKEN_END = find_value('JSON_TOKEN_END')

    tuple_field_type = find_type('tuple_field')

    def __init__(self, val):
        self.val = val

    def to_string(self):
        type = self.val['type']
        hash = self.val['hash']

        if type == self.JSON_TOKEN_NUM:
            s_id = ' id=' + str(self.val['num'])
        elif type == self.JSON_TOKEN_STR:
            s_id = ' id=' + self.val['str'].string('utf-8', 'strict', int(self.val['len']))
        else:
            s_id = str()
        s = 'type={}{} hash={} parent={} sibling_idx={}'.format(
            int(type),
            s_id,
            int(hash),
            str(self.val['parent']),
            int(self.val['sibling_idx']),
        )

        if self.val['parent'] != 0:
            field = container_of(self.val.address, self.tuple_field_type, 'token')
            s += ' field_id={} field_type={} offset_slot={} is_key/multikey_part={}/{}'.format(
                str(field['id']),
                str(field['type']),
                str(field['offset_slot']),
                str(field['is_key_part']),
                str(field['is_multikey_part']),
            )

        return s

    def children(self):
        for i in range(0, int(self.val['max_child_idx']) + 1):
            yield str(i), self.val['children'][i].dereference()

    def display_hint(self):
        return 'array'

if dwarf_has('json_token', values=(
        'JSON_TOKEN_NUM', 'JSON_TOKEN_STR', 'JSON_TOKEN_ANY', 'JSON_TOKEN_END'),
        types=('tuple_field',)):
    pp.add_printer('JsonToken', '^json_token$', JsonTokenPrinter)


class TuplePrinter(object):
    """Print a tuple object."""

    # Registered below only if the debug info has struct tuple and
    # tuple_formats.
    tuple_type = find_type('struct tuple')
    support_compact = tuple_type is not None and \
        gdb.types.has_field(tuple_type, 'data_offset_bsize_raw')
    # tuple_flag (TUPLE_IS_DIRTY...): the flags are shown in hex if not.
    tuple_flag_type = find_type('tuple_flag')

    @staticmethod
    def tuple_formats():
        """tuple_formats, read when a tuple is printed: since 3.7 it is a
        thread-local pointer, which gdb cannot read before there is a
        process, and which is NULL in a thread with no tuples of its own
        (WAL, iproto, coio): then it is the one of the main thread."""
        formats = ThreadLocal('tuple_formats').value()
        if formats.type.strip_typedefs().code == gdb.TYPE_CODE_PTR and \
                int(formats) == 0:
            main = main_thread()
            if main is not None and main != gdb.selected_thread():
                with SelectedThread():
                    main.switch()
                    formats = ThreadLocal('tuple_formats').value()
                    formats.fetch_lazy()
        return formats

    ptr_char = gdb.lookup_type('char').pointer()
    ptr_int32 = gdb.lookup_type('int32_t').pointer()
    ptr_uint32 = gdb.lookup_type('uint32_t').pointer()
    slot_extent_t = find_type('struct field_map_builder_slot_extent')

    def __init__(self, val):
        if not equal_types(val.type, self.tuple_type):
            raise gdb.GdbError("expression doesn't evaluate to tuple")
        self.val = val

    def is_compact(self): # tuple_is_compact
        return self.support_compact and self.val['data_offset_bsize_raw'] & 0x8000

    def format(self): # tuple_format
        return self.tuple_formats()[self.val['format_id']].dereference()

    def data_offset(self): # tuple_data_offset
        # prior to introducing of compact mode data offset was just stored
        # in the corresponding field
        if not self.support_compact:
            return self.val['data_offset']
        # after introducing support of compact mode offset is stored
        # in a different way
        res = self.val['data_offset_bsize_raw']
        is_compact_bit = res >> 15
        res = (res & 0x7fff) >> (is_compact_bit * 8)
        return res

    def data(self): # tuple_data
        return self.val.address.cast(self.ptr_char) + self.data_offset()

    def field_map(self):
        slots = self.data().cast(self.ptr_int32)
        num_slots = int(self.format()['field_map_size']) // slots.type.target().sizeof
        fields = []
        # key_by_offset = lambda offs: '{}(+{})'.format(str(TtMsgPack(self.data() + offs)), offs)
        key_by_offset = lambda offs: '(+{})'.format(offs)
        for i in range(1, num_slots + 1):
            field_offs = slots[-i]
            key = str()
            if field_offs > 0:
                key = key_by_offset(field_offs)
            elif field_offs < 0:
                assert self.slot_extent_t is not None
                ext = (self.data() + field_offs).cast(self.slot_extent_t.pointer()).dereference()
                num_ext_keys = ext['size']
                offsets = ext['offset']
                key = '[{}-{}]:'.format(slots - ext.address.cast(slots.type), int(num_ext_keys))
                for ikey in range(0, num_ext_keys):
                    key += key_by_offset(offsets[ikey])
            else:
                key = 'missed'
            fields.append('[{}]:{}'.format(i, key))

        return ', '.join(fields)

    def children(self):
        for field in self.val.type.fields():
            child_name = field.name
            if field.name is None:
                child_name = str()
                child_val = (self.val.address.cast(self.ptr_char) + field.bitpos // 8).cast(field.type.pointer()).dereference()
            elif field.name == 'flags':
                flags_val = self.val['flags']
                child_val = '0x{:02x}'.format(int(flags_val))
                bits_set = []
                for bit in range(flags_val.type.sizeof * 8):
                    if int(flags_val) & (1 << bit) != 0 and \
                            self.tuple_flag_type is not None:
                        bits_set.append(enumval_name(self.tuple_flag_type,
                                                     bit, '*unknown*'))
                if len(bits_set) != 0:
                    child_val += ' ({})'.format(', '.join(bits_set))
            else:
                child_val = self.val[field.name]
            yield child_name, child_val
        yield 'format', self.format()
        yield 'field_map', self.field_map()
        if self.support_compact:
            yield 'is_compact', self.is_compact()
        yield 'data_offset', self.data_offset()
        pp_msgpack.activated = True
        yield 'data', self.data()
        pp_msgpack.activated = False

if dwarf_has('tuple', types=('struct tuple',), symbols=('tuple_formats',)):
    pp.add_printer('Tuple', '^tuple$', TuplePrinter)


class ContainerFieldInfo(object):
    @classmethod
    def __find_field(cls, container_type, field_name):
        assert field_name, "unexpected empty field name"
        if gdb.types.has_field(container_type, field_name):
            field = container_type[field_name]
            return field, field.bitpos // 8
        for f in container_type.fields():
            if not f.name:
                field, offset = cls.__find_field(f.type, field_name)
                if field:
                    return field, f.bitpos // 8 + offset
        return None

    def __init__(self, field_info):
        self.__str = field_info
        container_type, _, fields = field_info.partition('::')
        self.__container_type = gdb.lookup_type('struct ' + container_type)
        self.__offset = 0
        container_type = self.__container_type
        for field_name in fields.split('::'):
            field, offset = self.__find_field(container_type, field_name)
            assert field, "field {} is missing unexpectedly".format(field_name)
            self.__offset += offset
            container_type = field.type

    def __eq__(self, other):
        return self.__str == other.__str

    def __hash__(self):
        return hash(str(self))

    def __str__(self):
        return self.__str

    @property
    def offset(self):
        return self.__offset

    @property
    def container_type(self):
        return self.__container_type

    def container_from_field(self, field_ptr):
        return cast_ptr(self.__container_type, field_ptr, -self.__offset)


class ListLut(object):
    @staticmethod
    def __build_symbols_map(symbols):
        ret = {}
        for sym, entry in symbols:
            if not symbol_exists(sym):
                continue        # Not in this build.
            try:
                ret[sym] = ContainerFieldInfo(entry)
            except Exception as e:
                logger.debug(str(e))
        return ret

    @staticmethod
    def __build_containers_map(containers):
        ret = {}
        for container, entry in containers:
            try:
                field_info = ContainerFieldInfo(container)
                container_entries = ret.setdefault(field_info.container_type.tag, {})
                container_entries[field_info.offset] = ContainerFieldInfo(entry)
            except Exception as e:
                logger.debug(str(e))
        return ret

    @classmethod
    def _init(cls):
        if not hasattr(cls, '_symbols_map'):
            cls._symbols_map = cls.__build_symbols_map(cls._symbols)
        if not hasattr(cls, '_containers_map'):
            cls._containers_map = cls.__build_containers_map(cls._containers)

    __symbol_re = re.compile(r'(\w+)(?:\s*\+\s*(\d+))?')

    @classmethod
    def lookup_entry_info(cls, address):
        """Try to identify the type of the list entries by the list head."""
        cls._init()

        address = int_from_address(address)
        symbol_info = gdb.execute('info symbol {:#x}'.format(address), False, True)
        if symbol_info.startswith('No symbol matches'):
            return None

        symbol_match = cls.__symbol_re.match(symbol_info)
        if symbol_match is None:
            logger.warning("Symbol is missing in '{}'".format(symbol_info))
            return None

        symbol = symbol_match.group(1)
        offset = int(symbol_match.group(2)) if symbol_match.lastindex == 2 else 0

        symbol_val = gdb.parse_and_eval(symbol)
        entry_info = None
        if equal_types(symbol_val.type, cls._list_type):
            entry_info = cls._symbols_map.get(symbol)
        elif symbol_val.type.code == gdb.TYPE_CODE_STRUCT:
            container = cls._containers_map.get(symbol_val.type.tag)
            entry_info = container.get(offset) if container is not None else None

        return entry_info

    @classmethod
    def lookup_entry_info_by_container(cls, container_info):
        cls._init()
        container = cls._containers_map.get(container_info.container_type.tag)
        return container.get(container_info.offset) if container is not None else None


class Rlist(object):
    gdb_type = gdb.lookup_type('rlist')
    item_gdb_type = gdb_type

    @classmethod
    def lookup_head(cls, item):
        # Try to identify the list head by the list item
        assert item.type.code == gdb.TYPE_CODE_PTR,\
            'lookup_head: unexpected item type (code={})'.format(item.type.code)
        assert equal_types(item.type.target(), cls.gdb_type),\
            'lookup_head: unexpected item type ({})'.format(dump_type(item.type.target()))
        entry_info = RlistLut.lookup_entry_info(item)
        if entry_info is not None:
            return item
        items = cls(item)
        for item in reversed(items):
            entry_info = RlistLut.lookup_entry_info(item)
            if entry_info is not None:
                return item
        return None

    @classmethod
    def len(cls, rlist):
        return len(cls(rlist))

    def __init__(self, val, is_item=False):
        self.__val = val
        self.__is_item = is_item
        self.__len = None

    @property
    def address(self):
        return None if self.__is_item else self.__val

    def title(self, entry_info):
        return "{list_type}<{entry_info}> of length {list_len}".format(
            list_type = self.gdb_type.tag,
            entry_info = str(entry_info) if entry_info is not None else '?',
            list_len = len(self),
        )

    def __iter__(self):
        item = self.__val if self.__is_item else self.__val['next']
        stop_item = item['prev']
        while item != stop_item:
            yield item
            item = item['next']

    def __reversed__(self):
        item = self.__val if self.__is_item else self.__val['prev']
        stop_item = item['next']
        while item != stop_item:
            yield item
            item = item['prev']

    def __len__(self):
        if self.__len is None:
            self.__len = sum(1 for _ in self)
        return self.__len

class RlistLut(ListLut):
    _list_type = Rlist.gdb_type
    _symbols = (
        ('box_on_call', 'trigger::link'),
        ('box_on_select', 'trigger::link'),
        ('box_on_shutdown_trigger_list', 'trigger::link'),
        ('box_raft_on_broadcast', 'trigger::link'),
        ('engines', 'engine::link'),
        ('log_rotate_list', 'log::in_log_list'),
        ('on_access_denied', 'trigger::link'),
        ('on_alter_func', 'trigger::link'),
        ('on_alter_sequence', 'trigger::link'),
        ('on_alter_space', 'trigger::link'),
        ('on_console_eval', 'trigger::link'),
        ('on_schema_init', 'trigger::link'),
        ('on_shutdown_trigger_list', 'on_shutdown_trigger::link'),
        ('popen_head', 'popen_handle::list'),
        ('replicaset_on_quorum_change', 'trigger::link'),
        # Up to 3.5; an entry whose symbol, type or field the DWARF lacks
        # is left out of the table.
        ('replicaset_on_quorum_gain', 'trigger::link'),
        ('replicaset_on_quorum_loss', 'trigger::link'),
        ('session_on_auth', 'trigger::link'),
        ('session_on_connect', 'trigger::link'),
        ('session_on_disconnect', 'trigger::link'),
        ('shutdown_list', 'session::in_shutdown_list'),
        ('txns', 'txn::in_txns'),
    )
    _containers = (
        ('alter_space::ops', 'AlterSpaceOp::link'),
        ('alter_space::key_list', 'index_def::link'),
        ('applier::on_state', 'trigger::link'),
        ('cbus::endpoints', 'cbus_endpoint::in_cbus'),
        ('checkpoint::entries', 'checkpoint_entry::link'),
        ('cord::alive', 'fiber::link'),
        ('cord::dead', 'fiber::link'),
        ('cord::ready', 'fiber::state'),
        ('cpipe::on_flush', 'trigger::link'),
        ('create_ck_constraint_parse_def::checks', 'ck_constraint_parse::link'),
        ('create_fk_constraint_parse_def::fkeys', 'fk_constraint_parse::link'),
        ('fiber::on_stop', 'trigger::link'),
        ('fiber::on_yield', 'trigger::link'),
        ('fiber::wake', 'fiber::state'),
        ('fiber_channel::waiters', 'fiber::state'),
        ('fiber_cond::waiters', 'fiber::state'),
        ('fiber_pool::idle', 'fiber::state'),
        ('func::func_cache_pin_list', 'func_cache_holder::link'),
        ('gc_checkpoint::refs', 'gc_checkpoint_ref::in_refs'),
        ('gc_state::checkpoints', 'gc_checkpoint::in_checkpoints'),
        ('index::full_scans', 'full_scan_item::in_full_scans'),
        ('index::nearby_gaps', 'gap_item::in_nearby_gaps'),
        ('iproto_thread::stopped_connections', 'iproto_connection::in_stop_list'),
        ('journal_queue::waiters', 'fiber::state'),
        ('latch::queue', 'fiber::state'),
        ('luaL_serializer::on_update', 'trigger::link'),
        ('mempool::cold_slabs', 'mslab::next_in_cold'),
        ('memtx_join_ctx::entries', 'memtx_join_entry::in_ctx'),
        ('memtx_story::reader_list', 'tx_read_tracker::in_reader_list'),
        ('memtx_story_link::nearby_gaps', 'gap_item::in_nearby_gaps'),
        ('point_hole_item::ring', 'point_hole_item::ring'),
        ('raft::on_update', 'trigger::link'),
        ('RebuildCkConstraints::ck_constraint', 'ck_constraint::link'),
        ('recovery::on_close_log', 'trigger::link'),
        ('replicaset::anon', 'replica::in_anon'),
        ('replicaset::on_ack', 'trigger::link'),
        ('replicaset::applier::on_rollback', 'trigger::link'),
        ('replicaset::applier::on_wal_write', 'trigger::link'),
        ('schema_module::funcs', 'func_c::item'),
        ('space::before_replace', 'trigger::link'),
        ('space::child_fk_constraint', 'fk_constraint::in_child_space'),
        ('space::ck_constraint', 'ck_constraint::link'),
        ('space::memtx_stories', 'memtx_story::in_space_stories'),
        ('space::on_replace', 'trigger::link'),
        ('space::parent_fk_constraint', 'fk_constraint::in_parent_space'),
        ('space::space_cache_pin_list', 'space_cache_holder::link'),
        ('swim::dissemination_queue', 'swim_member::in_dissemination_queue'),
        ('swim::on_member_event', 'trigger::link'),
        ('swim::round_queue', 'swim_member::in_round_queue'),
        ('swim_scheduler::queue_output', 'swim_task::in_queue_output'),
        ('tx_manager::read_view_txs', 'txn::in_read_view_txs'),
        ('tx_manager::all_txs', 'txn::in_all_txs'), # up to 3.3, then txns
        ('tx_manager::all_stories', 'memtx_story::in_all_stories'),
        ('tx_manager::traverse_all_stories', 'memtx_story::in_all_stories'), # struct rlist*
        ('txn::conflict_list', 'tx_conflict_tracker::in_conflict_list'),
        ('txn::conflicted_by_list', 'tx_conflict_tracker::in_conflicted_by_list'),
        ('txn::gap_list', 'gap_item::in_gap_list'),
        ('txn::full_scan_list', 'full_scan_item::in_full_scan_list'),
        ('txn::on_commit', 'trigger::link'),
        ('txn::on_rollback', 'trigger::link'),
        ('txn::on_wal_write', 'trigger::link'),
        ('txn::point_holes_list', 'point_hole_item::in_point_holes_list'),
        ('txn::read_set', 'tx_read_tracker::in_read_set'),
        ('txn::savepoints', 'txn_savepoint::link'),
        ('txn_limbo::queue', 'txn_limbo_entry::in_queue'),
        ('txn_stmt::on_commit', 'trigger::link'),
        ('txn_stmt::on_rollback', 'trigger::link'),
        ('sql_stmt_cache::gc_queue', 'stmt_cache_entry::link'),
        ('sys_alloc::allocations', 'container::rlist'),
        ('user::credentials_list', 'credentials::in_user'),
        ('vy_cache_env::cache_lru', 'vy_cache_node::in_lru'),
        ('vy_history::stmts', 'vy_history_node::link'),
        ('vy_join_ctx::entries', 'vy_join_entry::in_ctx'),
        ('vy_lsm::on_destroy', 'trigger::link'),
        ('vy_lsm::runs', 'vy_run::in_lsm'),
        ('vy_lsm::sealed', 'vy_mem::in_sealed'),
        ('vy_lsm_recovery_info::ranges', 'vy_range_recovery_info::in_lsm'),
        ('vy_lsm_recovery_info::runs', 'vy_run_recovery_info::in_lsm'),
        ('vy_quota::wait_queue', 'vy_quota_wait_node::in_wait_queue'),
        ('vy_range::slices', 'vy_slice::in_range'),
        ('vy_range_recovery_info::slices', 'vy_slice_recovery_info::in_range'),
        ('vy_recovery::lsms', 'vy_lsm_recovery_info::in_recovery'),
        ('vy_tx::on_destroy', 'trigger::link'),
        ('vy_tx_manager::read_views', 'vy_read_view::in_read_views'),
        ('vy_tx_manager::writers', 'vy_tx::in_writers'), # up to 3.3
        ('vy_write_iterator::src_list', 'vy_write_src::in_src_list'),
        ('wal_writer::watchers', 'wal_watcher::next'),
        ('watchable::pending_watchers', 'watcher::in_idle_or_pending'),
        ('watchable_node::all_watchers', 'watcher::in_all_watchers'),
        ('watchable_node::idle_watchers', 'watcher::in_idle_or_pending'),
    )


class Stailq(object):
    gdb_type = gdb.lookup_type('stailq')
    item_gdb_type = gdb.lookup_type('stailq_entry')
    entry_ptr_gdb_type = find_type('stailq_entry_ptr')

    if entry_ptr_gdb_type is None:
        @staticmethod
        def __entry(val):
            return val
    else:
        @staticmethod
        def __entry(val):
            return val['value']

    def __init__(self, val):
        if equal_types(val.type.target(), self.gdb_type):
            self.__address = val
            self.__start_entry = self.__entry(val['first'])
        elif equal_types(val.type.target(), self.item_gdb_type):
            self.__address = None
            self.__start_entry = val
        else:
            raise gdb.GdbError("unexpected type: {}".format(dump_type(val.type.target())))
        self.__len = None

    @property
    def address(self):
        return self.__address

    def title(self, entry_info):
        if self.address is not None:
            length = "of length {}".format(len(self))
        else:
            length = "of unknown length (at least {})".format(len(self))
        return "{list_type}<{entry_info}> {length}".format(
            list_type = self.gdb_type.tag,
            entry_info = str(entry_info) if entry_info is not None else '?',
            length = length,
        )

    def __iter__(self):
        entry = self.__start_entry
        while entry != 0:
            yield entry
            entry = self.__entry(entry['next'])

    def __reversed__(self):
        entries = list(self)
        for entry in entries[::-1]:
            yield entry

    def __len__(self):
        if self.__len is None:
            self.__len = sum(1 for _ in self)
        return self.__len


class StailqLut(ListLut):
    _list_type = Stailq.gdb_type
    _symbols = (
        ('swim_task_pool', 'swim_task::in_pool'),
        ('txn_cache', 'txn::in_txn_cache'),
    )
    _containers = (
        ('applier_data_msg::txs', 'applier_tx::next'),
        ('applier_tx::rows', 'applier_tx_row::next'),
        ('cbus_endpoint::output', 'cmsg_poison::msg::fifo'),
        ('cpipe::input', 'cmsg::fifo'),
        ('fiber_pool::output', 'cmsg::fifo'),
        ('iproto_stream::pending_requests', 'iproto_msg::in_stream'),
        ('MemtxAllocator<Allocator>::gc', 'memtx_block::in_gc'),
        ('MemtxAllocator<Allocator>::gc', 'memtx_tuple::in_gc'), # up to 3.5
        ('memtx_engine::gc_queue', 'memtx_gc_task::link'),
        ('memtx_block_rv_list::blocks', 'memtx_block::in_gc'),
        ('memtx_tuple_rv_list::tuples', 'memtx_tuple::in_gc'), # up to 3.5
        ('relay::pending_gc', 'relay_gc_msg::in_pending'),
        ('swim::event_queue', 'swim_member::in_event_queue'),
        ('txn::stmts', 'txn_stmt::next'),
        ('Vdbe::autoinc_id_list', 'autoinc_id_entry::link'),
        ('vy_log_tx::records', 'vy_log_record::in_tx'),
        ('vy_log::pending_tx', 'vy_log_tx::in_pending'),
        ('vy_scheduler::processed_tasks', 'vy_task::in_processed'),
        ('vy_tx::log', 'txv::next_in_log'),
        ('vy_worker_pool::idle_workers', 'vy_worker::in_idle'),
        ('wal_msg::commit', 'journal_entry::fifo'),
        ('wal_msg::rollback', 'journal_entry::fifo'),
        ('wal_writer::rollback', 'journal_entry::fifo'),
        ('xrow_update_field::map::items', 'xrow_update_map_item::in_items'),
    )


class TtPrintListEntryParameter(gdb.Parameter):
    name = 'print tt-list-entry'

    set_doc = 'Set printing of actual rlist entries'
    show_doc = 'Show printing of actual rlist entries'

    def __init__(self):
        super(TtPrintListEntryParameter, self).__init__(self.name,
            gdb.COMMAND_DATA, gdb.PARAM_BOOLEAN)
        self.value = True

    def get_set_string(self):
        return ''

    def get_show_string(self, svalue):
        return 'Printing of actual rlist entries is ' + svalue

TtPrintListEntryParameter()


class FieldsPrinter(gdb.printing.PrettyPrinter):
    class FieldsSubprinter(gdb.printing.SubPrettyPrinter):
        def __init__(self, val, fields):
            super(FieldsPrinter.FieldsSubprinter, self).__init__(val.type.tag)
            self.val = val
            self.fields = fields

        def children(self):
            for field in self.fields:
                yield field, self.val[field]

    container_type = None
    fields = None

    def __init__(self):
        super(FieldsPrinter, self).__init__("FieldsPrinter")

    def __call__(self, val):
        if self.container_type is None or not equal_types(val.type, self.container_type):
            return None
        return self.FieldsSubprinter(val, self.fields)


class TtListPrinter(object):
    """
Pretty-printer for rlist

To avoid recursive printing of rlist (each entry has 'rlist' field that is used
as the entry anchor) only one instance of this printer is allowed.
This limitation is managed with '__instance_exists' class variable.

This script holds the table of predefined 'rlist' references used in tarantool
(see class ListLut). When one tries to print 'rlist' first the script tries to
identify the head of the list by traversing along the list and checking against
the reference table. Once it succeed we have both the list head and the actual
type of the list entries. Then if the specified expression directly refers to
the head of the list, the entire list is displayed entry-by-entry. If
the expression refers to the certain entry then this only entry is displayed
(along with its index in the list).
This default behavior can be altered with the class variables (see below).

walk_mode
  Default is 'False'.
  If 'True' printer takes the next step in list walking.

predicate
  Default is 'None'.
  When it's not 'None' entries are filtered by predicate.
  See '-predicate' option of 'tt-list' command

fields
  Default is 'None'.
  By default all entry's fields are printed, but it can be restricted with this
  variable (string of comma separated fields).
  See '-fields' option of 'tt-list' command

reverse
  Default is 'False'.
  When it's 'True' it forces to iterate the list in reverse direction.
  Displayed indices are affected accordingly.
  See '-reverse' option of 'tt-list' command

entry_info
  Default is 'None'.
  When it's not 'None' it overrides the one discovered with the predefined
  reference table. It defines how to convert abstract rlist item into actual
  entry.
  See '-entry-info' option of 'tt-list' command

head
  Default is 'None'.
  When it's not 'None' it overrides the one discovered with the predefined
  reference table.
  See '-head' option of 'tt-list' command

is_item
  Default is 'False'
  'True' means that printed value refers to the item of the list, rather than
  to the list itself.
  See '-item' option of 'tt-list' command

from_tt_list
  Default is 'False'.
  It is set to 'True' when the printer is used from the 'tt-list' command
    """

    entry_printer = FieldsPrinter()
    gdb.printing.register_pretty_printer(gdb.current_objfile(), entry_printer, True)

    __instance_exists = False

    class WalkOrigin(object):
        def __init__(self, val, is_item, head, reverse, predicate):
            self.val = val
            self.is_item = is_item
            self.head = head
            self.reverse = reverse
            self.predicate = predicate
            # A walk is of one process: the next one (another run, an
            # attach, another core) has other items, maybe under a list
            # head at the same address (a non-PIE build).
            self.pid = gdb.selected_inferior().pid

        def __eq__(self, other):
            return other is not None and \
                self.pid == other.pid and \
                self.val == other.val and \
                self.is_item == other.is_item and \
                self.head == other.head and \
                self.reverse == other.reverse and \
                self.predicate == other.predicate and \
                True

    # Walk state
    __walk_origin = None
    __walk_items = None
    __walk_next_item = None
    __walk_entry_info = None

    @classmethod
    def __set_walk_origin(cls, origin):
        cls.__walk_origin = origin
        cls.__set_walk_data(None, None)

    @classmethod
    def __set_walk_data(cls, items, entry_info):
        cls.__walk_items = items
        cls.__walk_next_item = None
        cls.__walk_entry_info = entry_info

    @classmethod
    def __walk_next(cls):
        if cls.__walk_items is not None:
            cls.__walk_next_item = next(cls.__walk_items)

    @classmethod
    def reset_walk(cls):
        cls.__set_walk_origin(None)

    # Initialization of config with default values is deferred so it can be
    # done in a single place (namely in 'reset_config') to avoid duplication
    # of default constants
    __config = None

    @classmethod
    def reset_config(cls,
                    walk_mode=False,
                    entry_info=None,
                    head=None,
                    is_item=False,
                    predicate=None,
                    fields=None,
                    reverse=False,
                    from_tt_list=False,
                    print_args=None,
                ):
        cls.__config = dict(
            walk_mode=walk_mode,
            entry_info=entry_info,
            head=head,
            is_item=is_item,
            predicate=predicate,
            fields=fields,
            reverse=reverse,
            from_tt_list=from_tt_list,
            print_args=print_args,
        )
        if walk_mode:
            # Some walking manipulations
            val = gdb.parse_and_eval(cls.get_print_exp(print_args))
            walk_origin = cls.WalkOrigin(
                val.address,
                is_item,
                head,
                reverse,
                predicate,
            )
            if walk_origin == cls.__walk_origin:
                cls.__walk_next()
            else:
                cls.__set_walk_origin(walk_origin)

    def __new__(cls, val):
        # Deferred initialization of config
        if cls.__config is None:
            cls.reset_config()
        # Don't create multiple instances to avoid recursive printing
        if cls.__instance_exists:
            return None
        cls.__instance_exists = True
        return super(TtListPrinter, cls).__new__(cls)

    @staticmethod
    def resolve_value_with_predefined(value_title, value, predefined):
        if value is None:
            return predefined
        if predefined is not None and value != predefined:
            gdb.write(
                "Warning: the predefined {value_title} ({predefined})"
                " doesn't match the specified one ({value})\n"
                "The latter will be used, but please check,"
                " either one or both are incorrect.\n".format(
                        value_title=value_title,
                        value=value,
                        predefined=predefined,
                    )
            )
        return value

    @staticmethod
    def get_print_exp(print_args):
        if '--' in print_args:
            exp_args = print_args[print_args.index('--')+1:]
        else:
            exp_args = print_args
        return ' '.join(exp_args)

    @staticmethod
    def lookup_entry_info_from_list_exp(lut, list_exp):
        def split_last_field(expr):
            arrow_pos = expr.rfind('->')
            dot_pos = expr.rfind('.')
            field_sep = '->' if arrow_pos > dot_pos else '.'
            container, _, field = expr.rpartition(field_sep)
            return container, field

        # Nothing to tell for an expression with no field ('*$list', a
        # symbol not in the table) or a field of a struct not in it.
        container, field = split_last_field(list_exp)
        if not container:
            return None
        try:
            container = gdb.parse_and_eval(container)
            if container.type.code == gdb.TYPE_CODE_PTR:
                container = container.dereference()
            container_info = '{}::{}'.format(container.type.tag, field)
            container_info = ContainerFieldInfo(container_info)
        except (gdb.error, AssertionError, TypeError):
            return None

        return lut.lookup_entry_info_by_container(container_info)

    @classmethod
    def resolve_entry_info(cls, config, lut, lst):
        # Turn configured entry_info (if any) into ContainerFieldInfo
        entry_info_config = config['entry_info']
        if entry_info_config is not None:
            entry_info_config = ContainerFieldInfo(entry_info_config)

        # Try to find predefined entry info for the list
        entry_info_lut = None
        if lst.address is not None:
            entry_info_lut = lut.lookup_entry_info(lst.address)

        # Check if the configured entry info conflicts with the predefined one
        entry_info = cls.resolve_value_with_predefined(
            'entry info',
            entry_info_config,
            entry_info_lut,
        )

        # If still no entry info, then the last chance to identify it is
        # parsing the list expression that was specified in 'print' command
        if entry_info is None:
            print_args = config['print_args']
            head_exp = config['head']
            list_exp = head_exp if head_exp is not None else \
                    cls.get_print_exp(print_args) if print_args is not None else \
                    None
            if list_exp is not None:
                entry_info = cls.lookup_entry_info_from_list_exp(lut, list_exp)

        # Display hint if failed to identify the type of the list entries
        if entry_info is None:
            msg = "Warning: failed to identify the type of the list entries.\n"
            if config['from_tt_list']:
                msg += "Please, specify entry info explicitly with -e option.\n"
            else:
                msg += "Please, try 'tt-list' or 'tt-list-walk' command.\n"
            gdb.write("\n" + msg + "\n", gdb.STDERR)

        return entry_info

    @staticmethod
    def prepare_list_title(list, entry_info):
        s = list.title(entry_info)
        if list.address is not None:
            s += ", ref=*({}*){:#x}".format(
                list.gdb_type.tag,
                int_from_address(list.address),
            )
        return s

    @staticmethod
    def prepare_sequence(config, lst, val, entry_info):
        def create_predicate(gdb_condition, entry_info):
            substitutions = (
                ('$index', lambda item: str(item[0])),
                ('$item', lambda item: '(({}*){:#x})'.format(
                                lst.item_gdb_type.tag,
                                int_from_address(item[1])
                            )),
                ('$entry', lambda item: '(({}*){:#x})'.format(
                                entry_info.container_type.tag,
                                int_from_address(entry_info.container_from_field(item[1]))
                            )),
            )
            substitutions = filter(lambda s: gdb_condition.find(s[0]) != -1, substitutions)
            substitutions = dict(substitutions)

            # Check that the predicate expression can be evaluated
            if '$index' in substitutions and lst.address is None:
                raise gdb.GdbError("\n"
                    "Can't use $index placeholder when the head of the list is unknown.\n"
                    "Please, use -h option to specify it explicitly if it is not identified automatically.\n"
                    "\n"
                )
            if '$entry' in substitutions and entry_info is None:
                raise gdb.GdbError("\n"
                    "Can't use $entry placeholder when the type of entry is unknown.\n"
                    "Please, use -e option to specify it explicitly if it is not identified automatically.\n"
                    "\n"
                )

            # This function applies item-specific substitutions and is used as
            # a predicate for standard filter function. Its argument is a tuple
            # that contains item and its index
            def predicate(item):
                item_condition = gdb_condition
                for placeholder in substitutions.keys():
                    item_condition = item_condition.replace(
                        placeholder, substitutions[placeholder](item))
                return gdb.parse_and_eval(item_condition)
            return predicate

        # Prepare items and indices
        if config['reverse']:
            items = reversed(lst)
            indices = itertools.count(len(lst)-1, -1)
        else:
            items = iter(lst)
            indices = itertools.count()
        # Decorate indices with '?' if the head of the list is unknown
        if lst.address is None:
            indices = map(lambda index: '?+{}'.format(index) if index > 0 else '?', indices)
        # Link them
        items = zip(indices, items)

        # If the head of the list is specified the entire list is considered,
        # if the concrete item is specified only items started with the specified
        # one is considered (up to the specified one in case of reverse direction)
        if not equal_types(val.type, lst.gdb_type) or val.address != lst.address:
            items = itertools.dropwhile(lambda item: item[1] != val.address, items)

        # Deal with predicate
        if config['predicate'] is not None:
            # Create python function that could be used as a filter predicate
            predicate = create_predicate(
                config['predicate'],
                entry_info,
            )
            # Filter items with predicate
            items = filter(predicate, items)

        return items

    def __init__(self, val):
        assert self.__class__.__instance_exists, "__instance_exists must be True"
        try:
            self.__init(val)
        except BaseException:
            # The traceback keeps this instance alive: without the reset no
            # list would be printed again in this session.
            self.__class__.__instance_exists = False
            self.__failed = True
            raise

    def __init(self, val):
        assert equal_to_any_types(val.type, (
                Rlist.gdb_type,
                Stailq.gdb_type,
                Stailq.item_gdb_type,
                Stailq.entry_ptr_gdb_type,
            )), "expression doesn't refer to list (type: {})".format(dump_type(val.type))

        super(TtListPrinter, self).__init__()

        config = self.__class__.__config

        self.val = val
        self.title = None
        self.items = None
        self.entry_info = None

        if config['walk_mode'] and self.__class__.__walk_next_item is not None:
            # This means that walk state has been set up already and now we are
            # moving through the items
            self.items = [self.__class__.__walk_next_item]
            self.entry_info = self.__class__.__walk_entry_info
            return

        # Turn configured head (if any) into gdb.Value
        head = config['head']
        if head is not None:
            head = gdb.parse_and_eval(head)
            if head.type.code == gdb.TYPE_CODE_INT:
                if equal_to_any_types(val.type, (Rlist.gdb_type, Stailq.gdb_type)):
                    head_type = val.type.pointer()
                elif equal_to_any_types(val.type, (Stailq.item_gdb_type, Stailq.entry_ptr_gdb_type)):
                    head_type = Stailq.gdb_type.pointer()
                else:
                    raise gdb.GdbError("unexpected type: {}".format(dump_type(val.type)))
                head = head.cast(head_type)
            elif equal_to_any_types(head.type, (Rlist.gdb_type, Stailq.gdb_type)):
                head = head.address
            else:
                raise gdb.GdbError("unexpected head type {}".format(dump_type(head.type)))

        # Create corresponding python iterable
        lst = None
        if equal_types(val.type, Rlist.gdb_type):
            lut = RlistLut
            # Try to find the head of the list
            head_lut = Rlist.lookup_head(val.address)
            head = self.resolve_value_with_predefined(
                'list head',
                head,
                head_lut,
            )
            if head is not None:
                lst = Rlist(head)
            else:
                lst = Rlist(val.address, config['is_item'])

        elif equal_types(val.type, Stailq.gdb_type):
            if head is not None and head != val.address:
                raise gdb.GdbError("Inconsistent arguments: '-head' is to be used"
                                " only when LIST_EXP refers to the single"
                                " list item rather than the list itself.")
            lut = StailqLut
            lst = Stailq(val.address)

        elif equal_to_any_types(val.type, (Stailq.item_gdb_type, Stailq.entry_ptr_gdb_type)):
            if equal_types(val.type, Stailq.entry_ptr_gdb_type):
                self.val = val['value'].dereference()
            lut = StailqLut
            lst = Stailq(head if head is not None else self.val.address)

        else:
            raise gdb.GdbError("TtListPrinter.__init__: "
                "unreachable code: unexpected type {}".format(dump_type(val.type)))

        # Deal with entry_info
        self.entry_info = self.resolve_entry_info(config, lut, lst)

        # Prepare title of the list
        self.title = self.prepare_list_title(lst, self.entry_info)

        # Prepare items sequence
        self.items = self.prepare_sequence(config, lst, self.val, self.entry_info)

        if config['walk_mode']:
            # This means that this is the first 'walk' invocation so we need
            # to setup 'walk' state
            self.__class__.__set_walk_data(self.items, self.entry_info)
            self.items = []

    def __del__(self):
        if getattr(self, '_TtListPrinter__failed', False):
            return
        assert self.__class__.__instance_exists
        self.__class__.__instance_exists = False

    def to_string(self):
        return self.title

    @staticmethod
    def child(entry_info, item):
        item_index, item_ptr = item
        if entry_info is None:
            return '[{}]'.format(item_index), item_ptr
        entry_ptr = entry_info.container_from_field(item_ptr)
        child_name = '[{}] (({}*){})'.format(item_index,
            entry_ptr.type.target().tag, entry_ptr)
        return child_name, entry_ptr.dereference()

    def children(self):
        print_entry = gdb.parameter(TtPrintListEntryParameter.name)
        entry_info = self.entry_info if print_entry else None

        # Setup entry printer (if required)
        config_fields = self.__config['fields']
        if entry_info is not None and config_fields is not None:
            self.entry_printer.container_type = entry_info.container_type
            self.entry_printer.fields = config_fields.split(',')
            self.entry_printer.enabled = True

        # Items already prepared, just yield'em all.  gdb may stop early
        # (an error, 'print elements'): the entry printer goes off anyway.
        try:
            for item in self.items:
                yield self.child(entry_info, item)
        finally:
            self.entry_printer.enabled = False

pp.add_printer('rlist', '^rlist$', TtListPrinter)
pp.add_printer('stailq', '^stailq$', TtListPrinter)
pp.add_printer('stailq_entry', '^stailq_entry$', TtListPrinter)
if Stailq.entry_ptr_gdb_type:
    pp.add_printer('stailq_entry_ptr', '^stailq_entry_ptr$', TtListPrinter)


def create_list_argument_parser():
    """
Create parser that knows the arguments used both in tt-list and
tt-list-walk commands
    """
    parser = argparse.ArgumentParser(add_help=False)
    parser.add_argument('-entry-info', action='store')
    parser.add_argument('-predicate', action='store')
    parser.add_argument('-fields', action='store')
    parser.add_argument('-head', action='store')
    parser.add_argument('-item', action='store_true')
    parser.add_argument('-reverse', action='store_true')
    return parser


class TtListSelect(gdb.Command):
    """
tt-list
Display entries of the LIST_EXP.
If LIST_EXP refers to the concrete item rather than the entire list, only items
started with the specified one are displayed (up to the specified one in case
of reverse direction).

Usage: tt-list [[OPTION]... --] LIST_EXP

Options:
  -predicate PREDICATE
    Filter entries with PREDICATE. It is a boolean expression similar to
    the condition expression in 'condition' or 'break' command. It is evaluated
    for each list item, which may be referred within the expression with the
    following placeholders:
      $index - item index
      $item - pointer to the item
      $entry - pointer to the actual entry
    Note, that with this option if LIST_EXP refers to the single item the
    iteration starts from this item, i.e. items before the specified one are
    not checked. If you need to check the entire list either check in reverse
    direction as well (with -r option) or use the list head as LIST_EXP.

  -fields FIELDS
    Comma separated entry fields that should be printed.
    If omitted then all fields are printed.

  -reverse
    Iterate list in reverse direction.

  -entry-info entry_info
    Normally entry info (entry type and anchor field) is identified
    automatically, but if failed it can be specified explicitly with
    this option, where entry_info should have format 'type::field'
    (type -- entry type, field -- anchor field)
    Please, note that the fact that you need this option might indicate
    that the list referenced by LIST_EXP is missing in the predefined table
    and need to be checked.

  -head HEAD
    If the head of the list is not detected automatically, it should be
    specified explicitly with this option.

  -item
    Explicitly specify that LIST_EXP refers to the item of the list, rather
    than the list itself. Normally it is identified automatically, but in some
    cases there maybe lack of information to figure it out and this option
    might help.

  Any print option (see 'help print').

Examples:

# Display all engines ('engines' is a global variable)
(gdb) tt-list engines

# Display the first engine
(gdb) tt-list engines.next

# Display the engine of the name 'memtx'
(gdb) tt-list -p '$_streq($entry->name, "memtx")' -- engines

# Display engines which names start with 's'
(gdb) tt-list -p '$_memeq($entry->name, "s", 1)' -- engines

# Display fibers ids and names
(gdb) tt-list -pretty -fields fid,name -- main_cord.alive
    """

    cmd_name = 'tt-list'

    def __init__(self):
        super(TtListSelect, self).__init__(self.cmd_name, gdb.COMMAND_DATA)

    def invoke(self, arg, from_tty):
        parser = create_list_argument_parser()

        # It is assumed that unknown arguments are for 'print' command
        args, print_args = parser.parse_known_args(gdb.string_to_argv(arg))

        # Configure TtListPrinter according to the command arguments
        # Considering that the only one instance of this printer is allowed
        # at a time it looks quite safe to pass additional parameters to the
        # printer over the class variables.
        # It works as follows:
        # 1. Initially all the mentioned class variables have default values
        #    It corresponds to default behavior of the printer as it works
        #    with 'print' command
        # 2. Setup class variables in 'tt-list' command and execute 'print'
        # 3. Restore default printer configuration, so subsequent 'print'
        #    works the same
        TtListPrinter.reset_config(
            walk_mode = False,
            entry_info = args.entry_info,
            predicate = args.predicate,
            fields = args.fields,
            head = args.head,
            is_item = args.item,
            reverse = args.reverse,
            from_tt_list = True,
            print_args = print_args,
        )

        try:
            gdb.execute('print {}'.format(' '.join(print_args)), from_tty)

        except Exception as e:
            raise e

        finally:
            TtListPrinter.reset_config()

TtListSelect()


class TtListWalk(gdb.Command):
    """
tt-list-walk
This command implements 'walk' functionality, that is it iterates
through the list but unlike 'tt-list' it displays one entry per
invocation (sometimes it may be more convenient to see entries
one-by-one instead of all-at-once). To move through the list you just
need to repeat continuously the last command with the same arguments
(that is just enter blank line) until the list is exhausted and message
'No items left' is displayed.
There can be only one active walk at a time. Once LIST_EXP or any option
that affects entries sequence is changed, current active walk is dropped
and the new one is started automatically.

Usage: tt-list-walk [[OPTION]... --] LIST_EXP

Options:
  Any option of 'tt-list' (see 'help tt-list')

  -new
    Start new walk.
    Note, that the new walk starts automatically if any of the options
    that affects sequence or LIST_EXP itself is changed. This option is
    to be used if you need to restart the walk on the same sequence.

Examples (repeat the command until the list is exhausted):

# Walk through the engines
(gdb) tt-list-walk engines

# Walk through the fibers with 'fid' greater than 110
(gdb) tt-list-walk -p '$entry->fid > 110' -- main_cord.alive
    """

    cmd_name = 'tt-list-walk'

    def __init__(self):
        super(TtListWalk, self).__init__(self.cmd_name, gdb.COMMAND_DATA)

    def invoke(self, arg, from_tty):
        parser = create_list_argument_parser()
        parser.add_argument('-new', action='store_true')

        # It is assumed that unknown arguments are for 'print' command
        args, print_args = parser.parse_known_args(gdb.string_to_argv(arg))

        args.new and TtListPrinter.reset_walk()

        try:
            TtListPrinter.reset_config(
                walk_mode = True,
                entry_info = args.entry_info,
                predicate = args.predicate,
                fields = args.fields,
                head = args.head,
                is_item = args.item,
                reverse = args.reverse,
                from_tt_list = True,
                print_args = print_args,
            )
            gdb.execute('print {}'.format(' '.join(print_args)))

        except StopIteration:
            TtListPrinter.reset_walk()
            raise gdb.GdbError("No items left.")

        finally:
            TtListPrinter.reset_config()

TtListWalk()


class SearchMemory(gdb.Function):
    """
Return the address of the first occurrence of a value in memory.

Usage: $find(START_ADDRESS, LENGTH, VAL)

Arguments:
    START_ADDRESS: where to start
    LENGTH: how many bytes to search
    VAL: the value whose bytes to look for

It is gdb's memory search (see 'help find'): a range that runs into
memory gdb cannot read may give a wrong address.

See also:
    help function tl_var_find_offset
    """

    def __init__(self):
        super(SearchMemory, self).__init__("find")

    def invoke(self, start_address, length, val):
        # Turn gdb.Value into search pattern.
        val_as_array = val.cast(gdb.lookup_type("uint8_t").array(val.type.sizeof-1))
        search_pattern = bytes([val_as_array[i] for i in range(val.type.sizeof)])
        return gdb.selected_inferior().search_memory(start_address, length, search_pattern)


SearchMemory()


class TlVarSearchRangeParam(gdb.Parameter):
    param_name = "tl-var-search-range"

    __title = "range for searching thread-local variables"
    __doc_footer = """
        It is used by $tl_var_find_offset and $tl_var_like functions if
        "SEARCH_RANGE argument is omitted, or the size of the TLS segment of
        the executable if that is larger (in 3.7 and 3.8 cord_ptr is about
        200 KiB from the thread pointer)."""

    def __init__(self):
        self.set_doc = "Set {}.\n{}".format(self.__title, self.__doc_footer)
        self.show_doc = "Show {}.\n{}".format(self.__title, self.__doc_footer)
        super(TlVarSearchRangeParam, self).__init__(self.__class__.param_name,
            gdb.COMMAND_DATA, gdb.PARAM_UINTEGER)
        self.value = 0x10000

    def get_show_string(self, svalue):
        return "{} is {}".format(self.__title.capitalize(), svalue)

    def get_set_string(self, svalue):
        return "{} is set to {}".format(self.__title.capitalize(), svalue)


TlVarSearchRangeParam()


def tls_segment_size():
    """p_memsz of the PT_TLS segment of the executable (ELF64), or 0."""
    try:
        with open(gdb.current_progspace().filename, 'rb') as fh:
            ident = fh.read(16)
            if ident[:4] != b'\x7fELF' or ident[4] != 2:
                return 0
            e = '<' if ident[5] == 1 else '>'
            fh.seek(0x20)
            phoff = struct.unpack(e + 'Q', fh.read(8))[0]
            fh.seek(0x36)
            phentsize, phnum = struct.unpack(e + 'HH', fh.read(4))
            for i in range(phnum):
                fh.seek(phoff + i * phentsize)
                if struct.unpack(e + 'I', fh.read(4))[0] == 7:  # PT_TLS
                    fh.seek(phoff + i * phentsize + 0x28)       # p_memsz
                    return struct.unpack(e + 'Q', fh.read(8))[0]
    except (IOError, OSError, TypeError, struct.error):
        pass
    return 0


def default_search_range():
    """tl-var-search-range, or the TLS segment and a page if larger."""
    page = TlVarFindOffset.PAGE_SIZE
    size = (tls_segment_size() + 2 * page - 1) // page * page
    return max(int(gdb.parameter(TlVarSearchRangeParam.param_name) or 0), size)


def tls_arch():
    arch = gdb.selected_frame().architecture()
    arch_id = map_to_arch_id(arch)
    if arch_id is None:
        raise gdb.GdbError("tls_base: architecture '{}' is not supported".format(arch.name()))
    return arch_id


def tls_base():
    """The thread pointer of the selected thread, as a 'char *'."""
    # gdb names the AArch64 one tpidr; tpidr_el0 is for an older name.
    arch_id = tls_arch()
    exprs = {
        arch_id_x86_64: ["$fs_base"],
        arch_id_aarch64: ["$tpidr", "$tpidr_el0"],
    }[arch_id]
    for expr in exprs:
        try:
            base = gdb.parse_and_eval(expr)
        except gdb.error:
            continue
        # An unknown register is a void convenience variable.
        if base.type.code != gdb.TYPE_CODE_VOID:
            return base.cast(gdb.lookup_type('long')).cast(
                gdb.lookup_type('char').pointer())
    base = tls_base_by_thread_handle(arch_id)
    if base is not None:
        return base
    raise gdb.GdbError("tls_base: no {} in this thread, and no thread handle of "
                       "libthread_db".format(' or '.join(exprs)))


def tls_base_by_thread_handle(arch_id):
    """The thread pointer of the selected thread, from its glibc thread
    descriptor, for a gdb with no register for it: gdb 12 on AArch64 (the
    tpidr register came in gdb 13).  The thread handle of libthread_db is
    the pthread_t, the address of the descriptor (struct pthread);
    libthread_db maps the thread pointer to it the way glibc says
    (DB_THREAD_SELF in its tls.h).  On x86-64 the descriptor is at the
    thread pointer (TLS variant II); on AArch64 it ends there and starts
    _thread_db_const_thread_area bytes below, sizeof (struct pthread)
    (gdb's aarch64_ps_get_thread_area subtracts that from TPIDR_EL0).
    None without libthread_db or that variable."""
    try:
        handle = gdb.selected_thread().handle()
        base = gdb.Value(handle, gdb.lookup_type('char').pointer())
    except (gdb.error, RuntimeError, TypeError, ValueError):
        return None
    if arch_id == arch_id_aarch64:
        try:
            base += int(gdb.parse_and_eval(
                '*(unsigned int *)&_thread_db_const_thread_area'))
        except gdb.error:
            return None
    return base


def tls_below_base():
    """Thread-local variables are below the thread pointer (x86-64,
    TLS variant II) or above it (AArch64, variant I)."""
    return tls_arch() == arch_id_x86_64


class SelectedThread(object):
    """with SelectedThread(): ... switches threads freely and gets the
    thread and the frame selected before back."""

    def __enter__(self):
        self.__thread = gdb.selected_thread()
        try:
            self.__frame = gdb.selected_frame()
        except gdb.error:
            self.__frame = None
        return self

    def __exit__(self, *exc):
        if self.__thread is not None and self.__thread.is_valid():
            self.__thread.switch()
            if self.__frame is not None and self.__frame.is_valid():
                self.__frame.select()
        return False


def main_thread():
    """The main thread: the one whose LWP is the process id."""
    inferior = gdb.selected_inferior()
    threads = sorted(inferior.threads(), key=lambda t: t.num)
    for t in threads:
        if t.ptid[1] == inferior.pid:
            return t
    return threads[0] if threads else None


def threads_from_selected():
    """The threads, the selected one first."""
    cur = gdb.selected_thread()
    rest = sorted((t for t in gdb.selected_inferior().threads() if t != cur),
                  key=lambda t: t.num)
    return ([cur] if cur is not None else []) + rest


class TlVarAddr(gdb.Function):
    """Return address of the thread-local variable at the given offset for the current thread.

To be able to handle thread-local variables (the ones declared with
'thread_local' or similar specifiers) GDB needs additional thread-specific
debugging information that is provided usually with libthread_db and might
be unavailable for some reason. If so this function might be helpful.

It uses the fact that certain thread-local variable is stored at the same
offset relative to the base memory address of thread-local storage (TLS) so
if somehow value of certain thread-local variable is known for some thread
this function could be used to find it for the current thread.

Usage:
    $tl_var_addr(TLS_OFFSET)

Arguments:
    TLS_OFFSET:

Returns:
    Value of the variable for the current thread

Example:
    # print thread-local cord_ptr (it is known that in some thread its value is equal to &main_cord)
    print (cord*)$tl_var_addr(-112)

See also:
    help tl_var_like
    help tl_var_find_offset
    help show tl-var-search-range
    """

    def __init__(self):
        super(TlVarAddr, self).__init__("tl_var_addr")

    def invoke(self, tls_offset):
        return offset_ptr(tls_base(), int(tls_offset)).cast(
            gdb.lookup_type('void').pointer())


TlVarAddr()


# Generally it is expected that if 'find_tls_offset' function is needed it works fine.
# This function is mostly introduced to allow for a quick check if something goes wrong.
class TlVarFindOffset(gdb.Function):
    """
Find TLS-offset of thread local variable by known reference value.

To be able to handle thread-local variables (the ones declared with the
'thread_local' or similar specifier) in the same way as the other ones GDB
needs additional thread-specific debugging information that is provided usually
with libthread_db, but it might be unavailable (or mismatched) for some reason.
If so this function might be helpful.

It iterates over all threads and search every thread-local
storage (TLS) for the 'ref_val'. If found the offset from the base address of
the corresponding TLS is returned (the one closest to the base of all the
threads), otherwise it fails.
Thread-local variables are stored in a region preceding base of TLS on x86-64,
that is when found the returned offset is negative there; on AArch64 they
follow the base and the offset is positive.

It uses the fact that certain thread-local variable is stored with the same
offset relative to the base memory address of thread-local storage (TLS) so
if somehow value of the desired thread-local variable is known for some thread
(it's not necessary to know for which one) this function could be used to find
its offset from TLS, and it could be

It uses the fact that certain thread-local variable is stored with the same
offset relative to the base memory address of thread-local storage (TLS) so
if somehow value of the desired thread-local variable is known for some thread
this function could be used to find its  for the current thread.

Usage: $tl_var_find_offset(REF_VAL[, SEARCH_RANGE])

Arguments:
    REF_VAL: known value of the thread-local variable
    SEARCH_RANGE: size of searched area in TLS (optional, get default from 'tl-var-search-range')

Returns:
    Offset from the base address of TLS.

Example:
    # if not able to get thread local variable in a usual way
    print cord_ptr
    Cannot find thread-local variables on this target
    # but we know that 'cord_ptr' in some thread points to &main_cord
    print (cord*)($fs_base-$find_tls_offset(&main_cord))
    print ()

See also:
    help function find_tl_var_like
    help show tl-var-search-range
    """

    def __init__(self):
        super(TlVarFindOffset, self).__init__("tl_var_find_offset")
        self.__cached_offsets = {}

    def invoke(self, ref_val, search_range=None):
        if search_range is None:
            search_range = default_search_range()

        if search_range < 0:
            raise gdb.GdbError("Invalid range {}. It must be greater than zero.".format(search_range))

        if search_range < ref_val.type.sizeof:
            raise gdb.GdbError("Range of size {} is not able to contain variable of size {}.".format(
                search_range,
                ref_val.type.sizeof,
            ))

        # Turn gdb.Value into search pattern.
        ref_val_as_array = ref_val.cast(gdb.lookup_type("uint8_t").array(ref_val.type.sizeof-1))
        search_pattern = bytes((int(ref_val_as_array[i]) for i in range(ref_val.type.sizeof)))
        search_range = int(search_range)

        offset = self.__cached_offsets.get(search_pattern)
        if offset is None or search_range < abs(offset) + len(search_pattern):
            offset = self.find_offset(search_pattern, search_range)
            if offset is None:
                raise gdb.GdbError("Failed to find the given value in any TLS.")
            self.__cached_offsets[search_pattern] = offset

        return offset

    PAGE_SIZE = 4096

    @classmethod
    def read_tls(cls, base, search_range, below):
        """The bytes of the search_range bytes below (or above) base, as far
        from base as they can be read, and the address of the first of them."""
        inferior = gdb.selected_inferior()
        chunks = []
        if below:
            low, end = base - search_range, base
            while end > low:
                start = max(low, (end - 1) // cls.PAGE_SIZE * cls.PAGE_SIZE)
                try:
                    chunks.insert(0, bytes(inferior.read_memory(start, end - start)))
                except gdb.error:
                    break
                end = start
            return b''.join(chunks), end
        start, high = base, base + search_range
        while start < high:
            end = min(high, (start // cls.PAGE_SIZE + 1) * cls.PAGE_SIZE)
            try:
                chunks.append(bytes(inferior.read_memory(start, end - start)))
            except gdb.error:
                break
            start = end
        return b''.join(chunks), base

    @classmethod
    def find_offset(cls, search_pattern, search_range):
        """The offset of search_pattern from the TLS base, the one closest to
        the base of all the threads, or None.  It reads the memory instead of
        gdb's search_memory, which gives a wrong address when the range runs
        into memory it cannot read (the main thread's TLS is near the start of
        its mapping), and never looks farther than the readable part."""
        offset = None
        with SelectedThread():
            for thread in gdb.selected_inferior().threads():
                thread.switch()
                try:
                    base = int(tls_base())
                    below = tls_below_base()
                except gdb.error:
                    continue
                data, start = cls.read_tls(base, search_range, below)
                pos = data.rfind(search_pattern) if below else data.find(search_pattern)
                if pos < 0:
                    continue
                found = start + pos - base
                if offset is None or abs(found) < abs(offset):
                    offset = found
        return offset


class TlVarLike(gdb.Function):
    """Find thread-local variable with the given known value for some thread.

Generally thread-local variables (the ones declared with the 'thread_local' or
similar specifier) are handled in the same way as the general ones, so this
function is no needed. But when thread-specific debugging information is not
available (or mismatched) for some reason this function might be helpful.

It uses the fact that certain thread-local variable in all threads is stored
at the same offset relative to the base memory address of thread-local storage
(TLS) so if somehow value of the given thread-local variable is known for some
thread (it's not necessary to know for which one) this function could be used
to find its value for the current thread.

Usage:
    $tl_var_like(REF_VAL, [SEARCH_RANGE])

Arguments:
    REF_VAL: known value of the variable for some thread
    SEARCH_RANGE: search range in TLS (optional, default is 'tl-var-search-range')

Returns:
    Value of the variable for the current thread

Example:
    # print thread-local cord_ptr (it is known that in some thread its value is equal to &main_cord)
    print $tl_var_like(&main_cord)

See also:
    help tl_var_addr
    help tl_var_find_offset
    help show tl-var-search-range
    """

    def __init__(self, find_offset_func):
        super(TlVarLike, self).__init__("tl_var_like")
        self.__find_offset_func = find_offset_func

    def invoke(self, ref_val, search_range=None):
        offset = self.__find_offset_func.invoke(ref_val, search_range)
        return cast_ptr(ref_val.type, tls_base(), offset).dereference()


tl_var_find_offset = TlVarFindOffset()
TlVarLike(tl_var_find_offset)


class ThreadLocal(object):
    """A thread-local variable of tarantool (cord_ptr; since 3.7 also
    tuple_formats and the like).

    gdb reads one in the selected thread through libthread_db or, since gdb
    16, on its own, but not always: there is no libthread_db for a core of
    another system, and gdb's own lookup on a core finds the TLS of the main
    thread only.  A variable is at the same offset from the thread pointer
    in every thread, so then it is read there.  The offset comes from a
    thread where gdb can tell the address of the variable or, if there is
    none, from another variable of the same TLS block whose offset is known:
    cord_ptr, found by its value in the main thread (&main_cord) with
    $tl_var_find_offset, and the offsets of both within the block ('info
    address')."""

    __offsets = {}
    __anchor = ('cord_ptr', '&main_cord')

    def __init__(self, name):
        self.name = name

    @classmethod
    def reset(cls, event=None):
        cls.__offsets.clear()

    def value(self):
        try:
            val = gdb.parse_and_eval(self.name)
            val.fetch_lazy()
            return val
        except gdb.error:
            pass
        sym = gdb.lookup_global_symbol(self.name)
        if sym is None and hasattr(gdb, 'lookup_static_symbol'):
            sym = gdb.lookup_static_symbol(self.name)
        if sym is None:
            raise gdb.GdbError("No symbol \"{}\"".format(self.name))
        if self.block_offset(self.name) is None:
            # Not a thread-local one (tuple_formats before 3.7).
            return sym.value()
        return cast_ptr(sym.type, tls_base(), self.offset()).dereference()

    def offset(self):
        """The offset of the variable from the thread pointer."""
        offset = self.__offsets.get(self.name)
        if offset is None:
            offset = self.__find_offset()
            self.__offsets[self.name] = offset
        return offset

    def __find_offset(self):
        with SelectedThread():
            for thread in threads_from_selected():
                thread.switch()
                try:
                    addr = gdb.parse_and_eval('&' + self.name)
                    return int(addr.cast(gdb.lookup_type('long'))) - int(tls_base())
                except gdb.error:
                    continue
        name, ref = self.__anchor
        block = self.block_offset(self.name)
        if block is not None:
            if self.name == name:
                offset = tl_var_find_offset.find_offset(
                    self.pattern(ref), default_search_range())
                if offset is not None:
                    return offset
            else:
                anchor_block = self.block_offset(name)
                if anchor_block is not None and anchor_block[1] == block[1]:
                    return ThreadLocal(name).offset() - anchor_block[0] + block[0]
        raise gdb.GdbError("Cannot read the thread-local variable {}: gdb finds no "
                           "TLS for it in any thread and cannot tell its offset "
                           "otherwise".format(self.name))

    @staticmethod
    def pattern(expr):
        val = gdb.parse_and_eval(expr)
        raw = val.cast(gdb.lookup_type('uint8_t').array(val.type.sizeof - 1))
        return bytes(int(raw[i]) for i in range(val.type.sizeof))

    @staticmethod
    def block_offset(name):
        """(offset in its TLS block, the objfile of the block) of a
        thread-local variable, or None."""
        try:
            out = gdb.execute('info address {}'.format(name), False, True)
        except gdb.error:
            return None
        m = re.search(r'thread-local variable at offset (0x[0-9a-f]+|\d+) '
                      r'in the thread-local storage for `(.*)\'', out)
        if m is None:
            return None
        return int(m.group(1), 0), m.group(2)


gdb.events.new_objfile.connect(ThreadLocal.reset)
if hasattr(gdb.events, 'clear_objfiles'):
    gdb.events.clear_objfiles.connect(ThreadLocal.reset)


def cord():
    """The cord of the selected thread."""
    return ThreadLocal('cord_ptr').value()


def fiber():
    """The fiber running in the selected thread."""
    return cord()['fiber']


class Cord(object):
    def __init__(self):
        self.__cord_ptr = cord()
        if int(self.__cord_ptr) == 0:
            raise gdb.GdbError("No cord in this thread (cord_ptr is NULL)")

    def fiber(self):
        return self.__cord_ptr["fiber"]

    def fibers(self):
        fibers = self.__cord_ptr['alive']
        fibers = Rlist(fibers.address)
        list_entry_info = RlistLut.lookup_entry_info_by_container(ContainerFieldInfo("cord::alive"))
        fibers = map(lambda x: list_entry_info.container_from_field(x), fibers)
        return itertools.chain(fibers, [self.__cord_ptr['sched'].address])

    def find_fiber(self, fid):
        return next((f for f in self.fibers() if f['fid'] == fid), None)


try:
    import gdb.unwinder
    support_unwinders = True

except ImportError:
    support_unwinders = False
    msg_cant_explore_fiber_stack = "Exploring stack of non-running fiber is not supported with this version of GDB (requires GDB 8+)"
    gdb.write("WARNING: " + msg_cant_explore_fiber_stack + '\n')


if support_unwinders:

    class FiberUnwinderFrameFilter(object):
        def __init__(self):
            self.name = "TtFiberUnwinderFrameFilter"
            self.priority = 100
            self.enabled = True
            self.skip_frame_sp = None
            gdb.current_progspace().frame_filters[self.name] = self

        def filter(self, frame_iter):
            if self.skip_frame_sp is None:
                return frame_iter
            reg, val = self.skip_frame_sp
            return filter(lambda f: f.inferior_frame().read_register(reg) != val, frame_iter)


    class FrameId(object):
        def __init__(self, sp, pc):
            self.sp = sp
            self.pc = pc

    class FiberUnwinder(gdb.unwinder.Unwinder):
        __instance = None

        @classmethod
        def instance(cls):
            if cls.__instance is None:
                cls.__instance = cls()
                gdb.unwinder.register_unwinder(gdb.current_progspace(), cls.__instance, True)
            return cls.__instance

        def __init__(self):
            super(FiberUnwinder, self).__init__('TtFiberUnwinder')
            self.__frame_filter = FiberUnwinderFrameFilter()
            self.__unwind_mode = True
            self.__arch = None
            self.__set_fiber_internal(None)
            # The registers of the thread change when it runs, or when the
            # user writes one: the innermost frame is learned again.
            gdb.events.stop.connect(self.__on_new_registers)
            if hasattr(gdb.events, 'register_changed'):
                gdb.events.register_changed.connect(self.__on_new_registers)

        def fiber(self):
            """The fiber whose stack is explored: the one picked with
            set_fiber() in the selected thread, else the running one."""
            if self.__fiber is not None and self.__in_fiber_thread():
                return self.__fiber
            return fiber()

        def set_fiber(self, f):
            """Explore the stack of fiber f of the selected thread (None or
            the running fiber: the stack as it is)."""
            if f is not None and int(f) == int(fiber()):
                f = None
            self.__set_fiber_internal(f)
            self.__arch = self.get_arch()
            gdb.invalidate_cached_frames()

        def reset_fiber(self):
            """Back to the running fiber."""
            self.set_fiber(None)

        def __set_fiber_internal(self, f):
            self.__fiber = f
            self.__forget_innermost()
            # The selection belongs to this thread.  The unwinder tells a
            # thread by its ptid: reading cord_ptr there may need a thread
            # switch (a core with no libthread_db), which gdb does not
            # survive in an unwinder.
            thread = gdb.selected_thread()
            self.__ptid = thread.ptid if thread is not None else None

        def __in_fiber_thread(self):
            thread = gdb.selected_thread()
            return thread is not None and thread.ptid == self.__ptid

        def __forget_innermost(self):
            # (sp, pc) of the innermost frame of the fiber's thread, the
            # first frame the unwinder is asked about; see __call__.
            self.__innermost = None

        def __on_new_registers(self, event):
            # gdb has unwound frames at the new registers already (the stop
            # line), and at the old ones after the thread was resumed (a
            # step over a breakpoint), the innermost frame told by the old
            # registers.  Unwound again, the frames start from the innermost
            # one.
            self.__forget_innermost()
            if self.__fiber is not None:
                gdb.invalidate_cached_frames()

        def get_arch(self):
            if hasattr(gdb.selected_inferior(), "architecture"):
                return gdb.selected_inferior().architecture()
            self.__unwind_mode = False
            arch = gdb.selected_frame().architecture()
            self.__unwind_mode = True
            return arch

        def __call__(self, pending_frame):
            if not self.__unwind_mode:
                return None

            # For the currently running fiber, or in another thread than the
            # one of the selected fiber, use the default unwinder and don't
            # filter frames
            if self.__fiber is None or not self.__in_fiber_thread():
                self.__frame_filter.skip_frame_sp = None
                return None

            # Architecture specific parameters of coro context
            arch = self.__arch.name().lower()
            if arch.find('x86-64') != -1:
                coro_ctx_regs = [
                    'r15',
                    'r14',
                    'r13',
                    'r12',
                    'rbx',
                    'rbp',
                    'rip',
                ]
                ctx_offs_lr = coro_ctx_regs.index('rip')
                reg_pc = 'rip'
                reg_sp = 'rsp'

            elif arch.find('aarch64') != -1:
                coro_ctx_regs = [
                    'x19', 'x20',
                    'x21', 'x22',
                    'x23', 'x24',
                    'x25', 'x26',
                    'x27', 'x28',
                    'x29', 'x30',
                    'd8',  'd9',
                    'd10', 'd11',
                    'd12', 'd13',
                    'd14', 'd15',
                ]
                ctx_offs_lr = coro_ctx_regs.index('x30')
                reg_pc = 'pc'
                reg_sp = 'sp'

            else:
                raise gdb.GdbError("FiberUnwinder: architecture '{}' is not supported".format(arch.name()))

            orig_sp = pending_frame.read_register(reg_sp)
            orig_pc = pending_frame.read_register(reg_pc)

            # Only the innermost frame (of the running fiber) gets the saved
            # context of the selected fiber as its caller; then proceed with
            # the default unwinder.  That frame is the first one the
            # unwinder is asked about: gdb unwinds from the newest frame
            # (set_fiber() and a stop flush the frame cache), and inline
            # frames above it are gdb's inline unwinder's, never asked about
            # here (abort() in a glibc with debug info:
            # __pthread_kill_implementation and __pthread_kill_internal are
            # inline frames 0 and 1).  It is told by its registers, the
            # thread's own: the frames can be unwound again after the cache
            # is flushed (a thread switch and back, flushregs, another
            # extension), and the same frame comes back with the same sp and
            # pc.
            here = (int(orig_sp), int(orig_pc))
            if self.__innermost is None:
                self.__innermost = here
            elif here != self.__innermost:
                return None

            # Frame matching actual stack pointer should be skipped as it refers to
            # the running fiber
            self.__frame_filter.skip_frame_sp = (reg_sp, orig_sp)

            # Get fiber stack
            sp = self.__fiber['ctx']['sp']

            # Create UnwindInfo. Usually the frame is identified by the stack
            # pointer and the program counter.
            unwind_info = pending_frame.create_unwind_info(FrameId(sp, orig_pc))

            # Find the values of the registers in the caller's frame and
            # save them in the result:
            pc = (sp + ctx_offs_lr).dereference()
            for reg in coro_ctx_regs:
                unwind_info.add_saved_register(reg, sp.dereference())
                sp += 1
            unwind_info.add_saved_register(reg_pc, pc)
            unwind_info.add_saved_register(reg_sp, sp)

            # Return the result:
            return unwind_info


class FibersInfo(gdb.Command):
    """Display currently known tarantool fibers of the current cord.
Usage: info tt-fibers [ID]...
If ID is given, it is a space-separated list of IDs of fibers to display.
Otherwise, all fibers of the current cord are displayed."""

    def __init__(self):
        super(FibersInfo, self).__init__("info tt-fibers", gdb.COMMAND_STATUS)

    def invoke(self, arg, from_tty):
        cord = Cord()

        # Prepare sequence of fibers
        fibers = cord.fibers()
        ids = arg.split()
        if len(ids) > 0:
            ids = set(map(lambda x: int(x), ids))
            fibers = filter(lambda f: int(f['fid']) in ids, fibers)

        fiber_cur = fiber_to_unwind = cord.fiber()
        if support_unwinders and FiberUnwinder.instance().fiber():
            fiber_to_unwind = FiberUnwinder.instance().fiber()

        gdb.write('{marker_cur} {id:6} {target:8} {name:32} {marker_unwind} {stack:18} {func}\n'.format(
            marker_cur=' ',
            marker_unwind=' ',
            id='Id',
            target='Target',
            name='Name',
            stack='Stack',
            func='Function',
        ))
        for f in fibers:
            gdb.write('{marker_cur} {id:6} {target:8} {name:32} {marker_unwind} {stack:18} {func}\n'.format(
                marker_cur='*' if f == fiber_cur else ' ',
                marker_unwind='*' if f == fiber_to_unwind else ' ',
                id=str(f['fid']),
                target='TtFiber',
                name='"' + f['name'].string() + '"',
                stack=str(f['ctx']['sp']),
                func=f['f'],
            ))

FibersInfo()


class Fiber(gdb.Command):
    """tt-fiber [FIBER_ID]
Use this command to select fiber which stack need to be explored.
FIBER_ID must be currently known, if omitted displays current fiber info.

Please, be aware that the innermost frame (level #0, and the inline frames
up to the first one that is not, as at a crash in glibc with its debug
info) is filtered out for any fiber other than the one that is currently
running, so backtrace in this case starts with level #1 frame, or the one
after those inline frames; this command selects it. It's because GDB always
starts frame sequence with a frame that matches actual value of the stack
pointer register, but this frame only makes sense for the currently running
fiber.

Please, note that this command does NOT change currently running fiber,
it just selects stack to explore w/o changing any register/data."""

    def __init__(self):
        super(Fiber, self).__init__("tt-fiber", gdb.COMMAND_RUNNING)

    def invoke(self, arg, from_tty):
        if not support_unwinders:
            raise gdb.GdbError(msg_cant_explore_fiber_stack)

        cord = Cord()

        if not arg:
            f = FiberUnwinder.instance().fiber() or cord.fiber()
            gdb.write('Current fiber is {id} "{name}" {func}\n'.format(
                id=f['fid'],
                name=f['name'].string(),
                func=f['f'],
            ))
            return

        argv = gdb.string_to_argv(arg)
        try:
            fid = int(argv[0])

        except Exception as e:
            raise gdb.GdbError("Invalid fiber ID: {}".format(argv[0]))

        f = cord.find_fiber(fid)
        if f is None:
            raise gdb.GdbError("Unknown fiber {}.".format(argv[0]))

        fiber_to_unwind = None if f == cord.fiber() else f
        FiberUnwinder.instance().set_fiber(fiber_to_unwind)
        gdb.write("Switching stack unwinder to fiber {} ({:#x} ({}))\n".format(
            f["fid"],
            int_from_address(f),
            f["name"].string(),
        ))

        frame_to_select = 0
        if fiber_to_unwind is not None:
            frame_to_select = self.fiber_frame_level()
        gdb.execute("frame {}".format(frame_to_select), False, True)

    @staticmethod
    def fiber_frame_level():
        """The level of the newest frame of the selected fiber.  The frames
        above it are of the running fiber, all with the stack pointer of the
        thread: the innermost one and the inline frames over it."""
        frame = gdb.newest_frame()
        sp = frame.read_register('sp')
        level = 0
        while frame is not None and frame.read_register('sp') == sp:
            frame = frame.older()
            level += 1
        return level if frame is not None else 1

Fiber()
