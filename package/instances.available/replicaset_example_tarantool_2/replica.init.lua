box.cfg{
    listen = 3302,
    replication = {'replicator:password@127.0.0.1:3301',
                    'replicator:password@127.0.0.1:3302'},
    read_only = true
}

box.once("schema", function()
    box.schema.user.create('replicator', {password = 'password'})
    box.schema.user.grant('replicator', 'replication')
    box.schema.space.create("example")
    box.space.example:create_index("primary")
    print('box.once executed on replica')
end)
