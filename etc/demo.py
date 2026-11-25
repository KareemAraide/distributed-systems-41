#!/usr/bin/env python3
"""Stateful REST walkthrough. Uses only Python's standard library."""
import concurrent.futures
import json
import sys
import urllib.error
import urllib.request

BASE = (sys.argv[1] if len(sys.argv) > 1 else "http://localhost:8080").rstrip("/")
PEER = (sys.argv[2] if len(sys.argv) > 2 else BASE).rstrip("/")


def call(method, path, body=None, expected=200, base=BASE):
    data = None if body is None else (body.encode() if isinstance(body, str) else json.dumps(body).encode())
    req = urllib.request.Request(base + "/v1" + path, data=data, method=method)
    if data is not None:
        req.add_header("Content-Type", "application/json")
    try:
        with urllib.request.urlopen(req, timeout=10) as reply:
            code, headers, raw = reply.status, reply.headers, reply.read()
    except urllib.error.HTTPError as reply:
        code, headers, raw = reply.code, reply.headers, reply.read()
    result = json.loads(raw)
    if expected is not None:
        assert code == expected, (method, path, code, expected, result)
    if code >= 400:
        assert headers.get_content_type() == "application/problem+json", headers
    else:
        assert result["nodeId"] == "n1", result
    return code, result


def get_game(game_id, base=BASE):
    return call("GET", "/games/" + game_id, base=base)[1]["game"]


def pid(game, seat):
    return next(p["id"] for p in game["players"] if p["seat"] == seat)


def passed(message):
    print("PASS " + message, flush=True)


_, made = call("POST", "/games", {"name": "Kareem"}, 201)
game = made["game"]
gid = game["id"]
path = "/games/" + gid
_, joined = call("POST", path + "/players", {"name": "Awes", "seat": 1}, 201)
game = joined["game"]
call("GET", path + "/players/" + pid(game, 0) + "/hand", expected=412)
call("POST", path + "/players", {"name": "occupied", "seat": 0}, 409)
call("POST", "/games", "{bad json", 400)
call("POST", "/games", {"name": "bad", "deck": ["AH"]}, 400)
call("GET", "/games/missing", expected=404)
call("GET", path + "?fields=hand", expected=400)
call("GET", "/games?ids=" + gid + "&ids=" + gid, expected=400)

# The last action fails. Its earlier join and allocated player ID must disappear.
call("PATCH", "/games", {"actions": [
    {"gameId": gid, "join": {"name": "Dana", "seat": 2}},
    {"gameId": gid, "join": {"name": "blocked", "seat": 0}},
]}, 409)
assert len(get_game(gid)["players"]) == 2
_, batch = call("PATCH", "/games", {"actions": [
    {"gameId": gid, "join": {"name": "Dana", "seat": 2}},
    {"gameId": gid, "join": {"name": "Sami", "seat": 3}},
]})
assert [len(g["players"]) for g in batch["games"]] == [3, 4]
game = batch["games"][-1]
assert game["phase"] == "ready"
passed("create, joins, atomic batches, validation, and writes visible through reads")

_, other = call("POST", "/games", {"name": "Kareem"}, 201)
other_id = other["game"]["id"]
assert other_id != gid
_, read = call("GET", "/games?ids=" + other_id + "&ids=" + gid, base=PEER)
assert [g["id"] for g in read["games"]] == [other_id, gid]
_, masked = call("GET", path + "?fields=draw,dealerSeat", base=PEER)
assert masked["game"] == {"id": gid, "draw": False, "dealerSeat": 0}
call("GET", "/games?ids=" + gid + "&ids=missing", expected=404)
passed("single and batch reads, field masks, and forwarding to n1")

call("POST", path + "/deals", {"playerId": pid(game, 1)}, 412)
_, started = call("POST", path + "/deals", {"playerId": pid(game, 0)}, 201)
game = started["game"]
seen = set()
for seat in range(4):
    _, hand = call("GET", path + "/players/" + pid(game, seat) + "/hand")
    assert len(hand["cards"]) == 13
    assert not seen.intersection(hand["cards"])
    seen.update(hand["cards"])
