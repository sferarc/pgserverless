package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/sferarc/pgserverless"
)

func TestIsLoopback(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:4000": true,
		"localhost:4000": true,
		"[::1]:4000":     true,
		":4000":          false,
		"0.0.0.0:4000":   false,
		"10.0.0.1:4000":  false,
		"not-an-addr":    false,
	}
	for addr, want := range cases {
		if got := isLoopback(addr); got != want {
			t.Errorf("isLoopback(%q) = %v, want %v", addr, got, want)
		}
	}
}

func TestTLSMode(t *testing.T) {
	if got := tlsMode("postgres://u:p@db:5432/app?sslmode=disable"); got != pgserverless.TLSDisable {
		t.Errorf("sslmode=disable: got %q", got)
	}
	if got := tlsMode("postgres://u:p@db:5432/app"); got != pgserverless.TLSRequire {
		t.Errorf("no sslmode: got %q", got)
	}
}

func TestTLSModeReadsSSLModeAfterAPasswordURLParseRefuses(t *testing.T) {
	dbURL := "postgres://u:50%off@db:5432/app?sslmode=disable"
	if _, err := pgserverless.ParseConnectionString(dbURL); err != nil {
		t.Fatalf("run() accepts this DATABASE_URL, so tlsMode must read it: %v", err)
	}
	if got := tlsMode(dbURL); got != pgserverless.TLSDisable {
		t.Errorf("got %q, want %q", got, pgserverless.TLSDisable)
	}
	if got := tlsMode("postgres://u:50%off@db:5432/app?sslmode=require#x"); got != pgserverless.TLSRequire {
		t.Errorf("sslmode=require: got %q", got)
	}
	if got := tlsMode("postgres://u:50%off@db:5432/app#?sslmode=disable"); got != pgserverless.TLSRequire {
		t.Errorf("sslmode=disable in the fragment: got %q", got)
	}
}

func TestTLSModeIgnoresPassword(t *testing.T) {
	if got := tlsMode("postgres://u:sslmode=disable@db:5432/app"); got != pgserverless.TLSRequire {
		t.Errorf("sslmode in password: got %q", got)
	}
}

func TestHTTPAdmission(t *testing.T) {
	db, err := pgserverless.ParseConnectionString("postgres://owner:secret@db.example.test:5433/app")
	if err != nil {
		t.Fatal(err)
	}
	admit := httpAdmission(db, pgserverless.TLSDisable)

	t.Run("request credentials, DATABASE_URL host", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/sql", nil)
		req.Header.Set("Connection-String", "postgres://reader:pw@elsewhere.example.test/other")
		up, err := admit(req)
		if err != nil {
			t.Fatal(err)
		}
		want := pgserverless.Upstream{
			Addr: "db.example.test:5433", ServerName: "db.example.test",
			User: "reader", Password: "pw", Database: "other", TLSMode: pgserverless.TLSDisable,
		}
		if up != want {
			t.Errorf("got %+v, want %+v", up, want)
		}
	})

	t.Run("no header is refused, never DATABASE_URL credentials", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/sql", nil)
		up, err := admit(req)
		if err == nil {
			t.Fatalf("want an error, got %+v", up)
		}
	})

	t.Run("malformed header is refused", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/sql", nil)
		req.Header.Set("Connection-String", "not a url")
		if _, err := admit(req); err == nil {
			t.Error("want an error")
		}
	})
}

func TestWSAdmissionCarriesNoCredentials(t *testing.T) {
	db, err := pgserverless.ParseConnectionString("postgres://owner:secret@db.example.test:5433/app")
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/v2", nil)
	up, err := wsAdmission(db, pgserverless.TLSRequire)(req)
	if err != nil {
		t.Fatal(err)
	}
	want := pgserverless.Upstream{Addr: "db.example.test:5433", ServerName: "db.example.test", TLSMode: pgserverless.TLSRequire}
	if up != want {
		t.Errorf("got %+v, want %+v", up, want)
	}
}

func TestRunRedactsDatabaseURL(t *testing.T) {
	t.Setenv("PGSERVERLESS_ADDR", "127.0.0.1:0")
	t.Setenv("DATABASE_URL", "postgres://u:s3cret@db:bad/app")
	err := run()
	if err == nil || strings.Contains(err.Error(), "s3cret") {
		t.Errorf("run() = %v, want an error without the password", err)
	}
}

func TestRunRefusesRemoteListener(t *testing.T) {
	t.Setenv("PGSERVERLESS_ADDR", "0.0.0.0:0")
	t.Setenv("PGSERVERLESS_ALLOW_REMOTE", "")
	t.Setenv("DATABASE_URL", "postgres://u:p@db/app")
	if err := run(); err == nil || !strings.Contains(err.Error(), "refusing") {
		t.Errorf("run() = %v, want a refusal", err)
	}
}

func TestSplitOrigins(t *testing.T) {
	got := splitOrigins(" https://a.example.test, ,https://b.example.test ")
	if strings.Join(got, "|") != "https://a.example.test|https://b.example.test" {
		t.Errorf("got %q", got)
	}
	if got := splitOrigins(""); got != nil {
		t.Errorf("empty: got %q, want nil so CORS stays off", got)
	}
}
