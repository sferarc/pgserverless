package main

import (
	"bytes"
	"context"
	"crypto/x509"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/sferarc/pgserverless"
	"github.com/sferarc/pgserverless/pgfake"
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
	if got := tlsFor("postgres://u:p@db:5432/app?sslmode=disable").mode; got != pgserverless.TLSDisable {
		t.Errorf("sslmode=disable: got %q", got)
	}
	if got := tlsFor("postgres://u:p@db:5432/app").mode; got != pgserverless.TLSRequire {
		t.Errorf("no sslmode: got %q", got)
	}
}

func TestTLSModeFallsBackToTheClearOnlyWhenSSLModeAllowsIt(t *testing.T) {
	for mode, want := range map[string]string{
		"allow":       pgserverless.TLSPrefer,
		"prefer":      pgserverless.TLSPrefer,
		"require":     pgserverless.TLSRequire,
		"verify-ca":   pgserverless.TLSRequire,
		"verify-full": pgserverless.TLSRequire,
	} {
		if got := tlsFor("postgres://u:p@db:5432/app?sslmode=" + mode).mode; got != want {
			t.Errorf("sslmode=%s: got %q, want %q", mode, got, want)
		}
	}
}

func TestTLSForChecksTheCertificateFromVerifyCAOrWithARootCert(t *testing.T) {
	for query, want := range map[string]bool{
		"":                                    false,
		"?sslmode=disable":                    false,
		"?sslmode=allow":                      true,
		"?sslmode=prefer":                     true,
		"?sslmode=require":                    true,
		"?sslmode=verify-ca":                  false,
		"?sslmode=verify-full":                false,
		"?sslmode=require&sslrootcert=ca.pem": false,
		"?sslmode=prefer&sslrootcert=system":  false,
	} {
		if got := tlsFor("postgres://u:p@db:5432/app" + query).skipVerify; got != want {
			t.Errorf("%q: skipVerify = %v, want %v", query, got, want)
		}
	}
}

func TestTLSForChecksTheHostNameOnlyFromVerifyFull(t *testing.T) {
	for query, want := range map[string]bool{
		"":                                        false,
		"?sslmode=require":                        false,
		"?sslmode=verify-ca":                      false,
		"?sslmode=verify-full":                    false,
		"?sslmode=require&sslrootcert=ca.pem":     true,
		"?sslmode=prefer&sslrootcert=ca.pem":      true,
		"?sslmode=verify-ca&sslrootcert=ca.pem":   true,
		"?sslmode=verify-full&sslrootcert=ca.pem": false,
		"?sslmode=verify-ca&sslrootcert=system":   false,
		"?sslmode=require&sslrootcert=system":     false,
	} {
		if got := tlsFor("postgres://u:p@db:5432/app" + query).caOnly; got != want {
			t.Errorf("%q: caOnly = %v, want %v", query, got, want)
		}
	}
}

func TestAdmissionsCarryTheCertificateCheck(t *testing.T) {
	db, err := pgserverless.ParseConnectionString("postgres://owner:secret@db.example.test:5433/app")
	if err != nil {
		t.Fatal(err)
	}
	tls := tlsFor("postgres://owner:secret@db.example.test:5433/app?sslmode=require")
	req := httptest.NewRequest(http.MethodPost, "/sql", nil)
	req.Header.Set("Connection-String", "postgres://reader:pw@db.example.test/app")
	for name, admit := range map[string]pgserverless.HTTPAdmission{"http": httpAdmission(db, tls), "websocket": wsAdmission(db, tls)} {
		up, err := admit(req)
		if err != nil {
			t.Fatal(err)
		}
		if up.TLSMode != pgserverless.TLSRequire || !up.InsecureSkipVerify {
			t.Errorf("%s: got TLSMode %q InsecureSkipVerify %v, want require without the check", name, up.TLSMode, up.InsecureSkipVerify)
		}
	}
}

