//go:build integration

package api

import (
	"encoding/json"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"
	natsserver "github.com/nats-io/nats-server/v2/server"
	"github.com/nats-io/nats.go"

	teonats "github.com/teo-dev/teo/internal/nats"
	"github.com/teo-dev/teo/internal/testpg"
)

// startEmbeddedNATS spins up an in-process NATS server on a random port and
// returns its client URL. No Docker needed — this is the lightweight server the
// nats.go module ships for exactly this purpose.
func startEmbeddedNATS(t *testing.T) string {
	t.Helper()
	ns, err := natsserver.NewServer(&natsserver.Options{Host: "127.0.0.1", Port: -1})
	if err != nil {
		t.Fatalf("new nats server: %v", err)
	}
	ns.Start()
	if !ns.ReadyForConnections(5 * time.Second) {
		t.Fatal("embedded nats not ready")
	}
	t.Cleanup(ns.Shutdown)
	return ns.ClientURL()
}

// wsServerForHub builds one "API replica": its own NATS conn + hub + WS server,
// all sharing the given Postgres pool and NATS URL.
func wsServerForHub(t *testing.T, pool *pgxpool.Pool, url string) *httptest.Server {
	t.Helper()
	nc, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("nats connect: %v", err)
	}
	t.Cleanup(nc.Close)
	hub := NewHub(pool, nc, nil)
	srv := httptest.NewServer(withTestPrincipal(
		graphqlSubscriptionHandler(buildSchemaWithHub(pool, hub), hub)))
	t.Cleanup(srv.Close)
	return srv
}

type runChangedView struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

func decodeRunChanged(t *testing.T, msg wsMessage) runChangedView {
	t.Helper()
	var body struct {
		Data struct {
			RunChanged runChangedView `json:"runChanged"`
		} `json:"data"`
	}
	if err := json.Unmarshal(msg.Payload, &body); err != nil {
		t.Fatalf("decode runChanged: %v", err)
	}
	return body.Data.RunChanged
}

func subscribeRun(t *testing.T, conn *websocket.Conn, runID string) {
	t.Helper()
	writeWS(t, conn, wsMessage{Type: wsConnectionInit})
	if ack := readWS(t, conn); ack.Type != wsConnectionAck {
		t.Fatalf("expected ack, got %q", ack.Type)
	}
	q, _ := json.Marshal(wsSubscribePayload{
		Query: `subscription { runChanged(id: "` + runID + `") { id status shards { index status } } }`,
	})
	writeWS(t, conn, wsMessage{ID: "1", Type: wsSubscribe, Payload: q})
}

// TestSubscriptionEndToEndOverNATS drives the full path: a WS client subscribes,
// the run transitions in Postgres, a hint is published from a *separate* NATS
// connection ("another replica"), and the client receives the fresh snapshot and
// finally a complete when the run reaches a terminal status.
func TestSubscriptionEndToEndOverNATS(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	defer cleanup()
	ids := seed(t, pool)
	url := startEmbeddedNATS(t)

	srv := wsServerForHub(t, pool, url)
	pubNC, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("publisher connect: %v", err)
	}
	defer pubNC.Close()

	conn := dialWS(t, srv)
	defer conn.Close(websocket.StatusNormalClosure, "")
	subscribeRun(t, conn, ids.run2ID)

	if got := decodeRunChanged(t, readWS(t, conn)); got.Status != "running" {
		t.Fatalf("initial snapshot status %q, want running", got.Status)
	}

	// Transition the run to terminal, then publish the UI hint.
	mustExec(t, pool, `UPDATE teo.runs SET status='succeeded', finished_at=now() WHERE id=$1`, ids.run2ID)
	if err := teonats.PublishCore(pubNC, teonats.SubjUIRunChanged, teonats.UIRunChanged{RunID: ids.run2ID}); err != nil {
		t.Fatalf("publish hint: %v", err)
	}

	sawSucceeded := false
	for range 8 {
		msg := readWS(t, conn)
		switch msg.Type {
		case wsNext:
			if decodeRunChanged(t, msg).Status == "succeeded" {
				sawSucceeded = true
			}
		case wsComplete:
			if !sawSucceeded {
				t.Fatal("subscription completed before delivering the succeeded snapshot")
			}
			return
		default:
			t.Fatalf("unexpected message type %q", msg.Type)
		}
	}
	t.Fatal("never received complete after terminal status")
}

// TestSubscriptionNoStickySessions proves two independent API replicas, each
// with its own NATS connection and hub, both deliver an update from a single
// published hint — i.e. a client can connect to any replica (no sticky session).
func TestSubscriptionNoStickySessions(t *testing.T) {
	pool, cleanup := testpg.Start(t)
	defer cleanup()
	ids := seed(t, pool)
	url := startEmbeddedNATS(t)

	srvA := wsServerForHub(t, pool, url)
	srvB := wsServerForHub(t, pool, url)
	pubNC, err := nats.Connect(url)
	if err != nil {
		t.Fatalf("publisher connect: %v", err)
	}
	defer pubNC.Close()

	connA := dialWS(t, srvA)
	defer connA.Close(websocket.StatusNormalClosure, "")
	connB := dialWS(t, srvB)
	defer connB.Close(websocket.StatusNormalClosure, "")
	subscribeRun(t, connA, ids.run2ID)
	subscribeRun(t, connB, ids.run2ID)
	readWS(t, connA) // drain initial snapshots
	readWS(t, connB)

	if err := teonats.PublishCore(pubNC, teonats.SubjUIRunChanged, teonats.UIRunChanged{RunID: ids.run2ID}); err != nil {
		t.Fatalf("publish hint: %v", err)
	}

	for _, c := range []*websocket.Conn{connA, connB} {
		msg := readWS(t, c)
		if msg.Type != wsNext {
			t.Fatalf("replica delivered %q, want next from the shared hint", msg.Type)
		}
	}
}
