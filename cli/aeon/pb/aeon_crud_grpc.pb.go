// Code generated by protoc-gen-go-grpc. DO NOT EDIT.
// versions:
// - protoc-gen-go-grpc v1.5.1
// - protoc             v5.28.3
// source: aeon_crud.proto

package pb

import (
	context "context"
	grpc "google.golang.org/grpc"
	codes "google.golang.org/grpc/codes"
	status "google.golang.org/grpc/status"
)

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
// Requires gRPC-Go v1.64.0 or later.
const _ = grpc.SupportPackageIsVersion9

const (
	CRUDService_Execute_FullMethodName = "/aeon.CRUDService/Execute"
	CRUDService_Insert_FullMethodName  = "/aeon.CRUDService/Insert"
	CRUDService_Replace_FullMethodName = "/aeon.CRUDService/Replace"
	CRUDService_Delete_FullMethodName  = "/aeon.CRUDService/Delete"
	CRUDService_Get_FullMethodName     = "/aeon.CRUDService/Get"
	CRUDService_Select_FullMethodName  = "/aeon.CRUDService/Select"
)

// CRUDServiceClient is the client API for CRUDService service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://pkg.go.dev/google.golang.org/grpc/?tab=doc#ClientConn.NewStream.
//
// CRUD API to Aeon - a distributed database based on Tarantool.
type CRUDServiceClient interface {
	// Transactionally executes a set of read and write operations.
	Execute(ctx context.Context, in *ExecuteRequest, opts ...grpc.CallOption) (*ExecuteResponse, error)
	// Transactionally inserts tuples into a space.
	// Raises an error if a tuple with the same key already exists.
	Insert(ctx context.Context, in *InsertRequest, opts ...grpc.CallOption) (*InsertResponse, error)
	// Transactionally replaces tuples in a space.
	// If a tuple with the same key already exists, it will be replaced.
	Replace(ctx context.Context, in *ReplaceRequest, opts ...grpc.CallOption) (*ReplaceResponse, error)
	// Transactionally deletes tuples from a space.
	// If a key doesn't exist, it will be ignored (no error is raised).
	Delete(ctx context.Context, in *DeleteRequest, opts ...grpc.CallOption) (*DeleteResponse, error)
	// Transactionally queries tuples from a space.
	Get(ctx context.Context, in *GetRequest, opts ...grpc.CallOption) (*GetResponse, error)
	// Non-transactionally select tuples from a space.
	Select(ctx context.Context, in *SelectRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[SelectResponse], error)
}

type cRUDServiceClient struct {
	cc grpc.ClientConnInterface
}

func NewCRUDServiceClient(cc grpc.ClientConnInterface) CRUDServiceClient {
	return &cRUDServiceClient{cc}
}

