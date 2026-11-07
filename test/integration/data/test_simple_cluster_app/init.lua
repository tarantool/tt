local fiber = require('fiber')
local fio = require('fio')

box.cfg()

box.schema.user.create('test', { password = 'password' , if_not_exists = true })
box.schema.user.grant('test','read,write,execute,create,drop','universe')

-- The file helps to ensure that box.cfg({}) is already done.
fio.open('configured', 'O_CREAT'):close()

while true do
    fiber.sleep(5)
end
