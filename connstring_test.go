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
