--- This is a launch script that does the necessary preparation
-- before launching an instance.

local fun = require('fun')
local os = require('os')
local console = require('console')
local log = require('log')
local title = require('title')
local ffi = require('ffi')
local iter  = fun.iter

--- Accumulating function for iter:reduce().
local function reducer(result, left, right)
    if result ~= nil then
        return result
    end
    if tonumber(left) == tonumber(right) then
        return nil
    end
    return tonumber(left) > tonumber(right)
end

local function split_version(version_string)
    local version_table  = version_string:split('.')
    local version_table2 = version_table[3]:split('-')
    version_table[3], version_table[4] = version_table2[1], version_table2[2]
    return version_table
end

--- Returns true if version of tarantool is greater then expected
--- else false.
local function check_version(expected)
    local version = _TARANTOOL
    if type(version) == 'string' then
        version = split_version(version)
    end
    local res = iter(version):zip(expected):reduce(reducer, nil)
    if res or res == nil then res = true end
    return res
end

local origin_cfg = box.cfg

--- Wrapper for cfg to push our values over tarantool.
local function cfg_wrapper(cfg)
    ffi.cdef([[
        int chdir(const char *path);
    ]])
    ffi.C.chdir(os.getenv('TT_CLI_CONSOLE_SOCKET_DIR'))
    local cfg = cfg or {}
    local tt_cfg = {}
    tt_cfg.wal_dir = os.getenv('TT_WAL_DIR')
    tt_cfg.memtx_dir = os.getenv('TT_MEMTX_DIR')
    tt_cfg.vinyl_dir = os.getenv('TT_VINYL_DIR')
    for i, v in pairs(tt_cfg) do
        if cfg[i] == nil then
            cfg[i] = v
        end
    end
    local success, data = pcall(origin_cfg, cfg)
    ffi.C.chdir(os.getenv('TT_CLI_WORK_DIR'))
    if not success then
        log.error('Someting wrong happened when tried to set dataDir.')
    end
    return data
end

--- Start an Instance. The "init" file of the Instance passes
-- through "TT_CLI_INSTANCE".
local function start_instance()
    local instance_path = os.getenv('TT_CLI_INSTANCE')
    if instance_path == nil then
        log.error('Failed to get instance path')
        os.exit(1)
    end
    title.update{
        script_name = instance_path,
        __defer_update = true
    }

    -- Make stdout line buffered. gh-36.
    -- By default (when using "glibc") "stdout" is line buffered when connected
    -- to a TTY and block buffered (one page 4KB) when connected to a pipe / file.
    -- In luajit print() calls fwrite(3) from glibc,
    -- and since the launcher redirects stdout to a file, the write will be
    -- block buffered and the user will not be able to read the log file in real time.
    -- In order to change this behavior we will set "stdout" to line buffered mode
    -- by calling "setlinebuf(3)". "stderr" is set to no-buffering by default.
    --
    -- Several useful links:
    -- https://www.pixelbeat.org/programming/stdio_buffering/
    -- https://man7.org/linux/man-pages/man3/setbuf.3.html
    ffi.cdef([[
        typedef struct __IO_FILE FILE;
        void setlinebuf(FILE *stream);
    ]])

    if jit.os == 'OSX' then
        ffi.cdef([[
            FILE *__stdoutp;
        ]])
        ffi.C.setlinebuf(ffi.C.__stdoutp)
    else
        ffi.cdef([[
            FILE *stdout;
        ]])
        ffi.C.setlinebuf(ffi.C.stdout)
    end

    ffi.cdef([[
        int chdir(const char *path);
    ]])

    -- It became common that console socket path is longer than 108 symbols(sun_path limit).
	-- To reduce length of path we use relative path with
    -- chdir into a directory of console socket.
	-- e.g foo/bar/123.sock -> ./123.sock
    local console_sock_dir = os.getenv('TT_CLI_CONSOLE_SOCKET_DIR')
    if console_sock_dir ~= nil and console_sock_dir ~= '' then
        ffi.C.chdir(console_sock_dir)
    end

    -- If tarantool version is above 2.8.1, then can use environment variables
    -- instead of wrapping cfg.
    if not check_version({2,8,1,0}) then
        box.cfg = cfg_wrapper
    end
    -- Preparation of the "console" socket.
    local console_sock = os.getenv('TT_CLI_CONSOLE_SOCKET')
    if console_sock ~= nil and console_sock ~= '' then
        local cons_listen_sock = console.listen(console_sock)

        -- tarantool 1.10 does not have a trigger on terminate a process.
        -- So the socket will be closed automatically on termination and
        -- deleted from "running.go".
        if box.ctl.on_shutdown ~= nil then
            local close_sock_tr = box.ctl.on_shutdown(function()
                box.ctl.on_shutdown(nil, close_sock_tr)
                local res, err = pcall(cons_listen_sock.close, cons_listen_sock)
                if not res then
                    log.error('Failed to close console socket %s: %s', console_sock, err)
                end
            end)
        end

    end

    -- After making console socket chdir back to work directory.
    local work_dir = os.getenv('TT_CLI_WORK_DIR')
    if work_dir ~= nil and work_dir ~= '' then
        ffi.C.chdir(work_dir)
    end

    -- If stdin of the program was moved by command "tt run" to another fd
    -- then we need to move it back.
    -- It is used in cases when calling tarantool with "-" flag to hide input
	-- for example from ps|ax.
	-- e.g ./tt run - ... or test.lua | ./tt run -
    if os.getenv("TT_CLI_RUN_STDIN_FD") ~= nil then
        ffi.cdef([[
            int dup2(int old_handle, int new_handle);
        ]])
        ffi.C.dup2(tonumber(os.getenv("TT_CLI_RUN_STDIN_FD")), 0)
    end
    -- Start the Instance.

    -- Cartridge takes instance path from arg[0] and use this path
    -- for a workaround for rocks loading in tarantool 1.10.
    -- This can be removed when tarantool 1.10 is no longer supported.
    arg[0] = instance_path

    local success, data = pcall(dofile, instance_path)
    if not success then
        log.error('Failed to run instance: %s', instance_path)
        os.exit(1)
    end
    return 0
end

start_instance()
