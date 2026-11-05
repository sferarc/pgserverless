// Command pgserverless is the minimal reference server: it serves one
// PostgreSQL database over HTTP (POST /sql) and WebSocket (GET /v2).
//
// Configuration is environment variables. DATABASE_URL names the database
// to serve (only its host, port, sslmode and sslrootcert; clients bring their own
// credentials), and PGSERVERLESS_ADDR the listen address (default 127.0.0.1:4000).
// PGSERVERLESS_AUTH_TOKEN, when set, must arrive as Authorization: Bearer on
// POST /sql and GET /v2. A listener beyond loopback needs that token or
// PGSERVERLESS_ALLOW_REMOTE=true, which says the network already restricts
// who reaches it (PGSERVERLESS_ALLOW_UNAUTHENTICATED=true is read the same way).
// PGSERVERLESS_CORS_ORIGINS, a comma separated list or "*", lets browser pages
// on those origins call POST /sql; unset, no cross-origin page can read a
// response. With no token, those origins are also the only foreign ones whose
// WebSocket upgrades are accepted, and a loopback listener refuses a Host
// that is not a loopback name. PGSERVERLESS_LOG_LEVEL is debug, info (the default), warn or error,
// and PGSERVERLESS_LOG_FORMAT is text (the default) or json.
package main

import (
	"context"
	"crypto/subtle"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
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
	level, err := logLevel(os.Getenv("PGSERVERLESS_LOG_LEVEL"))
	if err != nil {
		return err
	}
	handler, err := logHandler(os.Getenv("PGSERVERLESS_LOG_FORMAT"), os.Stderr, level)
	if err != nil {
		return err
	}
	slog.SetDefault(slog.New(handler))

	addr := os.Getenv("PGSERVERLESS_ADDR")
	if addr == "" {
		addr = defaultAddr
	}
	token := os.Getenv("PGSERVERLESS_AUTH_TOKEN")
	if !isLoopback(addr) && token == "" && !allowRemote() {
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
	origins := splitOrigins(os.Getenv("PGSERVERLESS_CORS_ORIGINS"))
	admitHTTP := requireToken(token, httpAdmission(db, tls))
	admitWS := requireToken(token, wsAdmission(db, tls))
	if token == "" {
		admitHTTP, admitWS = guardBrowsers(isLoopback(addr), origins, admitHTTP, admitWS)
	}
	tunnels := &pgserverless.Tunnels{}
	mux := newMux(admitHTTP, admitWS, origins, tunnels)
	requestCtx, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
		BaseContext:       func(net.Listener) context.Context { return requestCtx },
	}

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

	return shutdown(srv, cancelRequests, tunnels, drainTimeout, cancelTimeout)
}

// Docker sends SIGKILL 10 s after SIGTERM, so the drain and the cancel together end before it.
const (
	drainTimeout  = 7 * time.Second
	cancelTimeout = 3 * time.Second
)

// shutdown lets running requests and tunnel statements finish for drain, then cancels the rest so PostgreSQL gets a
// CancelRequest for each statement rather than leaving it running there, and waits cancelWait for their handlers to answer.
func shutdown(srv *http.Server, cancelRequests context.CancelFunc, tunnels *pgserverless.Tunnels, drain, cancelWait time.Duration) error {
	drainCtx, cancelDrain := context.WithTimeout(context.Background(), drain)
	defer cancelDrain()
	err := srv.Shutdown(drainCtx)
	if err == nil {
		err = waitForTunnelStatements(drainCtx, tunnels)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	slog.Warn("pgserverless: cancelling requests still running at shutdown", "drain", drain)
	waitCtx, cancelWaitCtx := context.WithTimeout(context.Background(), cancelWait)
	defer cancelWaitCtx()
	// Before cancelRequests, which ends the tunnels and with them the record of what each was running.
	tunnels.CancelRunning(waitCtx)
	cancelRequests()
	return srv.Shutdown(waitCtx)
}

// waitForTunnelStatements returns once no tunnel has a statement in flight, polling as http.Server.Shutdown does.
func waitForTunnelStatements(ctx context.Context, tunnels *pgserverless.Tunnels) error {
	tick := time.NewTicker(50 * time.Millisecond)
	defer tick.Stop()
	for tunnels.Running() > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-tick.C:
		}
	}
	return nil
}

