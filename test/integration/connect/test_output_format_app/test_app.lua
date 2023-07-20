local fiber = require('fiber')
local fio = require('fio')

box.cfg({listen = 'localhost:3013'})

box.schema.user.create('test', { password = 'password' , if_not_exists = true })
box.schema.user.grant('guest','read,write,execute,create,drop','universe')
box.schema.user.grant('test','read,write,execute,create,drop','universe')

box.schema.create_space('customers', {
    format = {
        {name = 'id', type = 'unsigned'},
        {name = 'name', type = 'string'},
        {name = 'age', type = 'number'},
    }
})
box.space.customers:create_index('primary_index', {
    parts = {
        {field = 1, type = 'unsigned'},
    },
})

box.space.customers:insert({1, 'Elizabeth', 12})
box.space.customers:insert({2, 'Mary', 46})
box.space.customers:insert({3, 'David', 33})
box.space.customers:insert({4, 'William', 81})
box.space.customers:insert({5, 'Jack', 35})
box.space.customers:insert({6, 'William', 25})
box.space.customers:insert({7, 'Elizabeth', 18})

if box.execute ~= nil then
    box.execute([[CREATE TABLE table1 (column1 INT PRIMARY key, column2 VARCHAR(10));]])
    box.execute([[INSERT INTO table1 VALUES (10,'Hello SQL world!');]])
    box.execute([[INSERT INTO table1 VALUES (20,'Hello LUA world!');]])
    box.execute([[INSERT INTO table1 VALUES (30,'Hello YAML world!');]])
end

fh = fio.open('ready', {'O_WRONLY', 'O_CREAT'}, tonumber('644',8))
fh:close()

while true do
    fiber.sleep(5)
end
