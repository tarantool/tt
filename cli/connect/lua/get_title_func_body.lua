local ok, api_topology = pcall(require, 'cartridge.lua-api.topology')
if not ok then
    return ''
end

local self = api_topology.get_self()
if self.app_name == nil or self.instance_name == nil then
    return ''
end

return string.format('%s.%s', self.app_name, self.instance_name)
