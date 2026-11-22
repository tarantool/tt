//! Example Tarantool C module: a tiny string key/value store over a space
//! named `kv` (primary key = field 0). Demonstrates both SDK layers:
//! `kv_set`/`kv_del` are typed procs via `exportProc`; `kv_get` is a raw proc
//! so it can control the result tuple's lifetime while reading a field.
//!
//! Build into `kv.so`, then from Lua:
//!   box.schema.func.create('kv.kv_set', {language='C'})
//!   box.func['kv.kv_set']:call{'hello', 'world'}

const std = @import("std");
const t = @import("tarantool");
const box = t.box;
const proc = t.proc;
const msgpack = t.msgpack;
const c = t.c;

/// kv_set(key, value): upsert a row. Typed layer — args and return are
/// (de)serialized by the comptime wrapper.
fn kvSet(key: []const u8, value: []const u8) !void {
    const space = try box.Space.byName("kv");
    if (try space.replace(.{ key, value })) |row| row.deinit();
}

/// kv_del(key): remove a row if present. Typed layer.
fn kvDel(key: []const u8) !void {
    const space = try box.Space.byName("kv");
    if (try space.delete(.{key})) |row| row.deinit();
}

/// kv_get(key) -> value | nil. Raw layer via `exportRaw`: the read-back tuple
/// borrows host memory only until the next box call, so we read field 1 and
/// encode the result before unref'ing the tuple. Every fallible step is a `try`;
/// `exportRaw` maps a returned error to a box error (the host's real message on
/// a `BoxOp`, the error name otherwise).
fn kvGet(
    ctx: ?*c.box_function_ctx_t,
    args: [*c]const u8,
    args_end: [*c]const u8,
) !c_int {
    const len = @intFromPtr(args_end) - @intFromPtr(args);
    var dec = msgpack.Decoder.init(args[0..len]);
    const parsed = try msgpack.decode(struct { []const u8 }, &dec);

    const space = try box.Space.byName("kv");
    const maybe = try space.get(.{parsed[0]});

    if (maybe) |row| {
        defer row.deinit();
        if (try row.field(1, std.heap.c_allocator)) |v| return proc.returnValue(ctx, v);
    }
    return proc.returnValue(ctx, @as(?u8, null)); // nil
}

comptime {
    proc.exportProc("kv_set", kvSet);
    proc.exportProc("kv_del", kvDel);
    proc.exportRaw("kv_get", kvGet);
}
