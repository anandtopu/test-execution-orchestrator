package api

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/graphql-go/graphql"

	"github.com/teo-dev/teo/internal/auth"
)

// graphql-transport-ws message types (the "graphql-ws" library's protocol).
const (
	wsConnectionInit = "connection_init"
	wsConnectionAck  = "connection_ack"
	wsPing           = "ping"
	wsPong           = "pong"
	wsSubscribe      = "subscribe"
	wsNext           = "next"
	wsErrorMsg       = "error"
	wsComplete       = "complete"
)

// Protocol close codes per the graphql-transport-ws spec.
const (
	wsCloseBadRequest   = websocket.StatusCode(4400)
	wsCloseUnauthorized = websocket.StatusCode(4401)
	wsCloseInitTimeout  = websocket.StatusCode(4408)
	wsCloseSubExists    = websocket.StatusCode(4409)
	wsCloseTooManyInit  = websocket.StatusCode(4429)
)

const wsInitDeadline = 10 * time.Second

type wsMessage struct {
	ID      string          `json:"id,omitempty"`
	Type    string          `json:"type"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

type wsSubscribePayload struct {
	Query         string         `json:"query"`
	Variables     map[string]any `json:"variables"`
	OperationName string         `json:"operationName"`
}

// graphqlSubscriptionHandler serves /graphql/subscriptions over WebSocket using
// the graphql-transport-ws subprotocol. Auth is reused from the HTTP layer: the
// chi auth.Middleware validates the teo_session cookie (or API key) on the
// upgrade GET, so a principal is already in the request context — the same RBAC
// the POST /graphql handler enforces. Browsers cannot set an Authorization
// header on a WS upgrade, which is why cookie auth is the supported path.
func graphqlSubscriptionHandler(schema graphql.Schema, _ *Hub) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := auth.PrincipalFrom(r.Context())
		if p == nil || len(p.Roles) == 0 {
			writeProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication required")
			return
		}
		conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
			Subprotocols: []string{"graphql-transport-ws"},
		})
		if err != nil {
			return // Accept already wrote the HTTP error
		}
		if conn.Subprotocol() != "graphql-transport-ws" {
			_ = conn.Close(websocket.StatusProtocolError, "expected graphql-transport-ws subprotocol")
			return
		}
		// Carry the session principal into every subscription's context so
		// nested resolvers and future RBAC see it.
		sess := &wsSession{
			conn:    conn,
			schema:  schema,
			baseCtx: auth.WithPrincipal(context.Background(), p),
			ops:     make(map[string]context.CancelFunc),
		}
		sess.run(r.Context())
	})
}

type wsSession struct {
	conn    *websocket.Conn
	schema  graphql.Schema
	baseCtx context.Context

	mu      sync.Mutex
	ops     map[string]context.CancelFunc
	inited  bool
	writeMu sync.Mutex
}

func (s *wsSession) run(reqCtx context.Context) {
	ctx, cancel := context.WithCancel(reqCtx)
	defer cancel()
	defer s.cancelAll()

	// Close the connection if connection_init doesn't arrive in time.
	initDone := make(chan struct{})
	go func() {
		select {
		case <-initDone:
		case <-ctx.Done():
		case <-time.After(wsInitDeadline):
			_ = s.conn.Close(wsCloseInitTimeout, "connection initialisation timeout")
		}
	}()

	for {
		typ, data, err := s.conn.Read(ctx)
		if err != nil {
			return
		}
		if typ != websocket.MessageText {
			_ = s.conn.Close(websocket.StatusUnsupportedData, "expected text frames")
			return
		}
		var msg wsMessage
		if err := json.Unmarshal(data, &msg); err != nil {
			_ = s.conn.Close(wsCloseBadRequest, "invalid message")
			return
		}
		switch msg.Type {
		case wsConnectionInit:
			s.mu.Lock()
			already := s.inited
			s.inited = true
			s.mu.Unlock()
			if already {
				_ = s.conn.Close(wsCloseTooManyInit, "too many initialisation requests")
				return
			}
			close(initDone)
			if err := s.write(ctx, wsMessage{Type: wsConnectionAck}); err != nil {
				return
			}
		case wsPing:
			_ = s.write(ctx, wsMessage{Type: wsPong})
		case wsPong:
			// no-op
		case wsSubscribe:
			s.mu.Lock()
			ready := s.inited
			s.mu.Unlock()
			if !ready {
				_ = s.conn.Close(wsCloseUnauthorized, "unauthorized")
				return
			}
			if msg.ID == "" {
				_ = s.conn.Close(wsCloseBadRequest, "missing subscription id")
				return
			}
			s.startOp(ctx, msg)
		case wsComplete:
			s.stopOp(msg.ID)
		default:
			_ = s.conn.Close(wsCloseBadRequest, "unknown message type")
			return
		}
	}
}

func (s *wsSession) startOp(sessCtx context.Context, msg wsMessage) {
	var pl wsSubscribePayload
	if err := json.Unmarshal(msg.Payload, &pl); err != nil {
		_ = s.writeError(sessCtx, msg.ID, "invalid subscribe payload")
		return
	}

	s.mu.Lock()
	if _, exists := s.ops[msg.ID]; exists {
		s.mu.Unlock()
		_ = s.conn.Close(wsCloseSubExists, "subscriber for "+msg.ID+" already exists")
		return
	}
	opCtx, opCancel := context.WithCancel(s.baseCtx)
	s.ops[msg.ID] = opCancel
	s.mu.Unlock()

	results := graphql.Subscribe(graphql.Params{
		Schema:         s.schema,
		RequestString:  pl.Query,
		VariableValues: pl.Variables,
		OperationName:  pl.OperationName,
		Context:        opCtx,
	})

	go func() {
		defer s.stopOp(msg.ID)
		for {
			select {
			case <-opCtx.Done():
				return
			case <-sessCtx.Done():
				return
			case res, more := <-results:
				if !more {
					_ = s.write(sessCtx, wsMessage{ID: msg.ID, Type: wsComplete})
					return
				}
				payload, err := json.Marshal(res)
				if err != nil {
					continue
				}
				if err := s.write(sessCtx, wsMessage{ID: msg.ID, Type: wsNext, Payload: payload}); err != nil {
					return
				}
			}
		}
	}()
}

func (s *wsSession) stopOp(id string) {
	s.mu.Lock()
	cancel, ok := s.ops[id]
	if ok {
		delete(s.ops, id)
	}
	s.mu.Unlock()
	if ok {
		cancel()
	}
}

func (s *wsSession) cancelAll() {
	s.mu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.ops))
	for id, c := range s.ops {
		cancels = append(cancels, c)
		delete(s.ops, id)
	}
	s.mu.Unlock()
	for _, c := range cancels {
		c()
	}
}

func (s *wsSession) write(ctx context.Context, msg wsMessage) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.Write(ctx, websocket.MessageText, b)
}

func (s *wsSession) writeError(ctx context.Context, id, message string) error {
	payload, _ := json.Marshal([]map[string]string{{"message": message}})
	return s.write(ctx, wsMessage{ID: id, Type: wsErrorMsg, Payload: payload})
}
