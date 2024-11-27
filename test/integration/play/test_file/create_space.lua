box.schema.space.create('test', { id = 512 })

return box.space.test:create_index('0')
