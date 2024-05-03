local fiber = require('fiber')
local fio = require('fio')

box.cfg{}

-- Create something to generate xlogs.
box.schema.space.create('customers')

while true do
    fiber.sleep(5)
end
