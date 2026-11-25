package httpapi

import (
	"context"
	pb "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"net/http"
	"testing"
)

type faulty struct {
	pb.UnimplementedGameServiceServer
	mode string
	code codes.Code
}

func (f *faulty) CreateGame(context.Context, *pb.CreateGameRequest) (*pb.CreateGameResponse, error) {
	return nil, status.Error(f.code, "injected backend error")
}
func (f *faulty) GetGames(r *pb.GetGamesRequest, s grpc.ServerStreamingServer[pb.GetGamesResponse]) error {
	g := &pb.Game{Id: r.GetGameIds()[0], Phase: pb.GamePhase_GAME_PHASE_WAITING, Players: []*pb.Player{{Id: "p", Name: "test", MinimumBid: 2}}}
	if f.mode == "wrong-id" {
		g.Id = "other"
	}
	if err := s.Send(&pb.GetGamesResponse{Game: g, NodeId: "n1"}); err != nil {
		return err
	}
	switch f.mode {
	case "no-fin":
		return nil
	case "late-error":
		return status.Error(codes.Unavailable, "connection lost")
	case "after-fin":
		if err := s.Send(&pb.GetGamesResponse{NodeId: "n1", Fin: true}); err != nil {
			return err
		}
		return s.Send(&pb.GetGamesResponse{NodeId: "n1", Game: g})
	case "wrong-id":
		return s.Send(&pb.GetGamesResponse{NodeId: "n1", Fin: true})
	case "incomplete":
		return s.Send(&pb.GetGamesResponse{NodeId: "n1", Fin: true})
	}
	return s.Send(&pb.GetGamesResponse{NodeId: "n1", Fin: true})
}
func (f *faulty) GetHand(r *pb.GetHandRequest, s grpc.ServerStreamingServer[pb.GetHandResponse]) error {
	if err := s.Send(&pb.GetHandResponse{Item: &pb.GetHandResponse_Info{Info: &pb.HandInfo{GameId: r.GameId, PlayerId: r.PlayerId, NodeId: "n1", DealNumber: 1}}}); err != nil {
		return err
	}
	switch f.mode {
	case "no-fin":
		return nil
	case "late-error":
		return status.Error(codes.Unavailable, "connection lost")
	case "duplicate-card":
		for i := 0; i < 2; i++ {
			if err := s.Send(&pb.GetHandResponse{Item: &pb.GetHandResponse_Card{Card: "AH"}}); err != nil {
				return err
			}
		}
	}
	return s.Send(&pb.GetHandResponse{Fin: true})
}
func TestStreamCompletion(t *testing.T) {
	for _, tc := range []struct {
		mode, path string
		code       int
	}{
		{"no-fin", "/v1/games/a", 503}, {"late-error", "/v1/games/a", 503},
		{"after-fin", "/v1/games/a", 500}, {"wrong-id", "/v1/games/a", 500},
		{"incomplete", "/v1/games?ids=a&ids=b", 500},
		{"no-fin", "/v1/games/a/players/p/hand", 503}, {"late-error", "/v1/games/a/players/p/hand", 503},
		{"duplicate-card", "/v1/games/a/players/p/hand", 500}, {"empty", "/v1/games/a/players/p/hand", 200},
	} {
		t.Run(tc.mode+tc.path, func(t *testing.T) {
			h := httpFor(t, &faulty{mode: tc.mode})
			request(t, h, "GET", tc.path, nil, tc.code)
		})
	}
}
func TestTransportStatusMapping(t *testing.T) {
	for _, tc := range []struct {
		code codes.Code
		http int
	}{
		{codes.InvalidArgument, 400}, {codes.NotFound, 404}, {codes.AlreadyExists, 409}, {codes.FailedPrecondition, 412},
		{codes.ResourceExhausted, 503}, {codes.Unavailable, 503}, {codes.DeadlineExceeded, 504}, {codes.Internal, 500},
	} {
		t.Run(tc.code.String(), func(t *testing.T) {
			h := httpFor(t, &faulty{code: tc.code})
			request(t, h, http.MethodPost, "/v1/games", `{"name":"test"}`, tc.http)
		})
	}
}
