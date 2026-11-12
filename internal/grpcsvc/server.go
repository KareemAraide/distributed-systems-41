// Package grpcsvc implements the GameService gRPC service.
//
// STEP 4 — In Part I every RPC returns a DUMMY response: the goal is only to
// prove that the .proto compiles and that two processes can talk over gRPC.
// In Part II this type will apply operations to the state machine, and in
// Part III it will submit them to the replicated log.
package grpcsvc

import (
	"context"
	"fmt"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	gamev1 "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
)

// Server implements gamev1.GameServiceServer.
type Server struct {
	gamev1.UnimplementedGameServiceServer
	NodeID string // shown in dummy values, so you can see WHICH server answered
}

var _ gamev1.GameServiceServer = (*Server)(nil)

func New(nodeID string) *Server { return &Server{NodeID: nodeID} }

func (s *Server) CreateGame(ctx context.Context, req *gamev1.CreateGameRequest) (*gamev1.CreateGameResponse, error) {
	if req.GetName() == "" {
		return nil, status.Error(codes.InvalidArgument, "name is required")
	}
	g := s.dummyGame("game-" + s.NodeID)
	g.Phase = gamev1.GamePhase_GAME_PHASE_WAITING
	g.Players = g.Players[:1]
	g.Players[0].Name = req.GetName()
	g.CurrentDeal = nil
	return &gamev1.CreateGameResponse{Game: g, NodeId: s.NodeID}, nil
}

func (s *Server) GetGame(ctx context.Context, req *gamev1.GetGameRequest) (*gamev1.GetGameResponse, error) {
	if err := checkGameID(req.GetGameId()); err != nil {
		return nil, err
	}
	if err := checkConsistency(req.GetConsistency()); err != nil {
		return nil, err
	}
	g := s.dummyGame(req.GetGameId())
	if req.GetGameId() == "blocked" {
		prepareCut(g)
	}
	if req.GetGameId() == "high-score" {
		g.Players[0].Score = 30
		g.Players[0].MinimumBid = 3
	}
	if req.GetGameId() == "empty-hand" {
		prepareCompletedDeal(g)
	}
	return &gamev1.GetGameResponse{Game: g, NodeId: s.NodeID}, nil
}

func (s *Server) JoinGame(ctx context.Context, req *gamev1.JoinGameRequest) (*gamev1.JoinGameResponse, error) {
	if err := checkGameAction(req.GetGameId()); err != nil {
		return nil, err
	}
	if req.GetName() == "" || req.GetSeat() < 0 || req.GetSeat() > 3 {
		return nil, status.Error(codes.InvalidArgument, "give a name and a seat from 0 to 3")
	}
	if req.GetSeat() == 0 {
		return nil, status.Error(codes.AlreadyExists, "seat 0 is occupied by the creator")
	}
	g := s.dummyGame(req.GetGameId())
	p := g.Players[req.GetSeat()]
	p.Name = req.GetName()
	g.Players = []*gamev1.Player{g.Players[0], p}
	g.Phase = gamev1.GamePhase_GAME_PHASE_WAITING
	g.CurrentDeal = nil
	return &gamev1.JoinGameResponse{Game: g, NodeId: s.NodeID}, nil
}

func (s *Server) StartDeal(ctx context.Context, req *gamev1.StartDealRequest) (*gamev1.StartDealResponse, error) {
	if err := checkGameAction(req.GetGameId()); err != nil {
		return nil, err
	}
	if err := checkActor(req.GetPlayerId(), 0); err != nil {
		return nil, err
	}
	g := s.dummyGame(req.GetGameId())
	prepareCut(g)
	return &gamev1.StartDealResponse{Game: g, NodeId: s.NodeID}, nil
}

func (s *Server) CutDeck(ctx context.Context, req *gamev1.CutDeckRequest) (*gamev1.CutDeckResponse, error) {
	if err := checkGameAction(req.GetGameId()); err != nil {
		return nil, err
	}
	if err := checkDeal(req.GetDealNumber()); err != nil {
		return nil, err
	}
	if req.GetCutPosition() < 1 || req.GetCutPosition() > 52 {
		return nil, status.Error(codes.InvalidArgument, "cutPosition must be from 1 to 52")
	}
	if err := checkActor(req.GetPlayerId(), 3); err != nil {
		return nil, err
	}
	g := s.dummyGame(req.GetGameId())
	prepareBidding(g)
	g.CurrentDeal.CutPosition = int32Ptr(req.GetCutPosition())
	return &gamev1.CutDeckResponse{Game: g, NodeId: s.NodeID}, nil
}

