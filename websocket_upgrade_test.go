package pgserverless

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

const wsTestTimeout = 10 * time.Second

func startWSServer(t *testing.T, admit Admission, opts ...Option) *httptest.Server {
	t.Helper()
	var handler http.Handler
	if os.Getenv("PGSERVERLESS_TEST_STDLIB") == "true" {
		handler = stdlibWSHandler(admit, opts...)
	} else {
		e := echo.New()
		NewHandler(admit, opts...).RegisterWebSocket(e)
		handler = e
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv
}

// stdlibWSHandler runs an echo-shaped Admission and options through NewHTTPHandler.
func stdlibWSHandler(admit Admission, opts ...Option) http.Handler {
	cfg := &Handler{}
	for _, o := range opts {
		o(cfg)
	}
	e := echo.New()
	bind := func(a Admission) HTTPAdmission {
		return func(r *http.Request) (Upstream, error) { return a(e.NewContext(r, nil)) }
	}
	hopts := []HTTPOption{}
	if cfg.admitWS != nil {
		hopts = append(hopts, WithHTTPWebSocketAdmission(bind(cfg.admitWS)))
	}
	if cfg.startupTimeout != 0 {
		hopts = append(hopts, func(h *httpHandler) { h.startupTimeout = cfg.startupTimeout })
	}
	if cfg.onDialError != nil {
		hopts = append(hopts, WithHTTPDialErrorHook(func(r *http.Request, err error) {
			cfg.onDialError(e.NewContext(r, nil), err)
		}))
	}
	return NewHTTPHandler(bind(admit), hopts...)
}

func fakeUpstream(pg *pgfake.PG) Admission {
	return func(echo.Context) (Upstream, error) {
		return Upstream{Addr: pg.Addr(), InsecureSkipVerify: true}, nil
	}
}

func dialWS(t *testing.T, srv *httptest.Server) (context.Context, *websocket.Conn, net.Conn) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), wsTestTimeout)
	t.Cleanup(cancel)
	ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+WebSocketPath, &websocket.DialOptions{
		Subprotocols: []string{wsSubprotocol},
	})
	require.NoError(t, err)
	t.Cleanup(func() { ws.CloseNow() })
	return ctx, ws, websocket.NetConn(ctx, ws, websocket.MessageBinary)
}

func startupBytes(t *testing.T) []byte {
	t.Helper()
	b, err := (&pgproto3.StartupMessage{
		ProtocolVersion: pgproto3.ProtocolVersionNumber,
		Parameters:      map[string]string{"user": "u"},
	}).Encode(nil)
	require.NoError(t, err)
	return b
}

func sslRequestBytes(t *testing.T) []byte {
	t.Helper()
	b, err := (&pgproto3.SSLRequest{}).Encode(nil)
	require.NoError(t, err)
	return b
}

func TestUpgradeRelaysSessionToUpstream(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 42 AS n", pgfake.Result{
		Cols: []pgfake.Col{{Name: "n", TypeOID: oidInt4}},
		Rows: [][]any{{int32(42)}},
		Tag:  "SELECT 1",
	})
	srv := startWSServer(t, fakeUpstream(pg))
	_, _, conn := dialWS(t, srv)

	_, err := conn.Write(sslRequestBytes(t))
	require.NoError(t, err)
	answer := make([]byte, 1)
	_, err = io.ReadFull(conn, answer)
	require.NoError(t, err)
	assert.Equal(t, byte('N'), answer[0], "the tunnel declines TLS, the WebSocket already carries it")

	_, err = conn.Write(startupBytes(t))
	require.NoError(t, err)

	fe := pgproto3.NewFrontend(conn, conn)
	for {
		msg, err := fe.Receive()
		require.NoError(t, err)
		if _, ok := msg.(*pgproto3.ReadyForQuery); ok {
			break
		}
	}
	assert.Equal(t, "u", pg.StartupParam("user"), "the client's own startup reached the upstream")

	fe.Send(&pgproto3.Query{String: "SELECT 42 AS n"})
	require.NoError(t, fe.Flush())
	var row []byte
	for {
		msg, err := fe.Receive()
		require.NoError(t, err)
		if dr, ok := msg.(*pgproto3.DataRow); ok {
			row = dr.Values[0]
		}
		if _, ok := msg.(*pgproto3.ReadyForQuery); ok {
			break
		}
	}
	assert.Equal(t, "42", string(row))
}

