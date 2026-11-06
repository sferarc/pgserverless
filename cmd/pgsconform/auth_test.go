package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

const testToken = "conformance-token"

// requireBearer answers 401 with the error shape unless the request carries testToken, as cmd/pgserverless does with PGSERVERLESS_AUTH_TOKEN.
func requireBearer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+testToken {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"unauthorized"}`))
			return
		}
		next.ServeHTTP(w, r)
	})
}

func TestTokenReachesTheServerUnderTest(t *testing.T) {
	answer := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"fields":[],"rows":[],"command":"SELECT","rowCount":0,"rowAsArray":false}`))
	})
	srv := httptest.NewServer(requireBearer(answer))
	defer srv.Close()
	k := kase{ID: "x", Level: 1, Request: requestSpec{Path: "/sql", Body: []byte(`{"query":"SELECT 1"}`)}, Expect: []byte(`{"status":200}`)}

	tests := []struct {
		name  string
		token string
		want  outcome
	}{
		{"token set", testToken, outcomePass},
		{"no token", "", outcomeFail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &runner{server: srv.URL, authToken: tt.token}
			if got, msg := r.runCase(context.Background(), k); got != tt.want {
				t.Fatalf("outcome %d (%s), want %d", got, msg, tt.want)
			}
		})
	}
}

func TestTokenReachesTheWebSocketUpgrade(t *testing.T) {
	srv := httptest.NewServer(requireBearer(scriptedServer(t, nil)))
	defer srv.Close()
	k := kase{ID: "x", Level: 4, Requires: "websocket", Request: requestSpec{Path: "/v2", Method: http.MethodGet}, Expect: []byte(`{}`)}

	r := &runner{server: srv.URL, authToken: testToken}
	if got, msg := r.runCase(context.Background(), k); got != outcomePass {
		t.Fatalf("outcome %d (%s), want a pass", got, msg)
	}
}

// The refusing server is an instance whose token the harness never sends, so sending it there would turn the refusal into an upgrade.
func TestTokenIsNotSentToTheRefusingServer(t *testing.T) {
	refusing := httptest.NewServer(requireBearer(scriptedServer(t, nil)))
	defer refusing.Close()

	r := &runner{server: "http://127.0.0.1:1", refusingServer: refusing.URL, authToken: testToken}
	if got, msg := r.runCase(context.Background(), corpusCase(t, "websocket/admission-refused-before-upgrade")); got != outcomePass {
		t.Fatalf("outcome %d (%s), want a pass", got, msg)
	}
}

func TestTargetForCarriesTheTokenOnlyToTheServerUnderTest(t *testing.T) {
	r := &runner{server: "http://a", refusingServer: "http://b", unresolvableHost: "nowhere.invalid", authToken: testToken}
	tests := []struct {
		requires string
		want     string
	}{
		{"", testToken},
		{"websocket", testToken},
		{"unresolvable_host", testToken},
		{"refusing_admission", ""},
	}
	for _, tt := range tests {
		t.Run(tt.requires, func(t *testing.T) {
			got, why := r.targetFor(kase{Requires: tt.requires})
			if why != "" {
				t.Fatalf("targetFor: %s", why)
			}
			if got.authToken != tt.want {
				t.Fatalf("token %q, want %q", got.authToken, tt.want)
			}
		})
	}
}