func (s *Server) PlaceBid(ctx context.Context, req *gamev1.PlaceBidRequest) (*gamev1.PlaceBidResponse, error) {
	if err := checkGameAction(req.GetGameId()); err != nil {
		return nil, err
	}
	if err := checkDeal(req.GetDealNumber()); err != nil {
		return nil, err
	}
	points, err := bidPoints(req.GetAmount())
	if err != nil {
		return nil, err
	}
	if err := checkActor(req.GetPlayerId(), 0); err != nil {
		return nil, err
	}
	g := s.dummyGame(req.GetGameId())
	if req.GetGameId() == "high-score" {
		g.Players[0].Score = 30
		g.Players[0].MinimumBid = 3
	}
	if req.GetAmount() < g.Players[0].MinimumBid {
		return nil, status.Error(codes.FailedPrecondition, "bid is below the player's minimum")
	}
	prepareBidding(g)
	g.CurrentDeal.Bids = []*gamev1.Bid{{
		PlayerId: req.GetPlayerId(), Amount: req.GetAmount(), Points: points,
	}}
	g.CurrentDeal.NextSeat = int32Ptr(1)
	return &gamev1.PlaceBidResponse{Game: g, NodeId: s.NodeID}, nil
}

func (s *Server) PlayCard(ctx context.Context, req *gamev1.PlayCardRequest) (*gamev1.PlayCardResponse, error) {
	if err := checkGameAction(req.GetGameId()); err != nil {
		return nil, err
	}
	if err := checkDeal(req.GetDealNumber()); err != nil {
		return nil, err
	}
	if !validCard(req.GetCard()) {
		return nil, status.Error(codes.InvalidArgument, "card must be a rank followed by C, D, S, or H")
	}
	if err := checkActor(req.GetPlayerId(), 0); err != nil {
		return nil, err
	}
	ownsCard := false
	for _, card := range dummyHand(0) {
		if card == req.GetCard() {
			ownsCard = true
			break
		}
	}
	if !ownsCard {
		return nil, status.Error(codes.FailedPrecondition, "card is not in the dummy hand")
	}
	g := s.dummyGame(req.GetGameId())
	g.CurrentDeal.CurrentTrick.Plays = []*gamev1.CardPlay{{
		PlayerId: req.GetPlayerId(), Card: req.GetCard(),
	}}
	g.CurrentDeal.PlayerStates[0].CardsRemaining = 12
	g.CurrentDeal.NextSeat = int32Ptr(1)
	return &gamev1.PlayCardResponse{Game: g, NodeId: s.NodeID}, nil
}

// GetHand sends its metadata first, even if there are no cards.
func (s *Server) GetHand(req *gamev1.GetHandRequest, stream grpc.ServerStreamingServer[gamev1.GetHandResponse]) error {
	if err := checkGameAction(req.GetGameId()); err != nil {
		return err
	}
	if err := checkConsistency(req.GetConsistency()); err != nil {
		return err
	}
	seat, err := playerSeat(req.GetPlayerId())
	if err != nil {
		return err
	}
	info := &gamev1.HandInfo{
		GameId: req.GetGameId(), DealNumber: 1,
		PlayerId: req.GetPlayerId(), NodeId: s.NodeID,
	}
	if err := stream.Send(&gamev1.GetHandResponse{
		Item: &gamev1.GetHandResponse_Info{Info: info},
	}); err != nil {
		return err
	}
	if req.GetGameId() == "empty-hand" {
		return nil
	}
	for _, card := range dummyHand(seat) {
		if err := stream.Send(&gamev1.GetHandResponse{
			Item: &gamev1.GetHandResponse_Card{Card: card},
		}); err != nil {
			return err
		}
	}
	return nil
}

// Each call gets a fresh snapshot. Nothing survives between RPCs.
func (s *Server) dummyGame(id string) *gamev1.Game {
	g := &gamev1.Game{
		Id: id, Phase: gamev1.GamePhase_GAME_PHASE_PLAYING,
		CurrentDeal: &gamev1.Deal{
			Number: 1, Status: gamev1.DealStatus_DEAL_STATUS_PLAYING,
			NextSeat: int32Ptr(0), CutPosition: int32Ptr(20),
			CurrentTrick: &gamev1.Trick{Number: 1, Plays: []*gamev1.CardPlay{}},
		},
	}
	for seat, amount := range []int32{3, 3, 3, 2} {
		id := fmt.Sprintf("player-%d", seat)
		g.Players = append(g.Players, &gamev1.Player{
			Id: id, Name: fmt.Sprintf("dummy player %d from %s", seat, s.NodeID),
			Seat: int32(seat), Team: int32(seat % 2), MinimumBid: 2,
		})
		g.CurrentDeal.PlayerStates = append(g.CurrentDeal.PlayerStates, &gamev1.DealPlayerState{
			PlayerId: id, CardsRemaining: 13,
		})
		g.CurrentDeal.Bids = append(g.CurrentDeal.Bids, &gamev1.Bid{
			PlayerId: id, Amount: amount, Points: amount,
		})
	}
	return g
}

