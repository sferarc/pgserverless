package pgserverless

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/sferarc/pgserverless/pgfake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// libpqEnv is every variable pgx.ParseConfig reads from the process environment.
var libpqEnv = []string{
	"PGHOST", "PGPORT", "PGDATABASE", "PGUSER", "PGPASSWORD", "PGPASSFILE", "PGAPPNAME",
	"PGCONNECT_TIMEOUT", "PGSSLMODE", "PGSSLKEY", "PGSSLCERT", "PGSSLSNI", "PGSSLROOTCERT",
	"PGSSLPASSWORD", "PGSSLNEGOTIATION", "PGTARGETSESSIONATTRS", "PGSERVICE", "PGSERVICEFILE",
	"PGTZ", "PGOPTIONS", "PGMINPROTOCOLVERSION", "PGMAXPROTOCOLVERSION", "PGCHANNELBINDING",
	"PGREQUIREAUTH",
}

// clearLibpqEnv unsets the libpq variables for the test; ParseConfig skips an empty one.
func clearLibpqEnv(t *testing.T) {
	t.Helper()
	for _, name := range libpqEnv {
		t.Setenv(name, "")
	}
}

func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := ln.Addr().String()
	require.NoError(t, ln.Close())
	return addr
}

// An operator's PGHOST used to become a fallback the dial tried once the real upstream failed.
func TestSQLNeverFallsBackToTheServersLibpqHost(t *testing.T) {
	clearLibpqEnv(t)
	other := pgfake.New(t)
	other.SetResult("SELECT 1", oneInt(1))
	host, port, err := net.SplitHostPort(other.Addr())
	require.NoError(t, err)
	t.Setenv("PGHOST", host+","+host)
	t.Setenv("PGPORT", port)
	t.Setenv("PGSSLMODE", "require")

	h := NewHTTPHandler(StaticHTTPUpstream(closedAddr(t)))
	req := httptest.NewRequest(http.MethodPost, "/sql", strings.NewReader(`{"query":"SELECT 1"}`))
	req.Header.Set("Neon-Connection-String", "postgresql://u:p@"+conformanceHost+"/db")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusInternalServerError, rec.Code, rec.Body.String())
	assert.Empty(t, other.StartupParam("user"), "the query reached the server PGHOST names")
}

func TestDialSendsNoStartupParameterFromTheServersEnvironment(t *testing.T) {
	clearLibpqEnv(t)
	t.Setenv("PGTZ", "America/New_York")
	t.Setenv("PGOPTIONS", "-c search_path=other")
	t.Setenv("PGAPPNAME", "from-env")
	pg := pgfake.New(t)

	conn, err := Dial(context.Background(), Upstream{Addr: pg.Addr(), User: "u", Database: "db", InsecureSkipVerify: true}, false)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(context.Background()) })

	assert.Empty(t, pg.StartupParam("timezone"))
	assert.Empty(t, pg.StartupParam("options"))
	assert.Empty(t, pg.StartupParam("application_name"))
}

func TestUpstreamConfigIgnoresTheServersLibpqEnvironment(t *testing.T) {
	clearLibpqEnv(t)
	up := Upstream{Addr: "db.example.test:5432", User: "u", Database: "db"}
	// pgx's own defaults, so the values upstreamConfig resets to cannot drift from them.
	want, err := pgx.ParseConfig("")
	require.NoError(t, err)

	t.Setenv("PGSSLMODE", "require")
	t.Setenv("PGSSLNEGOTIATION", "direct")
	t.Setenv("PGTARGETSESSIONATTRS", "standby")
	t.Setenv("PGCONNECT_TIMEOUT", "7")
	t.Setenv("PGCHANNELBINDING", "require")
	t.Setenv("PGREQUIREAUTH", "password")
	t.Setenv("PGMINPROTOCOLVERSION", "3.2")
	got, err := upstreamConfig(up, 0)
	require.NoError(t, err)

	assert.Nil(t, got.ValidateConnect)
	assert.Equal(t, want.ConnectTimeout, got.ConnectTimeout)
	assert.Equal(t, want.SSLNegotiation, got.SSLNegotiation)
	assert.Equal(t, want.ChannelBinding, got.ChannelBinding)
	assert.Equal(t, want.RequireAuth, got.RequireAuth)
	assert.Equal(t, want.MinProtocolVersion, got.MinProtocolVersion)
	assert.Equal(t, want.MaxProtocolVersion, got.MaxProtocolVersion)
	assert.Empty(t, got.Fallbacks)
}
