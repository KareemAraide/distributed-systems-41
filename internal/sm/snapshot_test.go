package sm

import (
	pb "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"testing"
)

func TestMasksAndDetachedResults(t *testing.T) {
	s := fixture(t, "playing")
	before := proto.Clone(s)
	r := Execute(s, command(t, `read_games{game_ids:"game-1" read_mask{paths:"players" paths:"draw"}}`))
	g := r.Games[0]
	if g.Id != "game-1" || g.Phase != 0 || g.CurrentDeal != nil || len(g.Players) != 4 {
		t.Fatal(g)
	}
	g.Players[0].Name = "changed"
	hand := Execute(s, command(t, `read_hand{game_id:"game-1" player_id:"player-1"}`))
	hand.Hand.Cards[0] = "XX"
	if !proto.Equal(s, before) {
		t.Fatal("response aliases state")
	}
}
func TestDeterminismAndFullDeal(t *testing.T) {
	a, b := fixture(t, "ready"), fixture(t, "ready")
	run := func(c *pb.Command) {
		t.Helper()
		ra, rb := Execute(a, proto.Clone(c).(*pb.Command)), Execute(b, proto.Clone(c).(*pb.Command))
		if ra.Code != pb.Code_CODE_OK || !proto.Equal(ra, rb) || !proto.Equal(a, b) {
			t.Fatalf("diverged: %v / %v", ra, rb)
		}
		if err := ValidateSnapshot(TakeSnapshot(a, 1)); err != nil {
			t.Fatal(err)
		}
	}
	run(startCommand())
	for i, amount := range []int32{3, 3, 3, 2} {
		action := &pb.Action{GameId: "game-1", Kind: &pb.Action_PlaceBid{PlaceBid: &pb.PlaceBid{PlayerId: a.Games["game-1"].Participants[i].Id, DealNumber: 1, Amount: amount}}}
		run(actionCommand(action))
	}
	for a.Games["game-1"].Phase == pb.GamePhase_GAME_PHASE_PLAYING {
		g := a.Games["game-1"]
		p := g.Participants[*g.Deal.NextSeat]
		card := p.Hand[0]
		if len(g.Deal.CurrentTrick.Plays) > 0 {
			lead := g.Deal.CurrentTrick.Plays[0].Card
			for _, c := range p.Hand {
				if c[len(c)-1] == lead[len(lead)-1] {
					card = c
					break
				}
			}
		}
		action := &pb.Action{GameId: "game-1", Kind: &pb.Action_PlayCard{PlayCard: &pb.PlayCard{PlayerId: p.Id, DealNumber: 1, Card: card}}}
		run(actionCommand(action))
	}
	if a.Games["game-1"].Deal.CompletedTricks != 13 {
		t.Fatal("deal did not finish")
	}
}
func TestSnapshotRoundTrip(t *testing.T) {
	for _, name := range []string{"waiting", "ready", "bidding", "playing", "last-trick"} {
		t.Run(name, func(t *testing.T) {
			s := fixture(t, name)
			snap := TakeSnapshot(s, 27)
			if err := ValidateSnapshot(snap); err != nil {
				t.Fatal(err)
			}
			bytes, err := prototext.Marshal(snap)
			if err != nil {
				t.Fatal(err)
			}
			decoded := parse(t, string(bytes), &pb.Snapshot{})
			back, n := Restore(decoded)
			if n != 27 || !proto.Equal(back, s) {
				t.Fatal("snapshot changed state")
			}
			back.GameSequence = 99
			if snap.State.GameSequence == 99 || s.GameSequence == 99 {
				t.Fatal("snapshot aliases state")
			}
		})
	}
	// Counters continue from the saved state, rather than allocating game-1 again.
	s, n := Restore(TakeSnapshot(fixture(t, "waiting"), 8))
	r := Execute(s, command(t, `create{name:"next"}`))
	if n != 8 || r.GetGames()[0].Id != "game-2" || r.Games[0].Players[0].Id != "player-2" {
		t.Fatal("IDs were reused")
	}
}
func TestInvalidSnapshot(t *testing.T) {
	for _, change := range []func(*pb.Snapshot){
		func(s *pb.Snapshot) { s.Applied = -1 },
		func(s *pb.Snapshot) { s.State.Games["game-1"].Id = "wrong" },
		func(s *pb.Snapshot) { s.State.Games["game-1"].Participants[1].Id = "player-1" },
		func(s *pb.Snapshot) { s.State.Games["game-1"].Deal.TricksWon = nil },
		func(s *pb.Snapshot) { s.State.Games["game-1"].Participants[0].Hand[0] = "XX" },
	} {
		snap := TakeSnapshot(fixture(t, "playing"), 0)
		change(snap)
		if ValidateSnapshot(snap) == nil {
			t.Fatal("invalid snapshot accepted")
		}
	}
}
