local inst_name = os.getenv('TARANTOOL_INSTANCE_NAME')
local app_name = os.getenv('TARANTOOL_APP_NAME')

box.cfg{}

-- Create something to generate xlogs.
box.schema.space.create('customers')

while true do
    if app_name ~= nil and inst_name ~= nil then
        print(app_name .. ":" .. inst_name)
    else
        print("unknown instance")
    end
    require("fiber").sleep(1)
end
