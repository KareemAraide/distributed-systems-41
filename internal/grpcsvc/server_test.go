package grpcsvc

import (
	"context"
	"fmt"
	"io"
	"net"
	"slices"
	"strings"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	gamev1 "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
)

func testClient(t *testing.T, nodeID string) gamev1.GameServiceClient {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	gamev1.RegisterGameServiceServer(server, New(nodeID))
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)

	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return gamev1.NewGameServiceClient(conn)
}

func testContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)
	return ctx
}

type gameReply interface {
	GetGame() *gamev1.Game
	GetNodeId() string
}

func TestUnaryRPCs(t *testing.T) {
	client := testClient(t, "n1")
	ctx := testContext(t)
	cases := []struct {
		name  string
		phase gamev1.GamePhase
		call  func(context.Context) (gameReply, error)
	}{
		{"CreateGame", gamev1.GamePhase_GAME_PHASE_WAITING, func(ctx context.Context) (gameReply, error) {
			return client.CreateGame(ctx, &gamev1.CreateGameRequest{Name: "Kareem"})
		}},
		{"GetGame", gamev1.GamePhase_GAME_PHASE_PLAYING, func(ctx context.Context) (gameReply, error) {
			return client.GetGame(ctx, &gamev1.GetGameRequest{GameId: "demo"})
		}},
		{"JoinGame", gamev1.GamePhase_GAME_PHASE_WAITING, func(ctx context.Context) (gameReply, error) {
			return client.JoinGame(ctx, &gamev1.JoinGameRequest{GameId: "demo", Name: "Player Two", Seat: 1})
		}},
		{"StartDeal", gamev1.GamePhase_GAME_PHASE_CUTTING, func(ctx context.Context) (gameReply, error) {
			return client.StartDeal(ctx, &gamev1.StartDealRequest{GameId: "demo", PlayerId: "player-0"})
		}},
		{"CutDeck", gamev1.GamePhase_GAME_PHASE_BIDDING, func(ctx context.Context) (gameReply, error) {
			return client.CutDeck(ctx, &gamev1.CutDeckRequest{GameId: "demo", DealNumber: 1, PlayerId: "player-3", CutPosition: 20})
		}},
		{"PlaceBid", gamev1.GamePhase_GAME_PHASE_BIDDING, func(ctx context.Context) (gameReply, error) {
			return client.PlaceBid(ctx, &gamev1.PlaceBidRequest{GameId: "demo", DealNumber: 1, PlayerId: "player-0", Amount: 8})
		}},
		{"PlayCard", gamev1.GamePhase_GAME_PHASE_PLAYING, func(ctx context.Context) (gameReply, error) {
			return client.PlayCard(ctx, &gamev1.PlayCardRequest{GameId: "demo", DealNumber: 1, PlayerId: "player-0", Card: "10H"})
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reply, err := tc.call(ctx)
			if err != nil {
				t.Fatal(err)
			}
			g := reply.GetGame()
			if reply.GetNodeId() != "n1" || g == nil || g.GetId() == "" || g.GetPhase() != tc.phase {
				t.Fatalf("unexpected response: %v", reply)
			}
			for _, player := range g.GetPlayers() {
				if player.GetSeat() < 0 || player.GetSeat() > 3 || player.GetTeam() != player.GetSeat()%2 {
					t.Fatalf("invalid player: %v", player)
				}
			}
			if tc.name == "CreateGame" && (len(g.Players) != 1 || g.Players[0].Name != "Kareem" || g.CurrentDeal != nil) {
				t.Fatalf("invalid new game: %v", g)
			}
			if tc.name == "PlaceBid" && g.CurrentDeal.Bids[0].Points != 16 {
				t.Fatalf("bid 8 should be worth 16: %v", g.CurrentDeal.Bids)
			}
			if tc.name == "PlayCard" && (g.CurrentDeal.GetNextSeat() != 1 || g.CurrentDeal.PlayerStates[0].CardsRemaining != 12) {
				t.Fatalf("invalid play snapshot: %v", g.CurrentDeal)
			}
		})
	}
}

