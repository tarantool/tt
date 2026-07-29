local vshard = require('vshard')

-- Spread rows over both storage replicasets so every shard has data to back up.
function fill(count)
    for i = 1, count do
        local bucket_id = vshard.router.bucket_id_mpcrc32({i})
        vshard.router.callrw(bucket_id, 'put', {i, bucket_id, 'row-' .. i})
    end
end
