// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.0
// 	protoc        v5.28.3
// source: aeon_sql.proto

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

type SQLCheckStatus int32

const (
	SQLCheckStatus_SQL_QUERY_VALID      SQLCheckStatus = 0
	SQLCheckStatus_SQL_QUERY_INCOMPLETE SQLCheckStatus = 1
	SQLCheckStatus_SQL_QUERY_INVALID    SQLCheckStatus = 2
)

// Enum value maps for SQLCheckStatus.
var (
	SQLCheckStatus_name = map[int32]string{
		0: "SQL_QUERY_VALID",
		1: "SQL_QUERY_INCOMPLETE",
		2: "SQL_QUERY_INVALID",
	}
	SQLCheckStatus_value = map[string]int32{
		"SQL_QUERY_VALID":      0,
		"SQL_QUERY_INCOMPLETE": 1,
		"SQL_QUERY_INVALID":    2,
	}
)

func (x SQLCheckStatus) Enum() *SQLCheckStatus {
	p := new(SQLCheckStatus)
	*p = x
	return p
}

func (x SQLCheckStatus) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (SQLCheckStatus) Descriptor() protoreflect.EnumDescriptor {
	return file_aeon_sql_proto_enumTypes[0].Descriptor()
}

func (SQLCheckStatus) Type() protoreflect.EnumType {
	return &file_aeon_sql_proto_enumTypes[0]
}

func (x SQLCheckStatus) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use SQLCheckStatus.Descriptor instead.
func (SQLCheckStatus) EnumDescriptor() ([]byte, []int) {
	return file_aeon_sql_proto_rawDescGZIP(), []int{0}
}

type SQLRequest struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// SQL query.
	Query string `protobuf:"bytes,1,opt,name=query,proto3" json:"query,omitempty"`
	// Bind variables.
	Vars          map[string]*Value `protobuf:"bytes,2,rep,name=vars,proto3" json:"vars,omitempty" protobuf_key:"bytes,1,opt,name=key" protobuf_val:"bytes,2,opt,name=value"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *SQLRequest) Reset() {
	*x = SQLRequest{}
	mi := &file_aeon_sql_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SQLRequest) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SQLRequest) ProtoMessage() {}

func (x *SQLRequest) ProtoReflect() protoreflect.Message {
	mi := &file_aeon_sql_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SQLRequest.ProtoReflect.Descriptor instead.
func (*SQLRequest) Descriptor() ([]byte, []int) {
	return file_aeon_sql_proto_rawDescGZIP(), []int{0}
}

func (x *SQLRequest) GetQuery() string {
	if x != nil {
		return x.Query
	}
	return ""
}

func (x *SQLRequest) GetVars() map[string]*Value {
	if x != nil {
		return x.Vars
	}
	return nil
}

type SQLResponse struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Error information. Set only on failure.
	Error *Error `protobuf:"bytes,1,opt,name=error,proto3" json:"error,omitempty"`
	// Retrieved tuples.
	Tuples []*Tuple `protobuf:"bytes,2,rep,name=tuples,proto3" json:"tuples,omitempty"`
	// Format of the returned tuples.
	TupleFormat   *TupleFormat `protobuf:"bytes,3,opt,name=tuple_format,json=tupleFormat,proto3" json:"tuple_format,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *SQLResponse) Reset() {
	*x = SQLResponse{}
	mi := &file_aeon_sql_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SQLResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SQLResponse) ProtoMessage() {}

