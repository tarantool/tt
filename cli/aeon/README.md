# Update Aeon proto submodule
In case of updating the communication protocol with the Aeon server, the following manual actions are required:
1. Update `proto` files and re-generate new `.go` sources.
2. Make appropriate corrections to the `tt aeon connect` application code.

## Requirements
Ensure that the required utilities are installed on the developer's system.
Installation of `protoc` compiler may depend on your OS distribution.
```sh
apt-get -y install protobuf-compiler
go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
```

## Update submodule from repository
Get fresh files from the `master` branch.
```sh
cd tt/cli/aeon/protoc
git co master
git pull
cd ..
git add protoc
```

## Regenerate `pb` modules
After that, you need to regenerate the files and add them to the `git` repository.
```sh
tt/cli/aeon/generate-pb.sh
git add tt/cli/aeon/pb
```
