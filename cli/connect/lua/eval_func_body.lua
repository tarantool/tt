local yaml = require('yaml')
yaml.cfg{ encode_use_tostring = true }
local args = {...}
local cmd = table.remove(args, 1)
local is_sql_language = table.remove(args, 1)
local need_metainfo = table.remove(args, 1)

local function is_command(line)
    return line:sub(1, 1) == '\\'
end

if is_command(cmd) or is_sql_language == true then
    return require('console').eval(cmd)
end

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

local function format_equal(f1, f2)
    if #f1 ~= #f2 then
        return false
    end

    for i, field in ipairs(f1) do
        if field.name ~= f2[i].name or field.type ~= f2[i].type then
            return false
        end
    end
    return true
end

local ret = table_pack(pcall(fun, unpack(args)))
if not ret[1] then
    local err = unpack(ret, 2, ret.n)
    if err == nil then
        err = box.NULL
    end
    return yaml.encode({{error = err}})
end
if ret.n == 1 then
    return "---\n...\n"
end
for i=2,ret.n do
    if ret[i] == nil then
        ret[i] = box.NULL
    end
end
if not need_metainfo then
    return yaml.encode({unpack(ret, 2, ret.n)})
end

for i=2,ret.n do
    if box.tuple.is ~= nil and box.tuple.is(ret[i]) and ret[i].format then
        local ret_with_format = {
            metadata = ret[i]:format(),
            rows = { ret[i] },
        }
        if #ret_with_format.metadata > 0 then
            ret[i] = ret_with_format
        end
    end

    if type(ret[i]) == 'table' and #ret[i] > 0 then
        local ret_with_format = {
            metadata = {},
            rows = ret[i],
        }

        local same_format = true
        for tuple_ind, tuple in ipairs(ret[i]) do
            local is_tuple = box.tuple.is ~= nil and box.tuple.is(tuple) and tuple.format

            if tuple_ind == 1 then
                if not is_tuple then
                    same_format = false
                    break
                end

                ret_with_format.metadata = tuple:format()
            elseif not is_tuple or
                    not format_equal(tuple:format(), ret_with_format.metadata) then
                same_format = false
                break
            end
        end

        if #ret_with_format.metadata > 0 and same_format then
            ret[i] = ret_with_format
        end
    end
end

return yaml.encode({unpack(ret, 2, ret.n)})
