local fio = require('fio')

fio.open(os.getenv('started_flag_file'), 'O_CREAT'):close()

-- The instance exits on its own, with the status the test sets.
os.exit(tonumber(os.getenv('exit_status')))
