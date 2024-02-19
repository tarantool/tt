local fiber = require('fiber')
local fio = require('fio')

while true do
    if type(box.info.name) == 'string' then
        break
    end
    fiber.sleep(0.1)
end

while true do
    if #box.cfg.replication <= #box.info.replication then
        break
    end
    fiber.sleep(0.1)
end

fh = fio.open('ready-' .. box.info.name, {'O_WRONLY', 'O_CREAT'}, tonumber('644',8))
fh:close()
