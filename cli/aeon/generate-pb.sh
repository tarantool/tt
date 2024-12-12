#!/usr/bin/env bash
# Update and generate from `.proto` file

SCRIPTPATH=${0%/*}

if [ ! -x "$(command -v protoc)" ]; then
	cat << EOF
Not found proto compiler, please install it in you system.
Try: apt-get -y install protobuf-compiler
EOF
fi

if [ ! -x "$(command -v protoc-gen-go)" ]; then
	go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
fi

if [ ! -x "$(command -v protoc-gen-go-grpc)" ]; then
	go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
fi

cd ${SCRIPTPATH}
rm -rf pb
protoc --go_out=. --go-grpc_out=. --proto_path=protos aeon_router.proto
