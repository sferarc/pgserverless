package pgserverless

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// corpusWSCase is the part of a spec/cases/v1.json case that level 4 reads.
type corpusWSCase struct {
	ID       string `json:"id"`
	Requires string `json:"requires"`
	Request  struct {
		Path string `json:"path"`
	} `json:"request"`
	Expect struct {
		Status    int `json:"status"`
		WebSocket *struct {
			Upgraded              bool   `json:"upgraded"`
			Subprotocol           string `json:"subprotocol"`
			SSLRequestDeclined    bool   `json:"ssl_request_declined"`
			GSSEncRequestDeclined bool   `json:"gssenc_request_declined"`
		} `json:"websocket"`
	} `json:"expect"`
}

// The corpus's websocket cases pass against NewHTTPHandler with no database:
// every one is answered before the dial. This lives here rather than in
// cmd/pgsconform because the harness is published and must not link the
// implementation it judges.
func TestCorpusWebSocketCasesPassAgainstTheReferenceHandler(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("spec", "cases", "v1.json"))
	require.NoError(t, err)
	var corpus struct {
		Cases []corpusWSCase `json:"cases"`
	}
	require.NoError(t, json.Unmarshal(raw, &corpus))

	// Nothing listens upstream.
	srv := httptest.NewServer(NewHTTPHandler(StaticHTTPUpstream("127.0.0.1:1")))
	t.Cleanup(srv.Close)

	ran := 0
	for _, c := range corpus.Cases {
		if c.Requires != "websocket" {
			continue
		}
		ran++
		t.Run(c.ID, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), wsTestTimeout)
			defer cancel()
			ws, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(srv.URL, "http")+c.Request.Path, &websocket.DialOptions{
				Subprotocols: []string{wsSubprotocol},
				HTTPHeader:   http.Header{"Neon-Connection-String": []string{"postgresql://u:p@" + conformanceHost + "/db"}},
			})
			require.NoError(t, err)
			defer ws.CloseNow()
			if c.Expect.Status != 0 {
				assert.Equal(t, c.Expect.Status, resp.StatusCode)
			}
			exp := c.Expect.WebSocket
			if exp == nil {
				return
			}
			if exp.Subprotocol != "" {
				assert.Equal(t, exp.Subprotocol, ws.Subprotocol())
			}
			// Same order as libpq and the harness: GSSENCRequest, then SSLRequest.
			var reqs []pgproto3.FrontendMessage
			if exp.GSSEncRequestDeclined {
				reqs = append(reqs, &pgproto3.GSSEncRequest{})
			}
			if exp.SSLRequestDeclined {
				reqs = append(reqs, &pgproto3.SSLRequest{})
			}
			for _, m := range reqs {
				b, err := m.Encode(nil)
				require.NoError(t, err)
				require.NoError(t, ws.Write(ctx, websocket.MessageBinary, b))
				typ, data, err := ws.Read(ctx)
				require.NoError(t, err)
				assert.Equal(t, websocket.MessageBinary, typ)
				assert.Equal(t, "N", string(data), "%T must be declined with the single byte N", m)
			}
		})
	}
	require.GreaterOrEqual(t, ran, 3, "want the handshake and both encryption declines")
}
