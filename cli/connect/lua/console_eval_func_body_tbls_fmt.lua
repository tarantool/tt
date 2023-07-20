local yaml = require('yaml')

local function table_len(t)
    local len = 0
    for _ in pairs(t) do
        len = len + 1
    end

    return len
end

local function metadata_mapping(res)
    local mapped_res = {}
    if table_len(res[1].rows) == 1 then
        for k, v in ipairs(unpack(res[1].rows)) do
            mapped_res[res[1].metadata[k].name] = v
        end
    elseif table_len(res[1].rows) > 1 then
        for _, row in ipairs(res[1].rows) do
            local row_mapped = {}
            for row_col_num, row_col in ipairs(row) do
                row_mapped[res[1].metadata[row_col_num].name] = row_col
            end
            table.insert(mapped_res, row_mapped)
        end
    end

    if table_len(mapped_res) == 0 then
        for _, v in ipairs(res[1].metadata) do
            mapped_res[v.name] = ""
        end
    end

    if table_len(res[1].rows) > 1 then
        res = mapped_res
    else
        res = {mapped_res}
    end

    return res
end

local res = yaml.decode((require('console').eval(...)))

if type(res[1]) == 'table' and type(res[1].metadata) == 'table'
                           and type(res[1].rows) == 'table' then
    res = metadata_mapping(res)
end

if not (type(res) == 'table' or box.tuple.is(res)) then
    res = {res}
end

local ok, res_encoded = pcall(yaml.encode, res)
if not ok then
    return yaml.encode({{error = res_encoded}})
end

return res_encoded
