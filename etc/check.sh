#!/usr/bin/env sh
# Build, race tests, two real processes, stateful requests, capacity and restart.
set -eu
cd "$(dirname "$0")/.."
for tool in go protoc make curl python3; do
  command -v "$tool" >/dev/null || { echo "$tool is required"; exit 1; }
done
make build
make test
logs=$(mktemp -d /tmp/41-check.XXXXXX)
p1= p2= p3=
cleanup() {
  for pid in "$p1" "$p2" "$p3"; do
    if [ -n "$pid" ]; then kill "$pid" 2>/dev/null || true; fi
  done
  for pid in "$p1" "$p2" "$p3"; do
    if [ -n "$pid" ]; then wait "$pid" 2>/dev/null || true; fi
  done
}
trap cleanup EXIT INT TERM
ready() {
  tries=0
  until curl -fsS "$1/openapi.yaml" >/dev/null 2>&1; do
    tries=$((tries+1))
    if [ "$tries" -ge 50 ]; then
      echo "Server did not start. Logs: $logs"
      cat "$logs"/*.log
      exit 1
    fi
    sleep 0.1
  done
}
./bin/server -id n1 -http :18080 -grpc :15051 -snapshot etc/sample-snapshot.txtpb >"$logs/n1.log" 2>&1 & p1=$!
./bin/server -id n2 -http :18081 -grpc :15052 -target localhost:15051 >"$logs/n2.log" 2>&1 & p2=$!
ready http://localhost:18080
ready http://localhost:18081
./etc/demo.sh http://localhost:18080 http://localhost:18081

./bin/server -id capacity -http :18082 -grpc :15053 -snapshot etc/capacity-snapshot.txtpb >"$logs/capacity.log" 2>&1 & p3=$!
ready http://localhost:18082
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST http://localhost:18082/v1/games -H 'Content-Type: application/json' -d '{"name":"one"}')
[ "$code" = 201 ] || { echo "capacity setup failed: $code"; exit 1; }
code=$(curl -s -o /dev/null -w '%{http_code}' -X POST http://localhost:18082/v1/games -H 'Content-Type: application/json' -d '{"name":"two"}')
[ "$code" = 503 ] || { echo "capacity error: $code, expected 503"; exit 1; }
echo "PASS configured capacity returns 503"

kill "$p1"
wait "$p1" 2>/dev/null || true
p1=
code=$(curl -s -o /dev/null -w '%{http_code}' http://localhost:18081/v1/games/game-1)
[ "$code" = 503 ] || { echo "forwarding error: $code, expected 503"; exit 1; }
echo "PASS n2 returns 503 when n1 is stopped"

./bin/server -id n1 -http :18080 -grpc :15051 -snapshot etc/sample-snapshot.txtpb >"$logs/restart.log" 2>&1 & p1=$!
ready http://localhost:18080
code=$(curl -s -o /dev/null -w '%{http_code}' http://localhost:18080/v1/games/game-2)
[ "$code" = 404 ] || { echo "restart retained nonpersistent state: $code"; exit 1; }
curl -fsS http://localhost:18080/v1/games/game-1 | python3 -c 'import json,sys; g=json.load(sys.stdin)["game"]; assert g["phase"]=="ready" and len(g["players"])==4'
echo "PASS restart restores the initial snapshot"
echo "ALL CHECKS PASSED (logs: $logs)"
