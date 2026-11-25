// Converters keep RPC messages separate from the state machine's commands.
package grpcsvc

import (
	"crypto/rand"
	pb "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"math/big"
)

func createCommand(r *pb.CreateGameRequest) *pb.Command {
	return &pb.Command{Kind: &pb.Command_Create{Create: &pb.Create{Name: r.GetName()}}}
}
func gamesCommand(r *pb.GetGamesRequest) *pb.Command {
	read := &pb.ReadGames{GameIds: append([]string{}, r.GetGameIds()...)}
	if r.GetReadMask() != nil {
		read.ReadMask = proto.Clone(r.ReadMask).(*fieldmaskpb.FieldMask)
	}
	return &pb.Command{Kind: &pb.Command_ReadGames{ReadGames: read}}
}
func handCommand(r *pb.GetHandRequest) *pb.Command {
	return &pb.Command{Kind: &pb.Command_ReadHand{ReadHand: &pb.ReadHand{GameId: r.GetGameId(), PlayerId: r.GetPlayerId()}}}
}
func (s *Server) actionsCommand(r *pb.ApplyActionsRequest) (*pb.Command, error) {
	if len(r.GetActions()) < 1 || len(r.GetActions()) > 100 {
		return nil, status.Error(codes.InvalidArgument, "give 1-100 actions")
	}
	out := &pb.Apply{}
	for _, a := range r.GetActions() {
		action := &pb.Action{GameId: a.GetGameId()}
		switch k := a.GetAction().(type) {
		case *pb.GameAction_Join:
			action.Kind = &pb.Action_Join{Join: &pb.Join{Name: k.Join.GetName(), Seat: k.Join.GetSeat()}}
		case *pb.GameAction_StartDeal:
			deck, err := s.shuffle()
			if err != nil {
				return nil, status.Error(codes.Internal, "shuffle failed")
			}
			action.Kind = &pb.Action_StartDeal{StartDeal: &pb.StartDeal{PlayerId: k.StartDeal.GetPlayerId(), Deck: deck}}
		case *pb.GameAction_Bid:
			action.Kind = &pb.Action_PlaceBid{PlaceBid: &pb.PlaceBid{PlayerId: k.Bid.GetPlayerId(), DealNumber: k.Bid.GetDealNumber(), Amount: k.Bid.GetAmount()}}
		case *pb.GameAction_Play:
			action.Kind = &pb.Action_PlayCard{PlayCard: &pb.PlayCard{PlayerId: k.Play.GetPlayerId(), DealNumber: k.Play.GetDealNumber(), Card: k.Play.GetCard()}}
		default:
			return nil, status.Error(codes.InvalidArgument, "each action needs exactly one kind")
		}
		out.Actions = append(out.Actions, action)
	}
	return &pb.Command{Kind: &pb.Command_Apply{Apply: out}}, nil
}

// Fisher-Yates runs before Submit; Execute receives only the chosen permutation.
func randomDeck() ([]string, error) {
	deck := make([]string, 0, 52)
	for _, suit := range []string{"C", "D", "S", "H"} {
		for _, rank := range []string{"2", "3", "4", "5", "6", "7", "8", "9", "10", "J", "Q", "K", "A"} {
			deck = append(deck, rank+suit)
		}
	}
	for i := len(deck) - 1; i > 0; i-- {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(i+1)))
		if err != nil {
			return nil, err
		}
		j := int(n.Int64())
		deck[i], deck[j] = deck[j], deck[i]
	}
	return deck, nil
}
func toStatus(r *pb.Result) error {
	var code codes.Code
	switch r.GetCode() {
	case pb.Code_CODE_OK:
		return nil
	case pb.Code_CODE_INVALID_ARGUMENT:
		code = codes.InvalidArgument
	case pb.Code_CODE_NOT_FOUND:
		code = codes.NotFound
	case pb.Code_CODE_ALREADY_EXISTS:
		code = codes.AlreadyExists
	case pb.Code_CODE_FAILED_PRECONDITION:
		code = codes.FailedPrecondition
	case pb.Code_CODE_RESOURCE_EXHAUSTED:
		code = codes.ResourceExhausted
	default:
		code = codes.Internal
	}
	return status.Error(code, r.GetMessage())
}
