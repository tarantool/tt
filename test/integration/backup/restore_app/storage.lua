-- The restore app deliberately has no vinyl space: a backup archive stores
-- files flat, so vinyl run/index files lose the <space_id>/<index_id>
-- directories Tarantool looks for them in and a restored instance cannot
-- start. Restoring memtx data is what these tests are about.
box.cfg()

box.once('restore_test_spaces', function()
    local memtx = box.schema.space.create('restore_test', {
        format = {
            {name = 'id',   type = 'unsigned'},
            {name = 'data', type = 'string'},
        }
    })
    memtx:create_index('pk', {parts = {1}})
end)

-- Insert rows on RW (master) instances only.
if not box.info.ro then
    box.space.restore_test:truncate()
    for i = 1, 10 do
        box.space.restore_test:insert({i, 'row-' .. i})
    end
end
