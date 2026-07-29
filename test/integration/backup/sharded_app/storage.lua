box.once('backup_test_spaces', function()
    local memtx = box.schema.space.create('backup_test', {
        format = {
            {name = 'id',        type = 'unsigned'},
            {name = 'bucket_id', type = 'unsigned'},
            {name = 'data',      type = 'string'},
        }
    })
    memtx:create_index('pk', {parts = {'id'}})
    memtx:create_index('bucket_id', {parts = {'bucket_id'}, unique = false})

    local vinyl = box.schema.space.create('backup_test_vinyl', {
        engine = 'vinyl',
        format = {
            {name = 'id',        type = 'unsigned'},
            {name = 'bucket_id', type = 'unsigned'},
            {name = 'data',      type = 'string'},
        }
    })
    vinyl:create_index('pk', {parts = {'id'}})
    vinyl:create_index('bucket_id', {parts = {'bucket_id'}, unique = false})
end)

-- Called by the router to place a row on the shard owning bucket_id.
function put(id, bucket_id, data)
    box.space.backup_test:replace({id, bucket_id, data})
    box.space.backup_test_vinyl:replace({id, bucket_id, 'vinyl-' .. data})
end
