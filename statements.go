package pgserverless

import (
	"errors"

	"github.com/jackc/pgx/v5/pgconn"
)

// errMultipleCommands is what PostgreSQL answers when asked to prepare more
// than one statement, and so what Neon returns for such a query.
var errMultipleCommands = &pgconn.PgError{
	Severity: "ERROR",
	Code:     "42601",
	Message:  "cannot insert multiple commands into a prepared statement",
}

// errEmptyBatchItem refuses a batch item with no statement, which a joined
// simple query would skip, shifting every later result onto the wrong item.
var errEmptyBatchItem = &pgconn.PgError{
	Severity: "ERROR",
	Code:     "42601",
	Message:  "empty query in batch",
}

// checkSingleStatement refuses a query a simple Query message would run as
// several statements, as the extended protocol Neon uses would.
func checkSingleStatement(count func(string) (int, error), sql string) error {
	n, err := countStatements(count, sql)
	if err != nil {
		return err
	}
	if n > 1 {
		return errMultipleCommands
	}
	return nil
}

// checkBatchItem refuses a batch item that is not one complete statement, so
// that joining the items with ';' into one simple query keeps their boundaries.
func checkBatchItem(count func(string) (int, error), sql string) error {
	n, err := countStatements(count, sql)
	switch {
	case err != nil:
		return err
	case n == 0:
		return errEmptyBatchItem
	case n > 1:
		return errMultipleCommands
	}
	return nil
}

// countStatements refuses sql that count cannot parse rather than sending it:
// a server whose grammar differs could run statements count never saw.
func countStatements(count func(string) (int, error), sql string) (int, error) {
	n, err := count(sql)
	if err == nil {
		return n, nil
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
		return 0, pgErr
	}
	return 0, &pgconn.PgError{Severity: "ERROR", Code: "42601", Message: err.Error()}
}
