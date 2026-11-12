#!/usr/bin/env sh
# End-to-end check of the generators and the running servers.
#   ./etc/check.sh        (from the project root; needs Go >= 1.26, protoc, curl)
# 1. installs the generators, 2. generates + vets + builds, 3. starts two nodes,
# 4. calls every operation and compares the HTTP status code with the spec.
set -eu
cd "$(dirname "$0")/.."

step() { printf '\n== %s\n' "$*"; }

step "1/5 generators"
command -v protoc >/dev/null || { echo "protoc not found: https://protobuf.dev/installation/"; exit 1; }
make setup >/dev/null
go tool oapi-codegen -version
protoc --version

step "2/5 generate, vet, build, test"
make build
go test ./...

FAIL=0
P1=
P2=
CHECK_TMP=$(mktemp -d "${TMPDIR:-/tmp}/fortyone-check.XXXXXX")
cleanup() {
  check_status=$?
  [ -z "$P1" ] || kill "$P1" 2>/dev/null || true
  [ -z "$P2" ] || kill "$P2" 2>/dev/null || true
  [ -z "$P1" ] || wait "$P1" 2>/dev/null || true
  [ -z "$P2" ] || wait "$P2" 2>/dev/null || true
  if [ "$check_status" -ne 0 ] || [ "$FAIL" -ne 0 ]; then
    printf 'Server logs and last response: %s\n' "$CHECK_TMP"
  else
    rm -f "$CHECK_TMP/n1.log" "$CHECK_TMP/n2.log" "$CHECK_TMP/body" "$CHECK_TMP/headers"
    rmdir "$CHECK_TMP"
  fi
}
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM

step "3/5 start n1 (:18080/:15051) and n2 (:18081 -> n1)"
./bin/server -id n1 -http :18080 -grpc :15051 >"$CHECK_TMP/n1.log" 2>&1 & P1=$!
./bin/server -id n2 -http :18081 -grpc :15052 -target localhost:15051 >"$CHECK_TMP/n2.log" 2>&1 & P2=$!

wait_ready() {
  node_pid=$1
  node_url=$2
  node_log=$3
  attempts=0
  while [ "$attempts" -lt 50 ]; do
    if ! kill -0 "$node_pid" 2>/dev/null; then
      cat "$node_log"
      return 1
    fi
    if curl --max-time 1 -fsS -o /dev/null "$node_url/openapi.yaml" 2>/dev/null; then
      return 0
    fi
    attempts=$((attempts + 1))
    sleep 0.1
  done
  cat "$node_log"
  printf 'Server did not become ready: %s\n' "$node_url"
  return 1
}
wait_ready "$P1" http://localhost:18080 "$CHECK_TMP/n1.log"
wait_ready "$P2" http://localhost:18081 "$CHECK_TMP/n2.log"

J='Content-Type: application/json'
# expect <status> <method> <path> [curl arguments...]
expect() {
  want=$1
  method=$2
  path=$3
  shift 3
  if ! got=$(curl --connect-timeout 2 --max-time 10 -sS \
    -D "$CHECK_TMP/headers" -o "$CHECK_TMP/body" -w '%{http_code}' \
    -X "$method" "$B$path" "$@"); then
    printf '  FAIL request: %s %s\n' "$method" "$path"
    FAIL=1
    return
  fi
  if [ "$got" = "$want" ]; then
    printf '  ok   %s %-4s %s\n' "$got" "$method" "$path"
  else
    printf '  FAIL %s %-4s %s (want %s): %s\n' "$got" "$method" "$path" "$want" "$(head -c 300 "$CHECK_TMP/body")"
    FAIL=1
  fi
  case "$want" in
    200|201)
      if ! grep -Fq '"nodeId":"n1"' "$CHECK_TMP/body"; then
        printf '  FAIL response did not come from n1\n'
        FAIL=1
      fi
      ;;
    4??|5??)
      if ! grep -qi '^content-type: application/problem+json' "$CHECK_TMP/headers" ||
         ! grep -Fq "\"status\":$want" "$CHECK_TMP/body"; then
        printf '  FAIL error did not use the documented problem response\n'
        FAIL=1
      fi
      ;;
  esac
}
body_contains() {
  if ! grep -Fq "$2" "$CHECK_TMP/body"; then
    printf '  FAIL %s: %s\n' "$1" "$(head -c 300 "$CHECK_TMP/body")"
    FAIL=1
  fi
}

