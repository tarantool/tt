const std = @import("std");
const tarantool = @import("tarantool");

pub fn build(b: *std.Build) void {
    const target = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

    // zig-out/lib/kv.so, kv.dylib on macOS: the C functions kv.kv_set,
    // kv.kv_get and kv.kv_del of box.schema.func.
    const kv = tarantool.addModule(b, .{
        .name = "kv",
        .root_source_file = b.path("src/kv.zig"),
        .target = target,
        .optimize = optimize,
    });
    tarantool.installModule(b, kv);
}
