-- This is a script that play the contents of .snap/.xlog files to another Tarantool instance.
-- The files and uri passes through 'TT_CLI_PLAY_FILES_AND_URI'.
-- The --show-system flag passes through 'TT_CLI_PLAY_SHOW_SYS'.
-- The --space flags passes through 'TT_CLI_PLAY_SPACES'.
-- The --from flag passes through 'TT_CLI_PLAY_FROM'.
-- The --to flag passes through 'TT_CLI_PLAY_TO'.
-- The --timestamp flag passes through 'TT_CLI_PLAY_TIMESTAMP'.
-- The --replica flags passes through 'TT_CLI_PLAY_REPLICAS'.

local log = require('log')
local xlog = require('xlog')
local json = require('json')
local netbox = require('net.box')

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

local function play(positional_arguments, keyword_arguments, opts)
    local filter_opts = keyword_arguments
    local uri = table.remove(positional_arguments, 1)
    if uri == nil then
        log.error('Internal error: empty URI is provided')
        os.exit(1)
    end
    local remote = netbox.new(uri, opts)
    if not remote:wait_connected() then
        log.error('Fatal error: no connection to the host "%s"', uri)
        os.exit(1)
    end
    for _, file in ipairs(positional_arguments) do
        print(string.format('• Play is processing file "%s" •', file))
        io.stdout:flush()
        local gen, param, state = xlog.pairs(file)
        filter_xlog(gen, param, state, filter_opts, function(record)
            local sid = record.BODY and record.BODY.space_id
            if sid ~= nil then
                local args, so = {}, remote.space[sid]
                if so == nil then
                   log.error('Fatal error: no space #%s, stopping work', sid)
                   os.exit(1)
                end
                table.insert(args, so)
                table.insert(args, record.BODY.key)
                table.insert(args, record.BODY.tuple)
                table.insert(args, record.BODY.operations)
                so[record.HEADER.type:lower()](unpack(args))
            end
        end)
        print(string.format('• Done with file "%s" •', file))
        io.stdout:flush()
    end
    print('\n• Play result: completed successfully •')
    remote:close()
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

    local files_and_uri = os.getenv('TT_CLI_PLAY_FILES_AND_URI')
    if files_and_uri == nil then
        log.error('Internal error: failed to get play params from TT_CLI_PLAY_FILES_AND_URI')
        os.exit(1)
    end
    positional_arguments = json.decode(files_and_uri)

    local show_sys = os.getenv('TT_CLI_PLAY_SHOW_SYS')
    if str_to_bool(show_sys) then
        keyword_arguments['show-system'] = true
    end

    local spaces = os.getenv('TT_CLI_PLAY_SPACES')
    if spaces ~= nil then
        keyword_arguments['space'] = {}
        for _, val in pairs(json.decode(spaces)) do
            table.insert(keyword_arguments['space'], tonumber(val))
        end
    end

    local from = os.getenv('TT_CLI_PLAY_FROM')
    if from == nil then
        log.error('Internal error: failed to get play params from TT_CLI_PLAY_FROM')
        os.exit(1)
    end
    keyword_arguments['from'] = tonumber(from)

    local to = os.getenv('TT_CLI_PLAY_TO')
    if to == nil then
        log.error('Internal error: failed to get play params from TT_CLI_PLAY_TO')
        os.exit(1)
    end
    keyword_arguments['to'] = tonumber(to)

    local timestamp = os.getenv('TT_CLI_PLAY_TIMESTAMP')
    if timestamp == nil then
        log.error('Internal error: failed to get play params from TT_CLI_PLAY_TIMESTAMP')
        os.exit(1)
    end
    keyword_arguments['timestamp'] = tonumber(timestamp)

    local replicas = os.getenv('TT_CLI_PLAY_REPLICAS')
    if replicas ~= nil then
        keyword_arguments['replica'] = {}
        for _, val in pairs(json.decode(replicas)) do
            table.insert(keyword_arguments['replica'], tonumber(val))
        end
    end

    local opts = {
        user = os.getenv('TT_CLI_PLAY_USERNAME'),
        password = os.getenv('TT_CLI_PLAY_PASSWORD'),
    }
    play(positional_arguments, keyword_arguments, opts)
end

main()
os.exit(0)
