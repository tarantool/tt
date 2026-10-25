local fio = require('fio')

local inst_name = box.info.name

box.schema.user.create('test', { password = 'password' , if_not_exists = true })
box.schema.user.grant('test','read,write,execute,create,drop','universe',
    nil, {if_not_exists = true})

-- tt runs the instance in the project directory.
fio.open(inst_name, 'O_CREAT'):close()