func prepareCut(g *gamev1.Game) {
	prepareBidding(g)
	g.Phase = gamev1.GamePhase_GAME_PHASE_CUTTING
	g.CurrentDeal.Status = gamev1.DealStatus_DEAL_STATUS_CUTTING
	g.CurrentDeal.NextSeat = int32Ptr(3)
	g.CurrentDeal.CutPosition = nil
	for _, p := range g.CurrentDeal.PlayerStates {
		p.CardsRemaining = 0
	}
}

func prepareBidding(g *gamev1.Game) {
	g.Phase = gamev1.GamePhase_GAME_PHASE_BIDDING
	g.CurrentDeal.Status = gamev1.DealStatus_DEAL_STATUS_BIDDING
	g.CurrentDeal.Bids = []*gamev1.Bid{}
	g.CurrentDeal.CurrentTrick = nil
	g.CurrentDeal.NextSeat = int32Ptr(0)
}

func prepareCompletedDeal(g *gamev1.Game) {
	g.Phase = gamev1.GamePhase_GAME_PHASE_READY
	g.DealerSeat = 1
	d := g.CurrentDeal
	d.Status = gamev1.DealStatus_DEAL_STATUS_COMPLETED
	d.NextSeat = nil
	d.CurrentTrick = nil
	d.CompletedTricks = 13
	d.LastTrick = &gamev1.Trick{
		Number: 13, WinnerSeat: int32Ptr(3),
		Plays: []*gamev1.CardPlay{
			{PlayerId: "player-0", Card: "5C"},
			{PlayerId: "player-1", Card: "6C"},
			{PlayerId: "player-2", Card: "7C"},
			{PlayerId: "player-3", Card: "QC"},
		},
	}
	for seat, wins := range []int32{4, 3, 3, 3} {
		d.PlayerStates[seat].CardsRemaining = 0
		d.PlayerStates[seat].TricksWon = wins
		g.Players[seat].Score = int64(d.Bids[seat].Points)
	}
}

// A fixed deck makes the dummy hands repeatable and keeps the four hands disjoint.
func dummyHand(seat int32) []string {
	var cards []string
	index := 0
	for _, suit := range []string{"C", "D", "S", "H"} {
		for _, rank := range []string{"2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K", "A"} {
			if int32((index+1)%4) == seat {
				cards = append(cards, rank+suit)
			}
			index++
		}
	}
	return cards
}

func checkGameID(id string) error {
	switch id {
	case "":
		return status.Error(codes.InvalidArgument, "gameId is required")
	case "missing":
		return status.Error(codes.NotFound, "game not found")
	default:
		return nil
	}
}

func checkGameAction(id string) error {
	if err := checkGameID(id); err != nil {
		return err
	}
	if id == "blocked" {
		return status.Error(codes.FailedPrecondition, "the dummy game is not ready for this operation")
	}
	return nil
}

func checkDeal(number int64) error {
	if number < 1 {
		return status.Error(codes.InvalidArgument, "dealNumber must be positive")
	}
	if number != 1 {
		return status.Error(codes.NotFound, "the dummy game only has deal 1")
	}
	return nil
}

func playerSeat(id string) (int32, error) {
	if id == "" {
		return 0, status.Error(codes.InvalidArgument, "playerId is required")
	}
	for seat := int32(0); seat < 4; seat++ {
		if id == fmt.Sprintf("player-%d", seat) {
			return seat, nil
		}
	}
	return 0, status.Errorf(codes.NotFound, "player %q not found", id)
}

func checkActor(id string, expected int32) error {
	seat, err := playerSeat(id)
	if err != nil {
		return err
	}
	if seat != expected {
		return status.Errorf(codes.FailedPrecondition, "expected player-%d to act", expected)
	}
	return nil
}

func checkConsistency(c gamev1.Consistency) error {
	switch c {
	case gamev1.Consistency_CONSISTENCY_UNSPECIFIED,
		gamev1.Consistency_CONSISTENCY_SEQUENTIAL,
		gamev1.Consistency_CONSISTENCY_LINEARIZABLE:
		return nil
	default:
		return status.Error(codes.InvalidArgument, "unknown consistency level")
	}
}

func bidPoints(amount int32) (int32, error) {
	if amount >= 2 && amount <= 5 {
		return amount, nil
	}
	if amount >= 7 && amount <= 13 {
		return amount * 2, nil
	}
	return 0, status.Error(codes.InvalidArgument, "bid must be 2-13, excluding 6")
}

func validCard(card string) bool {
	if len(card) < 2 {
		return false
	}
	switch card[len(card)-1] {
	case 'C', 'D', 'S', 'H':
	default:
		return false
	}
	switch card[:len(card)-1] {
	case "2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K", "A":
		return true
	default:
		return false
	}
}

func int32Ptr(value int32) *int32 { return &value }
