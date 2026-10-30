package pgserverless

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// errMultipleCommands is what PostgreSQL answers when asked to prepare more
// than one statement, and so what Neon returns for such a query.
var errMultipleCommands = &pgconn.PgError{
	Severity: "ERROR",
	Code:     "42601",
	Message:  "cannot insert multiple commands into a prepared statement",
}

// bindCountError is what PostgreSQL answers when parameters are bound to a
// query with no statement, as Neon binds every query's parameters.
func bindCountError(params int) *pgconn.PgError {
	return &pgconn.PgError{
		Severity: "ERROR",
		Code:     "08P01",
		Message:  fmt.Sprintf(`bind message supplies %d parameters, but prepared statement "" requires 0`, params),
	}
}

// checkEmptyQueryParams refuses parameters for the empty string, which pgx
// sends as a simple Query that cannot carry them.
func checkEmptyQueryParams(sql string, params int) error {
	if sql == "" && params > 0 {
		return bindCountError(params)
	}
	return nil
}

// checkSingleStatement refuses a query a simple Query message would run as
// several statements, as the extended protocol Neon uses would.
func checkSingleStatement(count func(string) (int, error), sql string, params int) error {
	n, err := countStatements(count, sql)
	switch {
	case err != nil:
		return err
	case n == 0 && params > 0:
		return bindCountError(params)
	case n > 1:
		return errMultipleCommands
	}
	return nil
}

// checkBatchItem refuses a batch item that is more than one complete statement,
// so that joining the items with ';' into one simple query keeps their
// boundaries, and reports an item with none, which the join would drop.
func checkBatchItem(count func(string) (int, error), sql string, params int) (empty bool, err error) {
	n, err := countStatements(count, sql)
	switch {
	case err != nil:
		return false, err
	case n == 0 && params > 0:
		return false, bindCountError(params)
	case n > 1:
		return false, errMultipleCommands
	}
	return n == 0, nil
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
