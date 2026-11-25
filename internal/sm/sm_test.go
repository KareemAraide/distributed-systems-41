package sm

import (
	pb "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	"testing"
)

func parse[M proto.Message](t *testing.T, text string, m M) M {
	t.Helper()
	if err := prototext.Unmarshal([]byte(text), m); err != nil {
		t.Fatal(err)
	}
	return m
}
func fixture(t *testing.T, name string) *pb.State {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".txtpb"))
	if err != nil {
		t.Fatal(err)
	}
	return parse(t, string(b), &pb.State{})
}
func command(t *testing.T, text string) *pb.Command { return parse(t, text, &pb.Command{}) }
func orderedDeck() []string {
	var out []string
	for _, s := range []string{"C", "D", "S", "H"} {
		for _, r := range []string{"2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K", "A"} {
			out = append(out, r+s)
		}
	}
	return out
}
func startCommand() *pb.Command {
	a := &pb.Action{GameId: "game-1", Kind: &pb.Action_StartDeal{StartDeal: &pb.StartDeal{PlayerId: "player-1", Deck: orderedDeck()}}}
	return &pb.Command{Kind: &pb.Command_Apply{Apply: &pb.Apply{Actions: []*pb.Action{a}}}}
}
func TestExecuteTable(t *testing.T) {
	tests := []struct {
		name, fixture, cmd string
		code               pb.Code
		after              string
	}{
		{"create", "", `create {name:"Kareem"}`, pb.Code_CODE_OK, "waiting"},
		{"read game", "ready", `read_games {game_ids:"game-1"}`, pb.Code_CODE_OK, ""},
		{"read hand", "bidding", `read_hand {game_id:"game-1" player_id:"player-1"}`, pb.Code_CODE_OK, ""},
		{"empty command", "ready", ``, pb.Code_CODE_INVALID_ARGUMENT, ""},
		{"blank name", "ready", `create {name:"  "}`, pb.Code_CODE_INVALID_ARGUMENT, ""},
		{"missing game", "ready", `read_games {game_ids:"missing"}`, pb.Code_CODE_NOT_FOUND, ""},
		{"empty read", "ready", `read_games {}`, pb.Code_CODE_INVALID_ARGUMENT, ""},
		{"duplicate IDs", "ready", `read_games {game_ids:"game-1" game_ids:"game-1"}`, pb.Code_CODE_INVALID_ARGUMENT, ""},
		{"empty ID", "ready", `read_games {game_ids:""}`, pb.Code_CODE_INVALID_ARGUMENT, ""},
		{"private mask rejected", "ready", `read_games {game_ids:"game-1" read_mask{paths:"participants"}}`, pb.Code_CODE_INVALID_ARGUMENT, ""},
		{"missing player", "ready", `read_hand {game_id:"game-1" player_id:"other"}`, pb.Code_CODE_NOT_FOUND, ""},
		{"empty player ID", "ready", `read_hand {game_id:"game-1"}`, pb.Code_CODE_INVALID_ARGUMENT, ""},
		{"hand before deal", "ready", `read_hand {game_id:"game-1" player_id:"player-1"}`, pb.Code_CODE_FAILED_PRECONDITION, ""},
		{"occupied seat", "ready", `apply{actions{game_id:"game-1" join{name:"new" seat:0}}}`, pb.Code_CODE_ALREADY_EXISTS, ""},
		{"invalid seat", "waiting", `apply{actions{game_id:"game-1" join{name:"new" seat:4}}}`, pb.Code_CODE_INVALID_ARGUMENT, ""},
		{"blank join name", "waiting", `apply{actions{game_id:"game-1" join{seat:1}}}`, pb.Code_CODE_INVALID_ARGUMENT, ""},
		{"join after start", "bidding", `apply{actions{game_id:"game-1" join{name:"new" seat:1}}}`, pb.Code_CODE_FAILED_PRECONDITION, ""},
		{"no actions", "ready", `apply{}`, pb.Code_CODE_INVALID_ARGUMENT, ""},
		{"missing action kind", "ready", `apply{actions{game_id:"game-1"}}`, pb.Code_CODE_INVALID_ARGUMENT, ""},
		{"missing action game", "ready", `apply{actions{join{name:"new" seat:1}}}`, pb.Code_CODE_INVALID_ARGUMENT, ""},
		{"start before ready", "waiting", `apply{actions{game_id:"game-1" start_deal{player_id:"player-1"}}}`, pb.Code_CODE_FAILED_PRECONDITION, ""},
		{"start wrong dealer", "ready", `apply{actions{game_id:"game-1" start_deal{player_id:"player-2"}}}`, pb.Code_CODE_FAILED_PRECONDITION, ""},
		{"bad deck", "ready", `apply{actions{game_id:"game-1" start_deal{player_id:"player-1" deck:"AH"}}}`, pb.Code_CODE_INVALID_ARGUMENT, ""},
		{"bid six", "bidding", `apply{actions{game_id:"game-1" place_bid{player_id:"player-1" deal_number:1 amount:6}}}`, pb.Code_CODE_INVALID_ARGUMENT, ""},
		{"bid wrong turn", "bidding", `apply{actions{game_id:"game-1" place_bid{player_id:"player-2" deal_number:1 amount:3}}}`, pb.Code_CODE_FAILED_PRECONDITION, ""},
		{"missing deal", "bidding", `apply{actions{game_id:"game-1" place_bid{player_id:"player-1" deal_number:2 amount:3}}}`, pb.Code_CODE_NOT_FOUND, ""},
		{"invalid deal number", "bidding", `apply{actions{game_id:"game-1" place_bid{player_id:"player-1" amount:3}}}`, pb.Code_CODE_INVALID_ARGUMENT, ""},
		{"bid wrong phase", "playing", `apply{actions{game_id:"game-1" place_bid{player_id:"player-1" deal_number:1 amount:3}}}`, pb.Code_CODE_FAILED_PRECONDITION, ""},
		{"invalid card", "playing", `apply{actions{game_id:"game-1" play_card{player_id:"player-1" deal_number:1 card:"1H"}}}`, pb.Code_CODE_INVALID_ARGUMENT, ""},
		{"card not owned", "playing", `apply{actions{game_id:"game-1" play_card{player_id:"player-1" deal_number:1 card:"2C"}}}`, pb.Code_CODE_FAILED_PRECONDITION, ""},
		{"play wrong turn", "playing", `apply{actions{game_id:"game-1" play_card{player_id:"player-2" deal_number:1 card:"2C"}}}`, pb.Code_CODE_FAILED_PRECONDITION, ""},
		{"last batch action fails", "waiting", `apply{actions{game_id:"game-1" join{name:"Awes" seat:1}} actions{game_id:"game-1" join{name:"Dana" seat:0}}}`, pb.Code_CODE_ALREADY_EXISTS, ""},
		{"late missing game", "waiting", `apply{actions{game_id:"game-1" join{name:"Awes" seat:1}} actions{game_id:"missing" join{name:"Dana" seat:2}}}`, pb.Code_CODE_NOT_FOUND, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := NewState()
			if tc.fixture != "" {
				s = fixture(t, tc.fixture)
			}
			before := proto.Clone(s).(*pb.State)
			r := Execute(s, command(t, tc.cmd))
			if r.Code != tc.code {
				t.Fatalf("got %v: %s; want %v", r.Code, r.Message, tc.code)
			}
			want := before
			if tc.after != "" {
				want = fixture(t, tc.after)
			}
			if !proto.Equal(s, want) {
				t.Fatalf("state after:\n%s\nwant:\n%s", prototext.Format(s), prototext.Format(want))
			}
		})
	}
}
func TestJoinExpectedState(t *testing.T) {
	s := fixture(t, "waiting")
	r := Execute(s, command(t, `apply{actions{game_id:"game-1" join{name:"Awes" seat:1}}}`))
	want := parse(t, `game_sequence:1 player_sequence:2 games{key:"game-1" value{
 id:"game-1" phase:GAME_PHASE_WAITING
 participants{id:"player-1" name:"Kareem"}
 participants{id:"player-2" name:"Awes" seat:1}
 }}`, &pb.State{})
	if r.Code != pb.Code_CODE_OK || !proto.Equal(s, want) {
		t.Fatalf("join: %v\n%s", r, prototext.Format(s))
	}
}
func TestStartDealExpectedState(t *testing.T) {
	s := fixture(t, "ready")
	r := Execute(s, startCommand())
	if r.Code != pb.Code_CODE_OK || !proto.Equal(s, fixture(t, "bidding")) {
		t.Fatalf("deal: %v\n%s", r, prototext.Format(s))
	}
	c := startCommand()
	c.GetApply().Actions[0].GetStartDeal().Deck[0] = "AH"
	s = fixture(t, "ready")
	before := proto.Clone(s)
	if Execute(s, c).Code != pb.Code_CODE_INVALID_ARGUMENT || !proto.Equal(s, before) {
		t.Fatal("duplicate deck was accepted")
	}
}
