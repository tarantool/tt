-- A memtx-only app whose checkpoints carry a sort data file: the space is
-- there to give the sort data something to describe, and vinyl would only add
-- another engine's files to a backup these tests read file by file.
box.cfg()

box.once('sortdata_test_spaces', function()
    local memtx = box.schema.space.create('sortdata_test', {
        format = {
            {name = 'id',   type = 'unsigned'},
            {name = 'data', type = 'string'},
        }
    })
    memtx:create_index('pk', {parts = {1}})
end)

-- Insert rows on RW (master) instances only.
if not box.info.ro then
    box.space.sortdata_test:truncate()
    for i = 1, 10 do
        box.space.sortdata_test:insert({i, 'row-' .. i})
    end
end
