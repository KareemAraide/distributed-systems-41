package executor

import (
	"context"
	"fmt"
	pb "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"sync"
	"testing"
)

func TestConcurrentDifferentSeats(t *testing.T) {
	e := start(t)
	submit(t, e, cmd(t, `create{name:"creator"}`))
	var wg sync.WaitGroup
	for seat := 1; seat < 4; seat++ {
		wg.Add(1)
		go func(seat int) {
			defer wg.Done()
			text := fmt.Sprintf(`apply{actions{game_id:"game-1" join{name:"seat-%d" seat:%d}}}`, seat, seat)
			r, err := e.Submit(context.Background(), cmd(t, text))
			if err != nil || r.GetCode() != pb.Code_CODE_OK {
				t.Errorf("join %d: %v %v", seat, r, err)
			}
		}(seat)
	}
	wg.Wait()
	r := submit(t, e, cmd(t, `read_games{game_ids:"game-1"}`))
	g := r.Games[0]
	if len(g.Players) != 4 || g.Phase != pb.GamePhase_GAME_PHASE_READY {
		t.Fatal("successful joins were lost", g)
	}
	for seat, p := range g.Players {
		if p.Seat != int32(seat) {
			t.Fatal("seat order changed")
		}
	}
}
