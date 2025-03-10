// Code generated by protoc-gen-go. DO NOT EDIT.
// versions:
// 	protoc-gen-go v1.36.0
// 	protoc        v5.28.3
// source: aeon_schema.proto

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

// Type a space field can have.
type FieldType int32

const (
	FieldType_FIELD_TYPE_UNSPECIFIED FieldType = 0
	FieldType_FIELD_TYPE_ANY         FieldType = 1
	FieldType_FIELD_TYPE_UNSIGNED    FieldType = 2
	FieldType_FIELD_TYPE_STRING      FieldType = 3
	FieldType_FIELD_TYPE_NUMBER      FieldType = 4
	FieldType_FIELD_TYPE_DOUBLE      FieldType = 5
	FieldType_FIELD_TYPE_INTEGER     FieldType = 6
	FieldType_FIELD_TYPE_BOOLEAN     FieldType = 7
	FieldType_FIELD_TYPE_VARBINARY   FieldType = 8
	FieldType_FIELD_TYPE_SCALAR      FieldType = 9
	FieldType_FIELD_TYPE_DECIMAL     FieldType = 10
	FieldType_FIELD_TYPE_UUID        FieldType = 11
	FieldType_FIELD_TYPE_DATETIME    FieldType = 12
	FieldType_FIELD_TYPE_INTERVAL    FieldType = 13
	FieldType_FIELD_TYPE_ARRAY       FieldType = 14
	FieldType_FIELD_TYPE_MAP         FieldType = 15
)

// Enum value maps for FieldType.
var (
	FieldType_name = map[int32]string{
		0:  "FIELD_TYPE_UNSPECIFIED",
		1:  "FIELD_TYPE_ANY",
		2:  "FIELD_TYPE_UNSIGNED",
		3:  "FIELD_TYPE_STRING",
		4:  "FIELD_TYPE_NUMBER",
		5:  "FIELD_TYPE_DOUBLE",
		6:  "FIELD_TYPE_INTEGER",
		7:  "FIELD_TYPE_BOOLEAN",
		8:  "FIELD_TYPE_VARBINARY",
		9:  "FIELD_TYPE_SCALAR",
		10: "FIELD_TYPE_DECIMAL",
		11: "FIELD_TYPE_UUID",
		12: "FIELD_TYPE_DATETIME",
		13: "FIELD_TYPE_INTERVAL",
		14: "FIELD_TYPE_ARRAY",
		15: "FIELD_TYPE_MAP",
	}
	FieldType_value = map[string]int32{
		"FIELD_TYPE_UNSPECIFIED": 0,
		"FIELD_TYPE_ANY":         1,
		"FIELD_TYPE_UNSIGNED":    2,
		"FIELD_TYPE_STRING":      3,
		"FIELD_TYPE_NUMBER":      4,
		"FIELD_TYPE_DOUBLE":      5,
		"FIELD_TYPE_INTEGER":     6,
		"FIELD_TYPE_BOOLEAN":     7,
		"FIELD_TYPE_VARBINARY":   8,
		"FIELD_TYPE_SCALAR":      9,
		"FIELD_TYPE_DECIMAL":     10,
		"FIELD_TYPE_UUID":        11,
		"FIELD_TYPE_DATETIME":    12,
		"FIELD_TYPE_INTERVAL":    13,
		"FIELD_TYPE_ARRAY":       14,
		"FIELD_TYPE_MAP":         15,
	}
)

func (x FieldType) Enum() *FieldType {
	p := new(FieldType)
	*p = x
	return p
}

func (x FieldType) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (FieldType) Descriptor() protoreflect.EnumDescriptor {
	return file_aeon_schema_proto_enumTypes[0].Descriptor()
}

func (FieldType) Type() protoreflect.EnumType {
	return &file_aeon_schema_proto_enumTypes[0]
}

func (x FieldType) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use FieldType.Descriptor instead.
func (FieldType) EnumDescriptor() ([]byte, []int) {
	return file_aeon_schema_proto_rawDescGZIP(), []int{0}
}

type KeyPartDef_KeyPartSortOrder int32

