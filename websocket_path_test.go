package pgserverless

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// The client's wsProxy picks the path, and its own docs configure "/v1" for a non-Neon proxy.
func TestUpgradeOnAnyPath(t *testing.T) {
	pg := pgfake.New(t)
	srv := startWSServer(t, fakeUpstream(pg))

	for _, path := range []string{"/v1?address=db.example.test:5432", "/", "/some/proxy/path"} {
		t.Run(path, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), wsTestTimeout)
			defer cancel()
			ws, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+path, nil)
			require.NoError(t, err)
			defer ws.CloseNow()
			conn := websocket.NetConn(ctx, ws, websocket.MessageBinary)

			_, err = conn.Write(sslRequestBytes(t))
			require.NoError(t, err)
			answer := make([]byte, 1)
			_, err = io.ReadFull(conn, answer)
			require.NoError(t, err)
			assert.Equal(t, byte('N'), answer[0])
		})
	}
}

func TestNonUpgradeRequestOnOtherPathIsNotFound(t *testing.T) {
	admit := func(echo.Context) (Upstream, error) { return Upstream{}, assert.AnError }
	e := echo.New()
	NewHandler(admit).RegisterWebSocket(e)
	handlers := map[string]http.Handler{
		"echo":     e,
		"net/http": stdlibWSHandler(admit),
	}
	for name, h := range handlers {
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/v1", nil))
		assert.Equal(t, http.StatusNotFound, rec.Code, name)
	}
}

func TestIsWebSocketUpgrade(t *testing.T) {
	for _, tc := range []struct {
		method, connection, upgrade string
		want                        bool
	}{
		{http.MethodGet, "Upgrade", "websocket", true},
		{http.MethodGet, "keep-alive, Upgrade", "WebSocket", true},
		{http.MethodGet, "keep-alive", "websocket", false},
		{http.MethodGet, "Upgrade", "h2c", false},
		{http.MethodPost, "Upgrade", "websocket", false},
	} {
		r := httptest.NewRequest(tc.method, "/v1", nil)
		r.Header.Set("Connection", tc.connection)
		r.Header.Set("Upgrade", tc.upgrade)
		assert.Equal(t, tc.want, isWebSocketUpgrade(r), "%s %q %q", tc.method, tc.connection, tc.upgrade)
	}
}
