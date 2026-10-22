-- The app of the per-instance restore tests: no vinyl space (a backup archive
-- stores files flat, so vinyl run/index files lose the <space_id>/<index_id>
-- directories Tarantool looks for them in), one replicaset, two instances.
--
-- Everything the app ever writes is inside box.once: the data a restore
-- replays was written once, at first boot, and a restored instance has to
-- come up on exactly those rows rather than re-seed its own and move the
-- vclock past the recovery point.
box.cfg()

box.once('restore_test_spaces', function()
    local memtx = box.schema.space.create('restore_test', {
        format = {
            {name = 'id',   type = 'unsigned'},
            {name = 'data', type = 'string'},
        }
    })
    memtx:create_index('pk', {parts = {1}})

    for i = 1, 10 do
        box.space.restore_test:insert({i, 'row-' .. i})
    end
end)
