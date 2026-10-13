package pgserverless

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v4"
)

// Sentinel errors for request validation.
var (
	ErrInvalidRequest          = errors.New("invalid request body")
	ErrMissingQuery            = errors.New("query is required")
	ErrMissingConnectionString = errors.New("Connection-String header is required")
	ErrInvalidConnectionString = errors.New("invalid connection string")
	ErrResponseTooLarge        = errors.New("response too large")
)

var paramCountMismatch = regexp.MustCompile(`^(expected \d+ arguments, got \d+|unused argument: \d+)$`)

// PgErrorResponse maps a PostgreSQL or connection error to the status and body
// the driver expects. It is pure, so a caller not using echo can still produce
// the same response the handler would.
func PgErrorResponse(err error) (int, ErrorResponse) {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		resp := ErrorResponse{
			Message:  pgErr.Message,
			Code:     &pgErr.Code,
			Severity: &pgErr.Severity,
		}
		// Optional fields are populated only when PostgreSQL sent them, so the
		// JSON omits them rather than carrying empty strings the driver would
		// surface as blank values on NeonDbError.
		for _, f := range []struct {
			src string
			dst **string
		}{
			{pgErr.Detail, &resp.Detail},
			{pgErr.Hint, &resp.Hint},
			{pgErr.Where, &resp.Where},
			{pgErr.SchemaName, &resp.SchemaName},
			{pgErr.TableName, &resp.TableName},
			{pgErr.ColumnName, &resp.ColumnName},
			{pgErr.DataTypeName, &resp.DataTypeName},
			{pgErr.ConstraintName, &resp.ConstraintName},
			{pgErr.InternalQuery, &resp.InternalQuery},
			{pgErr.File, &resp.File},
			{pgErr.Routine, &resp.Routine},
		} {
			if f.src != "" {
				v := f.src
				*f.dst = &v
			}
		}
		// The three numeric fields the client reads. They are int32 on the
		// PostgreSQL error and strings on the client's object, and zero means
		// absent rather than a real position, so they are converted here
		// rather than left unpopulated as they were.
		for _, f := range []struct {
			src int32
			dst **string
		}{
			{pgErr.Position, &resp.Position},
			{pgErr.InternalPosition, &resp.InternalPosition},
			{pgErr.Line, &resp.Line},
		} {
			if f.src != 0 {
				v := strconv.FormatInt(int64(f.src), 10)
				*f.dst = &v
			}
		}
		return classifyPgErrorStatus(pgErr.Code), resp
	}

	// pgx rejects a parameter count that disagrees with the statement before
	// PostgreSQL sees it, with a different message in each query mode. PostgreSQL's own answer is a protocol violation, and
	// the driver only reads the code from a 400.
	if paramCountMismatch.MatchString(err.Error()) {
		code, severity := "08P01", "ERROR"
		return http.StatusBadRequest, ErrorResponse{Message: err.Error(), Code: &code, Severity: &severity}
	}

	// Non-PG errors are classified by message pattern and sanitized, because
	// the underlying text can carry a connection string.
	status, msg := classifyError(err)
	return status, ErrorResponse{Message: SanitizeError(msg)}
}

// HandlePgError writes the response PgErrorResponse produces.
func HandlePgError(c echo.Context, err error) error {
	status, resp := PgErrorResponse(err)
	return c.JSON(status, resp)
}

// classifyPgErrorStatus maps a SQLSTATE code prefix to an HTTP status.
func classifyPgErrorStatus(code string) int {
	switch {
	case strings.HasPrefix(code, "28"): // authentication
		return 401
	case strings.HasPrefix(code, "42"): // syntax/access
		return 400
	case strings.HasPrefix(code, "22"): // data exception
		return 400
	case strings.HasPrefix(code, "23"): // integrity constraint
		return 400
	case strings.HasPrefix(code, "53"): // insufficient resources
		return 503
	case strings.HasPrefix(code, "57"): // operator intervention
		return 503
	case code == "08P01": // protocol violation, caused by the request
		return 400
	case strings.HasPrefix(code, "08"): // connection exception
		return 504
	default:
		return 400
	}
}

// classifyError determines the HTTP status and message for non-PG errors.
func classifyError(err error) (int, string) {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "connection refused"),
		strings.Contains(msg, "no such host"),
		strings.Contains(msg, "connection reset"):
		return 504, "upstream connection failed"
	case strings.Contains(msg, "context deadline exceeded"),
		strings.Contains(msg, "timeout"):
		return 504, "upstream connection timed out"
	case strings.Contains(msg, "password authentication failed"):
		return 401, "authentication failed"
	case errors.Is(err, ErrResponseTooLarge):
		return 507, msg
	default:
		return 500, "internal server error"
	}
}

// SanitizeError removes credentials from error messages.
func SanitizeError(msg string) string {
	if strings.Contains(msg, "postgresql://") || strings.Contains(msg, "postgres://") {
		// Strip connection string entirely
		return "upstream connection failed"
	}
	if strings.Contains(msg, "password") && strings.Contains(msg, "@") {
		return "upstream connection failed"
	}
	return msg
}

// ParseConnectionString extracts host, user, password, database from a postgres:// URL.
func ParseConnectionString(s string) (*ConnInfo, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ErrMissingConnectionString
	}

	// Normalize scheme
	if !strings.HasPrefix(s, "postgres://") && !strings.HasPrefix(s, "postgresql://") {
		return nil, fmt.Errorf("%w: must start with postgres:// or postgresql://", ErrInvalidConnectionString)
	}

	u, err := url.Parse(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConnectionString, err)
	}

	password, _ := u.User.Password()
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "5432"
	}

	database := strings.TrimPrefix(u.Path, "/")
	if database == "" {
		database = u.User.Username()
	}

	if host == "" {
		return nil, fmt.Errorf("%w: missing host", ErrInvalidConnectionString)
	}
	if u.User.Username() == "" {
		return nil, fmt.Errorf("%w: missing user", ErrInvalidConnectionString)
	}

	return &ConnInfo{
		Host:     host,
		Port:     port,
		User:     u.User.Username(),
		Password: password,
		Database: database,
	}, nil
}