func TestReadConsistencyModes(t *testing.T) {
	client := testClient(t, "n1")
	ctx := testContext(t)
	for _, mode := range []gamev1.Consistency{
		gamev1.Consistency_CONSISTENCY_UNSPECIFIED,
		gamev1.Consistency_CONSISTENCY_SEQUENTIAL,
		gamev1.Consistency_CONSISTENCY_LINEARIZABLE,
	} {
		if _, err := client.GetGame(ctx, &gamev1.GetGameRequest{GameId: "demo", Consistency: mode}); err != nil {
			t.Fatal(err)
		}
	}
	_, err := client.GetGame(ctx, &gamev1.GetGameRequest{GameId: "demo", Consistency: gamev1.Consistency(99)})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("unknown consistency: got %v", err)
	}
}

func TestHandStream(t *testing.T) {
	client := testClient(t, "n1")
	ctx := testContext(t)
	allCards := map[string]bool{}
	ranks := []string{"2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K", "A"}
	for seat := 0; seat < 4; seat++ {
		playerID := fmt.Sprintf("player-%d", seat)
		stream, err := client.GetHand(ctx, &gamev1.GetHandRequest{GameId: "demo", PlayerId: playerID})
		if err != nil {
			t.Fatal(err)
		}
		first, err := stream.Recv()
		if err != nil || first.GetInfo() == nil {
			t.Fatalf("expected HandInfo first: %v, %v", first, err)
		}
		info := first.GetInfo()
		if info.NodeId != "n1" || info.GameId != "demo" || info.PlayerId != playerID || info.DealNumber != 1 {
			t.Fatalf("invalid HandInfo: %v", info)
		}
		count, previous := 0, -1
		for {
			message, err := stream.Recv()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Fatal(err)
			}
			card := message.GetCard()
			if message.GetInfo() != nil || len(card) < 2 {
				t.Fatalf("expected a card: %v", message)
			}
			suit := strings.IndexByte("CDSH", card[len(card)-1])
			rank := slices.Index(ranks, card[:len(card)-1])
			order := suit*13 + rank
			if suit < 0 || rank < 0 || order <= previous || allCards[card] {
				t.Fatalf("invalid, unsorted, or duplicate card %q", card)
			}
			allCards[card] = true
			previous = order
			count++
		}
		if count != 13 {
			t.Fatalf("%s received %d cards", playerID, count)
		}
	}
	if len(allCards) != 52 {
		t.Fatalf("four hands contain %d unique cards", len(allCards))
	}

	stream, err := client.GetHand(ctx, &gamev1.GetHandRequest{GameId: "empty-hand", PlayerId: "player-0"})
	if err != nil {
		t.Fatal(err)
	}
	first, err := stream.Recv()
	if err != nil || first.GetInfo() == nil || first.GetInfo().NodeId != "n1" {
		t.Fatalf("empty hand must still identify the responding node: %v, %v", first, err)
	}
	if _, err := stream.Recv(); err != io.EOF {
		t.Fatalf("empty hand should end after HandInfo: %v", err)
	}
}

