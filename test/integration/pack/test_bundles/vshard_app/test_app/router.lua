local vshard = require('vshard')
local log = require('log')

-- Bootstrap the vshard router.
while true do
    local ok, err = vshard.router.bootstrap({
        if_not_bootstrapped = true,
    })
    if ok then
        break
    end
    log.info(('Router bootstrap error: %s'):format(err))
end

-- Put data into the cluster.
function put(id, name, age)
    local bucket_id = vshard.router.bucket_id_mpcrc32({id})
    vshard.router.callrw(bucket_id, 'put', {id, bucket_id, name, age})
end

-- Get data from the cluster.
function get(id)
    local bucket_id = vshard.router.bucket_id_mpcrc32({id})
    return vshard.router.callro(bucket_id, 'get', {id})
end

-- Put sample data.
function put_sample_data()
    put(1, 'Elizabeth', 12)
    put(2, 'Mary', 46)
    put(3, 'David', 33)
    put(4, 'William', 81)
    put(5, 'Jack', 35)
end
