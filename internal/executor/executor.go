// PART II, STEP 4 — A single server with a command queue. YOUR TASK.
//
// Many goroutines (one per RPC) Submit commands; they go into a queue (a Go
// channel). One goroutine, Run, takes the commands from the queue one after
// another and executes each on the local state. Only Run ever touches the
// state, so no lock is needed. Each submitter waits until its command was
// executed, then returns its result.
//
//	RPC handler --Submit--> queue (channel) --next--> Run --sm.Execute--> state
//	     ^                                             |
//	     +--------------- reply channel <--------------+
//
// The life of one command, and the function that does each part:
//
//	Submit(ctx, cmd)                  an RPC handler calls it
//	  r := newRequest(cmd)            TODO 1  a request with a reply channel
//	  e.enqueue(ctx, r)               TODO 4a put r into the queue
//	  e.await(ctx, r)                 TODO 4b block until Run answers on r.reply
//	Run(ctx)                          the one goroutine that owns the state
//	  r := e.next(ctx)                TODO 5b the next command, in queue order
//	  e.execute(r)                    TODO 5c sm.Execute on the state, answer on r.reply
//
// The control flow of Submit and Run is already written below; you fill in
// the placeholders, in the order TODO 1-5 (the assignment PDF, Part II
// Step 4, explains each one). Every placeholder compiles as it is, so you can
// build after each TODO. internal/grpcsvc already calls New, Run and Submit.
// executor_test.go checks your work: go test -race ./internal/executor
package executor

import (
	"context"
	"errors"

	gamev1 "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"github.com/KareemAraide/distributed-systems-41/internal/sm"
)

// QueueLen is the number of commands that may wait; Submit blocks when the
// queue is full.
const QueueLen = 1024

// ErrStopped is returned by Submit once Run has returned.
var ErrStopped = errors.New("executor stopped")

// ---------------------------------------------------------------------------
// TODO 1 — A request is what travels through the queue: the command, and the
// channel on which Run sends back its result.

type request struct {
	cmd   *gamev1.Command
	reply chan *gamev1.Result
	// TODO 1a: add the reply channel:
	//   reply chan *gamev1.Result
}

// newRequest wraps a command for the queue.
func newRequest(cmd *gamev1.Command) request {
	// TODO 1b: also make the reply channel, with capacity 1:
	//   return request{cmd: cmd, reply: make(chan *gamev1.Result, 1)}
	// Capacity 1: Run's send never blocks, even when the caller already gave
	// up and nobody reads the reply. An unbuffered channel would let one slow
	// caller stop the whole executor.
	return request{cmd: cmd, reply: make(chan *gamev1.Result, 1)}
}

// ---------------------------------------------------------------------------
// TODO 2 — The executor's fields.

type Executor struct {
	queue   chan request
	stopped chan struct{}
	applied int64         // owned by Run, including reads and failed commands
	state   *gamev1.State // given: only Run reads or writes it (no lock needed)
	// TODO 2: add
	//   queue   chan request   // RPCs write requests here; only Run reads them
	//   stopped chan struct{}  // closed when Run returns: tells Submit to stop waiting
}

// ---------------------------------------------------------------------------
// TODO 3 — New makes the channels.

// New returns an executor over the given state (sm.NewState() to start
// empty, or a state restored from a snapshot).
func New(state *gamev1.State) *Executor { return NewAt(state, 0) }

// NewAt continues counting from a restored snapshot.
func NewAt(state *gamev1.State, applied int64) *Executor {
	e := &Executor{state: state, applied: applied, queue: make(chan request, QueueLen), stopped: make(chan struct{})}
	// TODO 3: make the two channels of TODO 2:
	//   e.queue = make(chan request, QueueLen) // buffered: Submit rarely waits
	//   e.stopped = make(chan struct{})        // closed (never sent on) by Run
	return e
}

// ---------------------------------------------------------------------------
// TODO 4 — Submit: called by every RPC, from many goroutines at once.
// Its control flow is given; fill in enqueue (4a) and await (4b).

// Submit enqueues cmd and waits for its result.
func (e *Executor) Submit(ctx context.Context, cmd *gamev1.Command) (*gamev1.Result, error) {
	r := newRequest(cmd)                      // TODO 1
	if err := e.enqueue(ctx, r); err != nil { // TODO 4a
		return nil, err // not queued, so never executed
	}
	return e.await(ctx, r) // TODO 4b
}

// enqueue puts r into the queue, unless ctx ends or the executor stopped first.
func (e *Executor) enqueue(ctx context.Context, r request) error {
	// TODO 4a: replace the return below with
	//   select {
	//   case e.queue <- r:   return nil        // queued: Run will execute it
	//   case <-ctx.Done():   return ctx.Err()  // the client gave up first
	//   case <-e.stopped:    return ErrStopped // Run has returned
	//   }
	// Why select: a plain  e.queue <- r  blocks forever on a full queue or a
	// stopped executor, and the client's timeout would be ignored.
	if err := ctx.Err(); err != nil {
		return err
	}
	select {
	case e.queue <- r:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-e.stopped:
		return ErrStopped
	}
}

// await blocks until Run sends r's result, unless ctx ends or the executor
// stopped first.
func (e *Executor) await(ctx context.Context, r request) (*gamev1.Result, error) {
	// TODO 4b: replace the return below with
	//   select {
	//   case res := <-r.reply: return res, nil
	//   case <-ctx.Done():     return nil, ctx.Err()
	//   case <-e.stopped:      return nil, ErrStopped
	//   }
	// Think: if ctx ends here, the command is already queued. Will it still
	// run? Can the client know whether its write happened?
	select {
	case res := <-r.reply:
		return res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-e.stopped:
		return nil, ErrStopped
	}
}

// ---------------------------------------------------------------------------
// TODO 5 — Run: the executor loop. Its control flow is given; add the defer
// (5a) and fill in next (5b) and execute (5c).

// Run executes the queued commands, one after another, until ctx ends.
// Call it once, in its own goroutine (the gRPC service starts it:
// go exec.Run(ctx)).
func (e *Executor) Run(ctx context.Context) {
	defer close(e.stopped)
	// TODO 5a: uncomment, once TODO 2 and 3 are done:
	//   defer close(e.stopped) // however Run ends, every waiting Submit returns
	for {
		r, ok := e.next(ctx) // TODO 5b: blocks until a command is queued
		if !ok {
			return // ctx ended: stop
		}
		e.execute(r) // TODO 5c
	}
}

// next returns the next request, in queue order; ok is false once ctx has
// ended.
func (e *Executor) next(ctx context.Context) (r request, ok bool) {
	// TODO 5b: replace the two placeholder lines below with
	//   select {
	//   case r := <-e.queue: return r, true
	//   case <-ctx.Done():   return request{}, false
	//   }

	if ctx.Err() != nil {
		return request{}, false
	}
	select {
	case r := <-e.queue:
		return r, true
	case <-ctx.Done():
		return request{}, false
	}
}

// execute runs one command on the state and answers its submitter.
func (e *Executor) execute(r request) {
	res := sm.Execute(e.state, r.cmd) // the state changes here, and only here
	// TODO 5c: send res to the waiting Submit:
	//   r.reply <- res // never blocks: the reply channel has capacity 1 (TODO 1b)
	e.applied++
	r.reply <- res
}
