local vshard = require('vshard')

-- Create 'customers' space.
box.once('customers', function()
    box.schema.create_space('customers', {
        format = {{
            name = 'id',
            type = 'unsigned'
        }, {
            name = 'bucket_id',
            type = 'unsigned'
        }, {
            name = 'name',
            type = 'string'
        }, {
            name = 'age',
            type = 'number'
        }}
    })
    box.space.customers:create_index('primary_index', {
        parts = {{
            field = 1,
            type = 'unsigned'
        }}
    })
    box.space.customers:create_index('bucket_id', {
        parts = {{
            field = 2,
            type = 'unsigned'
        }},
        unique = false
    })
    box.space.customers:create_index('age', {
        parts = {{
            field = 4,
            type = 'number'
        }},
        unique = false
    })
end)

-- Put data to the 'customers' space.
-- Function should be called by the router.
function put(id, bucket_id, name, age)
    box.space.customers:insert({id, bucket_id, name, age})
end

-- Get data from the 'customers' space.
-- Function should be called by the router.
function get(id)
    local tuple = box.space.customers:get(id)
    if tuple == nil then
        return nil
    end
    return {tuple.id, tuple.name, tuple.age}
end
