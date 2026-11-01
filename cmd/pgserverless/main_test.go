package main

import (
	"crypto/x509"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
