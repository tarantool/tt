local log = require('log')
local config = require('config')

log.info(('application \'%s\' is started'):format(config:get('app.file')))
