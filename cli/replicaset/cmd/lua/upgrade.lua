local ok, err
ok, err = pcall(box.schema.upgrade)
if ok then
   ok, err = pcall(box.snapshot)
end

return {
        lsn = box.info.lsn,
        iid = box.info.id,
        err = (not ok) and tostring(err) or nil,
}