-- gohello: the Lua side of a package with a program in Go, bin/gohello of
-- the tree, which an application runs as an external process.
local fio = require('fio')

local M = {}

-- program returns the path of bin/gohello of the tree this module is in:
-- <tree>/share/tarantool/gohello/init.lua -> <tree>/bin/gohello.
function M.program()
    local dir = debug.sourcedir()
    return fio.abspath(fio.pathjoin(dir, '..', '..', '..', 'bin', 'gohello'))
end

-- greet runs the program and returns what it printed.
function M.greet(name)
    local pipe = io.popen(("'%s' '%s'"):format(M.program(), name))
    local out = pipe:read('*a')
    pipe:close()
    return out
end

return M
