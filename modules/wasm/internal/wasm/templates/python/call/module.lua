local fio = require('fio')
local wasm = require('wasm')

-- The component is a file of this package, next to this module.
local component = wasm.load(fio.pathjoin(debug.sourcedir(), '{{.Output}}'))

return {
    add = function(a, b)
        return wasm.call(component, 'add', a, b)
    end,
}
