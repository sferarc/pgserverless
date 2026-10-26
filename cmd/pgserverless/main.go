// Command pgserverless is the minimal reference server: it serves one
// PostgreSQL database over HTTP (POST /sql) and WebSocket (GET /v2).
//
// Configuration is two environment variables. DATABASE_URL names the database
// to serve (only its host, port and sslmode; clients bring their own
// credentials), and PGSERVERLESS_ADDR the listen address (default 127.0.0.1:4000).
// A listener beyond loopback also needs PGSERVERLESS_ALLOW_REMOTE=true, because
// this server has no authentication of its own.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/signal"
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
	if !isLoopback(addr) && os.Getenv("PGSERVERLESS_ALLOW_REMOTE") != "true" {
		return fmt.Errorf("refusing to listen on %s: set PGSERVERLESS_ALLOW_REMOTE=true if the network already restricts access", addr)
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

	tls := tlsMode(dbURL)
	mux := http.NewServeMux()
	mux.Handle("/", pgserverless.NewHTTPHandler(httpAdmission(db, tls), pgserverless.WithHTTPWebSocketAdmission(wsAdmission(db, tls))))
	mux.HandleFunc("GET /health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte("{\"status\":\"ok\"}\n"))
	})

	srv := &http.Server{Addr: addr, Handler: mux, ReadHeaderTimeout: 10 * time.Second}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() {
		slog.Info("pgserverless: listening", "addr", addr, "upstream", net.JoinHostPort(db.Host, db.Port))
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
func httpAdmission(db *pgserverless.ConnInfo, tls string) pgserverless.HTTPAdmission {
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

// wsAdmission only routes: on GET /v2 the client authenticates inside the tunnel.
func wsAdmission(db *pgserverless.ConnInfo, tls string) pgserverless.HTTPAdmission {
	return func(*http.Request) (pgserverless.Upstream, error) {
		return upstream(db, tls), nil
	}
}

func upstream(db *pgserverless.ConnInfo, tls string) pgserverless.Upstream {
	return pgserverless.Upstream{
		Addr:       net.JoinHostPort(db.Host, db.Port),
		ServerName: db.Host,
		TLSMode:    tls,
	}
}

// tlsMode negotiates TLS with the database unless DATABASE_URL says sslmode=disable.
func tlsMode(dbURL string) string {
	if u, err := url.Parse(dbURL); err == nil && u.Query().Get("sslmode") == "disable" {
		return pgserverless.TLSDisable
	}
	return pgserverless.TLSRequire
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
