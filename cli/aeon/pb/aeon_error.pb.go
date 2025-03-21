// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.0
// 	protoc        v5.28.3
// source: aeon_error.proto

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

// Error information.
type Error struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Error type.
	// * AeonError for core Aeon errors.
	// * AeonSQLError for issues with SQL parsing.
	// * AeonGRPCError for issues with gRPC encoding.
	Type string `protobuf:"bytes,1,opt,name=type,proto3" json:"type,omitempty"`
	// Error name.
	Name string `protobuf:"bytes,2,opt,name=name,proto3" json:"name,omitempty"`
	// Error location: file, line.
	File string `protobuf:"bytes,3,opt,name=file,proto3" json:"file,omitempty"`
	Line uint64 `protobuf:"varint,4,opt,name=line,proto3" json:"line,omitempty"`
	// Human-readable error description.
	Msg string `protobuf:"bytes,5,opt,name=msg,proto3" json:"msg,omitempty"`
	// System errno (usually not set).
	Errno uint64 `protobuf:"varint,6,opt,name=errno,proto3" json:"errno,omitempty"`
	// Error code.
	Code uint64 `protobuf:"varint,7,opt,name=code,proto3" json:"code,omitempty"`
	// Fields with extra information.
	Fields *MapValue `protobuf:"bytes,8,opt,name=fields,proto3" json:"fields,omitempty"`
	// Previous error on the error stack (cause of this error).
	Prev          *Error `protobuf:"bytes,9,opt,name=prev,proto3" json:"prev,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Error) Reset() {
	*x = Error{}
	mi := &file_aeon_error_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Error) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Error) ProtoMessage() {}

func (x *Error) ProtoReflect() protoreflect.Message {
	mi := &file_aeon_error_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Error.ProtoReflect.Descriptor instead.
func (*Error) Descriptor() ([]byte, []int) {
	return file_aeon_error_proto_rawDescGZIP(), []int{0}
}

func (x *Error) GetType() string {
	if x != nil {
		return x.Type
	}
	return ""
}

func (x *Error) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *Error) GetFile() string {
	if x != nil {
		return x.File
	}
	return ""
}

func (x *Error) GetLine() uint64 {
	if x != nil {
		return x.Line
	}
	return 0
}

func (x *Error) GetMsg() string {
	if x != nil {
		return x.Msg
	}
	return ""
}

func (x *Error) GetErrno() uint64 {
	if x != nil {
		return x.Errno
	}
	return 0
}

func (x *Error) GetCode() uint64 {
	if x != nil {
		return x.Code
	}
	return 0
}

func (x *Error) GetFields() *MapValue {
	if x != nil {
		return x.Fields
	}
	return nil
}

func (x *Error) GetPrev() *Error {
	if x != nil {
		return x.Prev
	}
	return nil
}

var File_aeon_error_proto protoreflect.FileDescriptor

var file_aeon_error_proto_rawDesc = []byte{
	0x0a, 0x10, 0x61, 0x65, 0x6f, 0x6e, 0x5f, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x2e, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x12, 0x04, 0x61, 0x65, 0x6f, 0x6e, 0x1a, 0x10, 0x61, 0x65, 0x6f, 0x6e, 0x5f, 0x76,
	0x61, 0x6c, 0x75, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0xdc, 0x01, 0x0a, 0x05, 0x45,
	0x72, 0x72, 0x6f, 0x72, 0x12, 0x12, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x01, 0x20, 0x01,
	0x28, 0x09, 0x52, 0x04, 0x74, 0x79, 0x70, 0x65, 0x12, 0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65,
	0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x12, 0x12, 0x0a, 0x04,
	0x66, 0x69, 0x6c, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x66, 0x69, 0x6c, 0x65,
	0x12, 0x12, 0x0a, 0x04, 0x6c, 0x69, 0x6e, 0x65, 0x18, 0x04, 0x20, 0x01, 0x28, 0x04, 0x52, 0x04,
	0x6c, 0x69, 0x6e, 0x65, 0x12, 0x10, 0x0a, 0x03, 0x6d, 0x73, 0x67, 0x18, 0x05, 0x20, 0x01, 0x28,
	0x09, 0x52, 0x03, 0x6d, 0x73, 0x67, 0x12, 0x14, 0x0a, 0x05, 0x65, 0x72, 0x72, 0x6e, 0x6f, 0x18,
	0x06, 0x20, 0x01, 0x28, 0x04, 0x52, 0x05, 0x65, 0x72, 0x72, 0x6e, 0x6f, 0x12, 0x12, 0x0a, 0x04,
	0x63, 0x6f, 0x64, 0x65, 0x18, 0x07, 0x20, 0x01, 0x28, 0x04, 0x52, 0x04, 0x63, 0x6f, 0x64, 0x65,
	0x12, 0x26, 0x0a, 0x06, 0x66, 0x69, 0x65, 0x6c, 0x64, 0x73, 0x18, 0x08, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x0e, 0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x4d, 0x61, 0x70, 0x56, 0x61, 0x6c, 0x75, 0x65,
	0x52, 0x06, 0x66, 0x69, 0x65, 0x6c, 0x64, 0x73, 0x12, 0x1f, 0x0a, 0x04, 0x70, 0x72, 0x65, 0x76,
	0x18, 0x09, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x0b, 0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x45, 0x72,
	0x72, 0x6f, 0x72, 0x52, 0x04, 0x70, 0x72, 0x65, 0x76, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x33,
}

var (
	file_aeon_error_proto_rawDescOnce sync.Once
	file_aeon_error_proto_rawDescData = file_aeon_error_proto_rawDesc
)

func file_aeon_error_proto_rawDescGZIP() []byte {
	file_aeon_error_proto_rawDescOnce.Do(func() {
		file_aeon_error_proto_rawDescData = protoimpl.X.CompressGZIP(file_aeon_error_proto_rawDescData)
	})
	return file_aeon_error_proto_rawDescData
}

var file_aeon_error_proto_msgTypes = make([]protoimpl.MessageInfo, 1)
var file_aeon_error_proto_goTypes = []any{
	(*Error)(nil),    // 0: aeon.Error
	(*MapValue)(nil), // 1: aeon.MapValue
}
var file_aeon_error_proto_depIdxs = []int32{
	1, // 0: aeon.Error.fields:type_name -> aeon.MapValue
	0, // 1: aeon.Error.prev:type_name -> aeon.Error
	2, // [2:2] is the sub-list for method output_type
	2, // [2:2] is the sub-list for method input_type
	2, // [2:2] is the sub-list for extension type_name
	2, // [2:2] is the sub-list for extension extendee
	0, // [0:2] is the sub-list for field type_name
}

func init() { file_aeon_error_proto_init() }
func file_aeon_error_proto_init() {
	if File_aeon_error_proto != nil {
		return
	}
	file_aeon_value_proto_init()
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_aeon_error_proto_rawDesc,
			NumEnums:      0,
			NumMessages:   1,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_aeon_error_proto_goTypes,
		DependencyIndexes: file_aeon_error_proto_depIdxs,
		MessageInfos:      file_aeon_error_proto_msgTypes,
	}.Build()
	File_aeon_error_proto = out.File
	file_aeon_error_proto_rawDesc = nil
	file_aeon_error_proto_goTypes = nil
	file_aeon_error_proto_depIdxs = nil
}