func TestRPCStatusErrors(t *testing.T) {
	client := testClient(t, "n1")
	ctx := testContext(t)
	cases := []struct {
		name string
		code codes.Code
		call func() error
	}{
		{"empty name", codes.InvalidArgument, func() error {
			_, err := client.CreateGame(ctx, &gamev1.CreateGameRequest{})
			return err
		}},
		{"missing game", codes.NotFound, func() error {
			_, err := client.GetGame(ctx, &gamev1.GetGameRequest{GameId: "missing"})
			return err
		}},
		{"occupied seat", codes.AlreadyExists, func() error {
			_, err := client.JoinGame(ctx, &gamev1.JoinGameRequest{GameId: "demo", Name: "Player Two", Seat: 0})
			return err
		}},
		{"blocked game", codes.FailedPrecondition, func() error {
			_, err := client.JoinGame(ctx, &gamev1.JoinGameRequest{GameId: "blocked", Name: "Player Two", Seat: 1})
			return err
		}},
		{"wrong dealer", codes.FailedPrecondition, func() error {
			_, err := client.StartDeal(ctx, &gamev1.StartDealRequest{GameId: "demo", PlayerId: "player-1"})
			return err
		}},
		{"invalid cut", codes.InvalidArgument, func() error {
			_, err := client.CutDeck(ctx, &gamev1.CutDeckRequest{GameId: "demo", DealNumber: 1, PlayerId: "player-3", CutPosition: 53})
			return err
		}},
		{"unknown deal", codes.NotFound, func() error {
			_, err := client.CutDeck(ctx, &gamev1.CutDeckRequest{GameId: "demo", DealNumber: 404, PlayerId: "player-3", CutPosition: 20})
			return err
		}},
		{"forbidden bid", codes.InvalidArgument, func() error {
			_, err := client.PlaceBid(ctx, &gamev1.PlaceBidRequest{GameId: "demo", DealNumber: 1, PlayerId: "player-0", Amount: 6})
			return err
		}},
		{"below minimum", codes.FailedPrecondition, func() error {
			_, err := client.PlaceBid(ctx, &gamev1.PlaceBidRequest{GameId: "high-score", DealNumber: 1, PlayerId: "player-0", Amount: 2})
			return err
		}},
		{"invalid card", codes.InvalidArgument, func() error {
			_, err := client.PlayCard(ctx, &gamev1.PlayCardRequest{GameId: "demo", DealNumber: 1, PlayerId: "player-0", Card: "JOKER"})
			return err
		}},
		{"card not held", codes.FailedPrecondition, func() error {
			_, err := client.PlayCard(ctx, &gamev1.PlayCardRequest{GameId: "demo", DealNumber: 1, PlayerId: "player-0", Card: "2C"})
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.call(); status.Code(err) != tc.code {
				t.Fatalf("wanted %s, got %v", tc.code, err)
			}
		})
	}
	for _, tc := range []struct {
		game, player string
		code         codes.Code
	}{
		{"missing", "player-0", codes.NotFound},
		{"blocked", "player-0", codes.FailedPrecondition},
		{"demo", "missing", codes.NotFound},
	} {
		t.Run("hand "+tc.game+" "+tc.player, func(t *testing.T) {
			stream, err := client.GetHand(ctx, &gamev1.GetHandRequest{GameId: tc.game, PlayerId: tc.player})
			if err == nil {
				_, err = stream.Recv()
			}
			if status.Code(err) != tc.code {
				t.Fatalf("wanted %s, got %v", tc.code, err)
			}
		})
	}
}

func TestDummyCallsDoNotStoreState(t *testing.T) {
	client := testClient(t, "n1")
	ctx := testContext(t)
	before, err := client.GetGame(ctx, &gamev1.GetGameRequest{GameId: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.PlayCard(ctx, &gamev1.PlayCardRequest{GameId: "demo", DealNumber: 1, PlayerId: "player-0", Card: "10H"}); err != nil {
		t.Fatal(err)
	}
	after, err := client.GetGame(ctx, &gamev1.GetGameRequest{GameId: "demo"})
	if err != nil {
		t.Fatal(err)
	}
	if !proto.Equal(before, after) {
		t.Fatal("a dummy write changed a later read")
	}
}

func TestRespondingNode(t *testing.T) {
	ctx := testContext(t)
	for _, nodeID := range []string{"n1", "n2"} {
		client := testClient(t, nodeID)
		reply, err := client.GetGame(ctx, &gamev1.GetGameRequest{GameId: "demo"})
		if err != nil {
			t.Fatal(err)
		}
		if reply.NodeId != nodeID || !strings.Contains(reply.Game.Players[0].Name, nodeID) {
			t.Fatalf("response does not identify %s: %v", nodeID, reply)
		}
	}
}
