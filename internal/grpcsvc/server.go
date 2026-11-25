// Package grpcsvc converts RPCs into queued commands and streams detached results.
package grpcsvc

import (
	"context"
	"errors"
	"fmt"
	pb "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"github.com/KareemAraide/distributed-systems-41/internal/executor"
	"github.com/KareemAraide/distributed-systems-41/internal/sm"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/prototext"
	"os"
)

type Server struct {
	pb.UnimplementedGameServiceServer
	NodeID  string
	exec    *executor.Executor
	shuffle func() ([]string, error)
}

var _ pb.GameServiceServer = (*Server)(nil)

func New(ctx context.Context, nodeID string, state *pb.State, applied int64) *Server {
	e := executor.NewAt(state, applied)
	go e.Run(ctx)
	return NewWith(nodeID, e)
}
func NewWith(nodeID string, e *executor.Executor) *Server {
	return &Server{NodeID: nodeID, exec: e, shuffle: randomDeck}
}
func LoadSnapshot(path string) (*pb.State, int64, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, err
	}
	snap := &pb.Snapshot{}
	if err = prototext.Unmarshal(b, snap); err != nil {
		return nil, 0, fmt.Errorf("snapshot: %w", err)
	}
	if err = sm.ValidateSnapshot(snap); err != nil {
		return nil, 0, err
	}
	s, n := sm.Restore(snap)
	return s, n, nil
}
func (s *Server) run(ctx context.Context, c *pb.Command) (*pb.Result, error) {
	r, err := s.exec.Submit(ctx, c)
	if errors.Is(err, executor.ErrStopped) {
		return nil, status.Error(codes.Unavailable, "executor stopped")
	}
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return nil, status.FromContextError(err).Err()
		}
		return nil, status.Error(codes.Internal, "executor failed")
	}
	return r, toStatus(r)
}
func (s *Server) CreateGame(ctx context.Context, r *pb.CreateGameRequest) (*pb.CreateGameResponse, error) {
	out, err := s.run(ctx, createCommand(r))
	if err != nil {
		return nil, err
	}
	return &pb.CreateGameResponse{Game: out.Games[0], NodeId: s.NodeID}, nil
}
func (s *Server) GetGames(r *pb.GetGamesRequest, stream grpc.ServerStreamingServer[pb.GetGamesResponse]) error {
	out, err := s.run(stream.Context(), gamesCommand(r))
	if err != nil {
		return err
	}
	for _, g := range out.Games {
		if err = stream.Send(&pb.GetGamesResponse{Game: g, NodeId: s.NodeID}); err != nil {
			return err
		}
	}
	return stream.Send(&pb.GetGamesResponse{NodeId: s.NodeID, Fin: true})
}
func (s *Server) GetHand(r *pb.GetHandRequest, stream grpc.ServerStreamingServer[pb.GetHandResponse]) error {
	out, err := s.run(stream.Context(), handCommand(r))
	if err != nil {
		return err
	}
	h := out.GetHand()
	info := &pb.HandInfo{GameId: h.GameId, DealNumber: h.DealNumber, PlayerId: h.PlayerId, NodeId: s.NodeID}
	if err = stream.Send(&pb.GetHandResponse{Item: &pb.GetHandResponse_Info{Info: info}}); err != nil {
		return err
	}
	for _, card := range h.Cards {
		if err = stream.Send(&pb.GetHandResponse{Item: &pb.GetHandResponse_Card{Card: card}}); err != nil {
			return err
		}
	}
	return stream.Send(&pb.GetHandResponse{Fin: true})
}
func (s *Server) ApplyActions(ctx context.Context, r *pb.ApplyActionsRequest) (*pb.ApplyActionsResponse, error) {
	c, err := s.actionsCommand(r)
	if err != nil {
		return nil, err
	}
	out, err := s.run(ctx, c)
	if err != nil {
		return nil, err
	}
	return &pb.ApplyActionsResponse{Games: out.Games, NodeId: s.NodeID}, nil
}
