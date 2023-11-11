local version = rawget(_G, "_TARANTOOL"):split('-', 1)[1]
local major_minor_patch = version:split('.', 2)
if tonumber(major_minor_patch[1]) < 3 then
    local ok, cartridge = pcall(require, 'cartridge')
    if ok then
        return "cartridge"
    end
    return "custom"
end

return "centralized config"
