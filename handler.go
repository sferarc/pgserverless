package pgserverless

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/labstack/echo/v4"
)

// MaxBatchQueries is the largest batch the handler accepts. The driver's
// sql.transaction([...]) form sends an array; beyond this it is refused with 400
// rather than held open against the upstream.
const MaxBatchQueries = 100

// QueryTimeout bounds a single request's execution.
const QueryTimeout = 30 * time.Second

// Upstream says where a request's query should run and as whom. An Admission
// returns one once it has decided the request may proceed.
type Upstream struct {
	// Addr is the host:port to dial. A bare host defaults to port 5432.
	Addr string
	// ServerName is the TLS SNI to present. Empty presents Addr's host.
	// Hosts that route by SNI (a proxy fronting many databases on one port)
	// set this to the name the client asked for.
	ServerName string
	// User, Password and Database are the PostgreSQL credentials to connect
	// with.
	User     string
	Password string
	Database string
	// AppName, when set, is sent as application_name.
	AppName string
	// InsecureSkipVerify trusts the upstream certificate without verifying it.
	// Appropriate when dialing a loopback address whose certificate is
	// self-signed, and not otherwise.
	InsecureSkipVerify bool
	// TLSMode says whether to negotiate TLS with the upstream, using
	// PostgreSQL's own vocabulary. Empty means "require", which keeps the
	// behaviour every existing caller had.
	//
	// "disable" exists because a database on a loopback address or inside a
	// private network often does not serve TLS at all, and without this the
	// dial fails with "server refused TLS connection" and no way to proceed.
	TLSMode string
}

// TLS modes for Upstream.TLSMode.
const (
	// TLSRequire negotiates TLS and fails if the upstream refuses. The default.
	TLSRequire = "require"
	// TLSDisable connects in the clear.
	TLSDisable = "disable"
)

// AdmissionError refuses a request with a specific HTTP status. An Admission
// returns one when the reason for refusing has a better answer than 400, for
// example a suspended tenant (403) or an unknown host (404). A plain error is
// treated as 400.
type AdmissionError struct {
	Status  int
	Message string
}

func (e *AdmissionError) Error() string { return e.Message }

// Admission decides whether a request may run and where it should go.
// Returning an error refuses the request, and the error's text is the response
// body, so it must be safe to show a client. Return an *AdmissionError to
// choose the status.
//
// This is the single seam a host application implements. Connection-string
// parsing, tenant resolution, suspension checks, IP allowlists and rate limits
// all live behind it, which is why the interface is one function rather than a
// store: what a host needs to consult varies, and what the handler needs back
// does not.
type Admission func(c echo.Context) (Upstream, error)

// StaticUpstream is the trivial Admission. Every request goes to one PostgreSQL
// address, with the credentials the request's own connection string carries.
//
// It is what a deployment in front of a single database wants, and it is the
// reference for what an Admission has to produce.
func StaticUpstream(addr string) Admission {
	return func(c echo.Context) (Upstream, error) {
		ci, err := ParseConnectionString(c.Request().Header.Get("Connection-String"))
		if err != nil {
			return Upstream{}, err
		}
		return Upstream{
			Addr:               addr,
			ServerName:         ci.Host,
			User:               ci.User,
			Password:           ci.Password,
			Database:           ci.Database,
			InsecureSkipVerify: false,
		}, nil
	}
}

// Option configures a Handler.
type Option func(*Handler)

// WithDialErrorHook registers a callback invoked when the upstream dial fails.
// A host fronting many tenants uses it to feed brute-force accounting, which it
// cannot do from inside Admission because the dial happens afterwards.
func WithDialErrorHook(fn func(c echo.Context, err error)) Option {
	return func(h *Handler) { h.onDialError = fn }
}

// Handler serves both transports: the HTTP (fetch) transport at POST /sql, and
// the WebSocket tunnel at GET /v2.
//
// They are mounted separately, by Register and RegisterWebSocket, because a
// deployment may want one without the other.
type Handler struct {
	admit       Admission
	admitWS     Admission
	onDialError func(c echo.Context, err error)
}

// NewHandler returns a Handler that admits requests through admit.
func NewHandler(admit Admission, opts ...Option) *Handler {
	h := &Handler{admit: admit}
	for _, o := range opts {
		o(h)
	}
	return h
}

// Register mounts the handler on e at POST /sql, with the header normalization
// the driver's request shape needs.
func (h *Handler) Register(e *echo.Echo) {
	e.Use(NormalizeHeaders)
	e.POST("/sql", h.Query)
}

