package httpapi

import (
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"slices"
	"strings"

	gamev1 "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"github.com/KareemAraide/distributed-systems-41/gen/rest"
)

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

func maskFromFields(value *rest.Fields) (*fieldmaskpb.FieldMask, error) {
	if value == nil {
		return nil, nil
	}
	if strings.TrimSpace(string(*value)) == "" {
		return nil, status.Error(codes.InvalidArgument, "fields cannot be empty")
	}
	names := map[string]string{"id": "id", "phase": "phase", "dealerSeat": "dealer_seat", "players": "players", "draw": "draw", "currentDeal": "current_deal", "winnerTeam": "winner_team"}
	mask := &fieldmaskpb.FieldMask{}
	for _, part := range strings.Split(string(*value), ",") {
		name, ok := names[strings.TrimSpace(part)]
		if !ok {
			return nil, status.Error(codes.InvalidArgument, "unknown public field: "+part)
		}
		mask.Paths = append(mask.Paths, name)
	}
	mask.Normalize()
	return mask, nil
}
func viewFromProto(g *gamev1.Game, mask *fieldmaskpb.FieldMask) (rest.GameView, error) {
	if g == nil || g.GetId() == "" {
		return rest.GameView{}, status.Error(codes.Internal, "missing game")
	}
	out := rest.GameView{Id: g.Id}
	keep := func(name string) bool { return len(mask.GetPaths()) == 0 || slices.Contains(mask.Paths, name) }
	if keep("phase") {
		phase, err := gamePhaseFromProto(g.GetPhase())
		if err != nil {
			return out, err
		}
		out.Phase = &phase
	}
	if keep("dealer_seat") {
		seat := rest.Seat(g.GetDealerSeat())
		out.DealerSeat = &seat
	}
	if keep("draw") {
		draw := g.GetDraw()
		out.Draw = &draw
	}
	if keep("players") {
		players := make([]rest.Player, 0, len(g.Players))
		for _, p := range g.Players {
			players = append(players, rest.Player{Id: p.GetId(), Name: p.GetName(), Seat: rest.Seat(p.GetSeat()), Team: rest.Team(p.GetTeam()), Score: p.GetScore(), MinimumBid: rest.BidAmount(p.GetMinimumBid())})
		}
		out.Players = &players
	}
	if keep("current_deal") {
		d, err := dealFromProto(g.GetCurrentDeal())
		if err != nil {
			return out, err
		}
		out.CurrentDeal = d
	}
	if keep("winner_team") && g.WinnerTeam != nil {
		team := rest.Team(*g.WinnerTeam)
		out.WinnerTeam = &team
	}
	return out, nil
}
func actionFromREST(a rest.ActionBody) (*gamev1.GameAction, error) {
	out := &gamev1.GameAction{GameId: a.GameId}
	count := 0
	if a.Join != nil {
		count++
		out.Action = &gamev1.GameAction_Join{Join: &gamev1.JoinAction{Name: a.Join.Name, Seat: int32(a.Join.Seat)}}
	}
	if a.StartDeal != nil {
		count++
		out.Action = &gamev1.GameAction_StartDeal{StartDeal: &gamev1.StartDealAction{PlayerId: a.StartDeal.PlayerId}}
	}
	if a.Bid != nil {
		count++
		out.Action = &gamev1.GameAction_Bid{Bid: &gamev1.BidAction{PlayerId: a.Bid.PlayerId, DealNumber: a.Bid.DealNumber, Amount: int32(a.Bid.Amount)}}
	}
	if a.Play != nil {
		count++
		out.Action = &gamev1.GameAction_Play{Play: &gamev1.PlayAction{PlayerId: a.Play.PlayerId, DealNumber: a.Play.DealNumber, Card: a.Play.Card}}
	}
	if count != 1 {
		return nil, status.Error(codes.InvalidArgument, "each action needs exactly one kind")
	}
	return out, nil
}
func validWireCard(card string) bool {
	if len(card) < 2 || !strings.Contains("CDSH", card[len(card)-1:]) {
		return false
	}
	return slices.Contains([]string{"2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K", "A"}, card[:len(card)-1])
}
