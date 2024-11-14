-- This is a script that print into stdout the contents of .snap/.xlog files.
-- The files passes through 'TT_CLI_CAT_FILES'.
-- The --show-system flag passes through 'TT_CLI_CAT_SHOW_SYS'.
-- The --space flags passes through 'TT_CLI_CAT_SPACES'.
-- The --from flag passes through 'TT_CLI_CAT_FROM'.
-- The --to flag passes through 'TT_CLI_CAT_TO'.
-- The --timestamp flag passes through 'TT_CLI_CAT_TIMESTAMP'.
-- The --replica flags passes through 'TT_CLI_CAT_REPLICAS'.
-- The --format flags passes through 'TT_CLI_CAT_FORMAT'.

local log = require('log')
local xlog = require('xlog')
local yaml  = require('yaml')
local json = require('json')

local function cat_yaml_cb(record)
    print(yaml.encode(record):sub(1, -6))
end

local function cat_json_cb(record)
    print(json.encode(record))
end

local function write_lua_string(string)
    io.stdout:write("'")
    local pos, byte = 1, string:byte(1)
    while byte ~= nil do
        io.stdout:write(('\\x%02x'):format(byte))
        pos = pos + 1
        byte = string:byte(pos)
    end
    io.stdout:write("'")
end

local write_lua_table = nil

local function write_lua_value(value)
    if type(value) == 'string' then
        write_lua_string(value)
    elseif type(value) == 'table' then
        write_lua_table(value)
    else
        io.stdout:write(tostring(value))
    end
end

local function write_lua_fieldpair(key, val)
    io.stdout:write('[')
    write_lua_value(key)
    io.stdout:write('] = ')
    write_lua_value(val)
end

write_lua_table = function(tuple)
    io.stdout:write('{')
    local is_begin = true
    for key, val in pairs(tuple) do
        if is_begin == false then
            io.stdout:write(', ')
        else
            is_begin = false
        end
        write_lua_fieldpair(key, val)
    end
    io.stdout:write('}')
end

local function cat_lua_cb(record)
    -- Ignore both versions of IPROTO_NOP: the one without a
    -- body (new), and the one with empty body (old).
    if record.HEADER.type == 'NOP' or record.BODY == nil or
    record.BODY.space_id == nil then
        return
    end
    io.stdout:write(('box.space[%d]'):format(record.BODY.space_id))
    local op = record.HEADER.type:lower()
    io.stdout:write((':%s('):format(op))
    if op == 'insert' or op == 'replace' then
        write_lua_table(record.BODY.tuple)
    elseif op == 'delete' then
        write_lua_table(record.BODY.key)
    elseif op == 'update' then
        write_lua_table(record.BODY.key)
        io.stdout:write(', ')
        write_lua_table(record.BODY.tuple)
    elseif op == 'upsert' then
        write_lua_table(record.BODY.tuple)
        io.stdout:write(', ')
        write_lua_table(record.BODY.operations)
    end
    io.stdout:write(')\n')
end

local cat_formats = setmetatable({
    yaml = cat_yaml_cb,
    json = cat_json_cb,
    lua  = cat_lua_cb,
}, {
    __index = function(self, cmd)
        log.error('Internal error: unknown formatter "%s"', cmd)
        os.exit(1)
    end
})

local function find_in_list(id, list)
    if type(list) == 'number' then
        return id == list
    end
    for _, val in ipairs(list) do
        if val == id then
            return true
        end
    end
    return false
end

local function filter_xlog(gen, param, state, opts, cb)
    local from, to, timestamp, spaces = opts.from, opts.to, opts.timestamp, opts.space
    local show_system, replicas = opts['show-system'], opts.replica

    for lsn, record in gen, param, state do
        local sid = record.BODY and record.BODY.space_id
        local rid = record.HEADER.replica_id
        local ts = record.HEADER.timestamp or 0
        if replicas and #replicas == 1 and replicas[1] == rid and (lsn >= to or ts >= timestamp) then
            -- Stop, as we've finished reading tuple with lsn == to
            -- and the next lsn's will be bigger.
            break
        elseif (lsn < from) or (lsn >= to) or (ts >= timestamp) or
            (not spaces and sid and sid < 512 and not show_system) or
            (spaces and (sid == nil or not find_in_list(sid, spaces))) or
            (replicas and not find_in_list(rid, replicas)) then
            -- Pass this tuple, luacheck: ignore.
        else
            cb(record)
        end
    end
end

local function cat(positional_arguments, keyword_arguments)
    local opts = keyword_arguments
    local cat_format = opts.format
    local format_cb = cat_formats[cat_format]
    local is_printed = false
    for _, file in ipairs(positional_arguments) do
        io.stderr:write(string.format('• Result of cat: the file "%s" is processed below •\n', file))
        io.stdout:flush()
        local gen, param, state = xlog.pairs(file)
        filter_xlog(gen, param, state, opts, function(record)
            is_printed = true
            format_cb(record)
            io.stdout:flush()
        end)
        if opts.format == 'yaml' and is_printed then
            is_printed = false
            print('...\n')
        end
    end
end

local function str_to_bool(value)
    local map = {
        ['true']  = true,
        ['false'] = false
    }
    return map[value]
end

local function main()
    local positional_arguments = {}
    local keyword_arguments = {}

    local files = os.getenv('TT_CLI_CAT_FILES')
    if files == nil then
        log.error('Internal error: failed to get cat params from TT_CLI_CAT_FILES')
        os.exit(1)
    end
    positional_arguments = json.decode(files)

    local show_sys = os.getenv('TT_CLI_CAT_SHOW_SYS')
    if str_to_bool(show_sys) then
        keyword_arguments['show-system'] = true
    end

    local format = os.getenv('TT_CLI_CAT_FORMAT')
    keyword_arguments['format'] = format

    local spaces = os.getenv('TT_CLI_CAT_SPACES')
    if spaces ~= nil then
        keyword_arguments['space'] = {}
        for _, val in pairs(json.decode(spaces)) do
            table.insert(keyword_arguments['space'], tonumber(val))
        end
    end

    local from = os.getenv('TT_CLI_CAT_FROM')
    if from == nil then
        log.error('Internal error: failed to get cat params from TT_CLI_CAT_FROM')
        os.exit(1)
    end
    keyword_arguments['from'] = tonumber(from)

    local to = os.getenv('TT_CLI_CAT_TO')
    if to == nil then
        log.error('Internal error: failed to get cat params from TT_CLI_CAT_TO')
        os.exit(1)
    end
    keyword_arguments['to'] = tonumber(to)

    local timestamp = os.getenv('TT_CLI_CAT_TIMESTAMP')
    if timestamp == nil then
        log.error('Internal error: failed to get cat params from TT_CLI_CAT_TIMESTAMP')
        os.exit(1)
    end
    keyword_arguments['timestamp'] = tonumber(timestamp)

    local replicas = os.getenv('TT_CLI_CAT_REPLICAS')
    if replicas ~= nil then
        keyword_arguments['replica'] = {}
        for _, val in pairs(json.decode(replicas)) do
            table.insert(keyword_arguments['replica'], tonumber(val))
        end
    end

    cat(positional_arguments, keyword_arguments)
end

main()
os.exit(0)
