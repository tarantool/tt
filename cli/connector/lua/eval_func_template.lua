local function func(...)
    {{ .FunctionBody }}
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
