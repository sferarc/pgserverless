package pgserverless

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/sferarc/pgserverless/pgfake"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// net/url refused each of these outright, where Neon's proxy and libpq connect.
func TestParseConnectionStringReadsUserinfoLikeNeon(t *testing.T) {
	tests := []struct {
		input              string
		user, pass, dbname string
	}{
		{"postgres://u:50%off@h/db", "u", "50%off", "db"},
		{"postgres://u:100%@h/db", "u", "100%", "db"},
		{"postgres://u:pa^ss@h/db", "u", "pa^ss", "db"},
		{"postgres://u:p{w}|d@h/db", "u", "p{w}|d", "db"},
		{"postgres://u:pässw@h/db", "u", "pässw", "db"},
		{"postgres://u:p@ss@h/db", "u", "p@ss", "db"},
		{"postgres://u:a:b@h/db", "u", "a:b", "db"},
		{"postgres://u:p%41%2f@h/db", "u", "pA/", "db"},
		{"postgres://ops^team:pw@h/db", "ops^team", "pw", "db"},
		{"postgres://u:pw@h/50%off?sslmode=require", "u", "pw", "50%off"},
		{"postgres://u:pw@h/my%20db#frag", "u", "pw", "my db"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ci, err := ParseConnectionString(tt.input)
			require.NoError(t, err)
			assert.Equal(t, &ConnInfo{Host: "h", Port: "5432", User: tt.user, Password: tt.pass, Database: tt.dbname}, ci)
		})
	}
}

func TestParseConnectionStringRefusesAnEncodedNUL(t *testing.T) {
	for _, s := range []string{
		"postgres://u%00options:pw@h/db",
		"postgres://u:p%00w@h/db",
		"postgres://u:pw@h/d%00b",
	} {
		_, err := ParseConnectionString(s)
		assert.ErrorIs(t, err, ErrInvalidConnectionString, s)
	}
}

func TestParseConnectionStringErrorNeverQuotesThePassword(t *testing.T) {
	_, err := ParseConnectionString("postgres://u:s3cret@h:notaport/db")
	require.ErrorIs(t, err, ErrInvalidConnectionString)
	assert.NotContains(t, err.Error(), "s3cret")
}

// The driver forwards a password's bare '%' as written, and the request used to answer 400 invalid connection string.
func TestConformance_PasswordWithBarePercentConnects(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1", oneInt(1))
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT 1"}`, map[string]string{
		"Neon-Connection-String": "postgresql://ops^team:50%off@" + conformanceHost + "/app",
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var res QueryResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, "ops^team", pg.StartupParam("user"))
	assert.Equal(t, "app", pg.StartupParam("database"))
}

func TestParseRequestConnectionStringRequiresAPassword(t *testing.T) {
	for _, s := range []string{"postgres://u@h/db", "postgres://u:@h/db"} {
		_, err := ParseRequestConnectionString(s)
		assert.ErrorIs(t, err, ErrMissingPassword, s)
	}
	ci, err := ParseRequestConnectionString("postgres://u:pw@h/db")
	require.NoError(t, err)
	assert.Equal(t, "pw", ci.Password)
}

// Neon's proxy refuses this before connecting; dialed, a trust authenticated upstream ran the request as the role it named.
func TestConformance_ConnectionStringWithoutPasswordIsRefused(t *testing.T) {
	for _, conn := range []string{"postgresql://postgres@" + conformanceHost + "/db", "postgresql://postgres:@" + conformanceHost + "/db"} {
		t.Run(conn, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT 1", oneInt(1))
			e := newConformanceServer(t, pg)

			rec := doSQL(e, `{"query":"SELECT 1"}`, map[string]string{"Neon-Connection-String": conn})

			require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
			var res ErrorResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
			assert.Equal(t, ErrMissingPassword.Error(), res.Message)
			assert.Empty(t, pg.StartupParam("user"), "the upstream was dialed")
		})
	}
}

// Neon's proxy and libpq refuse a port past 65535; read as a uint16, a typo for 5432 dialed another port.
func TestParseConnectionStringRefusesAPortPastSixteenBits(t *testing.T) {
	for _, s := range []string{
		"postgres://u:pw@h:65536/db",
		"postgres://u:pw@h:543200/db",
		"postgres://u:pw@h:99999999999999999999/db",
	} {
		_, err := ParseConnectionString(s)
		assert.ErrorIs(t, err, ErrInvalidConnectionString, s)
	}
	for _, port := range []string{"1", "5432", "65535"} {
		ci, err := ParseConnectionString("postgres://u:pw@h:" + port + "/db")
		require.NoError(t, err, port)
		assert.Equal(t, port, ci.Port)
	}
}

