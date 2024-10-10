local fiber = require('fiber')
local log = require('log')

box.cfg{}

-- Create something to generate xlogs.
box.schema.space.create('test-' .. box.cfg.instance_name)

while true do
    log.info(pid_path)
    fiber.sleep(1)
end
