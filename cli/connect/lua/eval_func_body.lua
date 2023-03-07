local yaml = require('yaml')
local args = {...}
local cmd = table.remove(args, 1)
local fun, errmsg = loadstring("return "..cmd)
if not fun then
    fun, errmsg = loadstring(cmd)
end
if not fun then
    return yaml.encode({box.NULL})
end

local function table_pack(...)
    return {n = select('#', ...), ...}
end

local ret = table_pack(pcall(fun, unpack(args)))
if not ret[1] then
    return yaml.encode({box.NULL})
end
if ret.n == 1 then
    return "---\n...\n"
end
for i=2,ret.n do
    if ret[i] == nil then
        ret[i] = box.NULL
    end
end
return yaml.encode({unpack(ret, 2, ret.n)})
