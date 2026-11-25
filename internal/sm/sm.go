// Package sm contains the deterministic game logic. Only Execute changes state.
package sm

import (
	"fmt"
	"math"
	"strings"
	"unicode/utf8"

	pb "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"google.golang.org/protobuf/proto"
)

const (
	DefaultGameLimit = 1000
	MaxBatch         = 100
	MaxName          = 80
)

func NewState() *pb.State { return &pb.State{Games: map[string]*pb.StoredGame{}} }

// Reads return detached data. Writes run on a copy, including the ID counters.
// A failed action therefore cannot leave an earlier part of a batch behind.
func Execute(s *pb.State, c *pb.Command) *pb.Result {
	if s == nil {
		return fail(pb.Code_CODE_INVALID_ARGUMENT, "state is required")
	}
	switch k := c.GetKind().(type) {
	case *pb.Command_ReadGames:
		return readGames(s, k.ReadGames)
	case *pb.Command_ReadHand:
		return readHand(s, k.ReadHand)
	case *pb.Command_Create, *pb.Command_Apply:
		work := proto.Clone(s).(*pb.State)
		if work.Games == nil {
			work.Games = map[string]*pb.StoredGame{}
		}
		var r *pb.Result
		if k, ok := c.GetKind().(*pb.Command_Create); ok {
			r = create(work, k.Create)
		} else {
			r = apply(work, c.GetApply())
		}
		if r.GetCode() == pb.Code_CODE_OK {
			s.Games, s.GameSequence, s.PlayerSequence, s.GameLimit = work.Games, work.GameSequence, work.PlayerSequence, work.GameLimit
		}
		return r
	default:
		return fail(pb.Code_CODE_INVALID_ARGUMENT, "empty command")
	}
}
func fail(code pb.Code, message string) *pb.Result { return &pb.Result{Code: code, Message: message} }
func validName(name string) bool {
	return strings.TrimSpace(name) != "" && utf8.RuneCountInString(name) <= MaxName
}
func game(s *pb.State, id string) (*pb.StoredGame, *pb.Result) {
	if id == "" {
		return nil, fail(pb.Code_CODE_INVALID_ARGUMENT, "gameId is required")
	}
	g, ok := s.GetGames()[id]
	if !ok {
		return nil, fail(pb.Code_CODE_NOT_FOUND, "game not found: "+id)
	}
	return g, nil
}
func participant(g *pb.StoredGame, id string) (*pb.Participant, *pb.Result) {
	if id == "" {
		return nil, fail(pb.Code_CODE_INVALID_ARGUMENT, "playerId is required")
	}
	for _, p := range g.GetParticipants() {
		if p.GetId() == id {
			return p, nil
		}
	}
	return nil, fail(pb.Code_CODE_NOT_FOUND, "player does not belong to this game")
}
func nextPlayerID(s *pb.State) (string, *pb.Result) {
	for s.PlayerSequence < math.MaxInt64 {
		s.PlayerSequence++
		id := fmt.Sprintf("player-%d", s.PlayerSequence)
		found := false
		for _, g := range s.Games {
			for _, p := range g.GetParticipants() {
				if p.GetId() == id {
					found = true
				}
			}
		}
		if !found {
			return id, nil
		}
	}
	return "", fail(pb.Code_CODE_RESOURCE_EXHAUSTED, "player IDs exhausted")
}
func create(s *pb.State, c *pb.Create) *pb.Result {
	if !validName(c.GetName()) {
		return fail(pb.Code_CODE_INVALID_ARGUMENT, "name must contain 1-80 characters and not be blank")
	}
	limit := s.GetGameLimit()
	if limit == 0 {
		limit = DefaultGameLimit
	}
	if int32(len(s.Games)) >= limit {
		return fail(pb.Code_CODE_RESOURCE_EXHAUSTED, "game capacity reached")
	}
	var id string
	for {
		if s.GameSequence == math.MaxInt64 {
			return fail(pb.Code_CODE_RESOURCE_EXHAUSTED, "game IDs exhausted")
		}
		s.GameSequence++
		id = fmt.Sprintf("game-%d", s.GameSequence)
		if _, exists := s.Games[id]; !exists {
			break
		}
	}
	pid, f := nextPlayerID(s)
	if f != nil {
		return f
	}
	g := &pb.StoredGame{Id: id, Phase: pb.GamePhase_GAME_PHASE_WAITING, Participants: []*pb.Participant{{Id: pid, Name: strings.TrimSpace(c.GetName())}}}
	s.Games[id] = g
	return &pb.Result{Games: []*pb.Game{publicGame(g)}}
}
func apply(s *pb.State, c *pb.Apply) *pb.Result {
	if len(c.GetActions()) < 1 || len(c.GetActions()) > MaxBatch {
		return fail(pb.Code_CODE_INVALID_ARGUMENT, "give 1-100 actions")
	}
	r := &pb.Result{}
	for i, a := range c.GetActions() {
		g, f := game(s, a.GetGameId())
		if f != nil {
			return actionFailure(i, f)
		}
		if g.GetPhase() == pb.GamePhase_GAME_PHASE_FINISHED {
			return actionFailure(i, fail(pb.Code_CODE_FAILED_PRECONDITION, "game has finished"))
		}
		switch k := a.GetKind().(type) {
		case *pb.Action_Join:
			f = join(s, g, k.Join)
		case *pb.Action_StartDeal:
			f = startDeal(g, k.StartDeal)
		case *pb.Action_PlaceBid:
			f = placeBid(g, k.PlaceBid)
		case *pb.Action_PlayCard:
			f = playCard(g, k.PlayCard)
		default:
			f = fail(pb.Code_CODE_INVALID_ARGUMENT, "action must contain join, startDeal, bid, or play")
		}
		if f != nil {
			return actionFailure(i, f)
		}
		r.Games = append(r.Games, publicGame(g))
	}
	return r
}
func actionFailure(i int, r *pb.Result) *pb.Result {
	r.Message = fmt.Sprintf("action %d: %s", i, r.GetMessage())
	return r
}
func TakeSnapshot(s *pb.State, applied int64) *pb.Snapshot {
	return &pb.Snapshot{State: proto.Clone(s).(*pb.State), Applied: applied}
}
func Restore(snapshot *pb.Snapshot) (*pb.State, int64) {
	if snapshot.GetState() == nil {
		return NewState(), snapshot.GetApplied()
	}
	return proto.Clone(snapshot.GetState()).(*pb.State), snapshot.GetApplied()
}
