const std = @import("std");

pub fn build(b: *std.Build) void {
    const target   = b.standardTargetOptions(.{});
    const optimize = b.standardOptimizeOption(.{});

    const lib = b.addStaticLibrary(.{
        .name = "webbplace_perf",
        .root_source_file = b.path("src/lib.zig"),
        .target = target,
        .optimize = optimize,
    });
    lib.linkLibC();

    // Output to ./zig-out/lib/libwebbplace_perf.a + ./include/webbplace_perf.h
    b.installArtifact(lib);

    const header_install = b.addInstallFile(b.path("src/webbplace_perf.h"), "include/webbplace_perf.h");
    b.getInstallStep().dependOn(&header_install.step);
}
