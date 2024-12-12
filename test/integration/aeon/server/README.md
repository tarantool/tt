# Mock `aeon` server

## Update and generate from `.proto` file

1. cd `test/integration/aeon/server`;
2. Get a new [aeon_router.proto](https://github.com/tarantool/aeon/blob/master/proto/aeon_router.proto);
3. Run: `protoc --go_out=. --go-grpc_out=. aeon_router.proto`;
4. Directory `pb` will be created with files:
```
pb
├── aeon_router_grpc.pb.go
└── aeon_router.pb.go
```
5. Commit the new changes in the created files;
6. Update the `service/server.go` file to comply with the new `*.pb.go` requirements.
