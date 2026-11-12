# 41 — Practical Assignment, Part I

A Go service for the four-player card game 41. Clients use REST/JSON, and every
REST operation is answered by a call to the internal gRPC `GameService`.

```text
client -> generated HTTP routing -> HTTP handlers -> GameService over gRPC
          api/openapi.yaml          internal/httpapi  proto/game/v1/game.proto
```

Part I returns dummy responses. Each RPC constructs a fresh snapshot, so a write
does not change a later read. The responding gRPC node identifies itself through
`nodeId`. Part II adds the local game state; Part III adds replicated ordering
and enforces the declared consistency guarantees.

## Game semantics

- Four seats, numbered 0-3 in turn order to the right. Seats 0 and 2 form team 0;
  seats 1 and 3 form team 1. The creator occupies seat 0 and is the first dealer.
- A deal uses all 52 cards without jokers, with 13 cards per player.
- The server chooses the random shuffle. The player to the dealer's left cuts;
  cards through the selected position move from the top to the bottom. The
  server deals in equal one-card cycles starting to the dealer's right.
- Bidding starts with the dealer and proceeds right. Bids are 2-13, excluding 6.
  The minimum depends on the player's current score:

| Score | Minimum bid |
|---|---:|
| Below 30, including negative scores | 2 |
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

- A bid total below 11 skips the deal without scoring. The dealer moves right
  before a fresh shuffle, cut, deal and bidding sequence.
- The dealer leads the first trick; the winner of each trick leads the next.
  Players follow the leading suit when possible. Otherwise any card is allowed.
  The highest heart wins; without hearts, the highest card in the leading suit
  wins. Hearts may lead any trick. Ranks increase from 2 through A.
- After 13 tricks, meeting or exceeding a bid adds its point value; falling short
  subtracts that value. Bids 2-5 are worth their amount, and bids 7-13 are worth
  twice their amount. Extra tricks add no extra points. Scores may be negative.
- All four scores are updated before checking the outcome. A team qualifies when
  either teammate has at least 41. Exactly one qualifying team wins; if both or
  neither qualify, play continues with the next dealer.

Public snapshots contain scores, bids, turns and trick information. Hands and
the shuffled deck are excluded. Hand reads use the participant's player ID.
The teaching API assumes trusted clients using their own IDs.

## Build and run in WSL

Requirements: Go >= 1.26, `protoc`, `make`, and `curl`. Docker and grpcurl are
optional. Run commands from this directory, which contains `Makefile`.

```sh
make setup        # once: install the generators declared in go.mod
make build        # regenerate REST/protobuf code, tidy, vet, and compile bin/server
make run          # node n1: HTTP :8080, gRPC :50051
```

Open http://localhost:8080/docs for Swagger UI or
http://localhost:8080/editor for Swagger Editor. Both load the embedded
`/openapi.yaml` and use the same origin as the API. The browser needs internet
access for the Swagger CDN assets. If the editor shows an older document, use
File -> Import URL and load `http://localhost:8080/openapi.yaml`.

In another terminal:

```sh
./etc/demo.sh
```

This calls every operation and prints its HTTP status, headers and body,
including the responding `nodeId`. Press Ctrl+C in the server terminal to stop it.

## Two-node forwarding

With n1 running, start another server in a second terminal:

```sh
make run2
```

Node n2 listens on HTTP :8081 and gRPC :50052. Its HTTP handlers call n1's gRPC
address, localhost:50051. In a third terminal:

```sh
./etc/demo.sh http://localhost:8081
```

Every successful response still contains `"nodeId":"n1"`:

```text
client -> n2 HTTP :8081 -> n1 gRPC :50051 -> n2 -> client
```

There is no HTTP redirect. The receiving server makes the RPC on the client's
behalf. Calling n2's own gRPC service directly instead returns `nodeId: "n2"`.

## REST-to-gRPC mapping

Every RPC has a dedicated request and response message. Request/response names
follow the RPC name, such as `CreateGameRequest` and `CreateGameResponse`.

