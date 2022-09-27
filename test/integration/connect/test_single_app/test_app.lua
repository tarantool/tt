local fiber = require('fiber')

box.cfg({})

box.schema.user.create('test', { password = 'password' , if_not_exists = true })
box.schema.user.grant('test','read,write,execute,create,drop','universe')

while true do
    fiber.sleep(5)
end
