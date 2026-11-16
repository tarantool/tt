local fio = require('fio')

box.cfg({})

fh = fio.open('ready', {'O_WRONLY', 'O_CREAT'}, tonumber('644',8))
fh:close()

-- The file returns: one that never does keeps the config of the instance
-- from being applied, and tt instance start waits for that.
