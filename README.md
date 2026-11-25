# 41 — Practical Assignment, Part II

A four-player game service built on our Part I project. The server now keeps real
state in memory. REST requests call gRPC, and every read or write goes through one
executor goroutine.

## Run in WSL

Use Ubuntu/WSL from this folder. Requirements: Go >= 1.26, protoc, make, curl,
and Python 3 (the walkthrough uses only its standard library).

```sh
make setup       # install the generator versions already recorded in go.mod
make build       # regenerate, tidy, vet, compile bin/server
make test        # all Go tests with the race detector
make run         # n1: HTTP :8080, gRPC :50051, sample snapshot
```

Swagger UI: http://localhost:8080/docs
Swagger Editor: http://localhost:8080/editor

In another terminal:

```sh
make run2        # n2: HTTP :8081, gRPC :50052; its REST API calls n1
```

The commands inside the run targets are:

```sh
./bin/server -id n1 -http :8080 -grpc :50051 -snapshot etc/sample-snapshot.txtpb -cors-origin ''
./bin/server -id n2 -http :8081 -grpc :50052 -target localhost:50051
```

Start empty with `./bin/server -id n1 -http :8080 -grpc :50051` (omit
`-snapshot`). The sample contains game-1 and four seated players, before a deal.
Ctrl+C shuts down the server and its executor.

```sh
./etc/demo.sh http://localhost:8080 http://localhost:8081
make check
```

The demo creates its own games, calls every REST operation, plays all 52 cards,
checks scoring, and submits concurrent requests. `make check` builds and runs the
race tests, starts its own nodes on ports 18080-18082 / 15051-15053, runs the demo,
checks configured capacity, stops n1 to check forwarding, and verifies a restart.
It stops only the processes it started.

## Request flow

```text
HTTP client -> generated REST router -> internal/httpapi
  -> GameService over gRPC -> internal/grpcsvc/convert.go
  -> Executor.Submit -> queue -> Executor.Run -> sm.Execute
  <- HTTP response <- gRPC response <- Result <- per-request reply channel
```

Only Run owns the state. The HTTP layer and RPC handlers never read it directly.
Part III can replace local ordering with a replicated log.

## APIs

All resource paths below have the base path `/v1`. There are nine REST operations
and four RPCs. Each RPC has its own request and response message.

| REST | RPC | Success |
|---|---|---|
| POST /games | CreateGame | 201 |
| GET /games/{gameId} | GetGames with one ID | 200 |
| GET /games?ids=a&ids=b | GetGames | 200 |
| GET /games/{gameId}/players/{playerId}/hand | GetHand | 200 |
| POST /games/{gameId}/players | ApplyActions: join | 201 |
| POST /games/{gameId}/deals | ApplyActions: startDeal | 201 |
| POST /games/{gameId}/deals/{dealNumber}/bids | ApplyActions: bid | 201 |
| POST /games/{gameId}/deals/{dealNumber}/plays | ApplyActions: play | 201 |
| PATCH /games | ApplyActions: 1-100 actions | 200 |

Game IDs select objects. The optional `fields` parameter selects public top-level
sections: `id,phase,dealerSeat,players,draw,currentDeal,winnerTeam`. For example:

```sh
curl 'http://localhost:8080/v1/games/game-1?fields=phase,players'
```

It becomes a `google.protobuf.FieldMask` in GetGames. Omit it for all public
sections. `id` is always included; an empty or unknown mask returns 400.
Hands are never public game fields. Single/multiple reads take one queue position
and preserve the requested ID order; any missing game fails the whole read.
Each server stream ends with a fin-only message. REST buffers the entire stream
and requires `fin=true` before returning success.

An atomic batch can join two players:

```json
{"actions":[
  {"gameId":"game-2","join":{"name":"Awes","seat":1}},
  {"gameId":"game-2","join":{"name":"Dana","seat":2}}
]}
```

Each action has `gameId` and exactly one of `join`, `startDeal`, `bid`, `play`.
Bid/play actions in a batch include `dealNumber`. Each returned game is a detached
snapshot just after its corresponding action. Later actions see earlier effects.
If any action fails, the entire batch, including ID allocations, is discarded.

## Game rules

- Four seats 0-3 increase to the right. Seats 0 and 2 are team 0; 1 and 3 are team 1.
  The creator occupies seat 0 and is the first dealer. Names may repeat; IDs
  identify participants. Membership is fixed once the first deal starts.
- Only the current dealer starts a ready game. The server randomly shuffles all
  52 cards without jokers and deals one card per player per cycle, starting at
  the dealer's right. There is no manual cut or selectable dealing pattern.
- Bids run right from the dealer. Bids are 2-13 except 6. Bids 2-5 are worth their
  amount; bids 7-13 are worth twice their amount.

| Score | Minimum bid |
|---|---:|
| Below 30, including negative | 2 |
| 30-39 | 3 |
| 40-49 | 4 |
| 50-69 | 5 |
| 70-79 | 7 |
| 80-89 | 8 |
| 90-99 | 9 |
| 100-109 | 10 |
| 110-119 | 11 |
| 120-129 | 12 |
| 130 or above | 13 |

- A total bid below 11 skips the deal without scoring. The dealer moves right;
  the new dealer explicitly starts another deal.
- The dealer leads the first trick; each trick winner leads the next. Follow the
  leading suit if possible; otherwise play any card. The highest heart wins;
  without hearts the highest card in the leading suit wins. Hearts may lead.
  Ranks increase from 2 through A.
- After 13 tricks all scores update together. Meeting/exceeding a bid adds its
  points; otherwise subtract them. Extra tricks add nothing. Scores may be negative.
- A team qualifies if either teammate has at least 41. Exactly one qualifying
  team wins. If both qualify, play continues as a draw for now; if neither
  qualifies, play also continues. The next dealer moves right.

## State machine and snapshots

`proto/game/v1/sm.proto` describes State, Snapshot, Command and Result.
`internal/sm` contains the deterministic logic. It uses no clock, randomness,
I/O, goroutines or locks. A shuffle is generated once in the RPC converter and
included in the internal StartDeal command. Clients cannot supply a deck or seed.

Game/player counters live in State and advance only on successful commands.
The default capacity is 1000 games; a snapshot may specify a positive `game_limit`.
Reads and action batches are limited to 100 items. Names have at most 80
characters and must not be blank.

Snapshots contain the whole state and an applied-command count. Snapshot
validation and file reading happen at startup. State is not automatically saved:
a restart starts empty or reloads the supplied snapshot. A timeout after enqueue
does not prove whether a write executed, so writes are not automatically retried.

## Errors

All errors use `application/problem+json`.

| gRPC status | HTTP |
|---|---:|
| INVALID_ARGUMENT | 400 |
| NOT_FOUND | 404 |
| ALREADY_EXISTS (occupied seat) | 409 |
| FAILED_PRECONDITION (phase/turn/rule) | 412 |
| RESOURCE_EXHAUSTED or UNAVAILABLE | 503 |
| DEADLINE_EXCEEDED | 504 |
| Unexpected failures | 500 |

The service assumes trusted clients using their own player IDs. It has no
authentication, TLS, replication, automatic persistence or request deduplication.
`nodeId` identifies the responding gRPC server and is added outside the state machine.
`go.mod` keeps the dependency/generator versions from the working Part I solution.
