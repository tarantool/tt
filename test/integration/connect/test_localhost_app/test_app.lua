local fiber = require('fiber')
local fio = require('fio')

box.cfg({listen = 'localhost:3013'})

box.schema.user.create('test', { password = 'password' , if_not_exists = true })
box.schema.user.grant('guest','read,write,execute,create,drop','universe')
box.schema.user.grant('test','read,write,execute,create,drop','universe')

fh = fio.open('ready', {'O_WRONLY', 'O_CREAT'}, tonumber('644',8))
fh:close()

while true do
    fiber.sleep(5)
end
