-- The script of the instance that configures itself: box.cfg takes the
-- paths tt lays out for the options it leaves out. It reports how it runs
-- and leaves a mark in its data directory.
local fio = require('fio')

box.cfg{}

local mark = fio.open(fio.pathjoin(box.cfg.wal_dir, 'legacy.mark'),
    {'O_CREAT', 'O_WRONLY', 'O_TRUNC'}, tonumber('644', 8))
mark:write(os.getenv('LEGACY_GREETING') or '')
mark:close()

print(('legacy started: %s in %s'):format(os.getenv('LEGACY_GREETING'), fio.cwd()))
