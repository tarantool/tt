package service

import (
	"context"

	"github.com/tarantool/tt/cli/aeon/pb"
)

// Diag mock implementation of pb.DiagServiceServer.
type Diag struct {
	pb.UnimplementedDiagServiceServer
}

// Ping implements pb.DiagServiceServer.Ping method.
func (s *Diag) Ping(ctx context.Context, in *pb.PingRequest) (*pb.PingResponse, error) {
	return &pb.PingResponse{}, nil
}
