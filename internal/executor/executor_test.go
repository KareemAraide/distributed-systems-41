// Adapted from the course's executor tests: equivalence, concurrent writes,
// and timeout. The commands below belong to our game service.
package executor

import (
	"context"
	"errors"
	"fmt"
	pb "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"github.com/KareemAraide/distributed-systems-41/internal/sm"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	"sync"
	"testing"
	"time"
)

func cmd(t *testing.T, text string) *pb.Command {
	t.Helper()
	c := &pb.Command{}
	if err := prototext.Unmarshal([]byte(text), c); err != nil {
		t.Fatal(err)
	}
	return c
}
func start(t *testing.T) *Executor {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	e := New(sm.NewState())
	go e.Run(ctx)
	t.Cleanup(func() { cancel(); <-e.stopped })
	return e
}
func submit(t *testing.T, e *Executor, c *pb.Command) *pb.Result {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	r, err := e.Submit(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	return r
}
func TestSameAsExecute(t *testing.T) {
	e, ref := start(t), sm.NewState()
	for _, text := range []string{
		`create{name:"Kareem"}`,
		`apply{actions{game_id:"game-1" join{name:"Awes" seat:1}}}`,
		`read_games{game_ids:"game-1"}`,
		`apply{actions{game_id:"game-1" join{name:"Dana" seat:2}} actions{game_id:"game-1" join{name:"bad" seat:0}}}`,
		`read_hand{game_id:"game-1" player_id:"player-1"}`,
		`read_games{game_ids:"missing"}`,
	} {
		got := submit(t, e, cmd(t, text))
		want := sm.Execute(ref, cmd(t, text))
		if !proto.Equal(got, want) {
			t.Fatalf("%s:\ngot %v\nwant %v", text, got, want)
		}
	}
}
func TestConcurrentCreates(t *testing.T) {
	e := start(t)
	const n = 200
	ids := make(chan string, n)
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := e.Submit(context.Background(), &pb.Command{Kind: &pb.Command_Create{Create: &pb.Create{Name: "player"}}})
			if err != nil || r.GetCode() != pb.Code_CODE_OK {
				t.Errorf("create: %v %v", r, err)
				return
			}
			ids <- r.Games[0].Id
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatal("duplicate game ID")
		}
		seen[id] = true
	}
	if len(seen) != n {
		t.Fatalf("%d successful creates, want %d", len(seen), n)
	}
	for i := 1; i <= n; i++ {
		r := submit(t, e, cmd(t, fmt.Sprintf(`read_games{game_ids:"game-%d"}`, i)))
		if r.Code != pb.Code_CODE_OK {
			t.Fatal("lost update", r)
		}
	}
}
func TestConcurrentSeatClaims(t *testing.T) {
	e := start(t)
	submit(t, e, cmd(t, `create{name:"creator"}`))
	const n = 40
	var wg sync.WaitGroup
	results := make(chan pb.Code, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			r, err := e.Submit(context.Background(), cmd(t, `apply{actions{game_id:"game-1" join{name:"contender" seat:1}}}`))
			if err != nil {
				t.Error(err)
				return
			}
			results <- r.Code
		}()
	}
	wg.Wait()
	close(results)
	success, conflict := 0, 0
	for code := range results {
		switch code {
		case pb.Code_CODE_OK:
			success++
		case pb.Code_CODE_ALREADY_EXISTS:
			conflict++
		default:
			t.Fatalf("unexpected %v", code)
		}
	}
	if success != 1 || conflict != n-1 {
		t.Fatalf("%d successes, %d conflicts", success, conflict)
	}
	r := submit(t, e, cmd(t, `read_games{game_ids:"game-1"}`))
	if len(r.Games[0].Players) != 2 {
		t.Fatal("lost or duplicated seat")
	}
}
func TestTimeout(t *testing.T) {
	e := New(sm.NewState())
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := e.Submit(ctx, cmd(t, `create{name:"late"}`))
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
}
func TestFullQueueAndStop(t *testing.T) {
	e := New(sm.NewState())
	for i := 0; i < QueueLen; i++ {
		e.queue <- newRequest(cmd(t, `read_games{game_ids:"missing"}`))
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := e.Submit(ctx, cmd(t, `create{name:"full"}`)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	ctxRun, stop := context.WithCancel(context.Background())
	stop()
	go e.Run(ctxRun)
	<-e.stopped
	if _, err := e.Submit(context.Background(), cmd(t, `create{name:"stopped"}`)); !errors.Is(err, ErrStopped) {
		t.Fatal(err)
	}
}
func TestCanceledCallerDoesNotBlockRun(t *testing.T) {
	e := NewAt(sm.NewState(), 17)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { _, err := e.Submit(ctx, cmd(t, `create{name:"queued"}`)); done <- err }()
	deadline := time.After(time.Second)
	for len(e.queue) == 0 {
		select {
		case <-deadline:
			t.Fatal("not queued")
		default:
			time.Sleep(time.Millisecond)
		}
	}
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(context.Background())
	go e.Run(runCtx)
	submit(t, e, cmd(t, `create{name:"after"}`))
	stop()
	<-e.stopped
	if e.applied != 19 {
		t.Fatalf("applied %d, want 19", e.applied)
	}
}
