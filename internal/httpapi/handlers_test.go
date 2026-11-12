package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/getkin/kin-openapi/routers"
	"github.com/getkin/kin-openapi/routers/legacy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/status"

	"github.com/KareemAraide/distributed-systems-41/api"
	gamev1 "github.com/KareemAraide/distributed-systems-41/gen/game/v1"
	"github.com/KareemAraide/distributed-systems-41/gen/rest"
	"github.com/KareemAraide/distributed-systems-41/internal/grpcsvc"
)

func testHTTPHandler(t *testing.T, service gamev1.GameServiceServer) http.Handler {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server := grpc.NewServer()
	gamev1.RegisterGameServiceServer(server, service)
	go func() { _ = server.Serve(listener) }()
	t.Cleanup(server.Stop)
	conn, err := grpc.NewClient(listener.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return NewHTTPHandler(gamev1.NewGameServiceClient(conn), http.NewServeMux())
}

func testOpenAPIRouter(t *testing.T) routers.Router {
	t.Helper()
	doc, err := openapi3.NewLoader().LoadFromData(api.Spec)
	if err != nil {
		t.Fatal(err)
	}
	if err := doc.Validate(context.Background()); err != nil {
		t.Fatal(err)
	}
	router, err := legacy.NewRouter(doc)
	if err != nil {
		t.Fatal(err)
	}
	return router
}

func checkREST(t *testing.T, handler http.Handler, router routers.Router, method, path, body string, want int) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, "http://localhost:8080"+path, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != want {
		t.Fatalf("%s %s: wanted %d, got %d: %s", method, path, want, response.Code, response.Body.String())
	}
	route, params, err := router.FindRoute(request)
	if err != nil {
		t.Fatal(err)
	}
	err = openapi3filter.ValidateResponse(context.Background(), &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request: request, Route: route, PathParams: params,
		},
		Status: response.Code, Header: response.Header(),
		Body:    io.NopCloser(bytes.NewReader(response.Body.Bytes())),
		Options: &openapi3filter.Options{IncludeResponseStatus: true},
	})
	if err != nil {
		t.Fatalf("response violates openapi.yaml: %v\n%s", err, response.Body.String())
	}
	if want >= 400 {
		if response.Header().Get("Content-Type") != "application/problem+json" {
			t.Fatalf("wrong error content type: %v", response.Header())
		}
		var problem rest.Problem
		if err := json.Unmarshal(response.Body.Bytes(), &problem); err != nil || int(problem.Status) != want {
			t.Fatalf("wrong problem body: %s", response.Body.String())
		}
	} else if !strings.Contains(response.Body.String(), `"nodeId":"backend"`) {
		t.Fatalf("response lost the backend identity: %s", response.Body.String())
	}
	return response
}

