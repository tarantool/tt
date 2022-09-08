-- This is a script that check an application file for syntax errors.
-- The application file passes through "TT_CLI_INSTANCE".

-- The script is delivered inside the "tt" binary and is launched
-- to execution via the `-e` flag when starting the application instance.
-- AFAIU, due to such method of launching, we can reach the limit of the
-- command line length ("ARG_MAX") and in this case we will have to create
-- a file with the appropriate code. But, in the real world this limit is
-- quite high (I looked at it on several machines - it equals 2097152)
-- and we can not create a workaround for this situation yet.
--
-- Several useful links:
-- https://porkmail.org/era/unix/arg-max.html
-- https://unix.stackexchange.com/a/120842

local os  = require('os')
local log = require('log')

local function check()
    local instance_path = os.getenv('TT_CLI_INSTANCE')
    if instance_path == nil then
        log.error('Internal error: failed to get instance path from TT_CLI_INSTANCE')
        os.exit(1)
    end

    local func, err = loadfile(instance_path)
    if func == nil then
        log.error("Result of check: syntax errors detected: '%s'", err)
        os.exit(1)
    end
end

check()
os.exit(0)
