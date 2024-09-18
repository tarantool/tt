local log = require('log')

box.cfg{listen=3303}
box.schema.user.grant('guest', 'super')

box.session.on_connect(function()
        log.error("Connected")
end)
box.session.on_disconnect(function()
        log.error("Disconnected")
end)
