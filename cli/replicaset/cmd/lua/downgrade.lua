local version = ...
local allowed_versions = box.schema.downgrade_versions()

local function is_version_allowed(version, allowed_versions)
    for _, allowed_version in ipairs(allowed_versions) do
        if allowed_version == version then
            return true
        end
    end
    return false
end

local function format_allowed_versions(versions)
    return "[" .. table.concat(versions, ", ") .. "]"
end

local function downgrade_schema(version)
    if not is_version_allowed(version, allowed_versions) then
        local err = ("Version '%s' is not allowed.\nAllowed versions: %s"):format(
            version, format_allowed_versions(allowed_versions)
        )
        return {
            lsn = box.info.lsn,
            iid = box.info.id,
            err = err,
        }
    end

    local ok, err = pcall(function()
        box.schema.downgrade(version)
        box.snapshot()
    end)

    return {
        lsn = box.info.lsn,
        iid = box.info.id,
        err = not ok and tostring(err) or nil,
    }
end

return downgrade_schema(version)