func TestRESTResponsesMatchOpenAPI(t *testing.T) {
	handler := testHTTPHandler(t, grpcsvc.New("backend"))
	router := testOpenAPIRouter(t)
	cases := []struct {
		name, method, path, body string
		status                   int
	}{
		{"create", "POST", "/v1/games", `{"name":"Kareem"}`, 201},
		{"read default", "GET", "/v1/games/demo", "", 200},
		{"read sequential", "GET", "/v1/games/demo?consistency=sequential", "", 200},
		{"read linearizable", "GET", "/v1/games/demo?consistency=linearizable", "", 200},
		{"join", "POST", "/v1/games/demo/players", `{"name":"Player Two","seat":1}`, 201},
		{"start", "POST", "/v1/games/demo/deals", `{"playerId":"player-0"}`, 201},
		{"cut", "POST", "/v1/games/demo/deals/1/cuts", `{"playerId":"player-3","cutPosition":20}`, 201},
		{"bid", "POST", "/v1/games/demo/deals/1/bids", `{"playerId":"player-0","amount":8}`, 201},
		{"play", "POST", "/v1/games/demo/deals/1/plays", `{"playerId":"player-0","card":"10H"}`, 201},
		{"hand", "GET", "/v1/games/demo/players/player-0/hand", "", 200},
		{"empty hand", "GET", "/v1/games/empty-hand/players/player-0/hand", "", 200},
		{"completed deal", "GET", "/v1/games/empty-hand", "", 200},
		{"cutting deal", "GET", "/v1/games/blocked", "", 200},
		{"bad JSON", "POST", "/v1/games", "{", 400},
		{"empty body", "POST", "/v1/games", "", 400},
		{"empty name", "POST", "/v1/games", `{"name":""}`, 400},
		{"bad path parameter", "POST", "/v1/games/demo/deals/abc/cuts", `{"playerId":"player-3","cutPosition":20}`, 400},
		{"bad consistency", "GET", "/v1/games/demo?consistency=other", "", 400},
		{"missing game", "GET", "/v1/games/missing", "", 404},
		{"occupied seat", "POST", "/v1/games/demo/players", `{"name":"Player Two","seat":0}`, 409},
		{"blocked action", "POST", "/v1/games/blocked/deals", `{"playerId":"player-0"}`, 412},
		{"wrong actor", "POST", "/v1/games/demo/deals", `{"playerId":"player-1"}`, 412},
		{"bid 6", "POST", "/v1/games/demo/deals/1/bids", `{"playerId":"player-0","amount":6}`, 400},
		{"low bid", "POST", "/v1/games/high-score/deals/1/bids", `{"playerId":"player-0","amount":2}`, 412},
		{"card not held", "POST", "/v1/games/demo/deals/1/plays", `{"playerId":"player-0","card":"2C"}`, 412},
		{"blocked hand", "GET", "/v1/games/blocked/players/player-0/hand", "", 412},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			response := checkREST(t, handler, router, tc.method, tc.path, tc.body, tc.status)
			if tc.name == "empty hand" && !strings.Contains(response.Body.String(), `"cards":[]`) {
				t.Fatalf("empty cards must be an array: %s", response.Body.String())
			}
			if tc.name == "read default" && !strings.Contains(response.Body.String(), `"nextSeat":0`) {
				t.Fatalf("optional seat 0 was lost: %s", response.Body.String())
			}
		})
	}
}

type recordingService struct {
	*grpcsvc.Server
	reads chan gamev1.Consistency
}

func (s *recordingService) GetGame(ctx context.Context, req *gamev1.GetGameRequest) (*gamev1.GetGameResponse, error) {
	s.reads <- req.GetConsistency()
	return s.Server.GetGame(ctx, req)
}

func (s *recordingService) GetHand(req *gamev1.GetHandRequest, stream grpc.ServerStreamingServer[gamev1.GetHandResponse]) error {
	s.reads <- req.GetConsistency()
	return s.Server.GetHand(req, stream)
}

func TestReadConsistencyReachesGRPC(t *testing.T) {
	service := &recordingService{Server: grpcsvc.New("backend"), reads: make(chan gamev1.Consistency, 1)}
	handler := testHTTPHandler(t, service)
	router := testOpenAPIRouter(t)
	for _, path := range []string{"/v1/games/demo", "/v1/games/demo/players/player-0/hand"} {
		for _, tc := range []struct {
			query string
			want  gamev1.Consistency
		}{
			{"", gamev1.Consistency_CONSISTENCY_SEQUENTIAL},
			{"?consistency=sequential", gamev1.Consistency_CONSISTENCY_SEQUENTIAL},
			{"?consistency=linearizable", gamev1.Consistency_CONSISTENCY_LINEARIZABLE},
		} {
			checkREST(t, handler, router, "GET", path+tc.query, "", 200)
			select {
			case got := <-service.reads:
				if got != tc.want {
					t.Fatalf("wanted %s, got %s", tc.want, got)
				}
			case <-time.After(time.Second):
				t.Fatal("read never reached gRPC")
			}
		}
	}
}

type faultyService struct {
	*grpcsvc.Server
}

