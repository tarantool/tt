yaml = require("yaml")

if #arg ~= 1 then
    io.stderr:write("One argument is expected\n")
    os.exit(1)
end

dofile(arg[1])

if default_cfg == nil then
    io.stderr:write("tarantoolctl config does not initialize default_cfg\n")
    os.exit(1)
end

local cfg = {}
cfg.wal_dir = default_cfg.wal_dir or ""
cfg.vinyl_dir = default_cfg.vinyl_dir or ""
cfg.memtx_dir = default_cfg.memtx_dir or default_cfg.snap_dir or ""
cfg.log_dir = default_cfg.log or default_cfg.logger or ""
cfg.pid_file = default_cfg.pid_file or ""
cfg.instance_dir = instance_dir or ""

print(yaml.encode(cfg))