func TestUpgradeAdmissionRefusalIsAnHTTPError(t *testing.T) {
	for _, tc := range []struct {
		name string
		err  error
		want int
	}{
		{"typed status", &AdmissionError{Status: http.StatusForbidden, Message: "tenant suspended"}, http.StatusForbidden},
		{"plain error", io.ErrUnexpectedEOF, http.StatusBadRequest},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srv := startWSServer(t, func(echo.Context) (Upstream, error) { return Upstream{}, tc.err })

			resp, err := http.Get(srv.URL + WebSocketPath)
			require.NoError(t, err)
			defer resp.Body.Close()

			assert.Equal(t, tc.want, resp.StatusCode, "a refusal must not be a 101 followed by a close frame")
			var body ErrorResponse
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			assert.Equal(t, tc.err.Error(), body.Message)
		})
	}
}

func TestUpgradeUsesTheWebSocketAdmissionWhenSet(t *testing.T) {
	pg := pgfake.New(t)
	httpCalls := 0
	srv := startWSServer(t,
		func(echo.Context) (Upstream, error) { httpCalls++; return Upstream{}, io.EOF },
		WithWebSocketAdmission(fakeUpstream(pg)),
	)
	_, _, conn := dialWS(t, srv)

	_, err := conn.Write(append(sslRequestBytes(t), startupBytes(t)...))
	require.NoError(t, err)
	_, err = io.ReadFull(conn, make([]byte, 1))
	require.NoError(t, err)

	assert.Zero(t, httpCalls)
}

// openWSSession runs the startup through the tunnel and returns the session with the key the upstream issued.
func openWSSession(t *testing.T, srv *httptest.Server) (*pgproto3.Frontend, *pgproto3.BackendKeyData) {
	t.Helper()
	_, _, conn := dialWS(t, srv)
	_, err := conn.Write(append(sslRequestBytes(t), startupBytes(t)...))
	require.NoError(t, err)
	_, err = io.ReadFull(conn, make([]byte, 1))
	require.NoError(t, err)

	fe := pgproto3.NewFrontend(conn, conn)
	var key *pgproto3.BackendKeyData
	for {
		msg, err := fe.Receive()
		require.NoError(t, err)
		if k, ok := msg.(*pgproto3.BackendKeyData); ok {
			key = &pgproto3.BackendKeyData{ProcessID: k.ProcessID, SecretKey: bytes.Clone(k.SecretKey)}
		}
		if _, ok := msg.(*pgproto3.ReadyForQuery); ok {
			break
		}
	}
	require.NotNil(t, key, "the upstream issued no BackendKeyData")
	return fe, key
}

func TestUpgradeForwardsCancelRequestToTheUpstream(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT pg_sleep(60)", pgfake.Result{WaitForCancel: true})
	srv := startWSServer(t, fakeUpstream(pg))

	fe, key := openWSSession(t, srv)
	fe.Send(&pgproto3.Query{String: "SELECT pg_sleep(60)"})
	require.NoError(t, fe.Flush())

	// The driver cancels on a second connection, which is a second WebSocket.
	_, _, cancelConn := dialWS(t, srv)
	b, err := (&pgproto3.CancelRequest{ProcessID: key.ProcessID, SecretKey: key.SecretKey}).Encode(nil)
	require.NoError(t, err)
	_, err = cancelConn.Write(b)
	require.NoError(t, err)
	rest, _ := io.ReadAll(cancelConn)
	assert.Empty(t, rest, "PostgreSQL answers a CancelRequest with nothing")

	var pgErr *pgproto3.ErrorResponse
	for {
		msg, err := fe.Receive()
		require.NoError(t, err)
		if e, ok := msg.(*pgproto3.ErrorResponse); ok {
			pgErr = e
		}
		if _, ok := msg.(*pgproto3.ReadyForQuery); ok {
			break
		}
	}
	require.NotNil(t, pgErr, "the statement was not cancelled")
	assert.Equal(t, "57014", pgErr.Code)
	assert.Equal(t, 1, pg.CancelRequests())
}

func TestUpgradeCancelRequestToAnUnreachableUpstreamAnswersNothing(t *testing.T) {
	var dials atomic.Int32
	srv := startWSServer(t, func(echo.Context) (Upstream, error) { return Upstream{Addr: "127.0.0.1:1"}, nil },
		WithDialErrorHook(func(echo.Context, error) { dials.Add(1) }))
	_, _, conn := dialWS(t, srv)

	b, err := (&pgproto3.CancelRequest{ProcessID: 1, SecretKey: []byte{0, 0, 0, 1}}).Encode(nil)
	require.NoError(t, err)
	_, err = conn.Write(b)
	require.NoError(t, err)

	rest, _ := io.ReadAll(conn) // returns once the server drops the connection
	assert.Empty(t, rest, "a cancel connection gets no FATAL, as PostgreSQL sends none")
	assert.Equal(t, int32(1), dials.Load())
}

