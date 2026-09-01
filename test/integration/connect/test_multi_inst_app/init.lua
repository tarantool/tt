local fio = require('fio')

local inst_name = os.getenv('TARANTOOL_INSTANCE_NAME')
local app_name = os.getenv('TARANTOOL_APP_NAME')

box.cfg()

box.schema.user.create('test', { password = 'password' , if_not_exists = true })
box.schema.user.grant('test','read,write,execute,create,drop','universe')

fio.open(inst_name, 'O_CREAT'):close()
while true do
    if app_name ~= nil and inst_name ~= nil then
        print(app_name .. ":" .. inst_name)
    else
        print("unknown instance")
    end
    require("fiber").sleep(1)
end
