local fiber = require('fiber')

-- The endless work runs in a fiber of its own: an app.file that never
-- returns keeps the config of the instance from being applied, and
-- tt instance start waits for that.
fiber.create(function()
    while true do
        fiber.sleep(5)
    end
end)
