// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.0
// 	protoc        v5.28.3
// source: aeon_diag.proto

package pb

import (
	protoreflect "google.golang.org/protobuf/reflect/protoreflect"
	protoimpl "google.golang.org/protobuf/runtime/protoimpl"
	reflect "reflect"
	sync "sync"
)

const (
	// Verify that this generated code is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(20 - protoimpl.MinVersion)
	// Verify that runtime/protoimpl is sufficiently up-to-date.
	_ = protoimpl.EnforceVersion(protoimpl.MaxVersion - 20)
)

type PingRequest struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PingRequest) Reset() {
	*x = PingRequest{}
	mi := &file_aeon_diag_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PingRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PingRequest) ProtoMessage() {}

func (x *PingRequest) ProtoReflect() protoreflect.Message {
	mi := &file_aeon_diag_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PingRequest.ProtoReflect.Descriptor instead.
func (*PingRequest) Descriptor() ([]byte, []int) {
	return file_aeon_diag_proto_rawDescGZIP(), []int{0}
}

type PingResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Error information. Set only on failure.
	Error         *Error `protobuf:"bytes,1,opt,name=error,proto3" json:"error,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *PingResponse) Reset() {
	*x = PingResponse{}
	mi := &file_aeon_diag_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *PingResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*PingResponse) ProtoMessage() {}

func (x *PingResponse) ProtoReflect() protoreflect.Message {
	mi := &file_aeon_diag_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use PingResponse.ProtoReflect.Descriptor instead.
func (*PingResponse) Descriptor() ([]byte, []int) {
	return file_aeon_diag_proto_rawDescGZIP(), []int{1}
}

func (x *PingResponse) GetError() *Error {
	if x != nil {
		return x.Error
	}
	return nil
}

var File_aeon_diag_proto protoreflect.FileDescriptor

var file_aeon_diag_proto_rawDesc = []byte{
	0x0a, 0x0f, 0x61, 0x65, 0x6f, 0x6e, 0x5f, 0x64, 0x69, 0x61, 0x67, 0x2e, 0x70, 0x72, 0x6f, 0x74,
	0x6f, 0x12, 0x04, 0x61, 0x65, 0x6f, 0x6e, 0x1a, 0x10, 0x61, 0x65, 0x6f, 0x6e, 0x5f, 0x65, 0x72,
	0x72, 0x6f, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x0d, 0x0a, 0x0b, 0x50, 0x69, 0x6e,
	0x67, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x22, 0x31, 0x0a, 0x0c, 0x50, 0x69, 0x6e, 0x67,
	0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x21, 0x0a, 0x05, 0x65, 0x72, 0x72, 0x6f,
	0x72, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x0b, 0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x45,
	0x72, 0x72, 0x6f, 0x72, 0x52, 0x05, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x32, 0x3e, 0x0a, 0x0b, 0x44,
	0x69, 0x61, 0x67, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12, 0x2f, 0x0a, 0x04, 0x50, 0x69,
	0x6e, 0x67, 0x12, 0x11, 0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x50, 0x69, 0x6e, 0x67, 0x52, 0x65,
	0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x12, 0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x50, 0x69, 0x6e,
	0x67, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x00, 0x62, 0x06, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x33,
}

var (
	file_aeon_diag_proto_rawDescOnce sync.Once
	file_aeon_diag_proto_rawDescData = file_aeon_diag_proto_rawDesc
)

func file_aeon_diag_proto_rawDescGZIP() []byte {
	file_aeon_diag_proto_rawDescOnce.Do(func() {
		file_aeon_diag_proto_rawDescData = protoimpl.X.CompressGZIP(file_aeon_diag_proto_rawDescData)
	})
	return file_aeon_diag_proto_rawDescData
}

var file_aeon_diag_proto_msgTypes = make([]protoimpl.MessageInfo, 2)
var file_aeon_diag_proto_goTypes = []any{
	(*PingRequest)(nil),  // 0: aeon.PingRequest
	(*PingResponse)(nil), // 1: aeon.PingResponse
	(*Error)(nil),        // 2: aeon.Error
}
var file_aeon_diag_proto_depIdxs = []int32{
	2, // 0: aeon.PingResponse.error:type_name -> aeon.Error
	0, // 1: aeon.DiagService.Ping:input_type -> aeon.PingRequest
	1, // 2: aeon.DiagService.Ping:output_type -> aeon.PingResponse
	2, // [2:3] is the sub-list for method output_type
	1, // [1:2] is the sub-list for method input_type
	1, // [1:1] is the sub-list for extension type_name
	1, // [1:1] is the sub-list for extension extendee
	0, // [0:1] is the sub-list for field type_name
}

func init() { file_aeon_diag_proto_init() }
func file_aeon_diag_proto_init() {
	if File_aeon_diag_proto != nil {
		return
	}
	file_aeon_error_proto_init()
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_aeon_diag_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   2,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_aeon_diag_proto_goTypes,
		DependencyIndexes: file_aeon_diag_proto_depIdxs,
		MessageInfos:      file_aeon_diag_proto_msgTypes,
	}.Build()
	File_aeon_diag_proto = out.File
	file_aeon_diag_proto_rawDesc = nil
	file_aeon_diag_proto_goTypes = nil
	file_aeon_diag_proto_depIdxs = nil
}