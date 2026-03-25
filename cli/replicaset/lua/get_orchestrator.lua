local version = rawget(_G, "_TARANTOOL"):split('-', 1)[1]
local major_minor_patch = version:split('.', 2)
if tonumber(major_minor_patch[1]) < 3 then
    return "custom"
end

return "centralized config"
