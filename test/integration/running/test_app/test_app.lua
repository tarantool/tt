local fio = require('fio')

-- tt runs the instance in the project directory.
local fh = fio.open('flag', {'O_WRONLY', 'O_CREAT'})
if fh ~= nil then
    fh:close()
end
