package service

import (
	"context"
	"errors"
	"strings"

	"github.com/tarantool/tt/cli/aeon/pb"
)

// Server mock functions: SQL[Check|Stream] accepts SQL request with string ["ok", "error", ""].
// SQLCheck returns appropriate SQLCheckStatus value: "VALID", "INVALID", "INCOMPLETE".
// SQL[Stream] returns SQLResponse with two tuples on "ok" request and with Error other way.
type Server struct {
	pb.UnimplementedSQLServiceServer
}

func (s *Server) SQLCheck(ctx context.Context,
	request *pb.SQLRequest,
) (*pb.SQLCheckResponse, error) {
	if request == nil {
		return nil, errors.New("sql check request is nil")
	}
	status := pb.SQLCheckStatus_SQL_QUERY_INCOMPLETE
	switch strings.ToLower(request.GetQuery()) {
	case "ok":
		status = pb.SQLCheckStatus_SQL_QUERY_VALID
	case "error":
		status = pb.SQLCheckStatus_SQL_QUERY_INVALID
	}
	return &pb.SQLCheckResponse{Status: status}, nil
}

func (s *Server) SQL(ctx context.Context, in *pb.SQLRequest) (*pb.SQLResponse, error) {
	if in == nil {
		return nil, errors.New("sql request is nil")
	}
	res := makeSQLResponse(in.GetQuery())
	return &res, nil
}

func (s *Server) SQLStream(in *pb.SQLRequest, stream pb.SQLService_SQLStreamServer) error {
	if in == nil {
		return errors.New("sql stream request is nil")
	}
	res := makeSQLResponse(in.GetQuery())
	stream.Send(&res)
	return nil
}

func makeSQLResponse(query string) pb.SQLResponse {
	switch strings.ToLower(query) {
	case "ok":
		return pb.SQLResponse{
			TupleFormat: &pb.TupleFormat{Names: []string{"id", "name"}},
			Tuples: []*pb.Tuple{
				{
					Fields: []*pb.Value{
						{Kind: &pb.Value_IntegerValue{IntegerValue: 1}},
						{Kind: &pb.Value_StringValue{StringValue: "entry1"}},
					},
				},
				{
					Fields: []*pb.Value{
						{Kind: &pb.Value_IntegerValue{IntegerValue: 2}},
						{Kind: &pb.Value_StringValue{StringValue: "entry2"}},
					},
				},
			},
		}
	case "error":
		return pb.SQLResponse{Error: &pb.Error{
			Type: "AeonSQLError",
			Name: "SYNTAX_ERROR",
			Msg:  "error in mock SQL request",
		}}
	}
	return pb.SQLResponse{Error: &pb.Error{
		Type: "AeonError",
		Name: "UNEXPECTED_ERROR",
		Msg:  "incomplete in mock SQL request",
	}}
}
