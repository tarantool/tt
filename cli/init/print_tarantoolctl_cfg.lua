if #arg ~= 1 then
    os.exit(1)
end

dofile(arg[1])

if default_cfg == nil then
    io.stderr:write("tarantoolctl config does not initialize default_cfg")
    os.exit(1)
end

-- Check data directories are the same.
if default_cfg.wal_dir ~= default_cfg.snap_dir then
    io.stderr:write("ambiguous data directory")
    os.exit(1)
end
if default_cfg.vinyl_dir ~= default_cfg.snap_dir then
    io.stderr:write("ambiguous data directory")
    os.exit(1)
end

print("wal_dir=" .. (default_cfg.wal_dir or ""))
print("logger=" .. (default_cfg.logger or ""))
print("pid_file=" .. (default_cfg.pid_file or ""))
