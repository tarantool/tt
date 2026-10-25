local fiber = require('fiber')
local fio = require('fio')

-- tt runs the instance in the project directory, which is named after the
-- application.
local inst = fio.basename(fio.cwd()) .. ":" .. box.info.name

local fh = fio.open('flag-' .. inst, {'O_WRONLY', 'O_CREAT'})
if fh ~= nil then
    fh:close()
end

-- Create something to generate xlogs.
box.schema.space.create('customers', {if_not_exists = true})

fiber.create(function()
    while true do
        print(inst)
        io.stdout:flush()
        fiber.sleep(1)
    end
end)
