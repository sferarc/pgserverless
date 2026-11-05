package pgserverless

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

func TestMsgScannerFollowsFramingAcrossSplitWrites(t *testing.T) {
	t.Parallel()

	var stream []byte
	stream, _ = (&pgproto3.ParameterStatus{Name: "server_version", Value: "16"}).Encode(stream)
	stream, _ = (&pgproto3.BackendKeyData{ProcessID: 7, SecretKey: []byte{1, 2, 3, 4}}).Encode(stream)
	stream, _ = (&pgproto3.DataRow{Values: [][]byte{make([]byte, 100)}}).Encode(stream)
	stream, _ = (&pgproto3.ReadyForQuery{TxStatus: 'I'}).Encode(stream)

	for _, chunk := range []int{1, 3, 5, 64, len(stream)} {
		var types []byte
		var key []byte
		s := msgScanner{keep: 'K', onMsg: func(typ byte, body []byte) {
			types = append(types, typ)
			if typ == 'K' {
				key = append([]byte(nil), body...)
			}
		}}
		for rest := stream; len(rest) > 0; {
			n := min(chunk, len(rest))
			_, _ = s.Write(rest[:n])
			rest = rest[n:]
		}
		assert.Equal(t, []byte("SKDZ"), types, "chunk %d", chunk)
		assert.Equal(t, []byte{0, 0, 0, 7, 1, 2, 3, 4}, key, "chunk %d", chunk)
	}
}

func TestMsgScannerStopsOnAnImpossibleLength(t *testing.T) {
	t.Parallel()

	calls := 0
	s := msgScanner{onMsg: func(byte, []byte) { calls++ }}
	n, err := s.Write([]byte{'Q', 0, 0, 0, 2, 'x', 'Z', 0, 0, 0, 5, 'I'})
	require.NoError(t, err)
	assert.Equal(t, 12, n, "a tee must never fail the relay")
	assert.True(t, s.broken)
	assert.Zero(t, calls)
}

// openTunnelSession upgrades, declines TLS and completes the startup through the tunnel.
func openTunnelSession(t *testing.T, srv *httptest.Server) *pgproto3.Frontend {
	t.Helper()
	_, _, conn := dialWS(t, srv)
	_, err := conn.Write(startupBytes(t))
	require.NoError(t, err)
	fe := pgproto3.NewFrontend(conn, conn)
	for {
		msg, err := fe.Receive()
		require.NoError(t, err)
		if _, ok := msg.(*pgproto3.ReadyForQuery); ok {
			return fe
		}
	}
}

func waitForRunning(t *testing.T, tunnels *Tunnels, want int) {
	t.Helper()
	deadline := time.Now().Add(wsTestTimeout)
	for tunnels.Running() != want {
		if time.Now().After(deadline) {
			t.Fatalf("Running() = %d, want %d", tunnels.Running(), want)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestCancelRunningCancelsATunnelStatementOnTheUpstream(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT pg_sleep(60)", pgfake.Result{WaitForCancel: true})
	pg.SetResult("SELECT 1", pgfake.Result{Cols: []pgfake.Col{{Name: "n", TypeOID: oidInt4}}, Rows: [][]any{{int32(1)}}})
	tunnels := &Tunnels{}
	admit := func(*http.Request) (Upstream, error) { return Upstream{Addr: pg.Addr(), InsecureSkipVerify: true}, nil }
	srv := httptest.NewServer(NewHTTPHandler(admit, WithHTTPTunnels(tunnels)))
	t.Cleanup(srv.Close)

	idle := openTunnelSession(t, srv)
	idle.Send(&pgproto3.Query{String: "SELECT 1"})
	require.NoError(t, idle.Flush())
	for {
		msg, err := idle.Receive()
		require.NoError(t, err)
		if _, ok := msg.(*pgproto3.ReadyForQuery); ok {
			break
		}
	}
	assert.Zero(t, tunnels.Running(), "a session between statements has nothing in flight")

	busy := openTunnelSession(t, srv)
	busy.Send(&pgproto3.Query{String: "SELECT pg_sleep(60)"})
	require.NoError(t, busy.Flush())
	waitForRunning(t, tunnels, 1)

	ctx, cancel := context.WithTimeout(context.Background(), wsTestTimeout)
	defer cancel()
	assert.Equal(t, 1, tunnels.CancelRunning(ctx), "only the tunnel with a statement in flight gets a CancelRequest")
	assert.Equal(t, 1, pg.CancelRequests(), "the upstream received the CancelRequest for the running statement")

	msg, err := busy.Receive()
	require.NoError(t, err)
	er, ok := msg.(*pgproto3.ErrorResponse)
	require.True(t, ok, "got %T, want the statement's ErrorResponse", msg)
	assert.Equal(t, "57014", er.Code)
	msg, err = busy.Receive()
	require.NoError(t, err)
	assert.IsType(t, &pgproto3.ReadyForQuery{}, msg)
	waitForRunning(t, tunnels, 0)
}

func TestCancelRunningOnNilTunnelsDoesNothing(t *testing.T) {
	t.Parallel()

	var tunnels *Tunnels
	assert.Zero(t, tunnels.Running())
	assert.Zero(t, tunnels.CancelRunning(context.Background()))
}

func TestCancelRunningReturnsWhenTheContextEnds(t *testing.T) {
	t.Parallel()

	// A listener that accepts and never answers holds the cancel's TLS negotiation.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { c.Close() })
		}
	}()

	tunnels := &Tunnels{}
	tun := tunnels.add(Upstream{Addr: ln.Addr().String()})
	tun.serverMessage('K', []byte{0, 0, 0, 1, 0, 0, 0, 2})

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	assert.Equal(t, 1, tunnels.CancelRunning(ctx))
	assert.Less(t, time.Since(start), 2*time.Second)
}
