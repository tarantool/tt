local fiber = require('fiber')
local fio = require('fio')

fio.open(os.getenv('started_flag_file'), 'O_CREAT'):close()

while true do
    fiber.sleep(5)
end
