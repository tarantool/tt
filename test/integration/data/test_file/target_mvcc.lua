-- An instance tt data play applies transactions to, with MVCC on: a
-- transaction that spans IPROTO requests can change its memtx space.
box.cfg{memtx_use_mvcc_engine = true}
box.schema.space.create('test', {id = 512}):create_index('pk')
box.schema.user.create('test_user', {password = 'secret'})
box.schema.user.grant('test_user', 'super')
require('utils').bind_free_port(arg[0])
