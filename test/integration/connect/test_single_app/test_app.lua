local fio = require('fio')

box.cfg()

box.schema.user.create('test', { password = 'password' , if_not_exists = true })
box.schema.user.grant('test','read,write,execute,create,drop','universe')

-- \set language works only after box.cfg({}). The file helps to ensure that
-- box.cfg({}) is already done.
fio.open('configured', 'O_CREAT'):close()

-- The file returns: one that never does keeps the config of the instance
-- from being applied, and tt instance start waits for that.
