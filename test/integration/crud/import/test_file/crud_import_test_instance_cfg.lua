#!/usr/bin/env tarantool

 -- This config created on the basis of CRUD playground file.
 -- See crud/doc/playground.lua at CRUD repo.

 local fio = require('fio')
 local console = require('console')
 local vshard = require('vshard')
 local crud = require('crud')

 -- Trick to don't leave *.snap, *.xlog files.
 if os.getenv('KEEP_DATA') ~= nil then
     box.cfg()
 else
     local tempdir = fio.tempdir()
     box.cfg({
         memtx_dir = tempdir,
         wal_mode = 'none',
     })
     fio.rmtree(tempdir)
 end

 -- Setup vshard.
 _G.vshard = vshard
 box.once('guest', function()
     box.schema.user.grant('guest', 'super')
 end)
 local uri = 'guest@localhost:3301'
 local cfg = {
     bucket_count = 3000,
     sharding = {
         [box.info().cluster.uuid] = {
             replicas = {
                 [box.info().uuid] = {
                     uri = uri,
                     name = 'storage',
                     master = true,
                 },
             },
         },
     },
 }
 vshard.storage.cfg(cfg, box.info().uuid)
 vshard.router.cfg(cfg)
 vshard.router.bootstrap()

 -- Create the developers space for various tests.
 box.once('developers', function()
     box.schema.create_space('developers', {
         format = {
             {name = 'id', type = 'unsigned'},
             {name = 'bucket_id', type = 'unsigned'},
             {name = 'name', type = 'string'},
             {name = 'surname', type = 'string', is_nullable = true},
             {name = 'age', type = 'number', is_nullable = false},
         }
     })
     box.space.developers:create_index('primary_index', {
         parts = {
             {field = 1, type = 'unsigned'},
         },
     })
     box.space.developers:create_index('bucket_id', {
         parts = {
             {field = 2, type = 'unsigned'},
         },
         unique = false,
     })
     box.space.developers:create_index('age_index', {
         parts = {
             {field = 5, type = 'number'},
         },
         unique = false,
     })
     box.space.developers:create_index('full_name', {
         parts = {
             {field = 3, type = 'string'},
             {field = 4, type = 'string'},
         },
         unique = false,
     })
 end)

 -- Create the typetest space for type testing.
 box.once('typetest', function()
     box.schema.create_space('typetest', {
         format = {
             {name = 'id', type = 'unsigned'},
             {name = 'bucket_id', type = 'unsigned'},
             {name = 'string', type = 'string', is_nullable = true},
             {name = 'number', type = 'number', is_nullable = true},
             {name = 'integer', type = 'integer', is_nullable = true},
             {name = 'unsigned', type = 'unsigned', is_nullable = true},
             {name = 'double', type = 'double', is_nullable = true},
             {name = 'decimal', type = 'decimal', is_nullable = true},
             {name = 'boolean', type = 'boolean', is_nullable = true},
         }
     })
     box.space.typetest:create_index('primary_index', {
         parts = {
             {field = 1, type = 'unsigned'},
         },
     })
     box.space.typetest:create_index('bucket_id', {
         parts = {
             {field = 2, type = 'unsigned'},
         },
         unique = false,
     })
 end)

 -- Create the matchtest space for match testing.
 box.once('matchtest', function()
     box.schema.create_space('matchtest', {
         format = {
             {name = 'id', type = 'unsigned'},
             {name = 'bucket_id', type = 'unsigned'},
             {name = 'first_name', type = 'string', is_nullable = true},
             {name = 'second_name', type = 'string', is_nullable = true},
             {name = 'age', type = 'number', is_nullable = false},
             {name = 'data', type = 'string', is_nullable = true},
             {name = 'engaged', type = 'boolean', is_nullable = true},
         }
     })
     box.space.matchtest:create_index('primary_index', {
         parts = {
             {field = 1, type = 'unsigned'},
         },
     })
     box.space.matchtest:create_index('bucket_id', {
         parts = {
             {field = 2, type = 'unsigned'},
         },
         unique = false,
     })
 end)

 -- Initialize crud.
 crud.init_storage()
 crud.init_router()

 -- Start a console.
 console.start()
