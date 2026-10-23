-- The app of the membership-change restore tests: the synchronous twin of
-- multi_apply_app's spaces (no vinyl: a backup archive stores files flat, so
-- vinyl run/index files lose the <space_id>/<index_id> directories Tarantool
-- looks for them in), one replicaset of up to three instances.
--
-- The quorum of 2 out of 3 lets any two members commit while the third is
-- down: the leader promotes and seeds with one follower at boot, and a test
-- that stops both followers stalls a synchronous write on purpose.
--
-- Everything the app ever writes is inside box.once: the data a restore
-- replays was written once, at first boot, and a restored instance has to
-- come up on exactly those rows rather than re-seed its own and move the
-- vclock past the recovery point.
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
        -- quorum's acknowledgement, hence the wait above; the quorum of 2
        -- out of 3 is satisfied by the leader and either follower.
        box.ctl.promote()
    end

    for i = 1, 10 do
        box.space.restore_test:insert({i, 'row-' .. i})
        box.space.restore_test_sync:insert({i, 'sync-row-' .. i})
    end
end)
