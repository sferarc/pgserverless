// Command pgserverless is the minimal reference server: it serves one
// PostgreSQL database over HTTP (POST /sql) and WebSocket (GET /v2).
//
// Configuration is environment variables. DATABASE_URL names the database
// to serve (only its host, port, sslmode and sslrootcert; clients bring their own
// credentials), and PGSERVERLESS_ADDR the listen address (default 127.0.0.1:4000).
// PGSERVERLESS_AUTH_TOKEN, when set, must arrive as Authorization: Bearer on
// POST /sql and GET /v2. A listener beyond loopback needs that token or
// PGSERVERLESS_ALLOW_REMOTE=true, which says the network already restricts
// who reaches it. PGSERVERLESS_CORS_ORIGINS, a
// comma separated list or "*", lets browser pages on those origins call
// POST /sql; unset, no cross-origin page can read a response.
package main

import (
	"context"
	"crypto/subtle"
	"crypto/x509"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/sferarc/pgserverless"
)

const defaultAddr = "127.0.0.1:4000"

func main() {
	if err := run(); err != nil {
		slog.Error("pgserverless: exiting", "err", err)
		os.Exit(1)
	}
}

func run() error {
	addr := os.Getenv("PGSERVERLESS_ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	token := os.Getenv("PGSERVERLESS_AUTH_TOKEN")
	if !isLoopback(addr) && token == "" && os.Getenv("PGSERVERLESS_ALLOW_REMOTE") != "true" {
		return fmt.Errorf("refusing to listen on %s with no authentication: set PGSERVERLESS_AUTH_TOKEN, or PGSERVERLESS_ALLOW_REMOTE=true if the network already restricts access", addr)
	}

	dbURL := os.Getenv("DATABASE_URL")
	if dbURL == "" {
		return errors.New("DATABASE_URL is required")
	}
	db, err := pgserverless.ParseConnectionString(dbURL)
	if err != nil {
		// The parse error can quote the URL, password included, and it reaches the log.
		return errors.New("DATABASE_URL is not a valid postgres:// connection string")
	}

	tls := tlsFor(dbURL)
	if tls.roots, err = loadRootCAs(tls.rootCert); err != nil {
		return err
	}
	mux := newMux(
		requireToken(token, httpAdmission(db, tls)),
		requireToken(token, wsAdmission(db, tls)),
		splitOrigins(os.Getenv("PGSERVERLESS_CORS_ORIGINS")),
	)
	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("pgserverless: listening", "addr", addr, "upstream", net.JoinHostPort(db.Host, db.Port), "auth", token != "")
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdownCtx)
}

// httpAdmission sends every POST /sql to the database in DATABASE_URL as the
// user named in the request's own connection string. DATABASE_URL's
// credentials are never lent out: a request without the header is refused, so
// a cross-site form post to a loopback listener cannot run SQL.
func httpAdmission(db *pgserverless.ConnInfo, tls upstreamTLS) pgserverless.HTTPAdmission {
	return func(r *http.Request) (pgserverless.Upstream, error) {
		ci, err := pgserverless.ParseConnectionString(r.Header.Get("Connection-String"))
		if err != nil {
			return pgserverless.Upstream{}, err
		}
		up := upstream(db, tls)
		up.User, up.Password, up.Database = ci.User, ci.Password, ci.Database
		return up, nil
	}
}

func newMux(admitHTTP, admitWS pgserverless.HTTPAdmission, origins []string) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/", pgserverless.NewHTTPHandler(
		admitHTTP,
		pgserverless.WithHTTPWebSocketAdmission(admitWS),
		pgserverless.WithHTTPMaxResponseBytes(pgserverless.MaxResponseBytes),
		pgserverless.WithHTTPCORSOrigins(origins...),
	))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
	})
	return mux
}

// requireToken refuses a request whose Authorization header is not Bearer token before admit runs; an empty token admits every request.
func requireToken(token string, admit pgserverless.HTTPAdmission) pgserverless.HTTPAdmission {
	if token == "" {
		return admit
	}
	return func(r *http.Request) (pgserverless.Upstream, error) {
		if !bearerMatches(r.Header.Get("Authorization"), token) {
			return pgserverless.Upstream{}, &pgserverless.AdmissionError{Status: http.StatusUnauthorized, Message: "unauthorized"}
		}
		return admit(r)
	}
}