const (
	KeyPartDef_KEY_PART_SORT_ORDER_ASC  KeyPartDef_KeyPartSortOrder = 0
	KeyPartDef_KEY_PART_SORT_ORDER_DESC KeyPartDef_KeyPartSortOrder = 1
)

// Enum value maps for KeyPartDef_KeyPartSortOrder.
var (
	KeyPartDef_KeyPartSortOrder_name = map[int32]string{
		0: "KEY_PART_SORT_ORDER_ASC",
		1: "KEY_PART_SORT_ORDER_DESC",
	}
	KeyPartDef_KeyPartSortOrder_value = map[string]int32{
		"KEY_PART_SORT_ORDER_ASC":  0,
		"KEY_PART_SORT_ORDER_DESC": 1,
	}
)

func (x KeyPartDef_KeyPartSortOrder) Enum() *KeyPartDef_KeyPartSortOrder {
	p := new(KeyPartDef_KeyPartSortOrder)
	*p = x
	return p
}

func (x KeyPartDef_KeyPartSortOrder) String() string {
	return protoimpl.X.EnumStringOf(x.Descriptor(), protoreflect.EnumNumber(x))
}

func (KeyPartDef_KeyPartSortOrder) Descriptor() protoreflect.EnumDescriptor {
	return file_aeon_schema_proto_enumTypes[1].Descriptor()
}

func (KeyPartDef_KeyPartSortOrder) Type() protoreflect.EnumType {
	return &file_aeon_schema_proto_enumTypes[1]
}

func (x KeyPartDef_KeyPartSortOrder) Number() protoreflect.EnumNumber {
	return protoreflect.EnumNumber(x)
}

// Deprecated: Use KeyPartDef_KeyPartSortOrder.Descriptor instead.
func (KeyPartDef_KeyPartSortOrder) EnumDescriptor() ([]byte, []int) {
	return file_aeon_schema_proto_rawDescGZIP(), []int{4, 0}
}

// Tuple: array of values.
type Tuple struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Fields        []*Value               `protobuf:"bytes,1,rep,name=fields,proto3" json:"fields,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Tuple) Reset() {
	*x = Tuple{}
	mi := &file_aeon_schema_proto_msgTypes[0]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Tuple) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Tuple) ProtoMessage() {}

func (x *Tuple) ProtoReflect() protoreflect.Message {
	mi := &file_aeon_schema_proto_msgTypes[0]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Tuple.ProtoReflect.Descriptor instead.
func (*Tuple) Descriptor() ([]byte, []int) {
	return file_aeon_schema_proto_rawDescGZIP(), []int{0}
}

func (x *Tuple) GetFields() []*Value {
	if x != nil {
		return x.Fields
	}
	return nil
}

// Tuple format: array of field names.
type TupleFormat struct {
	state         protoimpl.MessageState `protogen:"open.v1"`
	Names         []string               `protobuf:"bytes,1,rep,name=names,proto3" json:"names,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *TupleFormat) Reset() {
	*x = TupleFormat{}
	mi := &file_aeon_schema_proto_msgTypes[1]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *TupleFormat) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*TupleFormat) ProtoMessage() {}