// httpAdmission sends every POST /sql to the database in DATABASE_URL as the
// user named in the request's own connection string. DATABASE_URL's
// credentials are never lent out: a request without the header is refused, so
// a cross-site form post to a loopback listener cannot run SQL.
func httpAdmission(db *pgserverless.ConnInfo, tls upstreamTLS) pgserverless.HTTPAdmission {
	return func(r *http.Request) (pgserverless.Upstream, error) {
		ci, err := pgserverless.ParseRequestConnectionString(r.Header.Get("Connection-String"))
		if err != nil {
			return pgserverless.Upstream{}, err
		}
		up := upstream(db, tls)
		up.User, up.Password, up.Database = ci.User, ci.Password, ci.Database
		return up, nil
	}
}

func newMux(admitHTTP, admitWS pgserverless.HTTPAdmission, origins []string, tunnels *pgserverless.Tunnels) *http.ServeMux {
	mux := http.NewServeMux()
	mux.Handle("/", pgserverless.NewHTTPHandler(
		admitHTTP,
		pgserverless.WithHTTPWebSocketAdmission(admitWS),
		pgserverless.WithHTTPMaxResponseBytes(pgserverless.MaxResponseBytes),
		pgserverless.WithHTTPCORSOrigins(origins...),
		pgserverless.WithHTTPTunnels(tunnels),
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

// guardBrowsers keeps web pages off a tokenless server: browsers apply no CORS to a WebSocket, and a DNS rebinding
// name makes a page same-origin with a loopback listener, so neither Origin nor Host would otherwise be checked.
func guardBrowsers(loopback bool, origins []string, admitHTTP, admitWS pgserverless.HTTPAdmission) (pgserverless.HTTPAdmission, pgserverless.HTTPAdmission) {
	admitWS = requireAllowedOrigin(origins, admitWS)
	if loopback {
		admitHTTP, admitWS = requireLoopbackHost(admitHTTP), requireLoopbackHost(admitWS)
	}
	return admitHTTP, admitWS
}

// requireAllowedOrigin admits a request with no Origin (not a browser), its own origin, or one origins lists.
func requireAllowedOrigin(origins []string, admit pgserverless.HTTPAdmission) pgserverless.HTTPAdmission {
	return func(r *http.Request) (pgserverless.Upstream, error) {
		origin := r.Header.Get("Origin")
		if origin != "" && !slices.Contains(origins, "*") && !slices.Contains(origins, origin) && !sameOrigin(origin, r.Host) {
			return pgserverless.Upstream{}, &pgserverless.AdmissionError{Status: http.StatusForbidden, Message: "origin not allowed: list it in PGSERVERLESS_CORS_ORIGINS"}
		}
		return admit(r)
	}
}

func sameOrigin(origin, host string) bool {
	u, err := url.Parse(origin)
	return err == nil && u.Host != "" && strings.EqualFold(u.Host, host)
}

// requireLoopbackHost refuses a Host that is not a loopback name, which on a loopback listener only DNS rebinding sends.
func requireLoopbackHost(admit pgserverless.HTTPAdmission) pgserverless.HTTPAdmission {
	return func(r *http.Request) (pgserverless.Upstream, error) {
		if !isLoopbackHost(r.Host) {
			return pgserverless.Upstream{}, &pgserverless.AdmissionError{Status: http.StatusForbidden, Message: "host is not a loopback name: set PGSERVERLESS_AUTH_TOKEN to serve other names"}
		}
		return admit(r)
	}
}

// isLoopbackHost reads a Host header; an empty one is allowed since every browser sends it.
func isLoopbackHost(host string) bool {
	if host == "" {
		return true
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	host = strings.ToLower(strings.TrimSuffix(strings.TrimSuffix(strings.TrimPrefix(host, "["), "]"), "."))
	// Browsers resolve localhost and its subdomains to loopback themselves (RFC 6761), so DNS cannot rebind them.
	if host == "localhost" || strings.HasSuffix(host, ".localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
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

// allowRemote also reads PGSERVERLESS_ALLOW_UNAUTHENTICATED, the older spelling, so deployments that set it keep starting.
func allowRemote() bool {
	return os.Getenv("PGSERVERLESS_ALLOW_REMOTE") == "true" || os.Getenv("PGSERVERLESS_ALLOW_UNAUTHENTICATED") == "true"
}

func logLevel(s string) (slog.Level, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("PGSERVERLESS_LOG_LEVEL %q is not debug, info, warn or error", s)
}

func logHandler(format string, w io.Writer, level slog.Level) (slog.Handler, error) {
	opts := &slog.HandlerOptions{Level: level}
	switch strings.ToLower(strings.TrimSpace(format)) {
	case "", "text":
		return slog.NewTextHandler(w, opts), nil
	case "json":
		return slog.NewJSONHandler(w, opts), nil
	}
	return nil, fmt.Errorf("PGSERVERLESS_LOG_FORMAT %q is not text or json", format)
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