func TestLoadRootCAsReadsTheSSLRootCertFile(t *testing.T) {
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(ca, pgfake.New(t).CertificatePEM(), 0o600); err != nil {
		t.Fatal(err)
	}
	notPEM := filepath.Join(dir, "not.pem")
	if err := os.WriteFile(notPEM, []byte("not a certificate"), 0o600); err != nil {
		t.Fatal(err)
	}

	roots, err := loadRootCAs(tlsFor("postgres://u:p@db/app?sslmode=verify-full&sslrootcert=" + ca).rootCert)
	if err != nil || roots == nil {
		t.Fatalf("a PEM file: got %v, %v, want its roots", roots, err)
	}
	for _, rootCert := range []string{"", "system"} {
		if roots, err := loadRootCAs(rootCert); roots != nil || err != nil {
			t.Errorf("%q: got %v, %v, want the system roots", rootCert, roots, err)
		}
	}
	for _, rootCert := range []string{filepath.Join(dir, "missing.pem"), notPEM} {
		if _, err := loadRootCAs(rootCert); err == nil {
			t.Errorf("%q: want an error", rootCert)
		}
	}
	if got := tlsFor("postgres://u:p@db/app?sslmode=disable&sslrootcert=" + ca).rootCert; got != "" {
		t.Errorf("sslmode=disable: rootCert = %q, want none read", got)
	}
}

func TestAdmissionsCarryTheRootCAs(t *testing.T) {
	db, err := pgserverless.ParseConnectionString("postgres://owner:secret@db.example.test:5433/app")
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	tls := upstreamTLS{mode: pgserverless.TLSRequire, roots: roots, caOnly: true}
	req := httptest.NewRequest(http.MethodPost, "/sql", nil)
	req.Header.Set("Connection-String", "postgres://reader:pw@db.example.test/app")
	for name, admit := range map[string]pgserverless.HTTPAdmission{"http": httpAdmission(db, tls), "websocket": wsAdmission(db, tls)} {
		up, err := admit(req)
		if err != nil {
			t.Fatal(err)
		}
		if up.RootCAs != roots || !up.VerifyCAOnly {
			t.Errorf("%s: RootCAs or VerifyCAOnly not passed to the upstream", name)
		}
	}
}

func TestRunRefusesAnUnreadableSSLRootCert(t *testing.T) {
	t.Setenv("PGSERVERLESS_ADDR", "127.0.0.1:0")
	t.Setenv("DATABASE_URL", "postgres://u:p@db/app?sslmode=verify-full&sslrootcert="+filepath.Join(t.TempDir(), "missing.pem"))
	if err := run(); err == nil || !strings.Contains(err.Error(), "sslrootcert") {
		t.Errorf("run() = %v, want an sslrootcert error", err)
	}
}

func TestTLSModeReadsSSLModeAfterAPasswordURLParseRefuses(t *testing.T) {
	dbURL := "postgres://u:50%off@db:5432/app?sslmode=disable"
	if _, err := pgserverless.ParseConnectionString(dbURL); err != nil {
		t.Fatalf("run() accepts this DATABASE_URL, so tlsFor must read it: %v", err)
	}
	if got := tlsFor(dbURL).mode; got != pgserverless.TLSDisable {
		t.Errorf("got %q, want %q", got, pgserverless.TLSDisable)
	}
	if got := tlsFor("postgres://u:50%off@db:5432/app?sslmode=require#x").mode; got != pgserverless.TLSRequire {
		t.Errorf("sslmode=require: got %q", got)
	}
	if got := tlsFor("postgres://u:50%off@db:5432/app#?sslmode=disable").mode; got != pgserverless.TLSRequire {
		t.Errorf("sslmode=disable in the fragment: got %q", got)
	}
}

func TestTLSModeIgnoresPassword(t *testing.T) {
	if got := tlsFor("postgres://u:sslmode=disable@db:5432/app").mode; got != pgserverless.TLSRequire {
		t.Errorf("sslmode in password: got %q", got)
	}
}

