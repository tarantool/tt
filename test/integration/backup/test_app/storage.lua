box.cfg()

box.once('backup_test_spaces', function()
    local memtx = box.schema.space.create('backup_test', {
        format = {
            {name = 'id',   type = 'unsigned'},
            {name = 'data', type = 'string'},
        }
    })
    memtx:create_index('pk', {parts = {1}})

    local vinyl = box.schema.space.create('backup_test_vinyl', {
        engine = 'vinyl',
        format = {
            {name = 'id',   type = 'unsigned'},
            {name = 'data', type = 'string'},
        }
    })
    vinyl:create_index('pk', {parts = {1}})
end)

-- Insert rows on RW (master) instances only.
if not box.info.ro then
    box.space.backup_test:truncate()
    box.space.backup_test_vinyl:truncate()
    for i = 1, 10 do
        box.space.backup_test:insert({i, 'row-' .. i})
        box.space.backup_test_vinyl:insert({i, 'vinyl-row-' .. i})
    end
end
