box.cfg()

box.once('backup_ssl_test_space', function()
    local memtx = box.schema.space.create('backup_test', {
        format = {
            {name = 'id',   type = 'unsigned'},
            {name = 'data', type = 'string'},
        }
    })
    memtx:create_index('pk', {parts = {1}})
end)

-- Rows give the backup a WAL to archive.
if not box.info.ro then
    box.space.backup_test:truncate()
    for i = 1, 10 do
        box.space.backup_test:insert({i, 'row-' .. i})
    end
end