| Method | Resource path | RPC | Success |
|---|---|---|---|
| POST | `/v1/games` | `CreateGame` | 201, game snapshot and node ID |
| GET | `/v1/games/{gameId}` | `GetGame` | 200, public snapshot and node ID |
| POST | `/v1/games/{gameId}/players` | `JoinGame` | 201, updated snapshot and node ID |
| POST | `/v1/games/{gameId}/deals` | `StartDeal` | 201, snapshot awaiting a cut |
| POST | `/v1/games/{gameId}/deals/{dealNumber}/cuts` | `CutDeck` | 201, snapshot ready for bidding |
| POST | `/v1/games/{gameId}/deals/{dealNumber}/bids` | `PlaceBid` | 201, snapshot with the bid |
| POST | `/v1/games/{gameId}/deals/{dealNumber}/plays` | `PlayCard` | 201, snapshot with the play |
| GET | `/v1/games/{gameId}/players/{playerId}/hand` | `GetHand` (server stream) | 200, hand and node ID |

The complete parameters, bodies, schemas and response codes are in
[`api/openapi.yaml`](api/openapi.yaml). Cards use rank followed by suit, such
as `10H` or `AS`.

`GetHand` first sends a `HandInfo` message, followed by zero to thirteen card
messages. Cards arrive in suit order C, D, S, H and increasing rank. The HTTP
handler collects the stream without changing that order. It waits for successful
completion before returning a response, so a later gRPC error still produces the
documented HTTP error. An empty hand returns `"cards":[]` with its metadata.

The HTTP layer translates protobuf enums to the REST strings, preserves optional
seat/team 0 values and signed scores, and holds no application state. Incoming
request contexts are passed to gRPC, with a five-second deadline for each HTTP
operation.

The gRPC client disables DNS-provided service configuration because its target
is explicit. This avoids stalled `_grpc_config.localhost` TXT lookups in WSL;
normal hostname address resolution remains enabled.

## Consistency

Both reads accept `?consistency=sequential|linearizable`, defaulting to sequential.
The HTTP layer passes the corresponding protobuf enum to gRPC. An unspecified
protobuf value also means sequential. Invalid consistency query values return 400.

Every write is specified as linearizable. Playing a card is a read-modify-write
operation: its turn/ownership/following-suit checks and updates form one atomic
transition, including trick resolution or end-of-deal scoring when applicable.
These are the service semantics; the guarantees are enforced from Part III.
Part I uses stateless dummy responses.

## Errors

Errors use `application/problem+json` (RFC 9457). One shared function,
`httpStatusFromGRPC`, maps the gRPC status to the HTTP response:

| gRPC status | HTTP status |
|---|---:|
| INVALID_ARGUMENT | 400 |
| NOT_FOUND | 404 |
| ALREADY_EXISTS | 409 |
| FAILED_PRECONDITION | 412 |
| INTERNAL and other internal failures | 500 |
| UNIMPLEMENTED | 501 |
| UNAVAILABLE | 503 |
| DEADLINE_EXCEEDED | 504 |

JSON and parameter-decoding failures also use the same problem-details format.
Malformed or incomplete upstream replies produce an internal error rather than
an incomplete success response.

Fixed inputs make the Part I examples repeatable:

| Input | Result |
|---|---|
| Normal game ID, such as `demo` | Fresh dummy snapshot |
| `gameId: "missing"` or an unknown player | 404 |
| Join seat 0 | 409, creator already occupies it |
| `gameId: "blocked"` for an action or hand read | 412 |
| Actor other than `player-0` for start/bid/play, or `player-3` for cutting | 412 |
| Bid 6, below 2, or above 13 | 400 |
| Bid 2 in `gameId: "high-score"`, where player 0 has score 30 | 412 |
| Invalid card/cut/seat, or an empty name | 400 |
| A valid card outside the fixed hand, such as player 0 playing `2C` | 412 |
| `gameId: "empty-hand"` for a hand read | 200 with an empty card array |