// Dial opens a connection to up. simpleProtocol selects the simple query
// protocol, which returns every value in text format.
func Dial(ctx context.Context, up Upstream, simpleProtocol bool) (*pgx.Conn, error) {
	host, portStr := parseHostPort(up.Addr)
	if host == "" {
		host = "127.0.0.1"
	}
	port, _ := strconv.Atoi(portStr)

	cfg, err := pgx.ParseConfig("")
	if err != nil {
		return nil, err
	}
	cfg.Host = host
	cfg.Port = uint16(port)
	cfg.User = up.User
	cfg.Password = up.Password
	cfg.Database = up.Database

	if up.TLSMode == TLSDisable {
		// pgx reads a nil TLSConfig as "do not attempt TLS".
		cfg.TLSConfig = nil
	} else {
		sni := up.ServerName
		if sni == "" {
			sni = host
		}
		cfg.TLSConfig = &tls.Config{ServerName: sni, InsecureSkipVerify: up.InsecureSkipVerify} //nolint:gosec // InsecureSkipVerify is the caller's explicit choice, see Upstream.
	}

	if up.AppName != "" {
		cfg.RuntimeParams["application_name"] = up.AppName
	}
	if simpleProtocol {
		cfg.DefaultQueryExecMode = pgx.QueryExecModeSimpleProtocol
	}
	return pgx.ConnectConfig(ctx, cfg)
}

// Query handles one serverless HTTP SQL request: single statement or batch,
// object or array rows, native or raw-text values.
func (h *Handler) Query(c echo.Context) error {
	ctx := c.Request().Context()

	up, aerr := h.admit(c)
	if aerr != nil {
		status := http.StatusBadRequest
		var ae *AdmissionError
		if errors.As(aerr, &ae) {
			status = ae.Status
		}
		return c.JSON(status, ErrorResponse{Message: aerr.Error()})
	}

	req, rerr := decodeSQLRequest(c)
	if rerr != nil {
		return c.JSON(http.StatusBadRequest, ErrorResponse{Message: rerr.Error()})
	}

	arrayMode := c.Request().Header.Get("Array-Mode") == "true"
	if req.ArrayMode != nil && *req.ArrayMode {
		arrayMode = true
	}

	// Raw-text output: the driver runs its own node-postgres type parsers
	// client-side keyed by dataTypeID, so it expects exactly what upstream
	// PostgreSQL would have sent in text format. It always sets this on its
	// real fetch path.
	rawText := c.Request().Header.Get("Raw-Text-Output") == "true"

	txnOpts, terr := parseTxnOptions(c.Request().Header)
	if terr != nil {
		return c.JSON(http.StatusBadRequest, ErrorResponse{Message: terr.Error()})
	}

	queryCtx, cancel := context.WithTimeout(ctx, QueryTimeout)
	defer cancel()

	// Raw-text mode selects the simple query protocol, which always returns
	// text-format values. That applies uniformly to single queries and to
	// batch pipelines, where per-query result-format directives do not exist.
	conn, err := Dial(queryCtx, up, rawText)
	if err != nil {
		if h.onDialError != nil {
			h.onDialError(c, err)
		}
		slog.Warn("pgserverless: connect failed", "err", SanitizeError(err.Error()))
		return HandlePgError(c, err)
	}
	defer conn.Close(context.Background())

	if req.Queries != nil {
		return h.executeBatch(c, conn, queryCtx, req.Queries, arrayMode, rawText, txnOpts)
	}
	return h.executeSingle(c, conn, queryCtx, *req.Query, ConvertParams(req.Params), arrayMode, rawText)
}

// decodeSQLRequest reads and validates the request body. Splitting it out of
// Query keeps one function per decision: this one decides whether the request
// is well formed, Query decides what to do with a well formed one.
func decodeSQLRequest(c echo.Context) (*SQLRequest, error) {
	var req SQLRequest
	if err := json.NewDecoder(c.Request().Body).Decode(&req); err != nil {
		return nil, ErrInvalidRequest
	}

	isBatch := req.Queries != nil
	if !isBatch && (req.Query == nil || *req.Query == "") {
		return nil, ErrMissingQuery
	}
	if isBatch && len(req.Queries) > MaxBatchQueries {
		return nil, fmt.Errorf("batch too large: max %d queries", MaxBatchQueries)
	}
	return &req, nil
}

// NormalizeHeaders maps the driver's Neon-prefixed request headers onto the
// plain names the handler reads. A header already set under the plain name
// wins, so a caller can drive the handler directly without the prefix.
func NormalizeHeaders(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		h := c.Request().Header
		for _, m := range [][2]string{
			{"Neon-Connection-String", "Connection-String"},
			{"Neon-Raw-Text-Output", "Raw-Text-Output"},
			{"Neon-Array-Mode", "Array-Mode"},
			{"Neon-Pool-Opt-In", "Pool-Opt-In"},
			{"Neon-Batch-Read-Only", "Batch-Read-Only"},
			{"Neon-Batch-Isolation-Level", "Batch-Isolation-Level"},
			{"Neon-Batch-Deferrable", "Batch-Deferrable"},
		} {
			if h.Get(m[1]) == "" {
				if v := h.Get(m[0]); v != "" {
					h.Set(m[1], v)
				}
			}
		}
		return next(c)
	}
}

// parseHostPort splits a host:port, defaulting the port to 5432.
func parseHostPort(addr string) (string, string) {
	idx := strings.LastIndex(addr, ":")
	if idx == -1 {
		return addr, "5432"
	}
	return addr[:idx], addr[idx+1:]
}