// readDialFailure reads what the server sends when the upstream dial fails: an ErrorResponse, then the close frame.
func readDialFailure(t *testing.T, ctx context.Context, ws *websocket.Conn) (*pgproto3.ErrorResponse, error) {
	t.Helper()
	typ, frame, err := ws.Read(ctx)
	require.NoError(t, err, "the client is told why before the tunnel closes")
	require.Equal(t, websocket.MessageBinary, typ)
	msg, err := pgproto3.NewFrontend(bytes.NewReader(frame), nil).Receive()
	require.NoError(t, err)
	errResp, ok := msg.(*pgproto3.ErrorResponse)
	require.True(t, ok, "got %T", msg)

	_, _, err = ws.Read(ctx)
	return errResp, err
}

func TestUpgradeDialFailureClosesWithReasonAndFiresHook(t *testing.T) {
	var hooked error
	srv := startWSServer(t, func(echo.Context) (Upstream, error) { return Upstream{Addr: "127.0.0.1:1"}, nil },
		WithDialErrorHook(func(_ echo.Context, err error) { hooked = err }))
	ctx, ws, conn := dialWS(t, srv)

	_, err := conn.Write(startupBytes(t))
	require.NoError(t, err)

	errResp, err := readDialFailure(t, ctx, ws)
	assert.Equal(t, "FATAL", errResp.Severity)
	assert.Equal(t, "08006", errResp.Code)
	assert.Equal(t, "upstream unavailable", errResp.Message)
	assert.Equal(t, websocket.StatusInternalError, websocket.CloseStatus(err))
	assert.Contains(t, err.Error(), "upstream unavailable")
	assert.Error(t, hooked)
}

func TestUpgradeRefusesUpstreamWithoutTLS(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = io.ReadFull(c, make([]byte, 8))
		_, _ = c.Write([]byte{'N'})
	}()

	srv := startWSServer(t, func(echo.Context) (Upstream, error) { return Upstream{Addr: ln.Addr().String()}, nil })
	ctx, ws, conn := dialWS(t, srv)

	_, err = conn.Write(startupBytes(t))
	require.NoError(t, err)

	errResp, err := readDialFailure(t, ctx, ws)
	assert.Equal(t, "08006", errResp.Code)
	assert.Equal(t, websocket.StatusInternalError, websocket.CloseStatus(err),
		"an upstream that declines TLS must not be talked to in the clear")
}

func TestUpgradeForwardsStartupVerbatimWhenTLSIsDisabled(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	startup := startupBytes(t)
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, len(startup))
		_, _ = io.ReadFull(c, buf)
		got <- buf
		_, _ = c.Write([]byte("pong"))
	}()

	srv := startWSServer(t, func(echo.Context) (Upstream, error) {
		return Upstream{Addr: ln.Addr().String(), TLSMode: TLSDisable}, nil
	})
	_, _, conn := dialWS(t, srv)

	_, err = conn.Write(startup)
	require.NoError(t, err)

	assert.Equal(t, startup, <-got)
	reply := make([]byte, 4)
	_, err = io.ReadFull(conn, reply)
	require.NoError(t, err)
	assert.Equal(t, "pong", string(reply))

	_, _ = io.ReadAll(conn) // returns only once the upstream's close reaches the client
}

func TestUpgradeEndsWhenTheClientHangsUp(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	upstreamClosed := make(chan struct{})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, c)
		close(upstreamClosed)
	}()

	srv := startWSServer(t, func(echo.Context) (Upstream, error) {
		return Upstream{Addr: ln.Addr().String(), TLSMode: TLSDisable}, nil
	})
	_, ws, conn := dialWS(t, srv)
	_, err = conn.Write(startupBytes(t))
	require.NoError(t, err)

	require.NoError(t, ws.Close(websocket.StatusNormalClosure, ""))

	select {
	case <-upstreamClosed:
	case <-time.After(wsTestTimeout):
		t.Fatal("the upstream connection stayed open after the client left")
	}
}

func TestHTTPHandlerWebSocketAdmissionRefusal(t *testing.T) {
	srv := httptest.NewServer(NewHTTPHandler(func(*http.Request) (Upstream, error) {
		return Upstream{}, &AdmissionError{Status: http.StatusForbidden, Message: "no"}
	}))
	t.Cleanup(srv.Close)
	resp, err := http.Get(srv.URL + WebSocketPath)
	require.NoError(t, err)
	defer resp.Body.Close()
	require.Equal(t, http.StatusForbidden, resp.StatusCode)
}

