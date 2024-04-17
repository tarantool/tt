local rw = false
local uuid = '00000000-0000-0000-0000-000000000000'

if type(box.cfg) ~= 'function' then
    local ok, is_rw = pcall(function()
        return box.cfg.read_only == false
    end)
    if ok then
        rw = is_rw
    end
    uuid = box.info().uuid
end


return {
    uuid = uuid,
    rw   = rw,
}