assert len(seen) == 52
call("POST", path + "/deals/1/bids", {"playerId": pid(game, 0), "amount": 6}, 400)
call("POST", path + "/deals/1/bids", {"playerId": pid(game, 1), "amount": 2}, 412)
for seat in range(4):
    _, result = call("POST", path + "/deals/1/bids", {"playerId": pid(game, seat), "amount": 2}, 201)
    game = result["game"]
assert game["phase"] == "ready" and game["dealerSeat"] == 1
assert game["currentDeal"]["status"] == "skipped"
assert all(p["score"] == 0 for p in game["players"])
passed("52-card shuffle/deal, bidding order, rejected bid 6, and skip without scoring")

_, started = call("POST", path + "/deals", {"playerId": pid(game, 1)}, 201)
game = started["game"]
assert game["currentDeal"]["number"] == 2
for offset, amount in enumerate([3, 3, 3, 2]):
    seat = (1 + offset) % 4
    _, result = call("POST", path + "/deals/2/bids", {"playerId": pid(game, seat), "amount": amount}, 201)
    game = result["game"]
assert game["phase"] == "playing"
plays = 0
while game["phase"] == "playing":
    deal = game["currentDeal"]
    player = pid(game, deal["nextSeat"])
    _, hand = call("GET", path + "/players/" + player + "/hand", base=PEER)
    cards = hand["cards"]
    trick = deal["currentTrick"]["plays"]
    legal = cards
    if trick:
        lead = trick[0]["card"][-1]
        same = [card for card in cards if card[-1] == lead]
        if same:
            legal = same
    card = legal[0]
    _, result = call("POST", path + "/deals/2/plays", {"playerId": player, "card": card}, 201)
    game = result["game"]
    if plays == 0:
        call("POST", path + "/deals/2/plays", {"playerId": player, "card": card}, 412)
    plays += 1
assert plays == 52 and game["currentDeal"]["completedTricks"] == 13
wins = {p["playerId"]: p["tricksWon"] for p in game["currentDeal"]["playerStates"]}
scores = {p["id"]: p["score"] for p in game["players"]}
for bid in game["currentDeal"]["bids"]:
    expected = bid["points"] if wins[bid["playerId"]] >= bid["amount"] else -bid["points"]
    assert scores[bid["playerId"]] == expected
for seat in range(4):
    _, hand = call("GET", path + "/players/" + pid(game, seat) + "/hand")
    assert hand["cards"] == []
assert get_game(gid, PEER) == game
passed("complete 13-trick deal, duplicate play rejection, scoring, and empty hands")

# Competing for one shared seat exercises a check followed by an update.
_, race = call("POST", "/games", {"name": "race"}, 201)
race_id = race["game"]["id"]
def claim(i):
    return call("POST", "/games/" + race_id + "/players",
                {"name": "contender " + str(i), "seat": 1},
                expected=None, base=BASE if i % 2 else PEER)[0]
with concurrent.futures.ThreadPoolExecutor(max_workers=16) as pool:
    codes = list(pool.map(claim, range(40)))
assert codes.count(201) == 1 and codes.count(409) == 39, codes
assert len(get_game(race_id)["players"]) == 2
passed("40 simultaneous seat claims: exactly one succeeds")

def create(i):
    return call("POST", "/games", {"name": "parallel " + str(i)}, 201,
                base=BASE if i % 2 else PEER)[1]["game"]["id"]
with concurrent.futures.ThreadPoolExecutor(max_workers=16) as pool:
    ids = list(pool.map(create, range(40)))
assert len(set(ids)) == 40
for game_id in ids:
    assert get_game(game_id, PEER)["id"] == game_id
passed("40 concurrent creates: distinct IDs, no lost games")
print("ALL DEMO CHECKS PASSED", flush=True)
