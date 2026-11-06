-- An instance tt data play applies transactions to, with MVCC off, as by
-- default: a transaction that spans IPROTO requests cannot change its memtx
-- space.
box.cfg{}
box.schema.space.create('test', {id = 512}):create_index('pk')
box.schema.user.create('test_user', {password = 'secret'})
box.schema.user.grant('test_user', 'super')
require('utils').bind_free_port(arg[0])
