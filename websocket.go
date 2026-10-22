package pgserverless

import (
	"context"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/labstack/echo/v4"
)

// WebSocketPath is where the transport is mounted. The client's Pool and Client
// types connect here.
const WebSocketPath = "/v2"

// wsSubprotocol is negotiated on the upgrade. The client asks for it and a
// server that answers with nothing still works, but answering is what lets a
// proxy in between tell this traffic apart from any other WebSocket.
const wsSubprotocol = "pg"

// dialTimeout bounds the upstream connection attempt on the WebSocket path.
// The HTTP path has QueryTimeout covering the whole request; a tunnel has no
// equivalent, because it is meant to stay open, so only the dial is bounded.
const dialTimeout = 10 * time.Second

// WithWebSocketAdmission sets the Admission consulted on the WebSocket path.
//
// It is separate from the HTTP one because the two paths carry a request's
// identity differently. On HTTP the connection string is a header, so an
// Admission can read credentials from it. On the WebSocket path there is no
// such header: the target is resolved from the HTTP Host of the upgrade
// request, and the credentials arrive inside the tunnel in the client's own
// StartupMessage, which this server forwards rather than reads.
//
// So on this path only Addr, ServerName and InsecureSkipVerify of the returned
// Upstream are consulted. User, Password and Database are ignored, because
// substituting them would mean authenticating as somebody the client did not
// ask to be.
//
// Unset, the HTTP Admission is used, which is right when it resolves an address
// without needing the connection string.
func WithWebSocketAdmission(admit Admission) Option {
	return func(h *Handler) { h.admitWS = admit }
}

// RegisterWebSocket mounts the WebSocket transport on e at GET /v2.
//
// Register mounts the HTTP transport only. They are separate calls because a
// deployment may want one without the other: a browser-facing endpoint may want
// HTTP alone, and a server standing in for a database's TCP port may want the
// tunnel alone.
func (h *Handler) RegisterWebSocket(e *echo.Echo) {
	e.GET(WebSocketPath, h.Upgrade)
}

// Upgrade serves the WebSocket transport: it upgrades the connection, declines
// the client's SSLRequest, and relays the PostgreSQL protocol to the upstream.
//
// The relay is deliberately dumb. Once the startup phase is over, this is a
// byte pipe: it does not parse messages, does not authenticate, and does not
// hold state. Authentication happens end to end between the client and the
// upstream, through the tunnel, which is what makes SCRAM work without this
// server ever holding a credential.
func (h *Handler) Upgrade(c echo.Context) error {
	ctx := c.Request().Context()

	// Admission runs BEFORE the upgrade, so a refusal is an ordinary HTTP
	// error the client can read. Refusing after the upgrade would send the
	// client a 101 and then a close frame, which reports as a network fault
	// rather than as a refusal.
	admit := h.admitWS
	if admit == nil {
		admit = h.admit
	}
	up, aerr := admit(c)
	if aerr != nil {
		status := http.StatusBadRequest
		if ae, ok := errors.AsType[*AdmissionError](aerr); ok {
			status = ae.Status
		}
		return c.JSON(status, ErrorResponse{Message: aerr.Error()})
	}

	ws, err := websocket.Accept(c.Response(), c.Request(), &websocket.AcceptOptions{
		Subprotocols: []string{wsSubprotocol},
		// The client runs in browsers and edge runtimes whose Origin this
		// server cannot enumerate. Origin is not an authentication signal
		// here; Admission is.
		OriginPatterns:     []string{"*"},
		CompressionMode:    websocket.CompressionDisabled,
		InsecureSkipVerify: true,
	})
	if err != nil {
		// websocket.Accept has already written a response.
		slog.Warn("pgserverless: websocket upgrade failed", "err", err)
		return nil
	}
	defer ws.CloseNow()

	// Binary frames. PostgreSQL messages are not text and a text frame would
	// be rejected as invalid UTF-8 by a conforming client.
	client := websocket.NetConn(ctx, ws, websocket.MessageBinary)

	startup, err := declineEncryptionAndReadStartup(client)
	if err != nil {
		slog.Warn("pgserverless: websocket startup failed", "err", err)
		return nil
	}
	if startup == nil {
		// CancelRequest. Section 9 permits ignoring it: cancellation arrives
		// on a second connection, which a scale-to-zero or anycast deployment
		// may route somewhere with no knowledge of the first.
		slog.Debug("pgserverless: websocket cancel request ignored")
		return nil
	}

	server, err := h.dialUpstreamRaw(ctx, up)
	if err != nil {
		if h.onDialError != nil {
			h.onDialError(c, err)
		}
		slog.Warn("pgserverless: websocket upstream dial failed", "err", SanitizeError(err.Error()))
		// The client is already upgraded, so the only channel left is a close
		// frame. Its reason is what a client surfaces to a developer.
		_ = ws.Close(websocket.StatusInternalError, "upstream unavailable")
		return nil
	}
	defer server.Close()

	// Forward the startup the client already sent. It was consumed to get here,
	// so the upstream never sees it unless it is replayed.
	if _, err := server.Write(startup); err != nil {
		slog.Warn("pgserverless: forwarding startup failed", "err", err)
		return nil
	}

	relay(client, server)
	return nil
}

