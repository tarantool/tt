local fio = require('fio')

local inst_name = box.info.name

-- tt runs the instance in the project directory.
fio.open(inst_name, 'O_CREAT'):close()
