if #arg ~= 1 then
    io.stderr:write("One argument is expected\n")
    os.exit(1)
end

dofile(arg[1])

if default_cfg == nil then
    io.stderr:write("tarantoolctl config does not initialize default_cfg\n")
    os.exit(1)
end

-- Data/lib dir discovery.
local lib_dir = default_cfg.wal_dir
for _, data_dir in pairs({default_cfg.memtx_dir, default_cfg.vinyl_dir, default_cfg.snap_dir}) do
    if lib_dir == nil then
        if data_dir ~= nil then
            lib_dir = data_dir
        end
    else
        if data_dir ~= nil and lib_dir ~= data_dir then
            io.stderr:write("Unable to identify data directory from taractoolctl config. " ..
            "There is uncertainty between " .. lib_dir .. " and " .. data_dir .. "\n")
            os.exit(1)
        end
    end
end

print("data_dir=" .. (lib_dir or ""))
print("log_dir=" .. (default_cfg.log or default_cfg.logger or ""))
print("pid_file=" .. (default_cfg.pid_file or ""))
print("instance_dir=" .. (instance_dir or ""))
