package sm

import (
	"fmt"
	pb "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
)

// Snapshot files are trusted input, but malformed state must fail at startup,
// rather than panic later in Execute. Restore itself remains a pure copy.
func ValidateSnapshot(snap *pb.Snapshot) error {
	if snap == nil || snap.GetState() == nil || snap.GetApplied() < 0 {
		return fmt.Errorf("snapshot needs a state and a nonnegative applied count")
	}
	s := snap.State
	if s.GameSequence < 0 || s.PlayerSequence < 0 || s.GameLimit < 0 {
		return fmt.Errorf("negative snapshot counter or limit")
	}
	limit := s.GameLimit
	if limit == 0 {
		limit = DefaultGameLimit
	}
	if len(s.Games) > int(limit) {
		return fmt.Errorf("snapshot exceeds game capacity")
	}
	ids := map[string]bool{}
	// Validation outcomes do not depend on map order: errors have one fixed message.
	for id, g := range s.Games {
		if !validStoredGame(id, g, ids) {
			return fmt.Errorf("snapshot contains an invalid game")
		}
	}
	return nil
}
func validStoredGame(id string, g *pb.StoredGame, ids map[string]bool) bool {
	if g == nil || id == "" || g.Id != id || g.DealerSeat < 0 || g.DealerSeat > 3 || len(g.Participants) < 1 || len(g.Participants) > 4 {
		return false
	}
	seats := map[int32]bool{}
	cards := map[string]bool{}
	previous := int32(-1)
	for _, p := range g.Participants {
		if p == nil || p.Id == "" || ids[p.Id] || !validName(p.Name) || p.Seat < 0 || p.Seat > 3 || p.Seat <= previous || seats[p.Seat] || len(p.Hand) > 13 {
			return false
		}
		ids[p.Id] = true
		seats[p.Seat] = true
		previous = p.Seat
		for _, c := range p.Hand {
			if !ValidCard(c) || cards[c] {
				return false
			}
			cards[c] = true
		}
	}
	if !seats[0] {
		return false
	}
	switch g.Phase {
	case pb.GamePhase_GAME_PHASE_WAITING:
		if len(g.Participants) == 4 || g.Deal != nil {
			return false
		}
	case pb.GamePhase_GAME_PHASE_READY:
		if len(g.Participants) != 4 {
			return false
		}
	case pb.GamePhase_GAME_PHASE_BIDDING, pb.GamePhase_GAME_PHASE_PLAYING, pb.GamePhase_GAME_PHASE_FINISHED:
		if len(g.Participants) != 4 || g.Deal == nil {
			return false
		}
	default:
		return false
	}
	if (g.Phase == pb.GamePhase_GAME_PHASE_FINISHED) != (g.WinnerTeam != nil) {
		return false
	}
	if g.WinnerTeam != nil && (*g.WinnerTeam < 0 || *g.WinnerTeam > 1) {
		return false
	}
	d := g.Deal
	if d == nil {
		for _, p := range g.Participants {
			if len(p.Hand) != 0 {
				return false
			}
		}
		return !g.Draw
	}
	if d.Number < 1 || d.DealerSeat < 0 || d.DealerSeat > 3 || len(d.TricksWon) != 4 || d.CompletedTricks < 0 || d.CompletedTricks > 13 || len(d.Bids) > 4 {
		return false
	}
	sum := int32(0)
	for _, n := range d.TricksWon {
		if n < 0 || n > 13 {
			return false
		}
		sum += n
	}
	if sum != d.CompletedTricks {
		return false
	}
	for i, b := range d.Bids {
		if b == nil || !validBid(b.Amount) || b.PlayerId != g.Participants[(int(d.DealerSeat)+i)%4].Id || b.Points != bidPoints(b.Amount) {
			return false
		}
	}
	if d.NextSeat != nil && (*d.NextSeat < 0 || *d.NextSeat > 3) {
		return false
	}
	switch d.Status {
	case pb.DealStatus_DEAL_STATUS_BIDDING:
		if g.Phase != pb.GamePhase_GAME_PHASE_BIDDING || len(d.Bids) >= 4 || d.CompletedTricks != 0 || d.CurrentTrick != nil || d.NextSeat == nil || *d.NextSeat != (d.DealerSeat+int32(len(d.Bids)))%4 {
			return false
		}
	case pb.DealStatus_DEAL_STATUS_PLAYING:
		if g.Phase != pb.GamePhase_GAME_PHASE_PLAYING || len(d.Bids) != 4 || d.CompletedTricks >= 13 || d.CurrentTrick == nil || d.NextSeat == nil {
			return false
		}
		t := d.CurrentTrick
		if !validTrick(g, t, false, cards) || t.Number != d.CompletedTricks+1 || *d.NextSeat != (t.LeaderSeat+int32(len(t.Plays)))%4 {
			return false
		}
	case pb.DealStatus_DEAL_STATUS_SKIPPED:
		total := int32(0)
		for _, b := range d.Bids {
			total += b.Amount
		}
		if g.Phase != pb.GamePhase_GAME_PHASE_READY || len(d.Bids) != 4 || total >= 11 || d.CompletedTricks != 0 || d.NextSeat != nil || d.CurrentTrick != nil || g.DealerSeat != (d.DealerSeat+1)%4 {
			return false
		}
	case pb.DealStatus_DEAL_STATUS_COMPLETED:
		if len(d.Bids) != 4 || d.CompletedTricks != 13 || d.NextSeat != nil || d.CurrentTrick != nil || (g.Phase != pb.GamePhase_GAME_PHASE_READY && g.Phase != pb.GamePhase_GAME_PHASE_FINISHED) {
			return false
		}
	default:
		return false
	}
	if d.LastTrick != nil && (!validTrick(g, d.LastTrick, true, cards) || d.LastTrick.Number != d.CompletedTricks) {
		return false
	}
	for _, p := range g.Participants {
		expected := 13 - int(d.CompletedTricks)
		if d.CurrentTrick != nil {
			for _, play := range d.CurrentTrick.Plays {
				if play.PlayerId == p.Id {
					expected--
				}
			}
		}
		if len(p.Hand) != expected {
			return false
		}
	}
	if g.Phase == pb.GamePhase_GAME_PHASE_BIDDING || g.Phase == pb.GamePhase_GAME_PHASE_PLAYING {
		if g.DealerSeat != d.DealerSeat {
			return false
		}
	}
	return true
}
func validTrick(g *pb.StoredGame, t *pb.Trick, resolved bool, cards map[string]bool) bool {
	if t.Number < 1 || t.Number > 13 || t.LeaderSeat < 0 || t.LeaderSeat > 3 {
		return false
	}
	if resolved {
		if len(t.Plays) != 4 || t.WinnerSeat == nil || *t.WinnerSeat < 0 || *t.WinnerSeat > 3 {
			return false
		}
	} else if len(t.Plays) > 3 || t.WinnerSeat != nil {
		return false
	}
	for i, play := range t.Plays {
		if play == nil || play.PlayerId != g.Participants[(int(t.LeaderSeat)+i)%4].Id || !ValidCard(play.Card) || cards[play.Card] {
			return false
		}
		cards[play.Card] = true
	}
	return !resolved || *t.WinnerSeat == trickWinner(t)
}
