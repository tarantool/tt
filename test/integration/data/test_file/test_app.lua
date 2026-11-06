local fiber = require('fiber')
local fio = require('fio')

box.cfg()

box.schema.user.create('test', { password = 'password' , if_not_exists = true })
box.schema.user.grant('test','read,write,execute,create,drop','universe')

box.schema.space.create('test', { id = 512 })
box.space.test:create_index('0')

fio.open('configured', 'O_CREAT'):close()

while true do
    fiber.sleep(5)
end
