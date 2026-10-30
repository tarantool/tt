local fiber = require('fiber')
local fio = require('fio')

box.cfg({})

fh = fio.open('ready', {'O_WRONLY', 'O_CREAT'}, tonumber('644',8))
fh:close()

while true do
    fiber.sleep(5)
end