// declineEncryptionAndReadStartup runs the startup phase on the client side.
//
// It returns the encoded StartupMessage to forward, or nil when the client sent
// a CancelRequest instead.
//
// Two things here are not obvious. The client defaults to pipelining its first
// messages, so the startup bytes may arrive in the same read as what follows;
// reading through a buffered pgproto3.Backend over a stream handles that,
// which is why this takes a net.Conn rather than framing reads itself. And a
// client may send SSLRequest and GSSEncRequest in sequence, so declining once
// is not enough.
func declineEncryptionAndReadStartup(conn net.Conn) ([]byte, error) {
	backend := pgproto3.NewBackend(conn, conn)

	for range 3 {
		msg, err := backend.ReceiveStartupMessage()
		if err != nil {
			return nil, fmt.Errorf("receive startup: %w", err)
		}

		switch m := msg.(type) {
		case *pgproto3.SSLRequest, *pgproto3.GSSEncRequest:
			// A single 'N' byte: encryption declined. The WebSocket already
			// carries it, and the client's default configuration expects this
			// answer.
			if _, err := conn.Write([]byte{'N'}); err != nil {
				return nil, fmt.Errorf("decline encryption: %w", err)
			}
		case *pgproto3.CancelRequest:
			return nil, nil
		case *pgproto3.StartupMessage:
			return m.Encode(nil)
		default:
			return nil, fmt.Errorf("unexpected startup message %T", msg)
		}
	}
	return nil, errors.New("too many encryption requests before startup")
}

// dialUpstreamRaw opens the upstream connection for the tunnel.
//
// Unlike Dial, this returns a raw net.Conn rather than a pgx connection,
// because nothing here speaks SQL: the client's own protocol goes over it
// untouched.
func (h *Handler) dialUpstreamRaw(ctx context.Context, up Upstream) (net.Conn, error) {
	host, port := parseHostPort(up.Addr)
	if host == "" {
		host = "127.0.0.1"
	}
	addr := net.JoinHostPort(host, port)

	dialCtx, cancel := context.WithTimeout(ctx, dialTimeout)
	defer cancel()

	var d net.Dialer
	conn, err := d.DialContext(dialCtx, "tcp", addr)
	if err != nil {
		return nil, err
	}
	if up.TLSMode == TLSDisable {
		return conn, nil
	}

	tlsConn, err := negotiateUpstreamTLS(conn, up, host)
	if err != nil {
		conn.Close()
		return nil, err
	}
	return tlsConn, nil
}

// negotiateUpstreamTLS performs PostgreSQL's SSLRequest handshake and wraps the
// connection.
//
// PostgreSQL does not speak TLS from the first byte. The client asks with an
// 8 byte SSLRequest and the server answers with one byte, 'S' to proceed or 'N'
// to refuse, and only then does the TLS handshake begin.
func negotiateUpstreamTLS(conn net.Conn, up Upstream, host string) (net.Conn, error) {
	req := make([]byte, 8)
	binary.BigEndian.PutUint32(req[0:4], 8)
	binary.BigEndian.PutUint32(req[4:8], 80877103)
	if _, err := conn.Write(req); err != nil {
		return nil, fmt.Errorf("upstream ssl request: %w", err)
	}

	var answer [1]byte
	if _, err := io.ReadFull(conn, answer[:]); err != nil {
		return nil, fmt.Errorf("upstream ssl response: %w", err)
	}
	if answer[0] != 'S' {
		// Refusing here rather than continuing in the clear is deliberate. The
		// deployer asked for TLS to the upstream; silently downgrading would
		// give them an unencrypted link that looks configured.
		return nil, errors.New("upstream refused TLS but upstream TLS is required")
	}

	sni := up.ServerName
	if sni == "" {
		sni = host
	}
	tlsConn := tls.Client(conn, &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: up.InsecureSkipVerify, //nolint:gosec // the caller's explicit choice, see Upstream.
	})
	if err := tlsConn.Handshake(); err != nil {
		return nil, fmt.Errorf("upstream tls handshake: %w", err)
	}
	return tlsConn, nil
}

// relay copies bytes in both directions until either side closes, then returns.
//
// Closing is the part worth being careful about. When one direction ends, the
// other is closed too, so a half-open connection cannot leave a goroutine
// parked on a read that will never return. Both copies are waited for, so the
// caller's deferred Close does not race a copy still in flight.
func relay(client, server net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(server, client)
		// Unblock the other direction.
		_ = server.Close()
		_ = client.Close()
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(client, server)
		_ = client.Close()
		_ = server.Close()
	}()

	wg.Wait()
}
