package httpapi

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	gamev1 "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"github.com/KareemAraide/distributed-systems-41/gen/rest"
)

func toProtoConsistency(value *rest.Consistency) (gamev1.Consistency, error) {
	if value == nil || string(*value) == "sequential" {
		return gamev1.Consistency_CONSISTENCY_SEQUENTIAL, nil
	}
	if string(*value) == "linearizable" {
		return gamev1.Consistency_CONSISTENCY_LINEARIZABLE, nil
	}
	return 0, status.Error(codes.InvalidArgument, "consistency must be sequential or linearizable")
}

func gameResponseFromProto(game *gamev1.Game, nodeID string) (rest.GameResponse, error) {
	if game == nil || nodeID == "" {
		return rest.GameResponse{}, status.Error(codes.Internal, "gRPC reply is missing its game or node ID")
	}
	phase, err := gamePhaseFromProto(game.GetPhase())
	if err != nil {
		return rest.GameResponse{}, err
	}
	out := rest.Game{
		Id: game.GetId(), Phase: phase, DealerSeat: rest.Seat(game.GetDealerSeat()),
		Draw: game.GetDraw(), Players: make([]rest.Player, 0, len(game.GetPlayers())),
	}
	for _, player := range game.GetPlayers() {
		out.Players = append(out.Players, rest.Player{
			Id: player.GetId(), Name: player.GetName(), Seat: rest.Seat(player.GetSeat()),
			Team: rest.Team(player.GetTeam()), Score: player.GetScore(),
			MinimumBid: rest.BidAmount(player.GetMinimumBid()),
		})
	}
	if game.WinnerTeam != nil {
		team := rest.Team(*game.WinnerTeam)
		out.WinnerTeam = &team
	}
	out.CurrentDeal, err = dealFromProto(game.GetCurrentDeal())
	if err != nil {
		return rest.GameResponse{}, err
	}
	return rest.GameResponse{Game: out, NodeId: nodeID}, nil
}

func dealFromProto(deal *gamev1.Deal) (*rest.Deal, error) {
	if deal == nil {
		return nil, nil
	}
	dealStatus, err := dealStatusFromProto(deal.GetStatus())
	if err != nil {
		return nil, err
	}
	out := &rest.Deal{
		Number: deal.GetNumber(), Status: dealStatus, DealerSeat: rest.Seat(deal.GetDealerSeat()),
		CompletedTricks: deal.GetCompletedTricks(), NextSeat: seatFromProto(deal.NextSeat),
		CutPosition:  deal.CutPosition,
		Bids:         make([]rest.Bid, 0, len(deal.GetBids())),
		PlayerStates: make([]rest.DealPlayerState, 0, len(deal.GetPlayerStates())),
		CurrentTrick: trickFromProto(deal.GetCurrentTrick()),
		LastTrick:    trickFromProto(deal.GetLastTrick()),
	}
	for _, bid := range deal.GetBids() {
		out.Bids = append(out.Bids, rest.Bid{
			PlayerId: bid.GetPlayerId(), Amount: rest.BidAmount(bid.GetAmount()),
			Points: rest.BidPoints(bid.GetPoints()),
		})
	}
	for _, player := range deal.GetPlayerStates() {
		out.PlayerStates = append(out.PlayerStates, rest.DealPlayerState{
			PlayerId: player.GetPlayerId(), CardsRemaining: player.GetCardsRemaining(),
			TricksWon: player.GetTricksWon(),
		})
	}
	return out, nil
}

func trickFromProto(trick *gamev1.Trick) *rest.Trick {
	if trick == nil {
		return nil
	}
	out := &rest.Trick{
		Number: trick.GetNumber(), LeaderSeat: rest.Seat(trick.GetLeaderSeat()),
		WinnerSeat: seatFromProto(trick.WinnerSeat),
		Plays:      make([]rest.CardPlay, 0, len(trick.GetPlays())),
	}
	for _, play := range trick.GetPlays() {
		out.Plays = append(out.Plays, rest.CardPlay{
			PlayerId: play.GetPlayerId(), Card: play.GetCard(),
		})
	}
	return out
}

// A nil pointer means absent; a pointer to zero means seat 0.
func seatFromProto(value *int32) *rest.Seat {
	if value == nil {
		return nil
	}
	seat := rest.Seat(*value)
	return &seat
}

func gamePhaseFromProto(phase gamev1.GamePhase) (rest.GamePhase, error) {
	switch phase {
	case gamev1.GamePhase_GAME_PHASE_WAITING:
		return rest.GamePhase("waiting"), nil
	case gamev1.GamePhase_GAME_PHASE_READY:
		return rest.GamePhase("ready"), nil
	case gamev1.GamePhase_GAME_PHASE_CUTTING:
		return rest.GamePhase("cutting"), nil
	case gamev1.GamePhase_GAME_PHASE_BIDDING:
		return rest.GamePhase("bidding"), nil
	case gamev1.GamePhase_GAME_PHASE_PLAYING:
		return rest.GamePhase("playing"), nil
	case gamev1.GamePhase_GAME_PHASE_FINISHED:
		return rest.GamePhase("finished"), nil
	default:
		return "", status.Errorf(codes.Internal, "unknown game phase from gRPC: %d", phase)
	}
}

func dealStatusFromProto(value gamev1.DealStatus) (rest.DealStatus, error) {
	switch value {
	case gamev1.DealStatus_DEAL_STATUS_CUTTING:
		return rest.DealStatus("cutting"), nil
	case gamev1.DealStatus_DEAL_STATUS_BIDDING:
		return rest.DealStatus("bidding"), nil
	case gamev1.DealStatus_DEAL_STATUS_PLAYING:
		return rest.DealStatus("playing"), nil
	case gamev1.DealStatus_DEAL_STATUS_SKIPPED:
		return rest.DealStatus("skipped"), nil
	case gamev1.DealStatus_DEAL_STATUS_COMPLETED:
		return rest.DealStatus("completed"), nil
	default:
		return "", status.Errorf(codes.Internal, "unknown deal status from gRPC: %d", value)
	}
}
