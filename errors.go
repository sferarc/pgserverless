package pgserverless

import (
	"context"
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
	ErrResponseTooLarge        = errors.New("response is too large")
	ErrRequestTooLarge         = errors.New("request is too large")
	// ErrQueryCancelled marks a statement stopped because its request ended, by the query timeout or the client going away.
	ErrQueryCancelled = errors.New("query was cancelled")
)

// queryCancelledMessage is what Neon's proxy answers for a cancelled statement, with code 08P01 and status 400.
const queryCancelledMessage = "Query cancelled, connection was terminated"

var keywordPassword = regexp.MustCompile(`(?i)(^|[\s,;:(\[{\x60'"])(ssl)?password\s*=`)

var paramCountMismatch = regexp.MustCompile(`^(expected \d+ arguments, got \d+|unused argument: \d+)$`)

// PgErrorResponse maps a PostgreSQL or connection error to the status and body
// the driver expects. It is pure, so a caller not using echo can still produce
// the same response the handler would.
func PgErrorResponse(err error) (int, ErrorResponse) {
	if errors.Is(err, ErrQueryCancelled) {
		code := "08P01"
		return http.StatusBadRequest, ErrorResponse{Message: queryCancelledMessage, Code: &code}
	}
	if pgErr, ok := errors.AsType[*pgconn.PgError](err); ok {
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
	writePgError(c.Response(), err)
	return nil
}

func writePgError(w http.ResponseWriter, err error) {
	status, resp := PgErrorResponse(err)
	writeJSON(w, status, resp)
}

// writeQueryError is writePgError for a statement run under ctx, which answers as Neon does once ctx has ended.
func writeQueryError(ctx context.Context, w http.ResponseWriter, err error) {
	if ctx.Err() != nil {
		err = fmt.Errorf("%w: %w", ErrQueryCancelled, err)
	}
	status, resp := PgErrorResponse(err)
	// Neon answers any error PostgreSQL raises for a statement with 400, the only status the driver reads the SQLSTATE from.
	if _, ok := errors.AsType[*pgconn.PgError](err); ok {
		status = http.StatusBadRequest
	}
	writeJSON(w, status, resp)
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
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "postgresql://") || strings.Contains(lower, "postgres://") {
		// Strip connection string entirely
		return "upstream connection failed"
	}
	if keywordPassword.MatchString(msg) {
		return "upstream connection failed"
	}
	if strings.Contains(msg, "password") && strings.Contains(msg, "@") {
		return "upstream connection failed"
	}
	return msg
}

// ParseConnectionString extracts host, user, password, database from a postgres:// URL.
// Unlike net/url it accepts a password holding a bare '%' (50%off) or an unencoded ^, as Neon's proxy and libpq do.
func ParseConnectionString(s string) (*ConnInfo, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, ErrMissingConnectionString
	}

	// Normalize scheme
	if !strings.HasPrefix(s, "postgres://") && !strings.HasPrefix(s, "postgresql://") {
		return nil, fmt.Errorf("%w: must start with postgres:// or postgresql://", ErrInvalidConnectionString)
	}

	_, authority, _ := strings.Cut(s, "://")
	var path string
	if i := strings.IndexAny(authority, "/?#"); i >= 0 {
		authority, path = authority[:i], authority[i:]
	}
	var userinfo string
	if at := strings.LastIndexByte(authority, '@'); at >= 0 {
		userinfo, authority = authority[:at], authority[at+1:]
	}
	if i := strings.IndexAny(path, "?#"); i >= 0 {
		path = path[:i]
	}

	// Only the host and port go through net/url, so its error never quotes the password.
	u, err := url.Parse("postgres://" + authority)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidConnectionString, err)
	}

	username, password, _ := strings.Cut(userinfo, ":")
	username, password = unescapeLenient(username), unescapeLenient(password)
	database := unescapeLenient(strings.TrimPrefix(path, "/"))
	host := u.Hostname()
	port := u.Port()
	if port == "" {
		port = "5432"
	}
	if database == "" {
		database = username
	}

	if host == "" {
		return nil, fmt.Errorf("%w: missing host", ErrInvalidConnectionString)
	}
	if username == "" {
		return nil, fmt.Errorf("%w: missing user", ErrInvalidConnectionString)
	}
	// A NUL would end the startup packet field early, so libpq refuses %00 too.
	if strings.ContainsRune(username+password+database, 0) {
		return nil, fmt.Errorf("%w: %%00 is not allowed", ErrInvalidConnectionString)
	}

	return &ConnInfo{
		Host:     host,
		Port:     port,
		User:     username,
		Password: password,
		Database: database,
	}, nil
}

// unescapeLenient decodes every %XX escape and keeps any other character as written, as Neon's proxy does.
func unescapeLenient(s string) string {
	if !strings.Contains(s, "%") {
		return s
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == '%' && i+2 < len(s) {
			if v, err := strconv.ParseUint(s[i+1:i+3], 16, 8); err == nil {
				b.WriteByte(byte(v))
				i += 2
				continue
			}
		}
		b.WriteByte(s[i])
	}
	return b.String()
}