// bearerMatches reads the scheme case-insensitively (RFC 7235 section 2.1) and compares the credential in constant time.
func bearerMatches(header, token string) bool {
	i := strings.IndexAny(header, " \t")
	if i < 0 || !strings.EqualFold(header[:i], "Bearer") {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(strings.Trim(header[i:], " \t")), []byte(token)) == 1
}

// wsAdmission only routes: on GET /v2 the client authenticates inside the tunnel.
func wsAdmission(db *pgserverless.ConnInfo, tls upstreamTLS) pgserverless.HTTPAdmission {
	return func(*http.Request) (pgserverless.Upstream, error) {
		return upstream(db, tls), nil
	}
}

func upstream(db *pgserverless.ConnInfo, tls upstreamTLS) pgserverless.Upstream {
	return pgserverless.Upstream{
		Addr:               net.JoinHostPort(db.Host, db.Port),
		ServerName:         db.Host,
		TLSMode:            tls.mode,
		InsecureSkipVerify: tls.skipVerify,
		VerifyCAOnly:       tls.caOnly,
		RootCAs:            tls.roots,
	}
}

type upstreamTLS struct {
	mode       string
	skipVerify bool
	// caOnly checks the certificate's issuer but not its host name.
	caOnly bool
	// rootCert is sslrootcert as written; run loads it into roots.
	rootCert string
	roots    *x509.CertPool
}

// tlsFor reads DATABASE_URL's sslmode as libpq does: allow (read as prefer) and prefer fall back to the clear,
// below verify-ca the certificate is not checked unless sslrootcert is set, and with an sslrootcert file only verify-full checks the host name.
// No sslmode, verify-ca without sslrootcert (libpq fails without root.crt) and sslrootcert=system (libpq refuses it below verify-full) verify fully.
func tlsFor(dbURL string) upstreamTLS {
	// Not url.Parse: it refuses a password ParseConnectionString accepts (50%off), which dropped sslmode=disable.
	beforeFragment, _, _ := strings.Cut(dbURL, "#")
	_, query, _ := strings.Cut(beforeFragment, "?")
	q, _ := url.ParseQuery(query)
	rootCert := q.Get("sslrootcert")
	unverified := rootCert == ""
	// The name check is skipped only against a root file: without one, any publicly issued certificate would pass.
	caOnly := rootCert != "" && rootCert != "system"
	switch q.Get("sslmode") {
	case "disable":
		return upstreamTLS{mode: pgserverless.TLSDisable}
	case "allow", "prefer":
		return upstreamTLS{mode: pgserverless.TLSPrefer, skipVerify: unverified, caOnly: caOnly, rootCert: rootCert}
	case "require":
		return upstreamTLS{mode: pgserverless.TLSRequire, skipVerify: unverified, caOnly: caOnly, rootCert: rootCert}
	case "verify-ca":
		return upstreamTLS{mode: pgserverless.TLSRequire, caOnly: caOnly, rootCert: rootCert}
	}
	return upstreamTLS{mode: pgserverless.TLSRequire, rootCert: rootCert}
}

// loadRootCAs reads sslrootcert as libpq does: a file of PEM certificates to trust, or "system" for the system roots.
// A file that cannot be read stops the server, where libpq below verify-ca would skip the check.
func loadRootCAs(rootCert string) (*x509.CertPool, error) {
	if rootCert == "" || rootCert == "system" {
		return nil, nil
	}
	pemCerts, err := os.ReadFile(rootCert)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL sslrootcert: %w", err)
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(pemCerts) {
		return nil, fmt.Errorf("DATABASE_URL sslrootcert: %s holds no PEM certificate", rootCert)
	}
	return roots, nil
}

func splitOrigins(s string) []string {
	var out []string
	for o := range strings.SplitSeq(s, ",") {
		if o = strings.TrimSpace(o); o != "" {
			out = append(out, o)
		}
	}
	return out
}

// isLoopback reports whether addr binds only to the loopback interface.
func isLoopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil || host == "" {
		return false
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
