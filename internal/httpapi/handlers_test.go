package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"github.com/KareemAraide/distributed-systems-41/api"
	pb "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"github.com/KareemAraide/distributed-systems-41/internal/grpcsvc"
	"github.com/KareemAraide/distributed-systems-41/internal/sm"
	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers/legacy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/test/bufconn"
	"google.golang.org/protobuf/encoding/prototext"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
)

func httpFor(t *testing.T, server pb.GameServiceServer) http.Handler {
	t.Helper()
	lis := bufconn.Listen(1 << 20)
	gs := grpc.NewServer()
	pb.RegisterGameServiceServer(gs, server)
	go func() { _ = gs.Serve(lis) }()
	conn, err := grpc.NewClient("passthrough:///test", grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithContextDialer(func(ctx context.Context, _ string) (net.Conn, error) { return lis.DialContext(ctx) }))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close(); gs.Stop(); lis.Close() })
	return NewHTTPHandler(pb.NewGameServiceClient(conn), http.NewServeMux())
}
func stateHTTP(t *testing.T, state *pb.State) http.Handler {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	return httpFor(t, grpcsvc.New(ctx, "n1", state, 0))
}
func request(t *testing.T, h http.Handler, method, path string, body any, want int) map[string]any {
	t.Helper()
	var b []byte
	if body != nil {
		if raw, ok := body.(string); ok {
			b = []byte(raw)
		} else {
			var err error
			b, err = json.Marshal(body)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	r := httptest.NewRequest(method, path, bytes.NewReader(b))
	if body != nil {
		r.Header.Set("Content-Type", "application/json")
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != want {
		t.Fatalf("%s %s: %d, want %d: %s", method, path, w.Code, want, w.Body.String())
	}
	if want >= 400 && !strings.HasPrefix(w.Header().Get("Content-Type"), "application/problem+json") {
		t.Fatal("error is not problem+json")
	}
	var out map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	// Successful and problem responses must satisfy our published schema.
	doc, err := openapi3.NewLoader().LoadFromData(api.Spec)
	if err != nil {
		t.Fatal(err)
	}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	route, params, err := router.FindRoute(r)
	if err == nil {
		input := &openapi3filter.ResponseValidationInput{RequestValidationInput: &openapi3filter.RequestValidationInput{Request: r, Route: route, PathParams: params}, Status: w.Code, Header: w.Header()}
		input.SetBodyBytes(w.Body.Bytes())
		if err = openapi3filter.ValidateResponse(context.Background(), input); err != nil {
			t.Fatalf("response violates OpenAPI: %v", err)
		}
	}
	return out
}
func gameOf(r map[string]any) map[string]any { return r["game"].(map[string]any) }
func playerID(g map[string]any, seat int) string {
	for _, v := range g["players"].([]any) {
		p := v.(map[string]any)
		if int(p["seat"].(float64)) == seat {
			return p["id"].(string)
		}
	}
	return ""
}
func TestOpenAPIContract(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromData(api.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if err = doc.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	service := pb.File_game_v1_game_proto.Services().ByName("GameService")
	if service.Methods().Len() != 4 {
		t.Fatal("expected four RPCs")
	}
	for i := 0; i < service.Methods().Len(); i++ {
		m := service.Methods().Get(i)
		if m.IsStreamingServer() && m.Output().Fields().ByName("fin") == nil {
			t.Fatal("stream lacks fin")
		}
	}
}
func TestStatefulRESTWorkflow(t *testing.T) {
	h := stateHTTP(t, sm.NewState())
	g := gameOf(request(t, h, "POST", "/v1/games", `{"name":"Kareem"}`, 201))
	id := g["id"].(string)
	for seat, name := range []string{"Awes", "Dana", "Sami"} {
		g = gameOf(request(t, h, "POST", "/v1/games/"+id+"/players", map[string]any{"name": name, "seat": seat + 1}, 201))
	}
	if g["phase"] != "ready" {
		t.Fatal(g)
	}
	request(t, h, "GET", "/v1/games/"+id+"/players/"+playerID(g, 0)+"/hand", nil, 412)
	request(t, h, "POST", "/v1/games/"+id+"/deals", map[string]any{"playerId": playerID(g, 1)}, 412)
	g = gameOf(request(t, h, "POST", "/v1/games/"+id+"/deals", map[string]any{"playerId": playerID(g, 0)}, 201))
	hand := request(t, h, "GET", "/v1/games/"+id+"/players/"+playerID(g, 0)+"/hand", nil, 200)
	if len(hand["cards"].([]any)) != 13 {
		t.Fatal(hand)
	}
	for i, amount := range []int{3, 3, 3, 2} {
		g = gameOf(request(t, h, "POST", "/v1/games/"+id+"/deals/1/bids", map[string]any{"playerId": playerID(g, i), "amount": amount}, 201))
	}
	if g["phase"] != "playing" {
		t.Fatal(g)
	}
	play := map[string]any{"playerId": playerID(g, 0), "card": hand["cards"].([]any)[0]}
	request(t, h, "POST", "/v1/games/"+id+"/deals/1/plays", play, 201)
	request(t, h, "POST", "/v1/games/"+id+"/deals/1/plays", play, 412)
	view := gameOf(request(t, h, "GET", "/v1/games/"+id+"?fields=draw,dealerSeat", nil, 200))
	if len(view) != 3 || view["draw"] != false || view["dealerSeat"] != float64(0) {
		t.Fatal("masked zero fields omitted", view)
	}
	g2 := gameOf(request(t, h, "POST", "/v1/games", `{"name":"another"}`, 201))
	id2 := g2["id"].(string)
	batch := map[string]any{"actions": []any{
		map[string]any{"gameId": id2, "join": map[string]any{"name": "Awes", "seat": 1}},
		map[string]any{"gameId": id2, "join": map[string]any{"name": "bad", "seat": 0}},
	}}
	request(t, h, "PATCH", "/v1/games", batch, 409)
	unchanged := gameOf(request(t, h, "GET", "/v1/games/"+id2, nil, 200))
	if len(unchanged["players"].([]any)) != 1 {
		t.Fatal("batch partially committed")
	}
	batch["actions"] = []any{
		map[string]any{"gameId": id2, "join": map[string]any{"name": "Awes", "seat": 1}},
		map[string]any{"gameId": id2, "join": map[string]any{"name": "Dana", "seat": 2}},
	}
	result := request(t, h, "PATCH", "/v1/games", batch, 200)
	games := result["games"].([]any)
	if len(games[0].(map[string]any)["players"].([]any)) != 2 || len(games[1].(map[string]any)["players"].([]any)) != 3 {
		t.Fatal("batch results are not per-action snapshots")
	}
	if playerID(games[0].(map[string]any), 1) != "player-6" {
		t.Fatal("failed batch consumed an ID")
	}
	read := request(t, h, "GET", "/v1/games?ids="+id2+"&ids="+id, nil, 200)["games"].([]any)
	if read[0].(map[string]any)["id"] != id2 || read[1].(map[string]any)["id"] != id {
		t.Fatal("read order changed")
	}
	if result["nodeId"] != "n1" {
		t.Fatal("wrong backend node")
	}
	request(t, h, "GET", "/v1/games?ids="+id+"&ids=missing", nil, 404)
}
func TestRESTErrorsAndCapacity(t *testing.T) {
	s := sm.NewState()
	s.GameLimit = 1
	h := stateHTTP(t, s)
	for _, tc := range []struct {
		method, path, body string
		code               int
	}{
		{"POST", "/v1/games", `{}`, 400}, {"POST", "/v1/games", `{bad`, 400},
		{"POST", "/v1/games", `{"name":"Kareem","deck":["AH"]}`, 400},
		{"GET", "/v1/games?ids=missing", "", 404}, {"GET", "/v1/games", "", 400},
		{"GET", "/v1/games?ids=a&ids=a", "", 400}, {"GET", "/v1/games/a?fields=hand", "", 400},
		{"GET", "/v1/games/a?fields=", "", 400}, {"PATCH", "/v1/games", `{"actions":[]}`, 400},
		{"PATCH", "/v1/games", `{"actions":[{"gameId":"a"}]}`, 400},
	} {
		var body any
		if tc.body != "" {
			body = tc.body
		}
		request(t, h, tc.method, tc.path, body, tc.code)
	}
	request(t, h, "POST", "/v1/games", `{"name":"Kareem"}`, 201)
	request(t, h, "POST", "/v1/games", `{"name":"Kareem"}`, 503)
	request(t, h, "POST", "/v1/games/game-1/players", `{"name":"Awes","seat":0}`, 409)
	request(t, h, "POST", "/v1/games/game-1/players", `{"name":"Awes"}`, 400)
	request(t, h, "POST", "/v1/games/game-1/deals", `{"playerId":"player-1"}`, 412)
	request(t, h, "GET", "/v1/games/game-1/players/missing/hand", nil, 404)
}
func TestWinnerZeroNegativeScoreAndEmptyHand(t *testing.T) {
	b, err := os.ReadFile("../sm/testdata/last-trick.txtpb")
	if err != nil {
		t.Fatal(err)
	}
	s := &pb.State{}
	if err = prototext.Unmarshal(b, s); err != nil {
		t.Fatal(err)
	}
	s.Games["game-1"].Participants[0].Score = 37
	s.Games["game-1"].Participants[1].Score = -7
	h := stateHTTP(t, s)
	for i, card := range []string{"AH", "KH", "QH", "JH"} {
		request(t, h, "POST", "/v1/games/game-1/deals/1/plays", map[string]any{"playerId": fmt.Sprintf("player-%d", i+1), "card": card}, 201)
	}
	g := gameOf(request(t, h, "GET", "/v1/games/game-1?fields=winnerTeam,draw,players", nil, 200))
	if g["winnerTeam"] != float64(0) || g["draw"] != false || g["players"].([]any)[1].(map[string]any)["score"] != float64(-4) {
		t.Fatal(g)
	}
	hand := request(t, h, "GET", "/v1/games/game-1/players/player-1/hand", nil, 200)
	if cards, ok := hand["cards"].([]any); !ok || len(cards) != 0 {
		t.Fatal("empty hand is not []", hand)
	}
	request(t, h, "POST", "/v1/games/game-1/deals", `{"playerId":"player-1"}`, 412)
}
func TestHTTPConcurrentCreates(t *testing.T) {
	h := stateHTTP(t, sm.NewState())
	const n = 30
	var wg sync.WaitGroup
	ids := make(chan string, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g := gameOf(request(t, h, "POST", "/v1/games", `{"name":"same"}`, 201))
			ids <- g["id"].(string)
		}()
	}
	wg.Wait()
	close(ids)
	seen := map[string]bool{}
	for id := range ids {
		if seen[id] {
			t.Fatal("duplicate ID")
		}
		seen[id] = true
		request(t, h, "GET", "/v1/games/"+id, nil, 200)
	}
	if len(seen) != n {
		t.Fatal("lost create")
	}
}
