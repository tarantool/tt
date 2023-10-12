local fiber = require('fiber')
local fio = require('fio')

fh = fio.open('flag', {'O_WRONLY', 'O_CREAT'})
if fh ~= nil then
    fh:close()
end

while true do
    fiber.sleep(5)
end