func (s *faultyService) GetGame(ctx context.Context, req *gamev1.GetGameRequest) (*gamev1.GetGameResponse, error) {
	switch req.GetGameId() {
	case "internal":
		return nil, status.Error(codes.Internal, "backend failed")
	case "unimplemented":
		return nil, status.Error(codes.Unimplemented, "RPC not implemented")
	case "unavailable":
		return nil, status.Error(codes.Unavailable, "backend unavailable")
	case "deadline":
		return nil, status.Error(codes.DeadlineExceeded, "backend deadline expired")
	case "wait":
		<-ctx.Done()
		return nil, status.FromContextError(ctx.Err()).Err()
	case "missing-info":
		return &gamev1.GetGameResponse{}, nil
	case "bad-phase":
		return &gamev1.GetGameResponse{Game: &gamev1.Game{Phase: gamev1.GamePhase(99)}, NodeId: "backend"}, nil
	case "zero-winner":
		reply, err := s.Server.GetGame(ctx, &gamev1.GetGameRequest{GameId: "empty-hand"})
		if err != nil {
			return nil, err
		}
		zero := int32(0)
		reply.Game.Id = req.GetGameId()
		reply.Game.Phase = gamev1.GamePhase_GAME_PHASE_FINISHED
		reply.Game.WinnerTeam = &zero
		reply.Game.Players[0].Score = 41
		reply.Game.Players[0].MinimumBid = 4
		reply.Game.Players[1].Score = -4
		reply.Game.CurrentDeal.LastTrick.WinnerSeat = &zero
		return reply, nil
	default:
		return s.Server.GetGame(ctx, req)
	}
}

func (s *faultyService) GetHand(req *gamev1.GetHandRequest, stream grpc.ServerStreamingServer[gamev1.GetHandResponse]) error {
	if req.GetGameId() == "no-messages" {
		return nil
	}
	if req.GetGameId() == "no-info" {
		return stream.Send(&gamev1.GetHandResponse{Item: &gamev1.GetHandResponse_Card{Card: "10H"}})
	}
	info := &gamev1.GetHandResponse{Item: &gamev1.GetHandResponse_Info{Info: &gamev1.HandInfo{
		GameId: req.GetGameId(), PlayerId: req.GetPlayerId(), DealNumber: 1, NodeId: "backend",
	}}}
	if err := stream.Send(info); err != nil {
		return err
	}
	if req.GetGameId() == "late-error" {
		if err := stream.Send(&gamev1.GetHandResponse{Item: &gamev1.GetHandResponse_Card{Card: "10H"}}); err != nil {
			return err
		}
		return status.Error(codes.FailedPrecondition, "stream failed after the first card")
	}
	return stream.Send(info)
}

func TestBackendFailuresRemainProblemResponses(t *testing.T) {
	handler := testHTTPHandler(t, &faultyService{Server: grpcsvc.New("backend")})
	router := testOpenAPIRouter(t)
	for _, tc := range []struct {
		game string
		code int
	}{
		{"internal", 500}, {"unimplemented", 501}, {"unavailable", 503},
		{"deadline", 504}, {"missing-info", 500}, {"bad-phase", 500},
	} {
		t.Run(tc.game, func(t *testing.T) {
			checkREST(t, handler, router, "GET", "/v1/games/"+tc.game, "", tc.code)
		})
	}
	for _, tc := range []struct {
		game string
		code int
	}{
		{"no-messages", 500}, {"no-info", 500}, {"repeated-info", 500}, {"late-error", 412},
	} {
		t.Run(tc.game, func(t *testing.T) {
			checkREST(t, handler, router, "GET", "/v1/games/"+tc.game+"/players/player-0/hand", "", tc.code)
		})
	}

	reply := checkREST(t, handler, router, "GET", "/v1/games/zero-winner", "", 200)
	var out rest.GameResponse
	if err := json.Unmarshal(reply.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Game.WinnerTeam == nil || *out.Game.WinnerTeam != 0 ||
		out.Game.CurrentDeal.LastTrick.WinnerSeat == nil || *out.Game.CurrentDeal.LastTrick.WinnerSeat != 0 ||
		out.Game.Players[1].Score != -4 {
		t.Fatalf("zero-valued optional fields or negative scores were lost: %s", reply.Body.String())
	}
}

func TestRequestDeadlineReachesGRPC(t *testing.T) {
	handler := testHTTPHandler(t, &faultyService{Server: grpcsvc.New("backend")})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	request := httptest.NewRequest("GET", "/v1/games/wait", nil).WithContext(ctx)
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != http.StatusGatewayTimeout || !strings.Contains(response.Body.String(), `"status":504`) {
		t.Fatalf("wanted timeout problem response, got %d: %s", response.Code, response.Body.String())
	}
}
