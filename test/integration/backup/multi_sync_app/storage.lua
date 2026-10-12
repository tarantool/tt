-- The app of the per-instance restore tests under synchronous replication:
-- the async space of multi_apply_app plus its synchronous twin, so a restore
-- is asserted on both kinds of write.
--
-- The synchronous rows need the other member's acknowledgement even at first
-- boot, and the leader comes up before the replica has joined, so the seeding
-- waits for a peer first. Everything stays inside box.once: a restored
-- instance comes up on the rows a restore replays and writes none of its own.
box.cfg()

local fiber = require('fiber')

local function peer_following()
    for _, peer in pairs(box.info.replication) do
        if peer.id ~= box.info.id then
            local upstream = (peer.upstream or {}).status
            local downstream = (peer.downstream or {}).status
            if upstream == 'follow' or downstream == 'follow' then
                return true
            end
        end
    end
    return false
end

box.once('restore_test_spaces', function()
    local function format()
        return {
            {name = 'id',   type = 'unsigned'},
            {name = 'data', type = 'string'},
        }
    end

    local memtx = box.schema.space.create('restore_test', {format = format()})
    memtx:create_index('pk', {parts = {1}})

    local sync = box.schema.space.create('restore_test_sync', {
        is_sync = true,
        format = format(),
    })
    sync:create_index('pk', {parts = {1}})

    if not box.info.ro then
        local deadline = fiber.clock() + 60
        while not peer_following() do
            if fiber.clock() > deadline then
                error('no replication peer joined within 60 seconds')
            end
            fiber.sleep(0.1)
        end
        -- A fresh replicaset has claimed no synchronous queue: no PROMOTE
        -- record exists yet, and the first synchronous write below is refused
        -- with an unclaimed queue until one is taken. Taking it needs the
        -- other member's acknowledgement, hence the wait above.
        box.ctl.promote()
    end

    for i = 1, 10 do
        box.space.restore_test:insert({i, 'row-' .. i})
        box.space.restore_test_sync:insert({i, 'sync-row-' .. i})
    end
end)