The dummy game has deal 1 and players `player-0` through `player-3`.
Positive deal numbers other than 1 return 404. Dummy IDs are stable; the demo
requests are independent rather than a sequence that creates stored game state.
The fixed deck is test data; actual random shuffling is implemented in Part II.

## Checks

```sh
make check
```

This installs the generators, regenerates and builds the server, runs the Go
tests, and starts n1 and n2 on HTTP :18080/:18081 and gRPC :15051/:15052. It calls
every REST operation through both HTTP servers, checks the statuses and error
format, and verifies that successful replies identify n1. It then stops n1 and
checks that a request through n2 returns 503. Test servers are stopped on exit;
on failure, the script prints the location of its logs.

```sh
go test -v ./...
```

The gRPC tests use real local TCP connections. They cover the RPCs, error
statuses, sorted/disjoint hands containing all 52 cards, empty hands, node
identity and the absence of stored state.

The HTTP tests call a gRPC backend and validate responses against
`api/openapi.yaml`. They cover all operations, read-consistency propagation,
request errors, optional zero values, negative scores, malformed streams, errors
after streaming has begun, and deadlines.

To check dependencies against the Go vulnerability database:

```sh
go install golang.org/x/vuln/cmd/govulncheck@latest
govulncheck -test ./...
```

## Inspect gRPC directly

Install grpcurl once:

```sh
go install github.com/fullstorydev/grpcurl/cmd/grpcurl@v1.9.4
```

With n1 running:

```sh
grpcurl -plaintext localhost:50051 list
grpcurl -plaintext -d '{"name":"Kareem"}' localhost:50051 game.v1.GameService/CreateGame
grpcurl -plaintext -d '{"gameId":"demo","playerId":"player-0"}' localhost:50051 game.v1.GameService/GetHand
```

grpcurl prints protobuf JSON. The REST layer translates this representation into
the types defined by OpenAPI.

## Layout

| Location | Purpose |
|---|---|
| `api/openapi.yaml` | REST contract and complete service semantics |
| `api/oapi-codegen.yaml` | Required strict standard-library HTTP generator configuration |
| `proto/game/v1/game.proto` | GameService RPCs and protobuf messages |
| `internal/grpcsvc/server.go` | Stateless dummy gRPC implementation |
| `internal/httpapi/handlers.go` | REST-to-RPC calls and shared error handling |
| `internal/httpapi/conversions.go` | Protobuf-to-REST translation |
| `cmd/server/main.go` | Server registration, HTTP serving, forwarding target and shutdown |
| `etc/demo.sh` | Calls for every operation and representative errors |
| `etc/check.sh` | Automated two-node check |
| `gen/` | Generated code; regenerate with `make gen` |
| `bin/server` | Compiled server |
| `TUTORIAL.md` | Course walkthrough |
| `TEMPLATE_README.md` | Template reference |

The optional self-hosted API tools remain available through `make tools-up`.
Swagger Editor is on :8090, Swagger UI on :8091, and Scalar on :8092.
For these cross-origin tools, start n1 with `make run CORS='*'` and select
`http://localhost:8080` as the API server. Use `make tools-down` to stop them.

See [SECURITY.md](SECURITY.md) for the security scope and vulnerability-reporting
instructions. Build output, local secrets, and submission artifacts are
excluded from version control by `.gitignore`.

Dependabot checks the Go dependencies every Monday at 10:00 in the
Asia/Jerusalem timezone. The configuration in
[`.github/dependabot.yml`](.github/dependabot.yml) includes indirect dependencies
and groups minor and patch updates into one pull request. Major updates use
separate pull requests. Review changes and run `make check` before merging.
The generated Go packages are checked in because Dependabot needs them to
resolve local imports. `make build` still regenerates them from OpenAPI and
protobuf. Include any regenerated changes when updating the dependencies.

The YAML API migrations in jsonpath 0.7.x, openapi 1.25.x, and the newer go-yit
commit are temporarily excluded from Dependabot updates because they do not
compile with oapi-codegen 2.8.2. Review these exclusions when upgrading to a
compatible generator.
