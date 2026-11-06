package pgserverless

import (
	"context"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

var uuidV4 = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// Neon's proxy adds Neon-Request-Id to every response, errors included, so a client has an id to quote.
func TestPOSTSQLResponsesCarryARequestID(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1", oneInt(1))
	ok := Upstream{Addr: pg.Addr(), InsecureSkipVerify: true, User: "u", Database: "db"}

	for _, tc := range []struct {
		name  string
		admit Admission
		body  string
		want  int
	}{
		{"success", func(echo.Context) (Upstream, error) { return ok, nil }, `{"query":"SELECT 1"}`, http.StatusOK},
		{"invalid body", func(echo.Context) (Upstream, error) { return ok, nil }, `{`, http.StatusBadRequest},
		{"admission refusal", func(echo.Context) (Upstream, error) {
			return Upstream{}, &AdmissionError{Status: http.StatusForbidden, Message: "suspended"}
		}, `{"query":"SELECT 1"}`, http.StatusForbidden},
		{"unreachable upstream", func(echo.Context) (Upstream, error) { return Upstream{Addr: "127.0.0.1:1"}, nil }, `{"query":"SELECT 1"}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			admit := tc.admit
			for name, h := range map[string]http.Handler{
				"echo":     newEchoServer(admit),
				"net/http": NewHTTPHandler(func(r *http.Request) (Upstream, error) { return admit(echo.New().NewContext(r, nil)) }),
			} {
				first := doSQL(h, tc.body, nil)
				second := doSQL(h, tc.body, nil)
				if tc.want != 0 {
					require.Equal(t, tc.want, first.Code, "%s: %s", name, first.Body.String())
				}
				id := first.Header().Get(requestIDHeader)
				assert.Regexp(t, uuidV4, id, name)
				assert.NotEqual(t, id, second.Header().Get(requestIDHeader), "%s: each request gets its own id", name)
			}
		})
	}
}

func TestPreflightCarriesARequestID(t *testing.T) {
	rec := preflight(NewHTTPHandler(refuseAll, WithHTTPCORSOrigins("*")), "https://app.example.test")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Regexp(t, uuidV4, rec.Header().Get(requestIDHeader))
}

func TestWebSocketUpgradeCarriesARequestID(t *testing.T) {
	pg := pgfake.New(t)
	srv := startWSServer(t, fakeUpstream(pg))

	ctx, cancel := context.WithTimeout(t.Context(), wsTestTimeout)
	defer cancel()
	ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+WebSocketPath, &websocket.DialOptions{
		Subprotocols: []string{wsSubprotocol},
	})
	require.NoError(t, err)
	defer ws.CloseNow()
	assert.Equal(t, http.StatusSwitchingProtocols, resp.StatusCode)
	assert.Regexp(t, uuidV4, resp.Header.Get(requestIDHeader))

	refused := startWSServer(t, func(echo.Context) (Upstream, error) { return Upstream{}, assert.AnError })
	rec, err := http.Get(refused.URL + WebSocketPath)
	require.NoError(t, err)
	defer rec.Body.Close()
	assert.Equal(t, http.StatusBadRequest, rec.StatusCode)
	assert.Regexp(t, uuidV4, rec.Header.Get(requestIDHeader))
}

func TestSetRequestIDIsAVersion4UUID(t *testing.T) {
	seen := map[string]bool{}
	for range 100 {
		id := setRequestID(httptest.NewRecorder())
		require.Regexp(t, uuidV4, id)
		require.False(t, seen[id], "repeated id %s", id)
		seen[id] = true
	}
}
