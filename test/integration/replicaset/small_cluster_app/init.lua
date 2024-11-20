local fio = require('fio')

fh = fio.open('ready-' .. box.info.name, {'O_WRONLY', 'O_CREAT'}, tonumber('644',8))
fh:close()
