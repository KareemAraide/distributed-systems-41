package sm

import (
	pb "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"slices"
	"strings"
)

// Mask paths are the public top-level sections only. id is always returned.
func ValidMask(mask *fieldmaskpb.FieldMask) bool {
	for _, p := range mask.GetPaths() {
		switch p {
		case "id", "phase", "dealer_seat", "players", "draw", "current_deal", "winner_team":
		default:
			return false
		}
	}
	return true
}
func publicGame(g *pb.StoredGame) *pb.Game {
	out := &pb.Game{Id: g.Id, Phase: g.Phase, DealerSeat: g.DealerSeat, Draw: g.Draw}
	if g.WinnerTeam != nil {
		out.WinnerTeam = proto.Int32(*g.WinnerTeam)
	}
	for _, p := range g.Participants {
		out.Players = append(out.Players, &pb.Player{Id: p.Id, Name: p.Name, Seat: p.Seat, Team: p.Seat % 2, Score: p.Score, MinimumBid: MinimumBid(p.Score)})
	}
	slices.SortFunc(out.Players, func(a, b *pb.Player) int { return int(a.Seat - b.Seat) })
	if d := g.Deal; d != nil {
		out.CurrentDeal = &pb.Deal{Number: d.Number, Status: d.Status, DealerSeat: d.DealerSeat, CompletedTricks: d.CompletedTricks}
		if d.NextSeat != nil {
			out.CurrentDeal.NextSeat = proto.Int32(*d.NextSeat)
		}
		for _, b := range d.Bids {
			out.CurrentDeal.Bids = append(out.CurrentDeal.Bids, proto.Clone(b).(*pb.Bid))
		}
		if d.CurrentTrick != nil {
			out.CurrentDeal.CurrentTrick = proto.Clone(d.CurrentTrick).(*pb.Trick)
		}
		if d.LastTrick != nil {
			out.CurrentDeal.LastTrick = proto.Clone(d.LastTrick).(*pb.Trick)
		}
		for _, p := range g.Participants {
			wins := int32(0)
			if int(p.Seat) < len(d.TricksWon) {
				wins = d.TricksWon[p.Seat]
			}
			out.CurrentDeal.PlayerStates = append(out.CurrentDeal.PlayerStates, &pb.DealPlayerState{PlayerId: p.Id, CardsRemaining: int32(len(p.Hand)), TricksWon: wins})
		}
	}
	return out
}
func readGames(s *pb.State, c *pb.ReadGames) *pb.Result {
	if len(c.GetGameIds()) < 1 || len(c.GetGameIds()) > MaxBatch {
		return fail(pb.Code_CODE_INVALID_ARGUMENT, "give 1-100 distinct game IDs")
	}
	if !ValidMask(c.GetReadMask()) {
		return fail(pb.Code_CODE_INVALID_ARGUMENT, "unknown public field mask")
	}
	seen := map[string]bool{}
	r := &pb.Result{}
	for _, id := range c.GetGameIds() {
		if id == "" || seen[id] {
			return fail(pb.Code_CODE_INVALID_ARGUMENT, "game IDs must be nonempty and distinct")
		}
		seen[id] = true
		g, f := game(s, id)
		if f != nil {
			return f
		}
		view := publicGame(g)
		if len(c.GetReadMask().GetPaths()) > 0 {
			keep := map[string]bool{"id": true}
			for _, path := range c.ReadMask.Paths {
				keep[path] = true
			}
			message := view.ProtoReflect()
			fields := message.Descriptor().Fields()
			for i := 0; i < fields.Len(); i++ {
				field := fields.Get(i)
				if !keep[string(field.Name())] {
					message.Clear(field)
				}
			}
		}
		r.Games = append(r.Games, view)
	}
	return r
}
func readHand(s *pb.State, c *pb.ReadHand) *pb.Result {
	g, f := game(s, c.GetGameId())
	if f != nil {
		return f
	}
	p, f := participant(g, c.GetPlayerId())
	if f != nil {
		return f
	}
	if g.Deal == nil {
		return fail(pb.Code_CODE_FAILED_PRECONDITION, "no deal has started")
	}
	cards := append([]string{}, p.Hand...)
	slices.SortFunc(cards, func(a, b string) int {
		ai, bi := strings.IndexByte("CDSH", a[len(a)-1]), strings.IndexByte("CDSH", b[len(b)-1])
		if ai != bi {
			return ai - bi
		}
		return rank(a) - rank(b)
	})
	return &pb.Result{Hand: &pb.Hand{GameId: g.Id, DealNumber: g.Deal.Number, PlayerId: p.Id, Cards: cards}}
}
