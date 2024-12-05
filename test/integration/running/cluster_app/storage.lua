local fiber = require('fiber')
local fio = require('fio')
local log = require('log')

local pid_path = os.getenv('TT_PROCESS_PID_FILE_DEFAULT')
local flag_path = fio.pathjoin(fio.dirname(pid_path), 'flag')

fh = fio.open(flag_path, {'O_WRONLY', 'O_CREAT'})
if fh ~= nil then
    fh:close()
end

box.cfg{}

-- Create something to generate xlogs.
box.schema.space.create('test-' .. box.cfg.instance_name)

while true do
    log.info(pid_path)
    fiber.sleep(1)
end
