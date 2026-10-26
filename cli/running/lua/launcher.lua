--- This is a launch script that does the necessary preparation
-- before launching an instance.

local os = require('os')
local console = require('console')
local log = require('log')
local title = require('title')
local ffi = require('ffi')

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

    if jit.os == 'OSX' or jit.os == 'BSD' then
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

    -- Preparation of the "console" socket.
    local console_sock = os.getenv('TT_CLI_CONSOLE_SOCKET')
    if console_sock ~= nil and console_sock ~= '' then
        local cons_listen_sock = console.listen(console_sock)

        local function close_sock_tr()
            box.ctl.on_shutdown(nil, close_sock_tr)
            local res, err = pcall(cons_listen_sock.close, cons_listen_sock)
            if not res then
                log.error('Failed to close console socket %s: %s', console_sock, err)
            end
        end
        box.ctl.on_shutdown(close_sock_tr)
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

    local ok, err = pcall(dofile, instance_path)
    if not ok then
        log.error('Failed to run instance: %s, error: "%s"', instance_path, err)
        os.exit(1)
    end
    return 0
end

start_instance()
