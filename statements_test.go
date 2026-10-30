package pgserverless

import (
	"errors"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCheckStatements(t *testing.T) {
	multiple := "42601: cannot insert multiple commands into a prepared statement"
	bind := `08P01: bind message supplies 1 parameters, but prepared statement "" requires 0`
	for _, tc := range []struct {
		name       string
		n          int
		params     int
		err        error
		wantSingle string
		wantItem   string
		wantEmpty  bool
	}{
		{name: "one statement", n: 1},
		{name: "one statement with params", n: 1, params: 1},
		{name: "no statement", n: 0, wantEmpty: true},
		{name: "no statement with params", n: 0, params: 1, wantSingle: bind, wantItem: bind},
		{name: "two statements", n: 2, wantSingle: multiple, wantItem: multiple},
		{
			name:       "parse error is refused, not sent",
			err:        errors.New(`syntax error at or near "("`),
			wantSingle: `42601: syntax error at or near "("`,
			wantItem:   `42601: syntax error at or near "("`,
		},
		{
			name:       "PostgreSQL error kept",
			err:        &pgconn.PgError{Code: "22021", Message: "invalid byte sequence"},
			wantSingle: "22021: invalid byte sequence",
			wantItem:   "22021: invalid byte sequence",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			count := func(string) (int, error) { return tc.n, tc.err }
			assertPgError(t, tc.wantSingle, checkSingleStatement(count, "q", tc.params))
			empty, err := checkBatchItem(count, "q", tc.params)
			assertPgError(t, tc.wantItem, err)
			assert.Equal(t, tc.wantEmpty, empty)
		})
	}
}

// assertPgError checks err against "code: message", or against nil for "".
func assertPgError(t *testing.T, want string, err error) {
	t.Helper()
	if want == "" {
		assert.NoError(t, err)
		return
	}
	var pgErr *pgconn.PgError
	require.ErrorAs(t, err, &pgErr)
	assert.Equal(t, want, pgErr.Code+": "+pgErr.Message)
}

func TestCheckEmptyQueryParams(t *testing.T) {
	assert.NoError(t, checkEmptyQueryParams("", 0))
	assert.NoError(t, checkEmptyQueryParams("SELECT $1", 1))
	assertPgError(t, `08P01: bind message supplies 2 parameters, but prepared statement "" requires 0`, checkEmptyQueryParams("", 2))
}
