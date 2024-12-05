local inst_name = os.getenv('TARANTOOL_INSTANCE_NAME')
local app_name = os.getenv('TARANTOOL_APP_NAME')
local fio = require('fio')

local inst = "unknown instance"
local file_name = 'flag'

if app_name ~= nil then
    if inst_name == nil then
        inst_name = "stateboard"
        app_name = app_name:sub(1, -inst_name:len()-2)
    end
    inst = app_name .. ":" .. inst_name
    file_name = file_name .. "-" .. inst
end

fh = fio.open(file_name, {'O_WRONLY', 'O_CREAT'})
if fh ~= nil then
    fh:close()
end

while true do
    print(inst)
    require("fiber").sleep(1)
end
