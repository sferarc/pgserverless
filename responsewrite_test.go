package pgserverless

import (
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

const shortWriteTimeout = 200 * time.Millisecond

// writeErrRecorder keeps the first response write error; Unwrap lets http.ResponseController reach the connection.
type writeErrRecorder struct {
	http.ResponseWriter
	err error
}

func (w *writeErrRecorder) Write(b []byte) (int, error) {
	n, err := w.ResponseWriter.Write(b)
	if err != nil && w.err == nil {
		w.err = err
	}
	return n, err
}

func (w *writeErrRecorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// handlerReturnTimeout only has to outlast building bigResult under the race detector on a busy runner; a handler with no write deadline never returns.
const handlerReturnTimeout = 60 * time.Second

// writeDeadlineServer is one transport on a real listener, since a write deadline needs a connection; returned gets each POST /sql handler's first write error once it returns.
type writeDeadlineServer struct {
	addr     string
	returned <-chan error
}

func writeDeadlineServers(t *testing.T, upstreamAddr string) map[string]writeDeadlineServer {
	t.Helper()
	static := StaticUpstream(upstreamAddr)
	e := echo.New()
	NewHandler(func(c echo.Context) (Upstream, error) {
		up, err := static(c)
		up.InsecureSkipVerify = true
		return up, err
	}, func(h *Handler) { h.writeTimeout = shortWriteTimeout }).Register(e)

	staticHTTP := StaticHTTPUpstream(upstreamAddr)
	std := NewHTTPHandler(func(r *http.Request) (Upstream, error) {
		up, err := staticHTTP(r)
		up.InsecureSkipVerify = true
		return up, err
	}, func(h *httpHandler) { h.writeTimeout = shortWriteTimeout })

	servers := map[string]writeDeadlineServer{}
	for name, h := range map[string]http.Handler{"echo": e, "net/http": std} {
		// Per transport, so a handler still running after its subtest cannot answer the next one.
		returned := make(chan error, 16)
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			rec := &writeErrRecorder{ResponseWriter: w}
			defer func() { returned <- rec.err }()
			h.ServeHTTP(rec, r)
		}))
		t.Cleanup(srv.Close)
		servers[name] = writeDeadlineServer{addr: strings.TrimPrefix(srv.URL, "http://"), returned: returned}
	}
	return servers
}

// bigResult is far larger than loopback socket buffers, so a client that never reads stalls the write.
func bigResult() pgfake.Result {
	value := strings.Repeat("x", 256<<10)
	rows := make([][]any, 128)
	for i := range rows {
		rows[i] = []any{value}
	}
	return pgfake.Result{Cols: []pgfake.Col{{Name: "v", TypeOID: oidText}}, Rows: rows, Tag: "SELECT 128"}
}

func TestStalledResponseReaderIsCutOff(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT big", bigResult())

	for name, srv := range writeDeadlineServers(t, pg.Addr()) {
		t.Run(name, func(t *testing.T) {
			conn, err := net.Dial("tcp", srv.addr)
			require.NoError(t, err)
			defer conn.Close()
			if tcp, ok := conn.(*net.TCPConn); ok {
				require.NoError(t, tcp.SetReadBuffer(4<<10))
			}

			body := `{"query":"SELECT big"}`
			_, err = io.WriteString(conn, "POST /sql HTTP/1.1\r\n"+
				"Host: "+conformanceHost+"\r\n"+
				"Neon-Connection-String: postgresql://u:p@"+conformanceHost+"/db\r\n"+
				"Content-Type: application/json\r\n"+
				"Content-Length: "+strconv.Itoa(len(body))+"\r\n\r\n"+body)
			require.NoError(t, err)

			start := time.Now()
			var writeErr error
			select {
			case writeErr = <-srv.returned:
			case <-time.After(handlerReturnTimeout):
				t.Fatal("the handler kept writing to a client that stopped reading")
			}
			assert.GreaterOrEqual(t, time.Since(start), shortWriteTimeout)
			// Draining what the server kernel already queued through a tiny window can outlast any client deadline on Linux, so check the cut server side.
			require.ErrorIs(t, writeErr, os.ErrDeadlineExceeded, "the response write was not cut by the deadline")
		})
	}
}

func TestResponseWriteDeadlineIsClearedForTheNextRequest(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1", oneInt(1))

	for name, srv := range writeDeadlineServers(t, pg.Addr()) {
		t.Run(name, func(t *testing.T) {
			client := &http.Client{Transport: &http.Transport{MaxConnsPerHost: 1}}
			defer client.CloseIdleConnections()
			for i := range 2 {
				if i > 0 {
					// Past the first response's deadline, on the same keep-alive connection.
					time.Sleep(2 * shortWriteTimeout)
				}
				req, err := http.NewRequest(http.MethodPost, "http://"+srv.addr+"/sql", strings.NewReader(`{"query":"SELECT 1"}`))
				require.NoError(t, err)
				req.Header.Set("Neon-Connection-String", "postgresql://u:p@"+conformanceHost+"/db")
				resp, err := client.Do(req)
				require.NoError(t, err)
				got, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, resp.StatusCode, string(got))
			}
		})
	}
}
