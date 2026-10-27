local fio = require('fio')

-- tt runs the instance in the project directory: the flavour its record
-- gave it lands there, in a file named after the instance.
local fh = fio.open('flavour-' .. box.info.name, {'O_WRONLY', 'O_CREAT', 'O_TRUNC'},
    tonumber('644', 8))
if fh ~= nil then
    fh:write(os.getenv('FLAVOUR') or '')
    fh:close()
end
