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

--- Returns true if version of tarantool is greater than expected
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

-- Types of available options.
-- Could be comma separated lua types or 'any' if any type is allowed.
--
-- get_option_from_env() leans on the set of types in use: don't
-- forget to update it when add a new type or a combination of
-- types here.
local template_cfg = {
    listen                          = 'string, number',
    memtx_memory                    = 'number',
    strip_core                      = 'boolean',
    memtx_min_tuple_size            = 'number',
    memtx_max_tuple_size            = 'number',
    slab_alloc_granularity          = 'number',
    slab_alloc_factor               = 'number',
    iproto_threads                  = 'number',
    work_dir                        = 'string',
    memtx_dir                       = 'string',
    wal_dir                         = 'string',
    vinyl_dir                       = 'string',
    vinyl_memory                    = 'number',
    vinyl_cache                     = 'number',
    vinyl_max_tuple_size            = 'number',
    vinyl_read_threads              = 'number',
    vinyl_write_threads             = 'number',
    vinyl_timeout                   = 'number',
    vinyl_run_count_per_level       = 'number',
    vinyl_run_size_ratio            = 'number',
    vinyl_range_size                = 'number',
    vinyl_page_size                 = 'number',
    vinyl_bloom_fpr                 = 'number',

    log                             = 'module',
    log_nonblock                    = 'module',
    log_level                       = 'module',
    log_format                      = 'module',

    io_collect_interval             = 'number',
    readahead                       = 'number',
    snap_io_rate_limit              = 'number',
    too_long_threshold              = 'number',
    wal_mode                        = 'string',
    rows_per_wal                    = 'number',
    wal_max_size                    = 'number',
    wal_dir_rescan_delay            = 'number',
    wal_cleanup_delay               = 'number',
    force_recovery                  = 'boolean',
    replication                     = 'string, number, table',
    instance_uuid                   = 'string',
    replicaset_uuid                 = 'string',
    custom_proc_title               = 'string',
    pid_file                        = 'string',
    background                      = 'boolean',
    username                        = 'string',
    coredump                        = 'boolean',
    checkpoint_interval             = 'number',
    checkpoint_wal_threshold        = 'number',
    wal_queue_max_size              = 'number',
    checkpoint_count                = 'number',
    read_only                       = 'boolean',
    hot_standby                     = 'boolean',
    memtx_use_mvcc_engine           = 'boolean',
    worker_pool_threads             = 'number',
    election_mode                   = 'string',
    election_timeout                = 'number',
    replication_timeout             = 'number',
    replication_sync_lag            = 'number',
    replication_sync_timeout        = 'number',
    replication_synchro_quorum      = 'string, number',
    replication_synchro_timeout     = 'number',
    replication_connect_timeout     = 'number',
    replication_connect_quorum      = 'number',
    replication_skip_conflict       = 'boolean',
    replication_anon                = 'boolean',
    feedback_enabled                = 'boolean',
    feedback_crashinfo              = 'boolean',
    feedback_host                   = 'string',
    feedback_interval               = 'number',
    net_msg_max                     = 'number',
    sql_cache_size                  = 'number',
}

local module_cfg_type = {
    -- Options for logging.
    log                 = 'string',
    log_nonblock        = 'boolean',
    log_level           = 'number, string',
    log_format          = 'string',
}

--
-- Parse TT_* environment variable that corresponds to given
-- option.
--
local function get_option_from_env(option)
    local param_type = template_cfg[option]
    assert(type(param_type) == 'string')

    if param_type == 'module' then
        -- Parameter from module.
        param_type = module_cfg_type[option]
    end

    local env_var_name = 'TT_' .. option:upper()
    local raw_value = os.getenv(env_var_name)

    if raw_value == nil or raw_value == '' then
        return nil
    end

    local err_msg_fmt = 'Environment variable %s has ' ..
        'incorrect value for option "%s": should be %s'

    -- This code leans on the existing set of template_cfg and
    -- module_cfg_type types for simplicity.
    if param_type:find('table') and raw_value:find(',') then
        assert(not param_type:find('boolean'))
        local res = {}
        for i, v in ipairs(raw_value:split(',')) do
            res[i] = tonumber(v) or v
        end
        return res
    elseif param_type:find('boolean') then
        assert(param_type == 'boolean')
        if raw_value:lower() == 'false' then
            return false
        elseif raw_value:lower() == 'true' then
            return true
        end
        error(err_msg_fmt:format(env_var_name, option, '"true" or "false"'))
    elseif param_type == 'number' then
        local res = tonumber(raw_value)
        if res == nil then
            error(err_msg_fmt:format(env_var_name, option,
                'convertible to a number'))
        end
        return res
    elseif param_type:find('number') then
        assert(not param_type:find('boolean'))
        return tonumber(raw_value) or raw_value
    else
        assert(param_type == 'string')
        return raw_value
    end
end

--- Fills config from environment variables.
local function get_env_cfg()
    local res = {}
    for option, _ in pairs(template_cfg) do
        res[option] = get_option_from_env(option)
    end
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
    tt_cfg = get_env_cfg()
    for i, v in pairs(tt_cfg) do
        if cfg[i] == nil then
            cfg[i] = v
        end
    end
    local success, data = pcall(origin_cfg, cfg)
    ffi.C.chdir(os.getenv('TT_CLI_WORK_DIR'))
    if not success then
        log.error('Someting wrong happened when tried to load environment variables.')
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
            local function close_sock_tr()
                box.ctl.on_shutdown(nil, close_sock_tr)
                local res, err = pcall(cons_listen_sock.close, cons_listen_sock)
                if not res then
                    log.error('Failed to close console socket %s: %s', console_sock, err)
                end
            end
            box.ctl.on_shutdown(close_sock_tr)
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

    local ok, err = pcall(dofile, instance_path)
    if not ok then
        log.error('Failed to run instance: %s, error: "%s"', instance_path, err)
        os.exit(1)
    end
    return 0
end

start_instance()
