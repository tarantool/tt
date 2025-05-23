fiber = require('fiber')

while box.info.ro do
  fiber.sleep(1)
end

box.cfg{ listen = 3301 }