step "4/5 every operation on n1 and through n2"
for B in http://localhost:18080 http://localhost:18081; do
  printf '\n %s\n' "$B"
  expect 201 POST "/v1/games" -H "$J" -d '{"name":"Kareem"}'
  body_contains "creator name" '"name":"Kareem"'
  expect 200 GET "/v1/games/demo"
  body_contains "REST enum conversion" '"phase":"playing"'
  body_contains "optional seat 0" '"nextSeat":0'
  expect 200 GET "/v1/games/demo?consistency=sequential"
  expect 200 GET "/v1/games/demo?consistency=linearizable"
  expect 201 POST "/v1/games/demo/players" -H "$J" -d '{"name":"Player Two","seat":1}'
  expect 201 POST "/v1/games/demo/deals" -H "$J" -d '{"playerId":"player-0"}'
  expect 201 POST "/v1/games/demo/deals/1/cuts" -H "$J" -d '{"playerId":"player-3","cutPosition":20}'
  body_contains "empty bids array" '"bids":[]'
  expect 201 POST "/v1/games/demo/deals/1/bids" -H "$J" -d '{"playerId":"player-0","amount":8}'
  body_contains "doubled bid points" '"points":16'
  expect 201 POST "/v1/games/demo/deals/1/plays" -H "$J" -d '{"playerId":"player-0","card":"10H"}'
  body_contains "played card" '"card":"10H"'
  expect 200 GET "/v1/games/demo/players/player-0/hand"
  expect 200 GET "/v1/games/demo/players/player-0/hand?consistency=sequential"
  expect 200 GET "/v1/games/demo/players/player-0/hand?consistency=linearizable"
  expect 200 GET "/v1/games/empty-hand/players/player-0/hand"
  body_contains "empty hand array" '"cards":[]'
  expect 200 GET "/v1/games/empty-hand"

  expect 400 POST "/v1/games" -H "$J" -d '{bad json'
  expect 400 POST "/v1/games" -H "$J" -d '{"name":""}'
  expect 400 POST "/v1/games/demo/deals/abc/cuts" -H "$J" -d '{"playerId":"player-3","cutPosition":20}'
  expect 400 GET "/v1/games/demo?consistency=other"
  expect 400 GET "/v1/games/demo/players/player-0/hand?consistency=other"
  expect 404 GET "/v1/games/missing"
  expect 404 GET "/v1/games/demo/players/missing/hand"
  expect 409 POST "/v1/games/demo/players" -H "$J" -d '{"name":"Player Two","seat":0}'
  expect 412 POST "/v1/games/blocked/deals" -H "$J" -d '{"playerId":"player-0"}'
  expect 412 POST "/v1/games/demo/deals" -H "$J" -d '{"playerId":"player-1"}'
  expect 412 POST "/v1/games/demo/deals/1/cuts" -H "$J" -d '{"playerId":"player-0","cutPosition":20}'
  expect 400 POST "/v1/games/demo/deals/1/cuts" -H "$J" -d '{"playerId":"player-3","cutPosition":53}'
  expect 404 POST "/v1/games/demo/deals/404/cuts" -H "$J" -d '{"playerId":"player-3","cutPosition":20}'
  expect 400 POST "/v1/games/demo/deals/1/bids" -H "$J" -d '{"playerId":"player-0","amount":6}'
  expect 412 POST "/v1/games/high-score/deals/1/bids" -H "$J" -d '{"playerId":"player-0","amount":2}'
  expect 400 POST "/v1/games/demo/deals/1/plays" -H "$J" -d '{"playerId":"player-0","card":"JOKER"}'
  expect 412 POST "/v1/games/demo/deals/1/plays" -H "$J" -d '{"playerId":"player-0","card":"2C"}'
  expect 412 GET "/v1/games/blocked/players/player-0/hand"
done

step "5/5 forwarding failure when n1 stops"
kill "$P1"
wait "$P1" || true
P1=
B=http://localhost:18081
expect 503 GET "/v1/games/demo"

if [ "$FAIL" -ne 0 ]; then
  printf '\nSOME CHECKS FAILED\n'
  exit 1
fi
printf '\nALL CHECKS PASSED\n'