func TestConformance_ConnectionStringWithAPortPastSixteenBitsIsRefused(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1", oneInt(1))
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT 1"}`, map[string]string{
		"Neon-Connection-String": "postgresql://u:p@" + conformanceHost + ":543200/db",
	})

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	var res ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Contains(t, res.Message, ErrInvalidConnectionString.Error())
	assert.Empty(t, pg.StartupParam("user"), "the upstream was dialed")
}

// The WebSocket path's net.Dial refuses these, and a uint16 cast on POST /sql dialed port 18912 for 543200.
func TestUpstreamConfigRefusesAnAddrWithAnInvalidPort(t *testing.T) {
	for _, addr := range []string{"db.example.test:65536", "db.example.test:543200", "db.example.test:"} {
		_, err := upstreamConfig(Upstream{Addr: addr, User: "u", Database: "db"}, 0)
		assert.Error(t, err, addr)
	}
	cfg, err := upstreamConfig(Upstream{Addr: "db.example.test:65535", User: "u", Database: "db"}, 0)
	require.NoError(t, err)
	assert.Equal(t, uint16(65535), cfg.Port)
}

// Neon's proxy parses the header with a URL parser, which lowercases the scheme and reads the database from the first path segment.
func TestParseRequestConnectionStringReadsSchemeAndDatabaseLikeNeon(t *testing.T) {
	tests := []struct{ input, dbname string }{
		{"POSTGRES://u:pw@h/db", "db"},
		{"PostgreSQL://u:pw@h/db", "db"},
		{"postgres://u:pw@h/db/extra", "db"},
		{"postgres://u:pw@h/db/?sslmode=require", "db"},
		{"postgres://u:pw@h/my%2Fdb/extra", "my/db"},
		{"postgres://u:pw@h/", "u"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			ci, err := ParseRequestConnectionString(tt.input)
			require.NoError(t, err)
			assert.Equal(t, &ConnInfo{Host: "h", Port: "5432", User: "u", Password: "pw", Database: tt.dbname}, ci)
		})
	}
	for _, s := range []string{"mysql://u:pw@h/db", "xpostgres://u:pw@h/db", "postgres:/u:pw@h/db"} {
		_, err := ParseRequestConnectionString(s)
		assert.ErrorIs(t, err, ErrInvalidConnectionString, s)
	}
}

// The url crate finds no path segments in a postgres:// URL with an empty path, which Neon's proxy refuses as missing database name.
func TestParseRequestConnectionStringRefusesNoDatabasePath(t *testing.T) {
	for _, s := range []string{
		"postgres://u:pw@h",
		"postgres://u:pw@h:5432",
		"postgres://u:pw@h?sslmode=require",
		"postgres://u:pw@h#frag",
		"postgres://u@h",
	} {
		_, err := ParseRequestConnectionString(s)
		assert.ErrorIs(t, err, ErrMissingDatabase, s)
	}
	_, err := ParseRequestConnectionString("postgres://u:pw@h:99999")
	assert.ErrorIs(t, err, ErrInvalidConnectionString, "a URL error comes first, as in Neon")

	ci, err := ParseConnectionString("postgres://u:pw@h")
	require.NoError(t, err)
	assert.Equal(t, "u", ci.Database, "DATABASE_URL keeps libpq's default")
}

func TestConformance_ConnectionStringWithoutADatabasePathIsRefused(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1", oneInt(1))
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT 1"}`, map[string]string{
		"Neon-Connection-String": "postgresql://u:pw@" + conformanceHost + "?sslmode=require",
	})

	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	var res ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &res))
	assert.Equal(t, ErrMissingDatabase.Error(), res.Message)
	assert.Empty(t, pg.StartupParam("user"), "the upstream was dialed")
}

// DATABASE_URL keeps libpq's reading, where the database is the whole path.
func TestParseConnectionStringKeepsTheWholePathAsTheDatabase(t *testing.T) {
	ci, err := ParseConnectionString("POSTGRES://u:pw@h/db/extra")
	require.NoError(t, err)
	assert.Equal(t, "db/extra", ci.Database)
}

func TestConformance_ConnectionStringSchemeAndDatabaseReadLikeNeon(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT 1", oneInt(1))
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT 1"}`, map[string]string{
		"Neon-Connection-String": "POSTGRESQL://u:pw@" + conformanceHost + "/app/extra",
	})

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, "u", pg.StartupParam("user"))
	assert.Equal(t, "app", pg.StartupParam("database"))
}
