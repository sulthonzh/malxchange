// The single member WebSocket endpoint (ADR-0011, ticket #44): /ws on
// the api public listener, proxied by Caddy. Plain WebSocket with JSON
// envelopes — no SockJS, no STOMP, no ack protocol; REST-first
// bootstrap lives client-side, this socket only relays deltas.
//
// Library: github.com/coder/websocket (pinned v1.8.15), the maintained
// continuation of nhooyr.io/websocket — that repository is frozen.
//
// Connect-time session auth: the access-auth-token header when a client
// can set one, else the "token" query parameter (browsers cannot set
// headers on WebSocket). A presented-but-rejected token refuses the
// handshake with 401 — silently downgrading an expired member to
// anonymous would hide the expiry; no token at all is an honest
// anonymous socket, bound to public channels only. The socket keeps its
// binding for its whole lifetime.
package api

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/syaikhipin/malxchange/internal/kafka"
	"github.com/syaikhipin/malxchange/internal/service"
)

// Socket timing knobs. A write that cannot complete within the deadline
// means the peer is not draining (its TCP window stalled): the socket
// dies — that is the slow-client policy, same as buffer overflow. Pings
// ride the write side of the pump, so a vanished peer is noticed within
// one interval.
const (
	wsWriteTimeout  = 10 * time.Second
	wsPingInterval  = 30 * time.Second
	wsReadLimit     = 4096                          // subscribe envelopes are tiny; anything larger is abuse
	wsSlowCloseCode = websocket.StatusTryAgainLater // 1013: overflow teardown when the peer is reachable
)

// wsClient is one connected socket: its session binding, its bounded
// outbound queue, and the connection itself. It implements the hub's
// wsTarget.
type wsClient struct {
	memberID string
	conn     *websocket.Conn
	out      chan []byte
	killCh   chan struct{}
	killOnce sync.Once
	log      *slog.Logger
}

func (c *wsClient) MemberID() string { return c.memberID }

// tryDeliver is the hub's slow-client gate: never block, never drop
// silently — false hands the socket to the killer.
func (c *wsClient) tryDeliver(msg []byte) bool {
	select {
	case c.out <- msg:
		return true
	default:
		return false
	}
}

// kill tears the socket down exactly once. The hub calls it on buffer
// overflow — the peer is by definition not draining, so CloseNow is
// correct even mid-write (it is documented-safe to unblock a stuck
// writer); the write pump sends the 1013 close frame on its own when it
// still owns a live connection.
func (c *wsClient) kill(reason string) {
	c.killOnce.Do(func() {
		close(c.killCh)
		c.log.Warn("ws: closing socket", "reason", reason, "member", c.memberID)
		go c.conn.CloseNow()
	})
}

// wsServer carries the /ws collaborators.
type wsServer struct {
	ctx  context.Context
	hub  *Hub
	pool *pgxpool.Pool
	env  *APIEnv
	log  *slog.Logger
}

// RegisterWS mounts the member WebSocket endpoint on the PUBLIC mux
// (Caddy proxies /ws unstripped, exactly this path).
func RegisterWS(ctx context.Context, mux *http.ServeMux, hub *Hub, pool *pgxpool.Pool, env *APIEnv, log *slog.Logger) {
	s := &wsServer{ctx: ctx, hub: hub, pool: pool, env: env, log: log}
	mux.HandleFunc("GET /ws", s.handleWS)
}

// handleWS authenticates, upgrades, then runs the read loop for the
// socket's lifetime. The write pump runs in its own goroutine; the only
// writer is that pump, so no write concurrency exists.
func (s *wsServer) handleWS(w http.ResponseWriter, r *http.Request) {
	token := r.Header.Get("access-auth-token")
	if token == "" {
		token = r.URL.Query().Get("token")
	}
	memberID := ""
	if token != "" {
		sess, err := resolveSession(r.Context(), s.pool, token, s.env.SessionTTL)
		if err != nil {
			// The same honest sentence REST answers with; missing,
			// expired, and revoked tokens are indistinguishable.
			WriteError(w, http.StatusUnauthorized, "Your session has expired. Sign in again.")
			return
		}
		memberID = sess.MemberID
	}

	// Caddy terminates TLS and the session token (not the browser
	// origin) is the credential; anonymous sockets carry public data
	// only, so there is no cross-site hijack surface to close here.
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{OriginPatterns: []string{"*"}})
	if err != nil {
		return
	}
	conn.SetReadLimit(wsReadLimit)

	client := &wsClient{
		memberID: memberID,
		conn:     conn,
		out:      make(chan []byte, s.env.WsBuffer),
		killCh:   make(chan struct{}),
		log:      s.log,
	}
	defer func() {
		s.hub.Remove(client)
		client.kill("read loop ended")
	}()

	go client.writePump(s.ctx)
	s.readLoop(client)
}

