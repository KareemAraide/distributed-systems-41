package sm

import (
	pb "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"google.golang.org/protobuf/proto"
	"strings"
	"testing"
)

func actionCommand(a *pb.Action) *pb.Command {
	return &pb.Command{Kind: &pb.Command_Apply{Apply: &pb.Apply{Actions: []*pb.Action{a}}}}
}
func play(t *testing.T, s *pb.State, seat int, card string) *pb.Result {
	t.Helper()
	a := &pb.Action{GameId: "game-1", Kind: &pb.Action_PlayCard{PlayCard: &pb.PlayCard{PlayerId: s.Games["game-1"].Participants[seat].Id, DealNumber: 1, Card: card}}}
	return Execute(s, actionCommand(a))
}
func bid(s *pb.State, seat int, amount int32) *pb.Result {
	a := &pb.Action{GameId: "game-1", Kind: &pb.Action_PlaceBid{PlaceBid: &pb.PlaceBid{PlayerId: s.Games["game-1"].Participants[seat].Id, DealNumber: 1, Amount: amount}}}
	return Execute(s, actionCommand(a))
}
func TestBiddingAndSkip(t *testing.T) {
	for _, skip := range []bool{false, true} {
		s := fixture(t, "bidding")
		for i, amount := range []int32{3, 3, 3, 2} {
			if skip {
				amount = 2
			}
			if r := bid(s, i, amount); r.Code != pb.Code_CODE_OK {
				t.Fatal(r)
			}
		}
		g := s.Games["game-1"]
		if skip {
			if g.Phase != pb.GamePhase_GAME_PHASE_READY || g.DealerSeat != 1 || g.Deal.Status != pb.DealStatus_DEAL_STATUS_SKIPPED {
				t.Fatal(g)
			}
			for _, p := range g.Participants {
				if p.Score != 0 {
					t.Fatal("skip scored")
				}
			}
			c := startCommand()
			c.GetApply().Actions[0].GetStartDeal().PlayerId = "player-2"
			if r := Execute(s, c); r.Code != pb.Code_CODE_OK {
				t.Fatal(r)
			}
			if s.Games["game-1"].Deal.Number != 2 || *s.Games["game-1"].Deal.NextSeat != 1 {
				t.Fatal("new dealer did not start deal 2")
			}
		} else if !proto.Equal(s, fixture(t, "playing")) {
			t.Fatal("bidding did not produce the expected playing state")
		}
	}
}
func TestMinimumBid(t *testing.T) {
	for _, tc := range []struct {
		score int64
		want  int32
	}{{-99, 2}, {29, 2}, {30, 3}, {39, 3}, {40, 4}, {49, 4}, {50, 5}, {60, 5}, {69, 5}, {70, 7}, {79, 7}, {80, 8}, {90, 9}, {100, 10}, {110, 11}, {120, 12}, {130, 13}, {9999, 13}} {
		if got := MinimumBid(tc.score); got != tc.want {
			t.Errorf("%d: %d != %d", tc.score, got, tc.want)
		}
	}
	s := fixture(t, "bidding")
	s.Games["game-1"].Participants[0].Score = 30
	before := proto.Clone(s)
	if r := bid(s, 0, 2); r.Code != pb.Code_CODE_FAILED_PRECONDITION || !proto.Equal(s, before) {
		t.Fatal(r)
	}
}
func TestFollowSuit(t *testing.T) {
	s := fixture(t, "playing")
	if r := play(t, s, 0, "5C"); r.Code != pb.Code_CODE_OK {
		t.Fatal(r)
	}
	before := proto.Clone(s)
	if r := play(t, s, 1, "5D"); r.Code != pb.Code_CODE_FAILED_PRECONDITION || !proto.Equal(s, before) {
		t.Fatalf("follow suit: %v", r)
	}
}
func TestTrickWinner(t *testing.T) {
	for _, tc := range []struct {
		name   string
		cards  []string
		winner int32
	}{
		{"highest lead", []string{"2C", "AC", "KC", "QC"}, 1},
		{"hearts trump", []string{"AC", "2H", "KC", "QC"}, 1},
		{"higher heart", []string{"AC", "KH", "AH", "KC"}, 2},
		{"other suit loses", []string{"2C", "AD", "3C", "AS"}, 2},
		{"hearts may lead", []string{"2H", "AH", "KH", "QH"}, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture(t, "last-trick")
			for i, c := range tc.cards {
				s.Games["game-1"].Participants[i].Hand = []string{c}
			}
			for i, c := range tc.cards {
				if r := play(t, s, i, c); r.Code != pb.Code_CODE_OK {
					t.Fatal(r)
				}
			}
			if got := s.Games["game-1"].Deal.LastTrick.GetWinnerSeat(); got != tc.winner {
				t.Fatalf("%d != %d", got, tc.winner)
			}
		})
	}
}
func TestScoringAndOutcome(t *testing.T) {
	tests := []struct {
		name           string
		score0, score1 int64
		draw, finished bool
		want0          int64
	}{
		{"ordinary", 0, 0, false, false, 4}, {"team zero wins", 37, 0, false, true, 41},
		{"simultaneous qualification", 37, 38, true, false, 41}, {"negative total", -20, 0, false, false, -16},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := fixture(t, "last-trick")
			s.Games["game-1"].Participants[0].Score = tc.score0
			s.Games["game-1"].Participants[1].Score = tc.score1
			for i, c := range []string{"AH", "KH", "QH", "JH"} {
				if r := play(t, s, i, c); r.Code != pb.Code_CODE_OK {
					t.Fatal(r)
				}
			}
			g := s.Games["game-1"]
			if g.Participants[0].Score != tc.want0 || g.Participants[1].Score != tc.score1+3 || g.Draw != tc.draw || (g.Phase == pb.GamePhase_GAME_PHASE_FINISHED) != tc.finished {
				t.Fatal(g)
			}
			if tc.finished {
				if g.WinnerTeam == nil || *g.WinnerTeam != 0 {
					t.Fatal("team zero omitted")
				}
				before := proto.Clone(s)
				if play(t, s, 0, "AH").Code != pb.Code_CODE_FAILED_PRECONDITION || !proto.Equal(s, before) {
					t.Fatal("finished game changed")
				}
			}
		})
	}
}
func TestBidPointsAndExtraTricks(t *testing.T) {
	for _, tc := range []struct {
		bid, wins int32
		points    int64
	}{{4, 5, 4}, {8, 9, 16}, {8, 7, -16}, {13, 0, -26}} {
		s := fixture(t, "last-trick")
		g := s.Games["game-1"]
		g.Deal.Bids[0].Amount = tc.bid
		g.Deal.Bids[0].Points = bidPoints(tc.bid)
		g.Deal.TricksWon = []int32{tc.wins - 1, 13 - tc.wins, 0, 0}
		if tc.wins == 0 {
			g.Deal.TricksWon = []int32{0, 12, 0, 0}
			g.Participants[0].Hand = []string{"2H"}
			g.Participants[1].Hand = []string{"AH"}
		}
		cards := []string{}
		for _, p := range g.Participants {
			cards = append(cards, p.Hand[0])
		}
		for i, c := range cards {
			if r := play(t, s, i, c); r.Code != pb.Code_CODE_OK {
				t.Fatal(r)
			}
		}
		if got := s.Games["game-1"].Participants[0].Score; got != tc.points {
			t.Fatalf("bid %d wins %d: %d != %d", tc.bid, tc.wins, got, tc.points)
		}
	}
}
func TestCapacityAndBounds(t *testing.T) {
	s := fixture(t, "waiting")
	s.GameLimit = 1
	before := proto.Clone(s)
	if r := Execute(s, command(t, `create{name:"another"}`)); r.Code != pb.Code_CODE_RESOURCE_EXHAUSTED || !proto.Equal(s, before) {
		t.Fatal(r)
	}
	for _, c := range []*pb.Command{
		{Kind: &pb.Command_Create{Create: &pb.Create{Name: strings.Repeat("x", 81)}}},
		{Kind: &pb.Command_Apply{Apply: &pb.Apply{Actions: make([]*pb.Action, 101)}}},
		{Kind: &pb.Command_ReadGames{ReadGames: &pb.ReadGames{GameIds: make([]string, 101)}}},
	} {
		if r := Execute(s, c); r.Code != pb.Code_CODE_INVALID_ARGUMENT || !proto.Equal(s, before) {
			t.Fatal(r)
		}
	}
}
