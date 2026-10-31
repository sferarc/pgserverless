package pgserverless

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// noTLSUpstream refuses every SSLRequest with 'N' and closes, and hands each
// connection that opens with anything else to plain, as a database that does
// not serve TLS does.
func noTLSUpstream(t *testing.T, plain func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { ln.Close() })
	sslRequest := string(sslRequestBytes(t))
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				head := make([]byte, 8)
				if _, err := io.ReadFull(c, head); err != nil {
					return
				}
				if string(head) == sslRequest {
					_, _ = c.Write([]byte{'N'})
					return
				}
				plain(&prefixedConn{Conn: c, r: io.MultiReader(strings.NewReader(string(head)), c)})
			}()
		}
	}()
	return ln.Addr().String()
}

type prefixedConn struct {
	net.Conn
	r io.Reader
}

func (p *prefixedConn) Read(b []byte) (int, error) { return p.r.Read(b) }

func TestDialUpstreamRawPreferFallsBackToTheClear(t *testing.T) {
	got := make(chan string, 1)
	addr := noTLSUpstream(t, func(c net.Conn) {
		buf := make([]byte, len("hello, upstream"))
		_, _ = io.ReadFull(c, buf)
		got <- string(buf)
	})

	conn, err := dialUpstreamRaw(context.Background(), Upstream{Addr: addr, TLSMode: TLSPrefer})
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	_, err = conn.Write([]byte("hello, upstream"))
	require.NoError(t, err)

	select {
	case s := <-got:
		assert.Equal(t, "hello, upstream", s, "the bytes after the refused SSLRequest must reach the upstream in the clear")
	case <-time.After(5 * time.Second):
		t.Fatal("no plaintext connection reached the upstream")
	}

	_, err = dialUpstreamRaw(context.Background(), Upstream{Addr: addr})
	require.Error(t, err, "require must not fall back to the clear")
}

func TestDialUpstreamRawPreferUsesTLSWhenOffered(t *testing.T) {
	pg := pgfake.New(t)
	conn, err := dialUpstreamRaw(context.Background(), Upstream{Addr: pg.Addr(), TLSMode: TLSPrefer, InsecureSkipVerify: true})
	require.NoError(t, err)
	t.Cleanup(func() { conn.Close() })
	assert.IsType(t, (*tls.Conn)(nil), conn)
}

// The POST /sql dial reaches the upstream's startup in the clear once TLS is refused.
func TestSQLPreferFallsBackToTheClear(t *testing.T) {
	startup := make(chan *pgproto3.StartupMessage, 1)
	addr := noTLSUpstream(t, func(c net.Conn) {
		be := pgproto3.NewBackend(c, c)
		msg, err := be.ReceiveStartupMessage()
		if err != nil {
			return
		}
		if m, ok := msg.(*pgproto3.StartupMessage); ok {
			startup <- m
		}
		be.Send(&pgproto3.ErrorResponse{Severity: "FATAL", SeverityUnlocalized: "FATAL", Code: "3D000", Message: `database "db" does not exist`})
		_ = be.Flush()
	})

	post := func(mode string) *httptest.ResponseRecorder {
		h := NewHTTPHandler(func(*http.Request) (Upstream, error) {
			return Upstream{Addr: addr, User: "u", Password: "p", Database: "db", TLSMode: mode}, nil
		})
		req := httptest.NewRequest(http.MethodPost, "/sql", strings.NewReader(`{"query":"SELECT 1"}`))
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec
	}

	rec := post(TLSPrefer)
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	var body ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.NotNil(t, body.Code)
	assert.Equal(t, "3D000", *body.Code)
	select {
	case m := <-startup:
		assert.Equal(t, "u", m.Parameters["user"])
		assert.Equal(t, "db", m.Parameters["database"])
	default:
		t.Fatal("the plaintext startup never reached the upstream")
	}

	rec = post(TLSRequire)
	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Empty(t, startup, "require must not fall back to the clear")
}

func TestSQLPreferUsesTLSWhenOffered(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1", oneInt(1))
	h := NewHTTPHandler(func(*http.Request) (Upstream, error) {
		return Upstream{Addr: pg.Addr(), User: "u", Database: "db", TLSMode: TLSPrefer, InsecureSkipVerify: true}, nil
	})
	req := httptest.NewRequest(http.MethodPost, "/sql", strings.NewReader(`{"query":"SELECT 1"}`))
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	assert.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
}
