package pgserverless

import (
	"bufio"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/sferarc/pgserverless/pgfake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const shortBodyTimeout = 200 * time.Millisecond

// bodyDeadlineServers serves both transports on a real listener, since a read deadline needs a connection.
func bodyDeadlineServers(t *testing.T, upstreamAddr string) map[string]string {
	t.Helper()
	static := StaticUpstream(upstreamAddr)
	e := echo.New()
	NewHandler(func(c echo.Context) (Upstream, error) {
		up, err := static(c)
		up.InsecureSkipVerify = true
		return up, err
	}, func(h *Handler) { h.bodyTimeout = shortBodyTimeout }).Register(e)

	staticHTTP := StaticHTTPUpstream(upstreamAddr)
	std := NewHTTPHandler(func(r *http.Request) (Upstream, error) {
		up, err := staticHTTP(r)
		up.InsecureSkipVerify = true
		return up, err
	}, func(h *httpHandler) { h.bodyTimeout = shortBodyTimeout })

	addrs := map[string]string{}
	for name, h := range map[string]http.Handler{"echo": e, "net/http": std} {
		srv := httptest.NewServer(h)
		t.Cleanup(srv.Close)
		addrs[name] = strings.TrimPrefix(srv.URL, "http://")
	}
	return addrs
}

func TestStalledRequestBodyIsCutOff(t *testing.T) {
	for name, addr := range bodyDeadlineServers(t, "127.0.0.1:1") {
		t.Run(name, func(t *testing.T) {
			conn, err := net.Dial("tcp", addr)
			require.NoError(t, err)
			defer conn.Close()

			start := time.Now()
			_, err = io.WriteString(conn, "POST /sql HTTP/1.1\r\n"+
				"Host: "+conformanceHost+"\r\n"+
				"Neon-Connection-String: postgresql://u:p@"+conformanceHost+"/db\r\n"+
				"Content-Type: application/json\r\n"+
				"Content-Length: 100\r\n\r\n"+
				`{"query":`)
			require.NoError(t, err)

			require.NoError(t, conn.SetReadDeadline(time.Now().Add(5*time.Second)))
			resp, err := http.ReadResponse(bufio.NewReader(conn), nil)
			require.NoError(t, err, "the handler kept waiting for a body that never arrived")
			defer resp.Body.Close()
			elapsed := time.Since(start)

			assert.GreaterOrEqual(t, elapsed, shortBodyTimeout)
			assert.Less(t, elapsed, 3*time.Second)
			require.Equal(t, http.StatusBadRequest, resp.StatusCode)
			var body ErrorResponse
			require.NoError(t, json.NewDecoder(resp.Body).Decode(&body))
			assert.Contains(t, body.Message, "did not arrive in time")
		})
	}
}

func TestBodyDeadlineSparesAQueryThatOutlastsIt(t *testing.T) {
	pg := pgfake.New(t)
	slow := oneInt(1)
	slow.Delay = 4 * shortBodyTimeout
	pg.SetResult("SELECT 1", slow)

	for name, addr := range bodyDeadlineServers(t, pg.Addr()) {
		t.Run(name, func(t *testing.T) {
			// Twice, so the second request reuses a connection whose deadline has passed.
			for range 2 {
				req, err := http.NewRequest(http.MethodPost, "http://"+addr+"/sql", strings.NewReader(`{"query":"SELECT 1"}`))
				require.NoError(t, err)
				req.Header.Set("Neon-Connection-String", "postgresql://u:p@"+conformanceHost+"/db")
				resp, err := http.DefaultClient.Do(req)
				require.NoError(t, err)
				body, err := io.ReadAll(resp.Body)
				_ = resp.Body.Close()
				require.NoError(t, err)
				require.Equal(t, http.StatusOK, resp.StatusCode, string(body))
			}
		})
	}
}
