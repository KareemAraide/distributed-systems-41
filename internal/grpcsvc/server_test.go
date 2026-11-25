package grpcsvc

import (
	"context"
	"errors"
	pb "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"github.com/KareemAraide/distributed-systems-41/internal/executor"
	"github.com/KareemAraide/distributed-systems-41/internal/sm"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func ready(t *testing.T) *pb.State {
	t.Helper()
	b, err := os.ReadFile("../sm/testdata/ready.txtpb")
	if err != nil {
		t.Fatal(err)
	}
	s := &pb.State{}
	if err = prototext.Unmarshal(b, s); err != nil {
		t.Fatal(err)
	}
	return s
}
func client(t *testing.T, s *Server) pb.GameServiceClient {
	t.Helper()
	listener := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	pb.RegisterGameServiceServer(gs, s)
	go func() { _ = gs.Serve(listener) }()
	conn, err := grpc.NewClient("passthrough:///buf", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return listener.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); gs.Stop(); listener.Close() })
	return pb.NewGameServiceClient(conn)
}
func TestRPCStateAndStreams(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	c := client(t, New(ctx, "n1", ready(t), 0))
	ctx, stop := context.WithTimeout(ctx, 5*time.Second)
	defer stop()
	made, err := c.CreateGame(ctx, &pb.CreateGameRequest{Name: "new"})
	if err != nil || made.GetGame().GetId() != "game-2" || made.GetNodeId() != "n1" {
		t.Fatalf("%v %v", made, err)
	}
	join := &pb.GameAction{GameId: "game-2", Action: &pb.GameAction_Join{Join: &pb.JoinAction{Name: "Awes", Seat: 1}}}
	conflict := &pb.GameAction{GameId: "game-2", Action: &pb.GameAction_Join{Join: &pb.JoinAction{Name: "bad", Seat: 0}}}
	if _, err = c.ApplyActions(ctx, &pb.ApplyActionsRequest{Actions: []*pb.GameAction{join, conflict}}); status.Code(err) != codes.AlreadyExists {
		t.Fatal(err)
	}
	stream, err := c.GetGames(ctx, &pb.GetGamesRequest{GameIds: []string{"game-2", "game-1"}, ReadMask: &fieldmaskpb.FieldMask{Paths: []string{"players"}}})
	if err != nil {
		t.Fatal(err)
	}
	first, err := stream.Recv()
	if err != nil || first.GetGame().GetId() != "game-2" || len(first.GetGame().GetPlayers()) != 1 || first.GetGame().GetPhase() != 0 {
		t.Fatalf("%v %v", first, err)
	}
	second, err := stream.Recv()
	if err != nil || second.GetGame().GetId() != "game-1" {
		t.Fatal(second, err)
	}
	fin, err := stream.Recv()
	if err != nil || !fin.GetFin() || fin.GetGame() != nil {
		t.Fatal(fin, err)
	}
	if _, err = stream.Recv(); !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	start := &pb.GameAction{GameId: "game-1", Action: &pb.GameAction_StartDeal{StartDeal: &pb.StartDealAction{PlayerId: "player-1"}}}
	if _, err = c.ApplyActions(ctx, &pb.ApplyActionsRequest{Actions: []*pb.GameAction{start}}); err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, id := range []string{"player-1", "player-2", "player-3", "player-4"} {
		stream, err := c.GetHand(ctx, &pb.GetHandRequest{GameId: "game-1", PlayerId: id})
		if err != nil {
			t.Fatal(err)
		}
		info, err := stream.Recv()
		if err != nil || info.GetInfo().GetPlayerId() != id {
			t.Fatal(info, err)
		}
		count := 0
		fin := false
		for {
			msg, err := stream.Recv()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			if msg.Fin {
				fin = true
				continue
			}
			card := msg.GetCard()
			if !sm.ValidCard(card) || seen[card] {
				t.Fatalf("duplicate or invalid %q", card)
			}
			seen[card] = true
			count++
		}
		if count != 13 || !fin {
			t.Fatalf("%d cards; fin=%v", count, fin)
		}
	}
	if len(seen) != 52 {
		t.Fatal("not a full deck")
	}
}
func TestStatusAndShuffleFailure(t *testing.T) {
	for _, tc := range []struct {
		code pb.Code
		want codes.Code
	}{
		{pb.Code_CODE_OK, codes.OK}, {pb.Code_CODE_INVALID_ARGUMENT, codes.InvalidArgument}, {pb.Code_CODE_NOT_FOUND, codes.NotFound},
		{pb.Code_CODE_ALREADY_EXISTS, codes.AlreadyExists}, {pb.Code_CODE_FAILED_PRECONDITION, codes.FailedPrecondition},
		{pb.Code_CODE_RESOURCE_EXHAUSTED, codes.ResourceExhausted}, {pb.Code(99), codes.Internal},
	} {
		if got := status.Code(toStatus(&pb.Result{Code: tc.code})); got != tc.want {
			t.Fatalf("%v -> %v", tc.code, got)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	s := New(ctx, "n1", ready(t), 0)
	s.shuffle = func() ([]string, error) { return nil, errors.New("random source failed") }
	_, err := s.ApplyActions(ctx, &pb.ApplyActionsRequest{Actions: []*pb.GameAction{{GameId: "game-1", Action: &pb.GameAction_StartDeal{StartDeal: &pb.StartDealAction{PlayerId: "player-1"}}}}})
	if status.Code(err) != codes.Internal {
		t.Fatal(err)
	}
}
func TestLoadSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "snapshot.txtpb")
	s := sm.TakeSnapshot(ready(t), 7)
	b, err := prototext.Marshal(s)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(path, b, 0600); err != nil {
		t.Fatal(err)
	}
	state, n, err := LoadSnapshot(path)
	if err != nil || n != 7 || len(state.Games) != 1 {
		t.Fatal(n, err)
	}
	if err = os.WriteFile(path, []byte("state { games: broken }"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, _, err = LoadSnapshot(path); err == nil {
		t.Fatal("invalid snapshot loaded")
	}
	if _, _, err = LoadSnapshot(path + "missing"); err == nil {
		t.Fatal("missing file loaded")
	}
}
func TestStoppedExecutor(t *testing.T) {
	e := executor.New(sm.NewState())
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	done := make(chan struct{})
	go func() { e.Run(ctx); close(done) }()
	<-done
	_, err := NewWith("n1", e).CreateGame(context.Background(), &pb.CreateGameRequest{Name: "test"})
	if status.Code(err) != codes.Unavailable {
		t.Fatal(err)
	}
}
