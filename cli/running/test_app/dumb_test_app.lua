local fiber = require('fiber')
local fio = require('fio')

fio.open(os.getenv('started_flag_file'), 'O_CREAT'):close()

box.cfg{}