func (c *cRUDServiceClient) Execute(ctx context.Context, in *ExecuteRequest, opts ...grpc.CallOption) (*ExecuteResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ExecuteResponse)
	err := c.cc.Invoke(ctx, CRUDService_Execute_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cRUDServiceClient) Insert(ctx context.Context, in *InsertRequest, opts ...grpc.CallOption) (*InsertResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(InsertResponse)
	err := c.cc.Invoke(ctx, CRUDService_Insert_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cRUDServiceClient) Replace(ctx context.Context, in *ReplaceRequest, opts ...grpc.CallOption) (*ReplaceResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(ReplaceResponse)
	err := c.cc.Invoke(ctx, CRUDService_Replace_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cRUDServiceClient) Delete(ctx context.Context, in *DeleteRequest, opts ...grpc.CallOption) (*DeleteResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(DeleteResponse)
	err := c.cc.Invoke(ctx, CRUDService_Delete_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cRUDServiceClient) Get(ctx context.Context, in *GetRequest, opts ...grpc.CallOption) (*GetResponse, error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	out := new(GetResponse)
	err := c.cc.Invoke(ctx, CRUDService_Get_FullMethodName, in, out, cOpts...)
	if err != nil {
		return nil, err
	}
	return out, nil
}

func (c *cRUDServiceClient) Select(ctx context.Context, in *SelectRequest, opts ...grpc.CallOption) (grpc.ServerStreamingClient[SelectResponse], error) {
	cOpts := append([]grpc.CallOption{grpc.StaticMethod()}, opts...)
	stream, err := c.cc.NewStream(ctx, &CRUDService_ServiceDesc.Streams[0], CRUDService_Select_FullMethodName, cOpts...)
	if err != nil {
		return nil, err
	}
	x := &grpc.GenericClientStream[SelectRequest, SelectResponse]{ClientStream: stream}
	if err := x.ClientStream.SendMsg(in); err != nil {
		return nil, err
	}
	if err := x.ClientStream.CloseSend(); err != nil {
		return nil, err
	}
	return x, nil
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type CRUDService_SelectClient = grpc.ServerStreamingClient[SelectResponse]

// CRUDServiceServer is the server API for CRUDService service.
// All implementations must embed UnimplementedCRUDServiceServer
// for forward compatibility.
//
// CRUD API to Aeon - a distributed database based on Tarantool.
type CRUDServiceServer interface {
	// Transactionally executes a set of read and write operations.
	Execute(context.Context, *ExecuteRequest) (*ExecuteResponse, error)
	// Transactionally inserts tuples into a space.
	// Raises an error if a tuple with the same key already exists.
	Insert(context.Context, *InsertRequest) (*InsertResponse, error)
	// Transactionally replaces tuples in a space.
	// If a tuple with the same key already exists, it will be replaced.
	Replace(context.Context, *ReplaceRequest) (*ReplaceResponse, error)
	// Transactionally deletes tuples from a space.
	// If a key doesn't exist, it will be ignored (no error is raised).
	Delete(context.Context, *DeleteRequest) (*DeleteResponse, error)
	// Transactionally queries tuples from a space.
	Get(context.Context, *GetRequest) (*GetResponse, error)
	// Non-transactionally select tuples from a space.
	Select(*SelectRequest, grpc.ServerStreamingServer[SelectResponse]) error
	mustEmbedUnimplementedCRUDServiceServer()
}

// UnimplementedCRUDServiceServer must be embedded to have
// forward compatible implementations.
//
// NOTE: this should be embedded by value instead of pointer to avoid a nil
// pointer dereference when methods are called.
type UnimplementedCRUDServiceServer struct{}

func (UnimplementedCRUDServiceServer) Execute(context.Context, *ExecuteRequest) (*ExecuteResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Execute not implemented")
}
func (UnimplementedCRUDServiceServer) Insert(context.Context, *InsertRequest) (*InsertResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Insert not implemented")
}
func (UnimplementedCRUDServiceServer) Replace(context.Context, *ReplaceRequest) (*ReplaceResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Replace not implemented")
}
func (UnimplementedCRUDServiceServer) Delete(context.Context, *DeleteRequest) (*DeleteResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Delete not implemented")
}
func (UnimplementedCRUDServiceServer) Get(context.Context, *GetRequest) (*GetResponse, error) {
	return nil, status.Errorf(codes.Unimplemented, "method Get not implemented")
}
func (UnimplementedCRUDServiceServer) Select(*SelectRequest, grpc.ServerStreamingServer[SelectResponse]) error {
	return status.Errorf(codes.Unimplemented, "method Select not implemented")
}
func (UnimplementedCRUDServiceServer) mustEmbedUnimplementedCRUDServiceServer() {}
func (UnimplementedCRUDServiceServer) testEmbeddedByValue()                     {}

// UnsafeCRUDServiceServer may be embedded to opt out of forward compatibility for this service.
// Use of this interface is not recommended, as added methods to CRUDServiceServer will
// result in compilation errors.
type UnsafeCRUDServiceServer interface {
	mustEmbedUnimplementedCRUDServiceServer()
}

func RegisterCRUDServiceServer(s grpc.ServiceRegistrar, srv CRUDServiceServer) {
	// If the following call pancis, it indicates UnimplementedCRUDServiceServer was
	// embedded by pointer and is nil.  This will cause panics if an
	// unimplemented method is ever invoked, so we test this at initialization
	// time to prevent it from happening at runtime later due to I/O.
	if t, ok := srv.(interface{ testEmbeddedByValue() }); ok {
		t.testEmbeddedByValue()
	}
	s.RegisterService(&CRUDService_ServiceDesc, srv)
}

func _CRUDService_Execute_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ExecuteRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CRUDServiceServer).Execute(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CRUDService_Execute_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CRUDServiceServer).Execute(ctx, req.(*ExecuteRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CRUDService_Insert_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(InsertRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CRUDServiceServer).Insert(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CRUDService_Insert_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CRUDServiceServer).Insert(ctx, req.(*InsertRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CRUDService_Replace_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(ReplaceRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CRUDServiceServer).Replace(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CRUDService_Replace_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CRUDServiceServer).Replace(ctx, req.(*ReplaceRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CRUDService_Delete_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(DeleteRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CRUDServiceServer).Delete(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CRUDService_Delete_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CRUDServiceServer).Delete(ctx, req.(*DeleteRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CRUDService_Get_Handler(srv interface{}, ctx context.Context, dec func(interface{}) error, interceptor grpc.UnaryServerInterceptor) (interface{}, error) {
	in := new(GetRequest)
	if err := dec(in); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(CRUDServiceServer).Get(ctx, in)
	}
	info := &grpc.UnaryServerInfo{
		Server:     srv,
		FullMethod: CRUDService_Get_FullMethodName,
	}
	handler := func(ctx context.Context, req interface{}) (interface{}, error) {
		return srv.(CRUDServiceServer).Get(ctx, req.(*GetRequest))
	}
	return interceptor(ctx, in, info, handler)
}

func _CRUDService_Select_Handler(srv interface{}, stream grpc.ServerStream) error {
	m := new(SelectRequest)
	if err := stream.RecvMsg(m); err != nil {
		return err
	}
	return srv.(CRUDServiceServer).Select(m, &grpc.GenericServerStream[SelectRequest, SelectResponse]{ServerStream: stream})
}

// This type alias is provided for backwards compatibility with existing code that references the prior non-generic stream type by name.
type CRUDService_SelectServer = grpc.ServerStreamingServer[SelectResponse]

// CRUDService_ServiceDesc is the grpc.ServiceDesc for CRUDService service.
// It's only intended for direct use with grpc.RegisterService,
// and not to be introspected or modified (even as a copy)
var CRUDService_ServiceDesc = grpc.ServiceDesc{
	ServiceName: "aeon.CRUDService",
	HandlerType: (*CRUDServiceServer)(nil),
	Methods: []grpc.MethodDesc{
		{
			MethodName: "Execute",
			Handler:    _CRUDService_Execute_Handler,
		},
		{
			MethodName: "Insert",
			Handler:    _CRUDService_Insert_Handler,
		},
		{
			MethodName: "Replace",
			Handler:    _CRUDService_Replace_Handler,
		},
		{
			MethodName: "Delete",
			Handler:    _CRUDService_Delete_Handler,
		},
		{
			MethodName: "Get",
			Handler:    _CRUDService_Get_Handler,
		},
	},
	Streams: []grpc.StreamDesc{
		{
			StreamName:    "Select",
			Handler:       _CRUDService_Select_Handler,
			ServerStreams: true,
		},
	},
	Metadata: "aeon_crud.proto",
}