func TestHTTPHandlerRelaysSessionToUpstream(t *testing.T) {
	pg := pgfake.New(t)
	srv := httptest.NewServer(NewHTTPHandler(func(*http.Request) (Upstream, error) {
		return Upstream{Addr: pg.Addr(), InsecureSkipVerify: true}, nil
	}))
	t.Cleanup(srv.Close)
	_, _, conn := dialWS(t, srv)

	_, err := conn.Write(sslRequestBytes(t))
	require.NoError(t, err)
	answer := make([]byte, 1)
	_, err = io.ReadFull(conn, answer)
	require.NoError(t, err)
	assert.Equal(t, byte('N'), answer[0])

	_, err = conn.Write(startupBytes(t))
	require.NoError(t, err)
	fe := pgproto3.NewFrontend(conn, conn)
	for {
		msg, err := fe.Receive()
		require.NoError(t, err)
		if _, ok := msg.(*pgproto3.ReadyForQuery); ok {
			break
		}
	}
	assert.Equal(t, "u", pg.StartupParam("user"))
}

// The driver's default pipelineConnect sends its password in the same message as the startup, before any challenge.
func TestUpgradeForwardsBytesPipelinedAfterStartup(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })

	startup := startupBytes(t)
	password, err := (&pgproto3.PasswordMessage{Password: "secret"}).Encode(nil)
	require.NoError(t, err)
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, len(startup)+len(password))
		_, _ = io.ReadFull(c, buf)
		got <- buf
	}()

	srv := startWSServer(t, func(echo.Context) (Upstream, error) {
		return Upstream{Addr: ln.Addr().String(), TLSMode: TLSDisable}, nil
	})
	_, _, conn := dialWS(t, srv)

	_, err = conn.Write(append(append([]byte{}, startup...), password...))
	require.NoError(t, err)

	select {
	case b := <-got:
		assert.Equal(t, startup, b[:len(startup)])
		assert.Equal(t, password, b[len(startup):], "the pipelined password reaches the upstream")
	case <-time.After(wsTestTimeout):
		t.Fatal("the bytes pipelined after the startup never reached the upstream")
	}
}

const shortStartupTimeout = 200 * time.Millisecond

func withStartupTimeout(d time.Duration) Option {
	return func(h *Handler) { h.startupTimeout = d }
}

func TestUpgradeClosesAClientThatNeverSendsItsStartup(t *testing.T) {
	for _, tc := range []struct {
		name  string
		first func(t *testing.T, conn net.Conn)
	}{
		{"silent after the upgrade", func(*testing.T, net.Conn) {}},
		{"silent after declining TLS", func(t *testing.T, conn net.Conn) {
			_, err := conn.Write(sslRequestBytes(t))
			require.NoError(t, err)
			_, err = io.ReadFull(conn, make([]byte, 1))
			require.NoError(t, err)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ln, err := net.Listen("tcp", "127.0.0.1:0")
			require.NoError(t, err)
			t.Cleanup(func() { ln.Close() })
			var dialed atomic.Bool
			go func() {
				if c, err := ln.Accept(); err == nil {
					dialed.Store(true)
					c.Close()
				}
			}()
			srv := startWSServer(t, func(echo.Context) (Upstream, error) {
				return Upstream{Addr: ln.Addr().String(), TLSMode: TLSDisable}, nil
			}, withStartupTimeout(shortStartupTimeout))
			ctx, ws, conn := dialWS(t, srv)
			tc.first(t, conn)

			closed := make(chan error, 1)
			go func() {
				_, _, err := ws.Read(ctx)
				closed <- err
			}()
			select {
			case err := <-closed:
				require.Error(t, err)
				assert.NotErrorIs(t, err, context.DeadlineExceeded, "the server, not the test, ended the tunnel")
			case <-time.After(wsTestTimeout / 2):
				t.Fatal("the tunnel stayed open for a client that never sent its startup")
			}
			assert.False(t, dialed.Load(), "a tunnel without a startup must not reach the upstream")
		})
	}
}

func TestUpgradeStartupDeadlineEndsWithTheStartup(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 42 AS n", pgfake.Result{
		Cols: []pgfake.Col{{Name: "n", TypeOID: oidInt4}},
		Rows: [][]any{{int32(42)}},
		Tag:  "SELECT 1",
	})
	fe, _ := openWSSession(t, startWSServer(t, fakeUpstream(pg), withStartupTimeout(shortStartupTimeout)))

	time.Sleep(3 * shortStartupTimeout)

	fe.Send(&pgproto3.Query{String: "SELECT 42 AS n"})
	require.NoError(t, fe.Flush())
	var row []byte
	for {
		msg, err := fe.Receive()
		require.NoError(t, err, "an idle session outlived the startup deadline")
		if dr, ok := msg.(*pgproto3.DataRow); ok {
			row = dr.Values[0]
		}
		if _, ok := msg.(*pgproto3.ReadyForQuery); ok {
			break
		}
	}
	assert.Equal(t, "42", string(row))
}
