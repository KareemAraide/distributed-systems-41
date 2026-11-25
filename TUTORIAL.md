# Working through Part II

Start in this repository in WSL. The implementation follows sections 5-9 of the
Part II assignment. The official PDFs and template are in `grades.cs/` locally.

## 1. Contracts and commands

Read `proto/game/v1/game.proto` first: four RPCs, public Game, private hand reads,
and actions. Then read `sm.proto`: the stored state has private hands; commands
carry only what Execute needs. The API never accepts a deck from a client.

`internal/grpcsvc/convert.go` maps requests to these commands. For StartDeal it
chooses the deck once before submission. Result codes become gRPC status here;
`internal/httpapi` maps them to HTTP.

```sh
make gen-proto
make gen-rest
```

Generated files in `gen/` are checked in, but regenerated from the contracts.

## 2. State transitions

Read `internal/sm/sm.go` and `game.go`. Execute dispatches reads directly.
For writes it copies State, runs every action on that copy, and commits only
on success. Helpers can modify the working copy; no other layer owns State.

`read.go` creates public views, derives team/minimum-bid/card-count fields, and
sorts hands. Public results are detached from the stored game.
`snapshot.go` checks startup invariants. TakeSnapshot/Restore copy protobuf data.

## 3. Pure tests

`internal/sm/testdata/*.txtpb` are fixed states: waiting, ready, bidding, playing,
and the final trick. Tests load a fresh state for every case and use proto.Equal.

```sh
go test ./internal/sm
```

In `sm_test.go` each row gives a command, result code, and expected state.
An empty expected-state name means unchanged. Other tests cover all 52 plays,
every scoring band, rollback, determinism, snapshots and detached responses.

## 4. Executor

`internal/executor/executor.go` is adapted from the supplied skeleton.
TODO 1-5 are implemented. A request has its command and a capacity-one reply
channel. Submit waits to enqueue, then waits for its own result. Both waits
observe cancellation and the stopped channel.

One Run goroutine takes requests in FIFO queue order and calls Execute.
It counts all executed commands, including reads and failed commands.
Canceling a caller after enqueue does not remove its command from the queue.

```sh
go test -race ./internal/executor
```

The tests compare direct and queued execution, create 200 games concurrently,
claim a shared seat, join different seats of one game without losing updates,
and exercise timeout/full-queue/shutdown behavior.

## 5. Service and end-to-end checks

`cmd/server/main.go` reads the initial snapshot and starts the gRPC service.
`internal/grpcsvc/server.go` starts the executor with the process context.
RPC handlers convert, submit, wait and convert back.

`internal/httpapi/validation.go` validates requests against OpenAPI.
The generated HTTP handlers decode requests; our handlers call gRPC.
GetGames and GetHand must receive fin before the HTTP response can succeed.

```sh
make check
```

This includes build/vet, all race tests, real n1/n2 forwarding, a full deal,
concurrency, capacity and snapshot restart. Transport error/stream fault
injection is covered by the Go HTTP tests rather than special production routes.

For manual exploration, run `make run`, visit `/docs`, then `make run2` in
another terminal. `./etc/demo.sh` creates its own games each time.
The private meeting guide explains the syntax and every changed source file.
