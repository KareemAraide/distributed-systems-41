#!/usr/bin/env sh
# Smoke test of the 41 API.
# Start `make run` (and optionally `make run2`) first.
# Usage: ./etc/demo.sh [base-url]     default: http://localhost:8080
set -eu
B=${1:-http://localhost:8080}
J='Content-Type: application/json'

request() {
  method=$1
  path=$2
  shift 2
  printf '\n%s %s\n' "$method" "$path"
  curl --connect-timeout 2 --max-time 10 -siS -X "$method" "$B$path" "$@"
  printf '\n'
}

# Each operation uses an independent dummy response in Part I.
request POST /v1/games -H "$J" -d '{"name":"Kareem"}'
request GET '/v1/games/demo?consistency=sequential'
request GET '/v1/games/demo?consistency=linearizable'
request POST /v1/games/demo/players -H "$J" -d '{"name":"Player Two","seat":1}'
request POST /v1/games/demo/deals -H "$J" -d '{"playerId":"player-0"}'
request POST /v1/games/demo/deals/1/cuts -H "$J" -d '{"playerId":"player-3","cutPosition":20}'
request POST /v1/games/demo/deals/1/bids -H "$J" -d '{"playerId":"player-0","amount":8}'
request POST /v1/games/demo/deals/1/plays -H "$J" -d '{"playerId":"player-0","card":"10H"}'
request GET '/v1/games/demo/players/player-0/hand?consistency=linearizable'
request GET /v1/games/empty-hand/players/player-0/hand

# Fixed inputs demonstrate 400, 404, 409, and 412 responses.
request POST /v1/games/demo/deals/1/bids -H "$J" -d '{"playerId":"player-0","amount":6}'
request GET /v1/games/missing
request POST /v1/games/demo/players -H "$J" -d '{"name":"Player Two","seat":0}'
request POST /v1/games/blocked/deals -H "$J" -d '{"playerId":"player-0"}'
