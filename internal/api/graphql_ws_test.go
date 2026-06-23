package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/teo-dev/teo/internal/auth"
)

// wsTestHub builds a Postgres-free hub for transport tests with the given
// snapshot and safety interval.
func wsTestHub(snap runSnapshotFn, safety time.Duration) *Hub {
	return &Hub{
		runs:     make(map[string]*runFanout),
		snapshot: snap,
		baseCtx:  context.Background(),
		safety:   safety,
	}
}

func withTestPrincipal(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := &auth.Principal{Roles: []auth.Role{auth.RoleEngineer}}
		h.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), p)))
	})
}

// dialWS opens a graphql-transport-ws connection to srv.
func dialWS(t *testing.T, srv *httptest.Server) *websocket.Conn {
	t.Helper()
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/graphql/subscriptions"
	conn, resp, err := websocket.Dial(t.Context(), url, &websocket.DialOptions{
		Subprotocols: []string{"graphql-transport-ws"},
	})
	if err != nil {
		t.Fatalf("dial: %v", err)
	}
	if resp != nil && resp.Body != nil {
		_ = resp.Body.Close()
	}
	return conn
}

func writeWS(t *testing.T, conn *websocket.Conn, msg wsMessage) {
	t.Helper()
	b, _ := json.Marshal(msg)
	if err := conn.Write(t.Context(), websocket.MessageText, b); err != nil {
		t.Fatalf("write %s: %v", msg.Type, err)
	}
}

func readWS(t *testing.T, conn *websocket.Conn) wsMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	_, data, err := conn.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var msg wsMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return msg
}

func TestSubscriptionHandlerRejectsUnauthenticated(t *testing.T) {
	hub := wsTestHub(func(_ context.Context, id string) (map[string]any, error) {
		return map[string]any{"id": id, "status": "running"}, nil
	}, time.Hour)
	h := graphqlSubscriptionHandler(buildSchemaWithHub(nil, hub), hub)

	// No principal in context (no withTestPrincipal wrapper) → 401 before upgrade.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/graphql/subscriptions", nil)
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status %d, want 401", rr.Code)
	}
}

func TestSubscriptionHandshakeAndFirstNext(t *testing.T) {
	hub := wsTestHub(func(_ context.Context, id string) (map[string]any, error) {
		return map[string]any{"id": id, "status": "running", "shards": []map[string]any{}}, nil
	}, time.Hour)
	srv := httptest.NewServer(withTestPrincipal(
		graphqlSubscriptionHandler(buildSchemaWithHub(nil, hub), hub)))
	defer srv.Close()

	conn := dialWS(t, srv)
	defer conn.Close(websocket.StatusNormalClosure, "")

	writeWS(t, conn, wsMessage{Type: wsConnectionInit})
	if ack := readWS(t, conn); ack.Type != wsConnectionAck {
		t.Fatalf("got %q, want connection_ack", ack.Type)
	}

	sub, _ := json.Marshal(wsSubscribePayload{
		Query: `subscription { runChanged(id: "r1") { id status } }`,
	})
	writeWS(t, conn, wsMessage{ID: "1", Type: wsSubscribe, Payload: sub})

	next := readWS(t, conn)
	if next.Type != wsNext || next.ID != "1" {
		t.Fatalf("got type=%q id=%q, want next/1", next.Type, next.ID)
	}
	var body struct {
		Data struct {
			RunChanged struct {
				ID     string `json:"id"`
				Status string `json:"status"`
			} `json:"runChanged"`
		} `json:"data"`
	}
	if err := json.Unmarshal(next.Payload, &body); err != nil {
		t.Fatalf("payload: %v", err)
	}
	if body.Data.RunChanged.ID != "r1" || body.Data.RunChanged.Status != "running" {
		t.Fatalf("got %+v, want runChanged{id:r1,status:running}", body.Data.RunChanged)
	}
}

func TestSubscriptionCompletesOnTerminalStatus(t *testing.T) {
	hub := wsTestHub(func(_ context.Context, id string) (map[string]any, error) {
		return map[string]any{"id": id, "status": "succeeded", "shards": []map[string]any{}}, nil
	}, 40*time.Millisecond) // short safety so the loop ticks, reads terminal, completes
	srv := httptest.NewServer(withTestPrincipal(
		graphqlSubscriptionHandler(buildSchemaWithHub(nil, hub), hub)))
	defer srv.Close()

	conn := dialWS(t, srv)
	defer conn.Close(websocket.StatusNormalClosure, "")

	writeWS(t, conn, wsMessage{Type: wsConnectionInit})
	readWS(t, conn) // ack

	sub, _ := json.Marshal(wsSubscribePayload{Query: `subscription { runChanged(id: "r1") { id status } }`})
	writeWS(t, conn, wsMessage{ID: "1", Type: wsSubscribe, Payload: sub})

	// Read until a complete arrives (initial next(s) may precede it).
	for range 5 {
		msg := readWS(t, conn)
		if msg.Type == wsComplete && msg.ID == "1" {
			return
		}
		if msg.Type != wsNext {
			t.Fatalf("unexpected message %q while awaiting complete", msg.Type)
		}
	}
	t.Fatal("never received complete after terminal status")
}