// wsClientMessage is the subscribe/unsubscribe envelope from the client.
type wsClientMessage struct {
	Op      string `json:"op"`
	Channel string `json:"channel"`
}

// readLoop consumes client envelopes until the socket dies. Subscribe
// errors answer inline (the only replies there are — no acks).
func (s *wsServer) readLoop(client *wsClient) {
	for {
		_, rd, err := client.conn.Reader(s.ctx)
		if err != nil {
			return
		}
		var m wsClientMessage
		if err := json.NewDecoder(rd).Decode(&m); err != nil {
			client.reply("", "Malformed message.")
			continue
		}
		switch m.Op {
		case "subscribe", "unsubscribe":
			ch, perr := parseChannel(m.Channel, client.memberID != "")
			if perr != nil {
				client.reply(m.Channel, channelErrorMessage(perr))
				continue
			}
			if m.Op == "subscribe" {
				s.hub.Subscribe(client, ch.name)
			} else {
				s.hub.Unsubscribe(client, ch.name)
			}
		default:
			client.reply("", "Unknown operation.")
		}
	}
}

// reply queues an error envelope through the bounded buffer — same
// overflow policy as events.
func (c *wsClient) reply(channel, message string) {
	b, err := json.Marshal(wsMessage{Channel: channel, Error: message})
	if err != nil {
		return
	}
	if !c.tryDeliver(b) {
		c.kill("outbound buffer overflow")
	}
}

// writePump is the socket's single writer: queued events with a
// deadline, pings for liveness, the overflow kill switch, and a clean
// GoingAway on service shutdown.
func (c *wsClient) writePump(ctx context.Context) {
	ping := time.NewTicker(wsPingInterval)
	defer ping.Stop()
	for {
		select {
		case msg := <-c.out:
			wctx, cancel := context.WithTimeout(ctx, wsWriteTimeout)
			err := c.conn.Write(wctx, websocket.MessageText, msg)
			cancel()
			if err != nil {
				c.kill("write stalled: " + err.Error())
				return
			}
		case <-ping.C:
			pctx, cancel := context.WithTimeout(ctx, wsPingInterval)
			err := c.conn.Ping(pctx)
			cancel()
			if err != nil {
				c.kill("ping failed: " + err.Error())
				return
			}
		case <-c.killCh:
			// Overflow or peer death. Best-effort 1013 so a reachable
			// peer learns why; CloseNow already fired otherwise. The
			// close handshake is internally bounded.
			_ = c.conn.Close(wsSlowCloseCode, "The connection is too slow.")
			return
		case <-ctx.Done():
			_ = c.conn.Close(websocket.StatusGoingAway, "The server is shutting down.")
			return
		}
	}
}

// startWS wires the endpoint for the service Extra: one shared consumer
// group feeding one hub, and /ws on the public mux. The hub holds no
// durable state, so a consumer that dies only costs live delivery —
// log-and-continue, never a process exit.
func startWS(ctx context.Context, d service.Deps, env *APIEnv) error {
	hub := NewHub(d.Log)
	cl, err := kafka.NewConsumer(d.Cfg.Kafka, wsConsumerGroup, wsTopics)
	if err != nil {
		return fmt.Errorf("ws hub consumer: %w", err)
	}
	go func() {
		hub.Run(ctx, cl)
		cl.Close()
	}()
	RegisterWS(ctx, d.Public, hub, d.Pool, env, d.Log)
	return nil
}