func (x *TupleFormat) ProtoReflect() protoreflect.Message {
	mi := &file_aeon_schema_proto_msgTypes[1]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use TupleFormat.ProtoReflect.Descriptor instead.
func (*TupleFormat) Descriptor() ([]byte, []int) {
	return file_aeon_schema_proto_rawDescGZIP(), []int{1}
}

func (x *TupleFormat) GetNames() []string {
	if x != nil {
		return x.Names
	}
	return nil
}

// Read or write operation executed in a transaction.
type Operation struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Target space name.
	Space string `protobuf:"bytes,1,opt,name=space,proto3" json:"space,omitempty"`
	// Target key in the space. Must be full (have all defined key parts).
	Key *Tuple `protobuf:"bytes,2,opt,name=key,proto3" json:"key,omitempty"`
	// In a request:
	// * Ignored for read operations.
	// * Specifies the tuple to write in a write operation.
	// In a response:
	// * Tuple read from or written to the target space.
	// The write operation type depends on the tuple value:
	// * NOP if the tuple is not set.
	// * DELETE if the tuple is set but has no fields.
	// * REPLACE otherwise. The tuple must match the target key.
	// The tuple may be overwritten by the user-defined function specified in
	// a request to change the written value or even operation type depending on
	// read values.
	Tuple         *Tuple `protobuf:"bytes,3,opt,name=tuple,proto3" json:"tuple,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *Operation) Reset() {
	*x = Operation{}
	mi := &file_aeon_schema_proto_msgTypes[2]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *Operation) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*Operation) ProtoMessage() {}

func (x *Operation) ProtoReflect() protoreflect.Message {
	mi := &file_aeon_schema_proto_msgTypes[2]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use Operation.ProtoReflect.Descriptor instead.
func (*Operation) Descriptor() ([]byte, []int) {
	return file_aeon_schema_proto_rawDescGZIP(), []int{2}
}

func (x *Operation) GetSpace() string {
	if x != nil {
		return x.Space
	}
	return ""
}

func (x *Operation) GetKey() *Tuple {
	if x != nil {
		return x.Key
	}
	return nil
}

func (x *Operation) GetTuple() *Tuple {
	if x != nil {
		return x.Tuple
	}
	return nil
}

// Space field definition.
type FieldDef struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Field name.
	Name string `protobuf:"bytes,1,opt,name=name,proto3" json:"name,omitempty"`
	// Field type.
	Type FieldType `protobuf:"varint,2,opt,name=type,proto3,enum=aeon.FieldType" json:"type,omitempty"`
	// If set to true, the field may store null values. Optional.
	IsNullable    bool `protobuf:"varint,3,opt,name=is_nullable,json=isNullable,proto3" json:"is_nullable,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *FieldDef) Reset() {
	*x = FieldDef{}
	mi := &file_aeon_schema_proto_msgTypes[3]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *FieldDef) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*FieldDef) ProtoMessage() {}

