local inst_name = os.getenv('TARANTOOL_INSTANCE_NAME')
local app_name = os.getenv('TARANTOOL_APP_NAME')

local inst = "unknown instance"
local file_name = 'flag'

if app_name ~= nil and inst_name ~= nil then
    inst = app_name .. ":" .. inst_name
    file_name = file_name .. "-" .. inst
end

fh = require('fio').open(file_name, {'O_WRONLY', 'O_CREAT'})
if fh ~= nil then
    fh:close()
end

box.cfg{}

-- Create something to generate xlogs.
box.schema.space.create('customers')

while true do
    print(inst)
    require("fiber").sleep(1)
end
