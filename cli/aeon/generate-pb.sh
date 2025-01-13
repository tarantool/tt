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
	cat << EOF
Not found proto Go generator, please install it in you system.
Try: go install google.golang.org/protobuf/cmd/protoc-gen-go@latest
EOF
fi

if [ ! -x "$(command -v protoc-gen-go-grpc)" ]; then
	cat << EOF
Not found gRPC Go generator, please install it in you system.
Try: go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest
EOF
fi

cd ${SCRIPTPATH}
rm -rf pb
protos/generate-go.sh --package ./pb --out .