func (x *FieldDef) ProtoReflect() protoreflect.Message {
	mi := &file_aeon_schema_proto_msgTypes[3]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use FieldDef.ProtoReflect.Descriptor instead.
func (*FieldDef) Descriptor() ([]byte, []int) {
	return file_aeon_schema_proto_rawDescGZIP(), []int{3}
}

func (x *FieldDef) GetName() string {
	if x != nil {
		return x.Name
	}
	return ""
}

func (x *FieldDef) GetType() FieldType {
	if x != nil {
		return x.Type
	}
	return FieldType_FIELD_TYPE_UNSPECIFIED
}

func (x *FieldDef) GetIsNullable() bool {
	if x != nil {
		return x.IsNullable
	}
	return false
}

// Key part definition.
type KeyPartDef struct {
	state protoimpl.MessageState `protogen:"open.v1"`
	// Indexed field ordinal number (1-based) or name.
	//
	// Types that are valid to be assigned to Field:
	//
	//	*KeyPartDef_Id
	//	*KeyPartDef_Name
	Field isKeyPartDef_Field `protobuf_oneof:"field"`
	// Key part type. Optional: if omitted, it will be deduced from
	// the corresponding space field type.
	Type FieldType `protobuf:"varint,3,opt,name=type,proto3,enum=aeon.FieldType" json:"type,omitempty"`
	// Sorting order: ascending (default) or descending.
	SortOrder     KeyPartDef_KeyPartSortOrder `protobuf:"varint,4,opt,name=sort_order,json=sortOrder,proto3,enum=aeon.KeyPartDef_KeyPartSortOrder" json:"sort_order,omitempty"`
	unknownFields protoimpl.UnknownFields
	sizeCache     protoimpl.SizeCache
}

func (x *KeyPartDef) Reset() {
	*x = KeyPartDef{}
	mi := &file_aeon_schema_proto_msgTypes[4]
	ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
	ms.StoreMessageInfo(mi)
}

func (x *KeyPartDef) String() string {
	return protoimpl.X.MessageStringOf(x)
}

func (*KeyPartDef) ProtoMessage() {}

func (x *KeyPartDef) ProtoReflect() protoreflect.Message {
	mi := &file_aeon_schema_proto_msgTypes[4]
	if x != nil {
		ms := protoimpl.X.MessageStateOf(protoimpl.Pointer(x))
		if ms.LoadMessageInfo() == nil {
			ms.StoreMessageInfo(mi)
		}
		return ms
	}
	return mi.MessageOf(x)
}

// Deprecated: Use KeyPartDef.ProtoReflect.Descriptor instead.
func (*KeyPartDef) Descriptor() ([]byte, []int) {
	return file_aeon_schema_proto_rawDescGZIP(), []int{4}
}

func (x *KeyPartDef) GetField() isKeyPartDef_Field {
	if x != nil {
		return x.Field
	}
	return nil
}

func (x *KeyPartDef) GetId() uint64 {
	if x != nil {
		if x, ok := x.Field.(*KeyPartDef_Id); ok {
			return x.Id
		}
	}
	return 0
}

func (x *KeyPartDef) GetName() string {
	if x != nil {
		if x, ok := x.Field.(*KeyPartDef_Name); ok {
			return x.Name
		}
	}
	return ""
}

func (x *KeyPartDef) GetType() FieldType {
	if x != nil {
		return x.Type
	}
	return FieldType_FIELD_TYPE_UNSPECIFIED
}

func (x *KeyPartDef) GetSortOrder() KeyPartDef_KeyPartSortOrder {
	if x != nil {
		return x.SortOrder
	}
	return KeyPartDef_KEY_PART_SORT_ORDER_ASC
}

type isKeyPartDef_Field interface {
	isKeyPartDef_Field()
}

type KeyPartDef_Id struct {
	Id uint64 `protobuf:"varint,1,opt,name=id,proto3,oneof"`
}

type KeyPartDef_Name struct {
	Name string `protobuf:"bytes,2,opt,name=name,proto3,oneof"`
}

func (*KeyPartDef_Id) isKeyPartDef_Field() {}

func (*KeyPartDef_Name) isKeyPartDef_Field() {}

var File_aeon_schema_proto protoreflect.FileDescriptor

var file_aeon_schema_proto_rawDesc = []byte{
	0x0a, 0x11, 0x61, 0x65, 0x6f, 0x6e, 0x5f, 0x73, 0x63, 0x68, 0x65, 0x6d, 0x61, 0x2e, 0x70, 0x72,
	0x6f, 0x74, 0x6f, 0x12, 0x04, 0x61, 0x65, 0x6f, 0x6e, 0x1a, 0x10, 0x61, 0x65, 0x6f, 0x6e, 0x5f,
	0x76, 0x61, 0x6c, 0x75, 0x65, 0x2e, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x22, 0x2c, 0x0a, 0x05, 0x54,
	0x75, 0x70, 0x6c, 0x65, 0x12, 0x23, 0x0a, 0x06, 0x66, 0x69, 0x65, 0x6c, 0x64, 0x73, 0x18, 0x01,
	0x20, 0x03, 0x28, 0x0b, 0x32, 0x0b, 0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x56, 0x61, 0x6c, 0x75,
	0x65, 0x52, 0x06, 0x66, 0x69, 0x65, 0x6c, 0x64, 0x73, 0x22, 0x23, 0x0a, 0x0b, 0x54, 0x75, 0x70,
	0x6c, 0x65, 0x46, 0x6f, 0x72, 0x6d, 0x61, 0x74, 0x12, 0x14, 0x0a, 0x05, 0x6e, 0x61, 0x6d, 0x65,
	0x73, 0x18, 0x01, 0x20, 0x03, 0x28, 0x09, 0x52, 0x05, 0x6e, 0x61, 0x6d, 0x65, 0x73, 0x22, 0x63,
	0x0a, 0x09, 0x4f, 0x70, 0x65, 0x72, 0x61, 0x74, 0x69, 0x6f, 0x6e, 0x12, 0x14, 0x0a, 0x05, 0x73,
	0x70, 0x61, 0x63, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x05, 0x73, 0x70, 0x61, 0x63,
	0x65, 0x12, 0x1d, 0x0a, 0x03, 0x6b, 0x65, 0x79, 0x18, 0x02, 0x20, 0x01, 0x28, 0x0b, 0x32, 0x0b,
	0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x54, 0x75, 0x70, 0x6c, 0x65, 0x52, 0x03, 0x6b, 0x65, 0x79,
	0x12, 0x21, 0x0a, 0x05, 0x74, 0x75, 0x70, 0x6c, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0b, 0x32,
	0x0b, 0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x54, 0x75, 0x70, 0x6c, 0x65, 0x52, 0x05, 0x74, 0x75,
	0x70, 0x6c, 0x65, 0x22, 0x64, 0x0a, 0x08, 0x46, 0x69, 0x65, 0x6c, 0x64, 0x44, 0x65, 0x66, 0x12,
	0x12, 0x0a, 0x04, 0x6e, 0x61, 0x6d, 0x65, 0x18, 0x01, 0x20, 0x01, 0x28, 0x09, 0x52, 0x04, 0x6e,
	0x61, 0x6d, 0x65, 0x12, 0x23, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28,
	0x0e, 0x32, 0x0f, 0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x46, 0x69, 0x65, 0x6c, 0x64, 0x54, 0x79,
	0x70, 0x65, 0x52, 0x04, 0x74, 0x79, 0x70, 0x65, 0x12, 0x1f, 0x0a, 0x0b, 0x69, 0x73, 0x5f, 0x6e,
	0x75, 0x6c, 0x6c, 0x61, 0x62, 0x6c, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x08, 0x52, 0x0a, 0x69,
	0x73, 0x4e, 0x75, 0x6c, 0x6c, 0x61, 0x62, 0x6c, 0x65, 0x22, 0xf3, 0x01, 0x0a, 0x0a, 0x4b, 0x65,
	0x79, 0x50, 0x61, 0x72, 0x74, 0x44, 0x65, 0x66, 0x12, 0x10, 0x0a, 0x02, 0x69, 0x64, 0x18, 0x01,
	0x20, 0x01, 0x28, 0x04, 0x48, 0x00, 0x52, 0x02, 0x69, 0x64, 0x12, 0x14, 0x0a, 0x04, 0x6e, 0x61,
	0x6d, 0x65, 0x18, 0x02, 0x20, 0x01, 0x28, 0x09, 0x48, 0x00, 0x52, 0x04, 0x6e, 0x61, 0x6d, 0x65,
	0x12, 0x23, 0x0a, 0x04, 0x74, 0x79, 0x70, 0x65, 0x18, 0x03, 0x20, 0x01, 0x28, 0x0e, 0x32, 0x0f,
	0x2e, 0x61, 0x65, 0x6f, 0x6e, 0x2e, 0x46, 0x69, 0x65, 0x6c, 0x64, 0x54, 0x79, 0x70, 0x65, 0x52,
	0x04, 0x74, 0x79, 0x70, 0x65, 0x12, 0x40, 0x0a, 0x0a, 0x73, 0x6f, 0x72, 0x74, 0x5f, 0x6f, 0x72,
	0x64, 0x65, 0x72, 0x18, 0x04, 0x20, 0x01, 0x28, 0x0e, 0x32, 0x21, 0x2e, 0x61, 0x65, 0x6f, 0x6e,
	0x2e, 0x4b, 0x65, 0x79, 0x50, 0x61, 0x72, 0x74, 0x44, 0x65, 0x66, 0x2e, 0x4b, 0x65, 0x79, 0x50,
	0x61, 0x72, 0x74, 0x53, 0x6f, 0x72, 0x74, 0x4f, 0x72, 0x64, 0x65, 0x72, 0x52, 0x09, 0x73, 0x6f,
	0x72, 0x74, 0x4f, 0x72, 0x64, 0x65, 0x72, 0x22, 0x4d, 0x0a, 0x10, 0x4b, 0x65, 0x79, 0x50, 0x61,
	0x72, 0x74, 0x53, 0x6f, 0x72, 0x74, 0x4f, 0x72, 0x64, 0x65, 0x72, 0x12, 0x1b, 0x0a, 0x17, 0x4b,
	0x45, 0x59, 0x5f, 0x50, 0x41, 0x52, 0x54, 0x5f, 0x53, 0x4f, 0x52, 0x54, 0x5f, 0x4f, 0x52, 0x44,
	0x45, 0x52, 0x5f, 0x41, 0x53, 0x43, 0x10, 0x00, 0x12, 0x1c, 0x0a, 0x18, 0x4b, 0x45, 0x59, 0x5f,
	0x50, 0x41, 0x52, 0x54, 0x5f, 0x53, 0x4f, 0x52, 0x54, 0x5f, 0x4f, 0x52, 0x44, 0x45, 0x52, 0x5f,
	0x44, 0x45, 0x53, 0x43, 0x10, 0x01, 0x42, 0x07, 0x0a, 0x05, 0x66, 0x69, 0x65, 0x6c, 0x64, 0x2a,
	0x83, 0x03, 0x0a, 0x09, 0x46, 0x69, 0x65, 0x6c, 0x64, 0x54, 0x79, 0x70, 0x65, 0x12, 0x1a, 0x0a,
	0x16, 0x46, 0x49, 0x45, 0x4c, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x55, 0x4e, 0x53, 0x50,
	0x45, 0x43, 0x49, 0x46, 0x49, 0x45, 0x44, 0x10, 0x00, 0x12, 0x12, 0x0a, 0x0e, 0x46, 0x49, 0x45,
	0x4c, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x41, 0x4e, 0x59, 0x10, 0x01, 0x12, 0x17, 0x0a,
	0x13, 0x46, 0x49, 0x45, 0x4c, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x55, 0x4e, 0x53, 0x49,
	0x47, 0x4e, 0x45, 0x44, 0x10, 0x02, 0x12, 0x15, 0x0a, 0x11, 0x46, 0x49, 0x45, 0x4c, 0x44, 0x5f,
	0x54, 0x59, 0x50, 0x45, 0x5f, 0x53, 0x54, 0x52, 0x49, 0x4e, 0x47, 0x10, 0x03, 0x12, 0x15, 0x0a,
	0x11, 0x46, 0x49, 0x45, 0x4c, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x4e, 0x55, 0x4d, 0x42,
	0x45, 0x52, 0x10, 0x04, 0x12, 0x15, 0x0a, 0x11, 0x46, 0x49, 0x45, 0x4c, 0x44, 0x5f, 0x54, 0x59,
	0x50, 0x45, 0x5f, 0x44, 0x4f, 0x55, 0x42, 0x4c, 0x45, 0x10, 0x05, 0x12, 0x16, 0x0a, 0x12, 0x46,
	0x49, 0x45, 0x4c, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x49, 0x4e, 0x54, 0x45, 0x47, 0x45,
	0x52, 0x10, 0x06, 0x12, 0x16, 0x0a, 0x12, 0x46, 0x49, 0x45, 0x4c, 0x44, 0x5f, 0x54, 0x59, 0x50,
	0x45, 0x5f, 0x42, 0x4f, 0x4f, 0x4c, 0x45, 0x41, 0x4e, 0x10, 0x07, 0x12, 0x18, 0x0a, 0x14, 0x46,
	0x49, 0x45, 0x4c, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x56, 0x41, 0x52, 0x42, 0x49, 0x4e,
	0x41, 0x52, 0x59, 0x10, 0x08, 0x12, 0x15, 0x0a, 0x11, 0x46, 0x49, 0x45, 0x4c, 0x44, 0x5f, 0x54,
	0x59, 0x50, 0x45, 0x5f, 0x53, 0x43, 0x41, 0x4c, 0x41, 0x52, 0x10, 0x09, 0x12, 0x16, 0x0a, 0x12,
	0x46, 0x49, 0x45, 0x4c, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x44, 0x45, 0x43, 0x49, 0x4d,
	0x41, 0x4c, 0x10, 0x0a, 0x12, 0x13, 0x0a, 0x0f, 0x46, 0x49, 0x45, 0x4c, 0x44, 0x5f, 0x54, 0x59,
	0x50, 0x45, 0x5f, 0x55, 0x55, 0x49, 0x44, 0x10, 0x0b, 0x12, 0x17, 0x0a, 0x13, 0x46, 0x49, 0x45,
	0x4c, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x44, 0x41, 0x54, 0x45, 0x54, 0x49, 0x4d, 0x45,
	0x10, 0x0c, 0x12, 0x17, 0x0a, 0x13, 0x46, 0x49, 0x45, 0x4c, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45,
	0x5f, 0x49, 0x4e, 0x54, 0x45, 0x52, 0x56, 0x41, 0x4c, 0x10, 0x0d, 0x12, 0x14, 0x0a, 0x10, 0x46,
	0x49, 0x45, 0x4c, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f, 0x41, 0x52, 0x52, 0x41, 0x59, 0x10,
	0x0e, 0x12, 0x12, 0x0a, 0x0e, 0x46, 0x49, 0x45, 0x4c, 0x44, 0x5f, 0x54, 0x59, 0x50, 0x45, 0x5f,
	0x4d, 0x41, 0x50, 0x10, 0x0f, 0x62, 0x06, 0x70, 0x72, 0x6f, 0x74, 0x6f, 0x33,
}

var (
	file_aeon_schema_proto_rawDescOnce sync.Once
	file_aeon_schema_proto_rawDescData = file_aeon_schema_proto_rawDesc
)

func file_aeon_schema_proto_rawDescGZIP() []byte {
	file_aeon_schema_proto_rawDescOnce.Do(func() {
		file_aeon_schema_proto_rawDescData = protoimpl.X.CompressGZIP(file_aeon_schema_proto_rawDescData)
	})
	return file_aeon_schema_proto_rawDescData
}

var file_aeon_schema_proto_enumTypes = make([]protoimpl.EnumInfo, 2)
var file_aeon_schema_proto_msgTypes = make([]protoimpl.MessageInfo, 5)
var file_aeon_schema_proto_goTypes = []any{
	(FieldType)(0),                   // 0: aeon.FieldType
	(KeyPartDef_KeyPartSortOrder)(0), // 1: aeon.KeyPartDef.KeyPartSortOrder
	(*Tuple)(nil),                    // 2: aeon.Tuple
	(*TupleFormat)(nil),              // 3: aeon.TupleFormat
	(*Operation)(nil),                // 4: aeon.Operation
	(*FieldDef)(nil),                 // 5: aeon.FieldDef
	(*KeyPartDef)(nil),               // 6: aeon.KeyPartDef
	(*Value)(nil),                    // 7: aeon.Value
}
var file_aeon_schema_proto_depIdxs = []int32{
	7, // 0: aeon.Tuple.fields:type_name -> aeon.Value
	2, // 1: aeon.Operation.key:type_name -> aeon.Tuple
	2, // 2: aeon.Operation.tuple:type_name -> aeon.Tuple
	0, // 3: aeon.FieldDef.type:type_name -> aeon.FieldType
	0, // 4: aeon.KeyPartDef.type:type_name -> aeon.FieldType
	1, // 5: aeon.KeyPartDef.sort_order:type_name -> aeon.KeyPartDef.KeyPartSortOrder
	6, // [6:6] is the sub-list for method output_type
	6, // [6:6] is the sub-list for method input_type
	6, // [6:6] is the sub-list for extension type_name
	6, // [6:6] is the sub-list for extension extendee
	0, // [0:6] is the sub-list for field type_name
}

func init() { file_aeon_schema_proto_init() }
func file_aeon_schema_proto_init() {
	if File_aeon_schema_proto != nil {
		return
	}
	file_aeon_value_proto_init()
	file_aeon_schema_proto_msgTypes[4].OneofWrappers = []any{
		(*KeyPartDef_Id)(nil),
		(*KeyPartDef_Name)(nil),
	}
	type x struct{}
	out := protoimpl.TypeBuilder{
		File: protoimpl.DescBuilder{
			GoPackagePath: reflect.TypeOf(x{}).PkgPath(),
			RawDescriptor: file_aeon_schema_proto_rawDesc,
			NumEnums:      2,
			NumMessages:   5,
			NumExtensions: 0,
			NumServices:   0,
		},
		GoTypes:           file_aeon_schema_proto_goTypes,
		DependencyIndexes: file_aeon_schema_proto_depIdxs,
		EnumInfos:         file_aeon_schema_proto_enumTypes,
		MessageInfos:      file_aeon_schema_proto_msgTypes,
	}.Build()
	File_aeon_schema_proto = out.File
	file_aeon_schema_proto_rawDesc = nil
	file_aeon_schema_proto_goTypes = nil
	file_aeon_schema_proto_depIdxs = nil
}
