package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/coder/websocket"
)

// The harness imports no implementation, so the run of the corpus against the
// reference handler lives in the pgserverless package
// (TestCorpusWebSocketCasesPassAgainstTheReferenceHandler).
func TestEncryptionDeclineFailsAWrongAnswer(t *testing.T) {
	tests := []struct {
		name    string
		answers [][]byte
		exp     expectedWSocket
	}{
		{"accepts TLS", [][]byte{{'S'}}, expectedWSocket{SSLRequestDeclined: true}},
		{"answers with more than N", [][]byte{[]byte("NE")}, expectedWSocket{SSLRequestDeclined: true}},
		{"declines only the first request", [][]byte{{'N'}}, expectedWSocket{GSSEncRequestDeclined: true, SSLRequestDeclined: true}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(scriptedServer(t, tt.answers))
			defer srv.Close()
			r := &runner{server: srv.URL}
			k := kase{ID: "x", Requires: "websocket", Request: requestSpec{Path: "/v2"}}
			if got, msg := r.runWebSocket(context.Background(), k, expectation{WebSocket: &tt.exp}); got != outcomeFail {
				t.Fatalf("outcome %d (%s), want a failure", got, msg)
			}
		})
	}
}

// scriptedServer upgrades, then answers each message it reads with the next of answers and hangs up when they run out.
func scriptedServer(t *testing.T, answers [][]byte) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, &websocket.AcceptOptions{Subprotocols: []string{"pg"}})
		if err != nil {
			t.Error(err)
			return
		}
		defer ws.CloseNow()
		for _, a := range answers {
			if _, _, err := ws.Read(r.Context()); err != nil {
				return
			}
			if err := ws.Write(r.Context(), websocket.MessageBinary, a); err != nil {
				return
			}
		}
		_ = ws.Close(websocket.StatusNormalClosure, "")
	})
}