func TestHTTPAdmission(t *testing.T) {
	db, err := pgserverless.ParseConnectionString("postgres://owner:secret@db.example.test:5433/app")
	if err != nil {
		t.Fatal(err)
	}
	admit := httpAdmission(db, upstreamTLS{mode: pgserverless.TLSDisable})

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

	t.Run("header without a password is refused like Neon", func(t *testing.T) {
		for _, conn := range []string{"postgres://postgres@elsewhere.example.test/app", "postgres://postgres:@elsewhere.example.test/app"} {
			req := httptest.NewRequest(http.MethodPost, "/sql", nil)
			req.Header.Set("Connection-String", conn)
			if _, err := admit(req); !errors.Is(err, pgserverless.ErrMissingPassword) {
				t.Errorf("%s: got %v, want ErrMissingPassword", conn, err)
			}
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
	up, err := wsAdmission(db, upstreamTLS{mode: pgserverless.TLSRequire})(req)
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

// A typo for 5432 used to start and dial port 18912 on POST /sql.
func TestRunRefusesADatabaseURLPortPastSixteenBits(t *testing.T) {
	t.Setenv("PGSERVERLESS_ADDR", "127.0.0.1:0")
	t.Setenv("DATABASE_URL", "postgres://u:p@db:543200/app")
	if err := run(); err == nil || !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Errorf("run() = %v, want the DATABASE_URL error", err)
	}
}

func TestRunRefusesRemoteListener(t *testing.T) {
	t.Setenv("PGSERVERLESS_ADDR", "0.0.0.0:0")
	t.Setenv("PGSERVERLESS_ALLOW_REMOTE", "")
	t.Setenv("PGSERVERLESS_ALLOW_UNAUTHENTICATED", "")
	t.Setenv("PGSERVERLESS_AUTH_TOKEN", "")
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

func TestBearerMatches(t *testing.T) {
	for header, want := range map[string]bool{
		"Bearer s3cret":    true,
		"bearer s3cret":    true,
		"BEARER\ts3cret ":  true,
		"Bearer   s3cret":  true,
		"Bearer s3cre":     false,
		"Bearer s3cretx":   false,
		"Bearer":           false,
		"Bearer ":          false,
		"Bearers3cret":     false,
		"Basic s3cret":     false,
		"":                 false,
		"Bearer s3 cret":   false,
		" Bearer s3cret":   false,
		"Bearer s3cret\tx": false,
	} {
		if got := bearerMatches(header, "s3cret"); got != want {
			t.Errorf("bearerMatches(%q) = %v, want %v", header, got, want)
		}
	}
}

func TestAuthTokenGuardsBothTransports(t *testing.T) {
	var reached []string
	inner := func(r *http.Request) (pgserverless.Upstream, error) {
		reached = append(reached, r.URL.Path)
		// A status no other path answers, so the test sees the token check passed without dialing a database.
		return pgserverless.Upstream{}, &pgserverless.AdmissionError{Status: http.StatusTeapot, Message: "admitted"}
	}
	srv := httptest.NewServer(newMux(requireToken("s3cret", inner), requireToken("s3cret", inner), []string{"*"}))
	defer srv.Close()

	do := func(method, path, auth string) int {
		t.Helper()
		req, err := http.NewRequest(method, srv.URL+path, strings.NewReader(`{"query":"SELECT 1","params":[]}`))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Connection-String", "postgres://u:p@db/app")
		if auth != "" {
			req.Header.Set("Authorization", auth)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	for _, path := range []string{"/sql", "/v2"} {
		method := http.MethodPost
		if path == "/v2" {
			method = http.MethodGet
		}
		for auth, want := range map[string]int{
			"":              http.StatusUnauthorized,
			"Bearer wrong":  http.StatusUnauthorized,
			"Basic s3cret":  http.StatusUnauthorized,
			"Bearer s3cret": http.StatusTeapot,
		} {
			if got := do(method, path, auth); got != want {
				t.Errorf("%s %s with %q: status %d, want %d", method, path, auth, got, want)
			}
		}
	}
	if strings.Join(reached, ",") != "/sql,/v2" {
		t.Errorf("admission reached for %q, want only the two requests with the token", reached)
	}

	// A browser preflight carries no Authorization, so it must not need the token.
	if got := do(http.MethodOptions, "/sql", ""); got != http.StatusOK {
		t.Errorf("OPTIONS /sql: status %d, want 200", got)
	}
	if got := do(http.MethodGet, "/health", ""); got != http.StatusOK {
		t.Errorf("GET /health: status %d, want 200", got)
	}
}

func TestNoAuthTokenAdmitsWithoutAuthorization(t *testing.T) {
	called := false
	admit := requireToken("", func(*http.Request) (pgserverless.Upstream, error) {
		called = true
		return pgserverless.Upstream{}, nil
	})
	if _, err := admit(httptest.NewRequest(http.MethodPost, "/sql", nil)); err != nil || !called {
		t.Errorf("got %v, called %v, want the inner admission to run", err, called)
	}
}

func TestRunAcceptsRemoteListenerWithAuthToken(t *testing.T) {
	t.Setenv("PGSERVERLESS_ADDR", "0.0.0.0:0")
	t.Setenv("PGSERVERLESS_ALLOW_REMOTE", "")
	t.Setenv("PGSERVERLESS_AUTH_TOKEN", "s3cret")
	// An invalid DATABASE_URL stops run right after the listener check, so nothing binds.
	t.Setenv("DATABASE_URL", "postgres://u:p@db:bad/app")
	if err := run(); err == nil || strings.Contains(err.Error(), "refusing") {
		t.Errorf("run() = %v, want the DATABASE_URL error, not a refusal", err)
	}
}

func TestRunAcceptsRemoteListenerWithEitherAllowSetting(t *testing.T) {
	for _, name := range []string{"PGSERVERLESS_ALLOW_REMOTE", "PGSERVERLESS_ALLOW_UNAUTHENTICATED"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv("PGSERVERLESS_ADDR", "0.0.0.0:0")
			t.Setenv("PGSERVERLESS_AUTH_TOKEN", "")
			t.Setenv("PGSERVERLESS_ALLOW_REMOTE", "")
			t.Setenv("PGSERVERLESS_ALLOW_UNAUTHENTICATED", "")
			t.Setenv(name, "true")
			t.Setenv("DATABASE_URL", "postgres://u:p@db:bad/app")
			if err := run(); err == nil || strings.Contains(err.Error(), "refusing") {
				t.Errorf("run() = %v, want the DATABASE_URL error, not a refusal", err)
			}
		})
	}
}

func TestLogLevel(t *testing.T) {
	for in, want := range map[string]slog.Level{"": slog.LevelInfo, "info": slog.LevelInfo, "DEBUG": slog.LevelDebug, " warn ": slog.LevelWarn, "error": slog.LevelError} {
		if got, err := logLevel(in); err != nil || got != want {
			t.Errorf("logLevel(%q) = %v, %v, want %v", in, got, err, want)
		}
	}
	if _, err := logLevel("verbose"); err == nil {
		t.Error(`logLevel("verbose") = nil error, want a refusal`)
	}
}

func TestRunRefusesAnUnknownLogLevel(t *testing.T) {
	t.Setenv("PGSERVERLESS_LOG_LEVEL", "verbose")
	if err := run(); err == nil || !strings.Contains(err.Error(), "PGSERVERLESS_LOG_LEVEL") {
		t.Errorf("run() = %v, want the log level refused", err)
	}
}

func TestLogHandler(t *testing.T) {
	for _, tc := range []struct{ format, want string }{
		{"", "level=INFO msg=hello n=1\n"},
		{"text", "level=INFO msg=hello n=1\n"},
		{" JSON ", `{"level":"INFO","msg":"hello","n":1}` + "\n"},
	} {
		var buf bytes.Buffer
		h, err := logHandler(tc.format, &buf, slog.LevelInfo)
		if err != nil {
			t.Fatalf("logHandler(%q) error: %v", tc.format, err)
		}
		slog.New(dropTime{h}).Info("hello", "n", 1)
		if got := buf.String(); got != tc.want {
			t.Errorf("logHandler(%q) wrote %q, want %q", tc.format, got, tc.want)
		}
	}
	if _, err := logHandler("logfmt", io.Discard, slog.LevelInfo); err == nil || !strings.Contains(err.Error(), "text or json") {
		t.Errorf(`logHandler("logfmt") = %v, want a refusal naming text and json`, err)
	}
}

func TestLogHandlerKeepsTheLevel(t *testing.T) {
	var buf bytes.Buffer
	h, err := logHandler("json", &buf, slog.LevelWarn)
	if err != nil {
		t.Fatal(err)
	}
	slog.New(h).Info("dropped")
	if buf.Len() != 0 {
		t.Errorf("json handler at warn wrote an info record: %q", buf.String())
	}
}

func TestRunRefusesAnUnknownLogFormat(t *testing.T) {
	t.Setenv("PGSERVERLESS_LOG_FORMAT", "logfmt")
	if err := run(); err == nil || !strings.Contains(err.Error(), "PGSERVERLESS_LOG_FORMAT") {
		t.Errorf("run() = %v, want the log format refused", err)
	}
}

// dropTime zeroes each record's time so the handlers leave the time attribute out.
type dropTime struct{ slog.Handler }

func (d dropTime) Handle(ctx context.Context, r slog.Record) error {
	r.Time = time.Time{}
	return d.Handler.Handle(ctx, r)
}

func TestIsLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"":                      true,
		"localhost":             true,
		"LOCALHOST:4000":        true,
		"app.localhost:4000":    true,
		"localhost.:4000":       true,
		"127.0.0.1:4000":        true,
		"127.1.2.3":             true,
		"[::1]:4000":            true,
		"[::1]":                 true,
		"attacker.example:4000": false,
		"localhost.example":     false,
		"10.0.0.5:4000":         false,
		"0.0.0.0:4000":          false,
	} {
		if got := isLoopbackHost(host); got != want {
			t.Errorf("isLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// guardedStatus runs one request through guardBrowsers and returns the status admission answered.
func guardedStatus(t *testing.T, loopback bool, origins []string, ws bool, host, origin string) int {
	t.Helper()
	admitted := func(*http.Request) (pgserverless.Upstream, error) {
		return pgserverless.Upstream{}, &pgserverless.AdmissionError{Status: http.StatusTeapot, Message: "admitted"}
	}
	admitHTTP, admitWS := guardBrowsers(loopback, origins, admitted, admitted)
	admit, method, path := admitHTTP, http.MethodPost, "/sql"
	if ws {
		admit, method, path = admitWS, http.MethodGet, "/v2"
	}
	req := httptest.NewRequest(method, "http://"+host+path, nil)
	if origin != "" {
		req.Header.Set("Origin", origin)
	}
	_, err := admit(req)
	var aerr *pgserverless.AdmissionError
	if !errors.As(err, &aerr) {
		t.Fatalf("admission returned %v, want an AdmissionError", err)
	}
	return aerr.Status
}

func TestTokenlessWebSocketRefusesForeignOrigins(t *testing.T) {
	for _, tc := range []struct {
		name    string
		origins []string
		origin  string
		want    int
	}{
		{"no Origin, as from Node", nil, "", http.StatusTeapot},
		{"another site", nil, "https://attacker.example", http.StatusForbidden},
		{"opaque origin", nil, "null", http.StatusForbidden},
		{"a dev page on another port", nil, "http://localhost:3000", http.StatusForbidden},
		{"its own origin", nil, "http://localhost:4000", http.StatusTeapot},
		{"a listed origin", []string{"http://localhost:3000"}, "http://localhost:3000", http.StatusTeapot},
		{"any origin under *", []string{"*"}, "https://attacker.example", http.StatusTeapot},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := guardedStatus(t, true, tc.origins, true, "localhost:4000", tc.origin); got != tc.want {
				t.Errorf("status %d, want %d", got, tc.want)
			}
		})
	}
	// POST /sql is left to CORS, which already keeps a foreign page from sending Connection-String.
	if got := guardedStatus(t, true, nil, false, "localhost:4000", "https://attacker.example"); got != http.StatusTeapot {
		t.Errorf("POST /sql with a foreign Origin: status %d, want it left to CORS", got)
	}
}

func TestTokenlessLoopbackListenerRefusesARebindingHost(t *testing.T) {
	for _, ws := range []bool{false, true} {
		if got := guardedStatus(t, true, nil, ws, "attacker.example:4000", ""); got != http.StatusForbidden {
			t.Errorf("ws=%v: rebinding Host answered %d, want 403", ws, got)
		}
		if got := guardedStatus(t, true, nil, ws, "127.0.0.1:4000", ""); got != http.StatusTeapot {
			t.Errorf("ws=%v: loopback Host answered %d, want it admitted", ws, got)
		}
		// Beyond loopback the server's own name is unknown, so PGSERVERLESS_ALLOW_REMOTE leaves Host to the network.
		if got := guardedStatus(t, false, nil, ws, "pgserverless.internal:4000", ""); got != http.StatusTeapot {
			t.Errorf("ws=%v: remote listener Host answered %d, want it admitted", ws, got)
		}
	}
}

func TestTokenlessGuardRefusesBeforeTheUpgrade(t *testing.T) {
	inner := func(*http.Request) (pgserverless.Upstream, error) {
		return pgserverless.Upstream{}, &pgserverless.AdmissionError{Status: http.StatusTeapot, Message: "admitted"}
	}
	admitHTTP, admitWS := guardBrowsers(true, nil, inner, inner)
	srv := httptest.NewServer(newMux(admitHTTP, admitWS, nil))
	defer srv.Close()

	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v2", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Connection", "Upgrade")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Sec-WebSocket-Version", "13")
	req.Header.Set("Sec-WebSocket-Key", "dGhlIHNhbXBsZSBub25jZQ==")
	req.Header.Set("Origin", "https://attacker.example")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("cross-origin upgrade: status %d, want 403 and no 101", resp.StatusCode)
	}

	// GET /health runs no admission and touches no database, so it answers any Host.
	req, err = http.NewRequest(http.MethodGet, srv.URL+"/health", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "attacker.example"
	resp, err = http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /health: status %d, want 200", resp.StatusCode)
	}
}