func (x *SQLResponse) ProtoReflect() protoreflect.Message {
	mi := &file_aeon_sql_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SQLResponse.ProtoReflect.Descriptor instead.
func (*SQLResponse) Descriptor() ([]byte, []int) {
	return file_aeon_sql_proto_rawDescGZIP(), []int{1}
}

func (x *SQLResponse) GetError() *Error {
	if x != nil {
		return x.Error
	}
	return nil
}

func (x *SQLResponse) GetTuples() []*Tuple {
	if x != nil {
		return x.Tuples
	}
	return nil
}

func (x *SQLResponse) GetTupleFormat() *TupleFormat {
	if x != nil {
		return x.TupleFormat
	}
	return nil
}

type SQLCheckResponse struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Status        SQLCheckStatus         `protobuf:"varint,1,opt,name=status,proto3,enum=aeon.SQLCheckStatus" json:"status,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *SQLCheckResponse) Reset() {
	*x = SQLCheckResponse{}
	mi := &file_aeon_sql_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *SQLCheckResponse) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*SQLCheckResponse) ProtoMessage() {}

func (x *SQLCheckResponse) ProtoReflect() protoreflect.Message {
	mi := &file_aeon_sql_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use SQLCheckResponse.ProtoReflect.Descriptor instead.
func (*SQLCheckResponse) Descriptor() ([]byte, []int) {
	return file_aeon_sql_proto_rawDescGZIP(), []int{2}
}

func (x *SQLCheckResponse) GetStatus() SQLCheckStatus {
	if x != nil {
		return x.Status
	}
	return SQLCheckStatus_SQL_QUERY_VALID
}

var File_aeon_sql_proto protoreflect.FileDescriptor

var file_aeon_sql_proto_rawDesc = []byte{
	0x0a, 0x0e, 0x61, 0x65, 0x6f, 0x6e, 0x5f, 0x73, 0x71, 0x6c, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f,
	0x12, 0x04, 0x61, 0x65, 0x6f, 0x6e, 0x1a, 0x10, 0x61, 0x65, 0x6f, 0x6e, 0x5f, 0x65, 0x72, 0x72,
	0x6f, 0x72, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x10, 0x61, 0x65, 0x6f, 0x6e, 0x5f, 0x76,
	0x61, 0x6c, 0x75, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x1a, 0x11, 0x61, 0x65, 0x6f, 0x6e,
	0x5f, 0x73, 0x63, 0x68, 0x65, 0x6d, 0x61, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x98, 0x01,
	0x0a, 0x0a, 0x53, 0x51, 0x4c, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x12, 0x14, 0x0a, 0x05,
	0x71, 0x75, 0x65, 0x72, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x71, 0x75, 0x65,
	0x72, 0x79, 0x12, 0x2e, 0x0a, 0x04, 0x76, 0x61, 0x72, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28, 0x0b,
	0x32, 0x1a, 0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x53, 0x51, 0x4c, 0x52, 0x65, 0x71, 0x75, 0x65,
	0x73, 0x74, 0x2e, 0x56, 0x61, 0x72, 0x73, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x52, 0x04, 0x76, 0x61,
	0x72, 0x73, 0x1a, 0x44, 0x0a, 0x09, 0x56, 0x61, 0x72, 0x73, 0x45, 0x6e, 0x74, 0x72, 0x79, 0x12,
	0x10, 0x0a, 0x03, 0x6b, 0x65, 0x79, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x03, 0x6b, 0x65,
	0x79, 0x12, 0x21, 0x0a, 0x05, 0x76, 0x61, 0x6c, 0x75, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b,
	0x32, 0x0b, 0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x56, 0x61, 0x6c, 0x75, 0x65, 0x52, 0x05, 0x76,
	0x61, 0x6c, 0x75, 0x65, 0x3a, 0x02, 0x38, 0x01, 0x22, 0x8b, 0x01, 0x0a, 0x0b, 0x53, 0x51, 0x4c,
	0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x21, 0x0a, 0x05, 0x65, 0x72, 0x72, 0x6f,
	0x72, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x0b, 0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x45,
	0x72, 0x72, 0x6f, 0x72, 0x52, 0x05, 0x65, 0x72, 0x72, 0x6f, 0x72, 0x12, 0x23, 0x0a, 0x06, 0x74,
	0x75, 0x70, 0x6c, 0x65, 0x73, 0x18, 0x02, 0x20, 0x03, 0x28, 0x0b, 0x32, 0x0b, 0x2e, 0x61, 0x65,
	0x6f, 0x6e, 0x2e, 0x54, 0x75, 0x70, 0x6c, 0x65, 0x52, 0x06, 0x74, 0x75, 0x70, 0x6c, 0x65, 0x73,
	0x12, 0x34, 0x0a, 0x0c, 0x74, 0x75, 0x70, 0x6c, 0x65, 0x5f, 0x66, 0x6f, 0x72, 0x6d, 0x61, 0x74,
	0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x11, 0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x54, 0x75,
	0x70, 0x6c, 0x65, 0x46, 0x6f, 0x72, 0x6d, 0x61, 0x74, 0x52, 0x0b, 0x74, 0x75, 0x70, 0x6c, 0x65,
	0x46, 0x6f, 0x72, 0x6d, 0x61, 0x74, 0x22, 0x40, 0x0a, 0x10, 0x53, 0x51, 0x4c, 0x43, 0x68, 0x65,
	0x63, 0x6b, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x12, 0x2c, 0x0a, 0x06, 0x73, 0x74,
	0x61, 0x74, 0x75, 0x73, 0x18, 0x01, 0x20, 0x01, 0x28, 0x0e, 0x32, 0x14, 0x2e, 0x61, 0x65, 0x6f,
	0x6e, 0x2e, 0x53, 0x51, 0x4c, 0x43, 0x68, 0x65, 0x63, 0x6b, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73,
	0x52, 0x06, 0x73, 0x74, 0x61, 0x74, 0x75, 0x73, 0x2a, 0x56, 0x0a, 0x0e, 0x53, 0x51, 0x4c, 0x43,
	0x68, 0x65, 0x63, 0x6b, 0x53, 0x74, 0x61, 0x74, 0x75, 0x73, 0x12, 0x13, 0x0a, 0x0f, 0x53, 0x51,
	0x4c, 0x5f, 0x51, 0x55, 0x45, 0x52, 0x59, 0x5f, 0x56, 0x41, 0x4c, 0x49, 0x44, 0x10, 0x00, 0x12,
	0x18, 0x0a, 0x14, 0x53, 0x51, 0x4c, 0x5f, 0x51, 0x55, 0x45, 0x52, 0x59, 0x5f, 0x49, 0x4e, 0x43,
	0x4f, 0x4d, 0x50, 0x4c, 0x45, 0x54, 0x45, 0x10, 0x01, 0x12, 0x15, 0x0a, 0x11, 0x53, 0x51, 0x4c,
	0x5f, 0x51, 0x55, 0x45, 0x52, 0x59, 0x5f, 0x49, 0x4e, 0x56, 0x41, 0x4c, 0x49, 0x44, 0x10, 0x02,
	0x32, 0xa8, 0x01, 0x0a, 0x0a, 0x53, 0x51, 0x4c, 0x53, 0x65, 0x72, 0x76, 0x69, 0x63, 0x65, 0x12,
	0x2c, 0x0a, 0x03, 0x53, 0x51, 0x4c, 0x12, 0x10, 0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x53, 0x51,
	0x4c, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x11, 0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e,
	0x53, 0x51, 0x4c, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x00, 0x12, 0x34, 0x0a,
	0x09, 0x53, 0x51, 0x4c, 0x53, 0x74, 0x72, 0x65, 0x61, 0x6d, 0x12, 0x10, 0x2e, 0x61, 0x65, 0x6f,
	0x6e, 0x2e, 0x53, 0x51, 0x4c, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73, 0x74, 0x1a, 0x11, 0x2e, 0x61,
	0x65, 0x6f, 0x6e, 0x2e, 0x53, 0x51, 0x4c, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22,
	0x00, 0x30, 0x01, 0x12, 0x36, 0x0a, 0x08, 0x53, 0x51, 0x4c, 0x43, 0x68, 0x65, 0x63, 0x6b, 0x12,
	0x10, 0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x53, 0x51, 0x4c, 0x52, 0x65, 0x71, 0x75, 0x65, 0x73,
	0x74, 0x1a, 0x16, 0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x53, 0x51, 0x4c, 0x43, 0x68, 0x65, 0x63,
	0x6b, 0x52, 0x65, 0x73, 0x70, 0x6f, 0x6e, 0x73, 0x65, 0x22, 0x00, 0x62, 0x06, 0x70, 0x72, 0x6f,
	0x74, 0x6f, 0x33,
}

var (
	file_aeon_sql_proto_rawDescOnce sync.Once
	file_aeon_sql_proto_rawDescData = file_aeon_sql_proto_rawDesc
)

func file_aeon_sql_proto_rawDescGZIP() []byte {
	file_aeon_sql_proto_rawDescOnce.Do(func() {
		file_aeon_sql_proto_rawDescData = protoimpl.X.CompressGZIP(file_aeon_sql_proto_rawDescData)
	})
	return file_aeon_sql_proto_rawDescData
}

var file_aeon_sql_proto_enumTypes = make([]protoimpl.EnumInfo, 1)
var file_aeon_sql_proto_msgTypes = make([]protoimpl.MessageInfo, 4)
var file_aeon_sql_proto_goTypes = []any{
	(SQLCheckStatus)(0),      // 0: aeon.SQLCheckStatus
	(*SQLRequest)(nil),       // 1: aeon.SQLRequest
	(*SQLResponse)(nil),      // 2: aeon.SQLResponse
	(*SQLCheckResponse)(nil), // 3: aeon.SQLCheckResponse
	nil,                      // 4: aeon.SQLRequest.VarsEntry
	(*Error)(nil),            // 5: aeon.Error
	(*Tuple)(nil),            // 6: aeon.Tuple
	(*TupleFormat)(nil),      // 7: aeon.TupleFormat
	(*Value)(nil),            // 8: aeon.Value
}
var file_aeon_sql_proto_depIdxs = []int32{
	4, // 0: aeon.SQLRequest.vars:type_name -> aeon.SQLRequest.VarsEntry
	5, // 1: aeon.SQLResponse.error:type_name -> aeon.Error
	6, // 2: aeon.SQLResponse.tuples:type_name -> aeon.Tuple
	7, // 3: aeon.SQLResponse.tuple_format:type_name -> aeon.TupleFormat
	0, // 4: aeon.SQLCheckResponse.status:type_name -> aeon.SQLCheckStatus
	8, // 5: aeon.SQLRequest.VarsEntry.value:type_name -> aeon.Value
	1, // 6: aeon.SQLService.SQL:input_type -> aeon.SQLRequest
	1, // 7: aeon.SQLService.SQLStream:input_type -> aeon.SQLRequest
	1, // 8: aeon.SQLService.SQLCheck:input_type -> aeon.SQLRequest
	2, // 9: aeon.SQLService.SQL:output_type -> aeon.SQLResponse
	2, // 10: aeon.SQLService.SQLStream:output_type -> aeon.SQLResponse
	3, // 11: aeon.SQLService.SQLCheck:output_type -> aeon.SQLCheckResponse
	9, // [9:12] is the sub-list for method output_type
	6, // [6:9] is the sub-list for method input_type
	6, // [6:6] is the sub-list for extension type_name
	6, // [6:6] is the sub-list for extension extendee
	0, // [0:6] is the sub-list for field type_name
}

func init() { file_aeon_sql_proto_init() }
func file_aeon_sql_proto_init() {
	if File_aeon_sql_proto != nil {
		return
	}
	file_aeon_error_proto_init()
	file_aeon_value_proto_init()
	file_aeon_schema_proto_init()
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_aeon_sql_proto_rawDesc,
			NumEnums:      1,
			NumMessages:   4,
			NumExtensions: 0,
			NumServices:   1,
		},
		GoTypes:           file_aeon_sql_proto_goTypes,
		DependencyIndexes: file_aeon_sql_proto_depIdxs,
		EnumInfos:         file_aeon_sql_proto_enumTypes,
		MessageInfos:      file_aeon_sql_proto_msgTypes,
	}.Build()
	File_aeon_sql_proto = out.File
	file_aeon_sql_proto_rawDesc = nil
	file_aeon_sql_proto_goTypes = nil
	file_aeon_sql_proto_depIdxs = nil
}
