package sm

import (
	pb "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"google.golang.org/protobuf/proto"
	"math"
	"slices"
	"strings"
)

func join(s *pb.State, g *pb.StoredGame, a *pb.Join) *pb.Result {
	if !validName(a.GetName()) || a.GetSeat() < 0 || a.GetSeat() > 3 {
		return fail(pb.Code_CODE_INVALID_ARGUMENT, "give a name and a seat from 0 to 3")
	}
	if g.GetDeal() != nil {
		return fail(pb.Code_CODE_FAILED_PRECONDITION, "membership is fixed after the first deal starts")
	}
	for _, p := range g.Participants {
		if p.Seat == a.GetSeat() {
			return fail(pb.Code_CODE_ALREADY_EXISTS, "seat is occupied")
		}
	}
	id, f := nextPlayerID(s)
	if f != nil {
		return f
	}
	g.Participants = append(g.Participants, &pb.Participant{Id: id, Name: strings.TrimSpace(a.GetName()), Seat: a.GetSeat()})
	slices.SortFunc(g.Participants, func(a, b *pb.Participant) int { return int(a.Seat - b.Seat) })
	if len(g.Participants) == 4 {
		g.Phase = pb.GamePhase_GAME_PHASE_READY
	}
	return nil
}
func startDeal(g *pb.StoredGame, a *pb.StartDeal) *pb.Result {
	p, f := participant(g, a.GetPlayerId())
	if f != nil {
		return f
	}
	if g.GetPhase() != pb.GamePhase_GAME_PHASE_READY || p.Seat != g.DealerSeat || len(g.Participants) != 4 {
		return fail(pb.Code_CODE_FAILED_PRECONDITION, "only the dealer may start a ready game")
	}
	if !validDeck(a.GetDeck()) {
		return fail(pb.Code_CODE_INVALID_ARGUMENT, "deck must contain all 52 distinct cards")
	}
	number := int64(1)
	if g.Deal != nil {
		if g.Deal.Number == math.MaxInt64 {
			return fail(pb.Code_CODE_RESOURCE_EXHAUSTED, "deal numbers exhausted")
		}
		number = g.Deal.Number + 1
	}
	for _, p := range g.Participants {
		p.Hand = nil
	}
	for i, card := range a.Deck {
		seat := (int(g.DealerSeat) + 1 + i) % 4
		g.Participants[seat].Hand = append(g.Participants[seat].Hand, card)
	}
	g.Deal = &pb.StoredDeal{Number: number, Status: pb.DealStatus_DEAL_STATUS_BIDDING, DealerSeat: g.DealerSeat, NextSeat: proto.Int32(g.DealerSeat), TricksWon: make([]int32, 4)}
	g.Phase = pb.GamePhase_GAME_PHASE_BIDDING
	return nil
}
func actor(g *pb.StoredGame, id string, number int64, phase pb.GamePhase) (*pb.Participant, *pb.Result) {
	if number < 1 {
		return nil, fail(pb.Code_CODE_INVALID_ARGUMENT, "dealNumber must be positive")
	}
	p, f := participant(g, id)
	if f != nil {
		return nil, f
	}
	if g.Deal == nil || g.Deal.Number != number {
		return nil, fail(pb.Code_CODE_NOT_FOUND, "deal is not the current deal")
	}
	if g.Phase != phase || g.Deal.NextSeat == nil || *g.Deal.NextSeat != p.Seat {
		return nil, fail(pb.Code_CODE_FAILED_PRECONDITION, "wrong phase or turn")
	}
	return p, nil
}
func MinimumBid(score int64) int32 {
	switch {
	case score < 30:
		return 2
	case score < 40:
		return 3
	case score < 50:
		return 4
	case score < 70:
		return 5
	case score >= 130:
		return 13
	default:
		return int32(score / 10)
	}
}
func bidPoints(amount int32) int32 {
	if amount >= 7 {
		return amount * 2
	}
	return amount
}
func validBid(amount int32) bool { return amount >= 2 && amount <= 13 && amount != 6 }
func placeBid(g *pb.StoredGame, a *pb.PlaceBid) *pb.Result {
	if !validBid(a.GetAmount()) {
		return fail(pb.Code_CODE_INVALID_ARGUMENT, "bid must be 2-13, excluding 6")
	}
	p, f := actor(g, a.GetPlayerId(), a.GetDealNumber(), pb.GamePhase_GAME_PHASE_BIDDING)
	if f != nil {
		return f
	}
	if a.GetAmount() < MinimumBid(p.Score) {
		return fail(pb.Code_CODE_FAILED_PRECONDITION, "bid is below the player's minimum")
	}
	d := g.Deal
	d.Bids = append(d.Bids, &pb.Bid{PlayerId: p.Id, Amount: a.Amount, Points: bidPoints(a.Amount)})
	if len(d.Bids) < 4 {
		d.NextSeat = proto.Int32((p.Seat + 1) % 4)
		return nil
	}
	total := int32(0)
	for _, b := range d.Bids {
		total += b.Amount
	}
	if total < 11 {
		d.Status = pb.DealStatus_DEAL_STATUS_SKIPPED
		d.NextSeat = nil
		g.Phase = pb.GamePhase_GAME_PHASE_READY
		g.DealerSeat = (g.DealerSeat + 1) % 4
	} else {
		d.Status = pb.DealStatus_DEAL_STATUS_PLAYING
		d.NextSeat = proto.Int32(d.DealerSeat)
		d.CurrentTrick = &pb.Trick{Number: 1, LeaderSeat: d.DealerSeat}
		g.Phase = pb.GamePhase_GAME_PHASE_PLAYING
	}
	return nil
}
func ValidCard(card string) bool {
	if len(card) < 2 {
		return false
	}
	suit := card[len(card)-1:]
	return strings.Contains("CDSH", suit) && suit != "" && rank(card) > 0
}
func rank(card string) int {
	if len(card) < 2 {
		return 0
	}
	switch card[:len(card)-1] {
	case "2":
		return 2
	case "3":
		return 3
	case "4":
		return 4
	case "5":
		return 5
	case "6":
		return 6
	case "7":
		return 7
	case "8":
		return 8
	case "9":
		return 9
	case "10":
		return 10
	case "J":
		return 11
	case "Q":
		return 12
	case "K":
		return 13
	case "A":
		return 14
	}
	return 0
}
func validDeck(deck []string) bool {
	if len(deck) != 52 {
		return false
	}
	seen := map[string]bool{}
	for _, c := range deck {
		if !ValidCard(c) || seen[c] {
			return false
		}
		seen[c] = true
	}
	return true
}
func playCard(g *pb.StoredGame, a *pb.PlayCard) *pb.Result {
	if !ValidCard(a.GetCard()) {
		return fail(pb.Code_CODE_INVALID_ARGUMENT, "card must be a rank followed by C, D, S, or H")
	}
	p, f := actor(g, a.GetPlayerId(), a.GetDealNumber(), pb.GamePhase_GAME_PHASE_PLAYING)
	if f != nil {
		return f
	}
	index := slices.Index(p.Hand, a.Card)
	if index < 0 {
		return fail(pb.Code_CODE_FAILED_PRECONDITION, "card is not in this player's hand")
	}
	d := g.Deal
	t := d.CurrentTrick
	if len(t.GetPlays()) > 0 {
		lead := t.Plays[0].Card[len(t.Plays[0].Card)-1]
		if a.Card[len(a.Card)-1] != lead {
			for _, c := range p.Hand {
				if c[len(c)-1] == lead {
					return fail(pb.Code_CODE_FAILED_PRECONDITION, "must follow the leading suit")
				}
			}
		}
	}
	p.Hand = append(p.Hand[:index], p.Hand[index+1:]...)
	t.Plays = append(t.Plays, &pb.CardPlay{PlayerId: p.Id, Card: a.Card})
	if len(t.Plays) < 4 {
		d.NextSeat = proto.Int32((p.Seat + 1) % 4)
		return nil
	}
	winner := trickWinner(t)
	t.WinnerSeat = proto.Int32(winner)
	d.LastTrick = t
	d.CompletedTricks++
	d.TricksWon[winner]++
	if d.CompletedTricks == 13 {
		d.CurrentTrick = nil
		d.NextSeat = nil
		d.Status = pb.DealStatus_DEAL_STATUS_COMPLETED
		return scoreDeal(g)
	}
	d.CurrentTrick = &pb.Trick{Number: d.CompletedTricks + 1, LeaderSeat: winner}
	d.NextSeat = proto.Int32(winner)
	return nil
}
func trickWinner(t *pb.Trick) int32 {
	best := 0
	lead := t.Plays[0].Card[len(t.Plays[0].Card)-1]
	for i := 1; i < len(t.Plays); i++ {
		a, b := t.Plays[i].Card, t.Plays[best].Card
		as, bs := a[len(a)-1], b[len(b)-1]
		if (as == 'H' && bs != 'H') || (as == bs && rank(a) > rank(b)) || (bs != 'H' && bs != lead && as == lead) {
			best = i
		}
	}
	return (t.LeaderSeat + int32(best)) % 4
}
func scoreDeal(g *pb.StoredGame) *pb.Result {
	d := g.Deal
	for _, b := range d.Bids {
		p, f := participant(g, b.PlayerId)
		if f != nil {
			return f
		}
		delta := int64(bidPoints(b.Amount))
		if d.TricksWon[p.Seat] < b.Amount {
			delta = -delta
		}
		if (delta > 0 && p.Score > math.MaxInt64-delta) || (delta < 0 && p.Score < math.MinInt64-delta) {
			return fail(pb.Code_CODE_RESOURCE_EXHAUSTED, "score range exhausted")
		}
		p.Score += delta
	}
	qualifies := [2]bool{}
	for _, p := range g.Participants {
		if p.Score >= 41 {
			qualifies[p.Seat%2] = true
		}
	}
	g.Draw = qualifies[0] && qualifies[1]
	if qualifies[0] != qualifies[1] {
		winner := int32(0)
		if qualifies[1] {
			winner = 1
		}
		g.Phase = pb.GamePhase_GAME_PHASE_FINISHED
		g.WinnerTeam = proto.Int32(winner)
	} else {
		g.Phase = pb.GamePhase_GAME_PHASE_READY
		g.DealerSeat = (g.DealerSeat + 1) % 4
		g.WinnerTeam = nil
	}
	return nil
}
