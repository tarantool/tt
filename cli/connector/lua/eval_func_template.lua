local func, err = load(string.fromhex('{{ .FunctionBody }}'), '=eval')
if func == nil then
    error(err, 0)
end
local args = require('msgpack').decode(string.fromhex('{{ .ArgsEncoded }}'))

local ret = {
    load(
        'local func, args = ... return func(unpack(args))',
        '@eval'
    )(func, args)
}
return {
    data_enc = require('digest').base64_encode(
        require('msgpack').encode(ret)
    )
}
