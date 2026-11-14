-- An instance for the tests of the authentication methods of tt.
--
-- It listens on TEST_PLAIN_PORT and, with TEST_SSL_PORT set, on a TLS port
-- too, with the certificate of localhost beside this file. User "chap" gets
-- a chap-sha1 password. With TEST_PAP set the auth_type of the instance
-- becomes pap-sha256 after that, and user "pap" gets a pap-sha256 password:
-- "chap" is then a user whose password predates the auth_type. Both have
-- the password "secret". The space "tester" takes the rows of
-- test/integration/data/test_file/test.xlog. The file "ready" says that the
-- instance is configured.
local fio = require('fio')
local fiber = require('fiber')

local listen = {'127.0.0.1:' .. os.getenv('TEST_PLAIN_PORT')}
local ssl_port = os.getenv('TEST_SSL_PORT')
if ssl_port ~= nil and ssl_port ~= '' then
    table.insert(listen, {
        uri = '127.0.0.1:' .. ssl_port,
        params = {
            transport = 'ssl',
            ssl_key_file = 'localhost.key',
            ssl_cert_file = 'localhost.crt',
        },
    })
end

box.cfg({listen = listen})

box.schema.user.create('chap', {password = 'secret'})
box.schema.user.grant('chap', 'super')

if os.getenv('TEST_PAP') == '1' then
    box.cfg({auth_type = 'pap-sha256'})
    box.schema.user.create('pap', {password = 'secret'})
    box.schema.user.grant('pap', 'super')
end

local tester = box.schema.space.create('tester', {id = 999})
tester:format({
    {name = 'id', type = 'unsigned'},
    {name = 'band_name', type = 'string'},
    {name = 'year', type = 'unsigned'},
})
tester:create_index('primary', {type = 'tree', parts = {'id'}})

local fh = fio.open('ready', {'O_WRONLY', 'O_CREAT'}, tonumber('644', 8))
fh:close()

while true do
    fiber.sleep(5)
end
