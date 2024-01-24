-- It is a workaround for https://github.com/tarantool/tt/issues/640
-- Probably won't be needed after these patches will go into release:
-- https://github.com/yuin/gopher-lua/pull/456
-- https://github.com/yuin/gopher-lua/pull/458
-- Original macosx.lua is not needed for any luarocks operation.
local macosx = {}
return macosx
