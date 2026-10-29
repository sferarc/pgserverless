package pgserverless

import (
	"bytes"
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

// dialTimeout bounds the upstream connection attempt on the WebSocket path,
// including the SSLRequest exchange and the TLS handshake.
// The HTTP path has QueryTimeout covering the whole request; a tunnel has no
// equivalent, because it is meant to stay open, so only the dial is bounded.
const dialTimeout = 10 * time.Second

// startupTimeout bounds how long a client may take, after the upgrade, to send its StartupMessage.
const startupTimeout = 30 * time.Second

// upstreamUnavailable is sent through the tunnel when the dial fails; class 08 is what client retry logic keys on.
var upstreamUnavailable, _ = (&pgproto3.ErrorResponse{
	Severity:            "FATAL",
	SeverityUnlocalized: "FATAL",
	Code:                "08006",
	Message:             "upstream unavailable",
}).Encode(nil)

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
	admit := h.admitWS
	if admit == nil {
		admit = h.admit
	}
	serveWebSocket(c.Response(), c.Request(), func() (Upstream, error) { return admit(c) }, h.dialErrorFor(c), h.startupTimeout)
	return nil
}

// serveWebSocket is the transport-independent core of GET /v2.
// A zero startupWait allows startupTimeout.
func serveWebSocket(w http.ResponseWriter, r *http.Request, admit func() (Upstream, error), onDialError func(error), startupWait time.Duration) {
	ctx := r.Context()

	// Admission runs BEFORE the upgrade, so a refusal is an ordinary HTTP
	// error the client can read. Refusing after the upgrade would send the
	// client a 101 and then a close frame, which reports as a network fault
	// rather than as a refusal.
	up, aerr := admit()
	if aerr != nil {
		writeAdmissionError(w, aerr)
		return
	}

	ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{
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
		return
	}
	defer ws.CloseNow()

	// Binary frames. PostgreSQL messages are not text and a text frame would
	// be rejected as invalid UTF-8 by a conforming client.
	client := websocket.NetConn(ctx, ws, websocket.MessageBinary)

	// The upgrade has left net/http's timeouts behind, so a client that never sends its startup would hold the tunnel forever.
	if startupWait <= 0 {
		startupWait = startupTimeout
	}
	_ = client.SetReadDeadline(time.Now().Add(startupWait))
	startup, cancel, err := declineEncryptionAndReadStartup(client)
	if err != nil {
		slog.Warn("pgserverless: websocket startup failed", "err", err)
		return
	}
	_ = client.SetReadDeadline(time.Time{})

	server, err := dialUpstreamRaw(ctx, up)
	if err != nil {
		if onDialError != nil {
			onDialError(err)
		}
		slog.Warn("pgserverless: websocket upstream dial failed", "err", SanitizeError(err.Error()))
		if cancel {
			// PostgreSQL answers a CancelRequest with nothing, so its client reads nothing either.
			return
		}
		// A PostgreSQL client reads only the protocol, so a bare close frame surfaces as "Connection terminated unexpectedly".
		_, _ = client.Write(upstreamUnavailable)
		_ = ws.Close(websocket.StatusInternalError, "upstream unavailable")
		return
	}
	defer server.Close()

	if cancel {
		// The relay keeps no session state: the key the client holds is the upstream's own, so any instance can deliver it.
		deliverCancel(server, startup)
		return
	}

	// Forward the startup the client already sent. It was consumed to get here,
	// so the upstream never sees it unless it is replayed.
	if _, err := server.Write(startup); err != nil {
		slog.Warn("pgserverless: forwarding startup failed", "err", err)
		return
	}

	relay(client, server)
}

// declineEncryptionAndReadStartup runs the startup phase on the client side.
//
// It returns the encoded packet to forward: the StartupMessage, or the
// CancelRequest with the bool set.
//
// Two things here are not obvious. The client defaults to pipelining its first
// messages, so the bytes after the startup (its password) may arrive in the
// same WebSocket message. Each packet is therefore read to its declared length
// and no further, leaving the rest on conn for the relay; a pgproto3.Backend
// over conn buffers ahead and dropped them. And a client may send SSLRequest
// and GSSEncRequest in sequence, so declining once is not enough.
func declineEncryptionAndReadStartup(conn net.Conn) ([]byte, bool, error) {
	for range 3 {
		packet, err := readStartupPacket(conn)
		if err != nil {
			return nil, false, fmt.Errorf("receive startup: %w", err)
		}
		msg, err := pgproto3.NewBackend(bytes.NewReader(packet), io.Discard).ReceiveStartupMessage()
		if err != nil {
			return nil, false, fmt.Errorf("receive startup: %w", err)
		}

		switch m := msg.(type) {
		case *pgproto3.SSLRequest, *pgproto3.GSSEncRequest:
			// A single 'N' byte: encryption declined. The WebSocket already
			// carries it, and the client's default configuration expects this
			// answer.
			if _, err := conn.Write([]byte{'N'}); err != nil {
				return nil, false, fmt.Errorf("decline encryption: %w", err)
			}
		case *pgproto3.CancelRequest:
			return packet, true, nil
		case *pgproto3.StartupMessage:
			b, err := m.Encode(nil)
			return b, false, err
		default:
			return nil, false, fmt.Errorf("unexpected startup message %T", msg)
		}
	}
	return nil, false, errors.New("too many encryption requests before startup")
}

// deliverCancel sends a CancelRequest packet and waits, bounded by dialTimeout,
// for the upstream to close the connection, which is how PostgreSQL says it has
// acted on it (libpq's PQcancel waits the same way).
func deliverCancel(server net.Conn, packet []byte) {
	_ = server.SetDeadline(time.Now().Add(dialTimeout))
	if _, err := server.Write(packet); err != nil {
		slog.Warn("pgserverless: forwarding cancel request failed", "err", err)
		return
	}
	_, _ = io.Copy(io.Discard, server)
}

// maxStartupPacketLen is PostgreSQL's MAX_STARTUP_PACKET_LENGTH.
const maxStartupPacketLen = 10000

// readStartupPacket reads one length-prefixed startup-phase packet and nothing after it.
func readStartupPacket(r io.Reader) ([]byte, error) {
	var head [4]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(head[:])
	if n < 8 || n > maxStartupPacketLen {
		return nil, fmt.Errorf("invalid startup packet length %d", n)
	}
	packet := make([]byte, n)
	copy(packet, head[:])
	if _, err := io.ReadFull(r, packet[4:]); err != nil {
		return nil, err
	}
	return packet, nil
}

// dialUpstreamRaw opens the upstream connection for the tunnel.
//
// Unlike Dial, this returns a raw net.Conn rather than a pgx connection,
// because nothing here speaks SQL: the client's own protocol goes over it
// untouched.
func dialUpstreamRaw(ctx context.Context, up Upstream) (net.Conn, error) {
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

	tlsConn, err := negotiateUpstreamTLS(dialCtx, conn, up, host)
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
func negotiateUpstreamTLS(ctx context.Context, conn net.Conn, up Upstream, host string) (net.Conn, error) {
	// An upstream that accepts TCP and then never answers would otherwise hold the tunnel open forever.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	defer stop()

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
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		return nil, fmt.Errorf("upstream tls handshake: %w", err)
	}
	if !stop() {
		return nil, fmt.Errorf("upstream tls handshake: %w", ctx.Err())
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
