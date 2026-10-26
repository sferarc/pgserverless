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
	for _, tc := range []struct {
		name       string
		n          int
		err        error
		wantSingle string
		wantItem   string
	}{
		{name: "one statement", n: 1},
		{name: "no statement", n: 0, wantItem: "42601: empty query in batch"},
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
			assertPgError(t, tc.wantSingle, checkSingleStatement(count, "q"))
			assertPgError(t, tc.wantItem, checkBatchItem(count, "q"))
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
