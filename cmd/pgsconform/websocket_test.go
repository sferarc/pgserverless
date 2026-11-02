package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
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

// The published refusal cases pass a server that refuses before the upgrade and fail one that upgrades or answers without the error shape.
func TestRefusedUpgradeCases(t *testing.T) {
	const badHost = "unknown.example.test"
	refuse := func(body string) http.HandlerFunc {
		return func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(body))
		}
	}
	// Refuses only the host the harness was told cannot be resolved, so a pass shows the Host header was sent.
	byHost := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Host == badHost {
			refuse(`{"message":"no such endpoint"}`)(w, r)
			return
		}
		scriptedServer(t, nil).ServeHTTP(w, r)
	})
	tests := []struct {
		name    string
		id      string
		handler http.Handler
		want    outcome
	}{
		{"host refused", "websocket/unresolvable-host-refused-before-upgrade", byHost, outcomePass},
		{"host upgraded", "websocket/unresolvable-host-refused-before-upgrade", scriptedServer(t, nil), outcomeFail},
		{"admission refused", "websocket/admission-refused-before-upgrade", refuse(`{"message":"unauthorized"}`), outcomePass},
		{"admission upgraded", "websocket/admission-refused-before-upgrade", scriptedServer(t, nil), outcomeFail},
		{"refusal without a message", "websocket/admission-refused-before-upgrade", refuse(`{"error":"unauthorized"}`), outcomeFail},
		{"refusal not JSON", "websocket/admission-refused-before-upgrade", refuse(`unauthorized`), outcomeFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(tt.handler)
			defer srv.Close()
			r := &runner{server: srv.URL, unresolvableHost: badHost, refusingServer: srv.URL}
			if got, msg := r.runCase(context.Background(), corpusCase(t, tt.id)); got != tt.want {
				t.Fatalf("outcome %d (%s), want %d", got, msg, tt.want)
			}
		})
	}
}

// Without the server or host to send them to, the refusal cases are skipped, never passed.
func TestRefusedUpgradeCasesSkipWhenNotArranged(t *testing.T) {
	r := &runner{server: "http://127.0.0.1:1"}
	for _, id := range []string{"websocket/unresolvable-host-refused-before-upgrade", "websocket/admission-refused-before-upgrade"} {
		if got, msg := r.runCase(context.Background(), corpusCase(t, id)); got != outcomeSkip {
			t.Fatalf("%s: outcome %d (%s), want a skip", id, got, msg)
		}
	}
}

func corpusCase(t *testing.T, id string) kase {
	t.Helper()
	c, err := loadCorpus(filepath.Join("..", "..", "spec", "cases", "v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, k := range c.Cases {
		if k.ID == id {
			return k
		}
	}
	t.Fatalf("corpus has no case %s", id)
	return kase{}
}
