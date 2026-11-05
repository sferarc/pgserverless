package pgserverless

import (
	"bytes"
	"cmp"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgconn/ctxwatch"
	"github.com/labstack/echo/v4"
)

// MaxBatchQueries is the largest batch the handler accepts. The driver's
// sql.transaction([...]) form sends an array; beyond this it is refused with 400
// rather than held open against the upstream.
const MaxBatchQueries = 100

// QueryTimeout bounds a single request's execution.
const QueryTimeout = 30 * time.Second

// MaxRequestBodyBytes is the largest POST /sql body the handler reads, Neon's
// default of 10 MiB. A larger body is refused with 413 before the upstream is
// dialed.
const MaxRequestBodyBytes = 10 << 20

// MaxResponseBytes is Neon's default response cap, for WithMaxResponseBytes.
const MaxResponseBytes = 10 << 20

// cancelGrace bounds how long a cancelled statement, and dialing its CancelRequest, may take before the socket is cut.
const cancelGrace = time.Second

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
	// RootCAs, when set, verifies the upstream certificate against these roots instead of the system's, as libpq's sslrootcert does.
	RootCAs *x509.CertPool
	// VerifyCAOnly, with RootCAs set, checks that one of them issued the upstream certificate but not that it names the host, as libpq's sslmode=verify-ca does.
	// Without RootCAs the host name is still checked, since any publicly issued certificate would otherwise pass.
	VerifyCAOnly bool
	// TLSMode says whether to negotiate TLS with the upstream, using
	// PostgreSQL's own vocabulary. Empty means "require", which keeps the
	// behaviour every existing caller had.
	//
	// "disable" exists because a database on a loopback address or inside a
	// private network often does not serve TLS at all, and without this the
	// dial fails with "server refused TLS connection" and no way to proceed.
	// "prefer" is for an upstream that may or may not serve TLS.
	TLSMode string
	// RawTextSimpleProtocol keeps raw-text requests on the simple query
	// protocol. It is for an upstream that can only rewrite or hold a statement
	// sent as a simple query, such as a policy proxy. A query holding several
	// statements is still refused, and a batch is still joined into one query.
	// It is ignored unless the handler has a statement counter
	// (WithStatementCounter).
	RawTextSimpleProtocol bool
	// MaxResponseBytes overrides the handler's response cap (WithMaxResponseBytes) for this request: zero keeps it, a negative value lifts it.
	MaxResponseBytes int64
}

// TLS modes for Upstream.TLSMode.
const (
	// TLSRequire negotiates TLS and fails if the upstream refuses. The default.
	TLSRequire = "require"
	// TLSDisable connects in the clear.
	TLSDisable = "disable"
	// TLSPrefer negotiates TLS and, if that fails, dials again in the clear, as libpq's sslmode=prefer does.
	TLSPrefer = "prefer"
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
		return staticUpstream(addr, c.Request())
	}
}

func staticUpstream(addr string, r *http.Request) (Upstream, error) {
	ci, err := ParseRequestConnectionString(r.Header.Get("Connection-String"))
	if err != nil {
		return Upstream{}, err
	}
	return Upstream{
		Addr:       addr,
		ServerName: ci.Host,
		User:       ci.User,
		Password:   ci.Password,
		Database:   ci.Database,
	}, nil
}

// Option configures a Handler.
type Option func(*Handler)

// WithDialErrorHook registers a callback invoked when the upstream dial fails.
// A host fronting many tenants uses it to feed brute-force accounting, which it
// cannot do from inside Admission because the dial happens afterwards.
func WithDialErrorHook(fn func(c echo.Context, err error)) Option {
	return func(h *Handler) { h.onDialError = fn }
}

// WithStatementCounter sets the function that returns how many statements
// PostgreSQL parses sql into, or the syntax error it reports. The simple
// protocol of Upstream.RawTextSimpleProtocol relies on it to refuse a query or
// batch item that does not hold exactly one statement.
func WithStatementCounter(fn func(sql string) (int, error)) Option {
	return func(h *Handler) { h.countStatements = fn }
}

// WithMaxResponseBytes fails a POST /sql response with 507 once the rows
// PostgreSQL sent for it, framing included, pass n bytes, as Neon does, so one
// cross join cannot exhaust the server's memory. Zero, the default, leaves
// responses uncapped. Upstream.MaxResponseBytes overrides it per request.
func WithMaxResponseBytes(n int64) Option {
	return func(h *Handler) { h.maxResponseBytes = n }
}

// Handler serves both transports: the HTTP (fetch) transport at POST /sql, and
// the WebSocket tunnel at GET /v2.
//
// They are mounted separately, by Register and RegisterWebSocket, because a
// deployment may want one without the other.
type Handler struct {
	admit            Admission
	admitWS          Admission
	onDialError      func(c echo.Context, err error)
	countStatements  func(sql string) (int, error)
	maxResponseBytes int64
	startupTimeout   time.Duration
	bodyTimeout      time.Duration
	writeTimeout     time.Duration
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
	var mode pgx.QueryExecMode
	if simpleProtocol {
		mode = pgx.QueryExecModeSimpleProtocol
	}
	return dial(ctx, up, mode)
}

// dial opens a connection to up that runs queries in mode; zero keeps pgx's default.
func dial(ctx context.Context, up Upstream, mode pgx.QueryExecMode) (*pgx.Conn, error) {
	cfg, err := upstreamConfig(up, mode)
	if err != nil {
		return nil, err
	}
	if t := copyTripFrom(ctx); t != nil {
		cfg.BuildFrontend = t.frontend
	}
	return pgx.ConnectConfig(ctx, cfg)
}

// upstreamConfig is the pgx configuration for one connection to up and nothing else.
func upstreamConfig(up Upstream, mode pgx.QueryExecMode) (*pgx.ConnConfig, error) {
	host, portStr := parseHostPort(up.Addr)
	if host == "" {
		host = "127.0.0.1"
	}
	port, err := strconv.ParseUint(portStr, 10, 16)
	if err != nil {
		// The WebSocket path's net.Dial refuses such an Addr; a uint16 cast dialed another port.
		return nil, fmt.Errorf("upstream address %q: invalid port", up.Addr)
	}

	cfg, err := pgx.ParseConfig("")
	if err != nil {
		return nil, err
	}
	// ParseConfig("") also applies this process's own libpq environment (PGHOST, PGTZ, PGOPTIONS, ...); none of it is the client's.
	cfg.Fallbacks = nil
	// The JSON both ways is UTF-8; unset, the session takes the database's or role's encoding and non-ASCII text arrives garbled.
	cfg.RuntimeParams = map[string]string{"client_encoding": "UTF8"}
	cfg.ValidateConnect = nil
	cfg.ConnectTimeout = 0
	cfg.SSLNegotiation = ""
	cfg.ChannelBinding = "prefer"
	cfg.RequireAuth = ""
	cfg.MinProtocolVersion = "3.0"
	cfg.MaxProtocolVersion = "3.0"
	cfg.Host = host
	cfg.Port = uint16(port)
	cfg.User = up.User
	cfg.Password = up.Password
	cfg.Database = up.Database

	if up.TLSMode == TLSDisable {
		// pgx reads a nil TLSConfig as "do not attempt TLS".
		cfg.TLSConfig = nil
	} else {
		cfg.TLSConfig = upstreamTLSConfig(up, host)
	}
	if up.TLSMode == TLSPrefer {
		// pgx tries the fallback when the TLS attempt fails, as it does for sslmode=prefer.
		cfg.Fallbacks = []*pgconn.FallbackConfig{{Host: host, Port: uint16(port)}}
	}

	if up.AppName != "" {
		cfg.RuntimeParams["application_name"] = up.AppName
	}
	if mode != 0 {
		cfg.DefaultQueryExecMode = mode
	}
	// pgx's default only cuts the socket, which leaves the statement running on the database after the request has ended.
	cfg.BuildContextWatcherHandler = func(c *pgconn.PgConn) ctxwatch.Handler {
		return &pgconn.CancelRequestContextWatcherHandler{Conn: c, DeadlineDelay: cancelGrace}
	}
	return cfg, nil
}

// upstreamTLSConfig is the TLS client configuration both transports dial the upstream with.
func upstreamTLSConfig(up Upstream, host string) *tls.Config {
	sni := up.ServerName
	if sni == "" {
		sni = host
	}
	cfg := &tls.Config{ServerName: sni, RootCAs: up.RootCAs, InsecureSkipVerify: up.InsecureSkipVerify} //nolint:gosec // InsecureSkipVerify is the caller's explicit choice, see Upstream.
	if up.VerifyCAOnly && up.RootCAs != nil && !up.InsecureSkipVerify {
		// crypto/tls checks the chain and the name together, so the chain alone is checked in VerifyConnection.
		cfg.InsecureSkipVerify = true
		cfg.VerifyConnection = func(cs tls.ConnectionState) error { return verifyIssuer(cs, up.RootCAs) }
	}
	return cfg
}

// verifyIssuer checks the peer's certificate chain against roots without checking any name.
func verifyIssuer(cs tls.ConnectionState, roots *x509.CertPool) error {
	if len(cs.PeerCertificates) == 0 {
		return errors.New("upstream sent no certificate")
	}
	opts := x509.VerifyOptions{Roots: roots, Intermediates: x509.NewCertPool()}
	for _, cert := range cs.PeerCertificates[1:] {
		opts.Intermediates.AddCert(cert)
	}
	_, err := cs.PeerCertificates[0].Verify(opts)
	return err
}

// Query handles one serverless HTTP SQL request: single statement or batch,
// object or array rows, native or raw-text values.
func (h *Handler) Query(c echo.Context) error {
	serveSQL(c.Response(), c.Request(), func() (Upstream, error) { return h.admit(c) }, h.dialErrorFor(c), h.countStatements, h.maxResponseBytes, h.bodyTimeout, h.writeTimeout)
	return nil
}

func (h *Handler) dialErrorFor(c echo.Context) func(error) {
	if h.onDialError == nil {
		return nil
	}
	return func(err error) { h.onDialError(c, err) }
}

// serveSQL is the transport-independent core of POST /sql.
func serveSQL(w http.ResponseWriter, r *http.Request, admit func() (Upstream, error), onDialError func(error), count func(string) (int, error), maxResponse int64, bodyTimeout, writeTimeout time.Duration) {
	r.Body = http.MaxBytesReader(w, r.Body, MaxRequestBodyBytes)
	limitBodyRead(w, r, bodyTimeout)

	up, aerr := admit()
	if aerr != nil {
		writeAdmissionError(w, aerr)
		return
	}

	req, rerr := decodeSQLRequest(r)
	if rerr != nil {
		status := http.StatusBadRequest
		if errors.Is(rerr, ErrRequestTooLarge) {
			status = http.StatusRequestEntityTooLarge
		}
		writeJSON(w, status, ErrorResponse{Message: rerr.Error()})
		return
	}

	arrayMode := requestArrayMode(r.Header, req)

	// Raw-text output: the driver runs its own node-postgres type parsers
	// client-side keyed by dataTypeID, so it expects exactly what upstream
	// PostgreSQL would have sent in text format. It always sets this on its
	// real fetch path.
	rawText := r.Header.Get("Raw-Text-Output") == "true"

	txnOpts, terr := parseTxnOptions(r.Header)
	if terr != nil {
		writeJSON(w, http.StatusBadRequest, ErrorResponse{Message: terr.Error()})
		return
	}

	queryCtx, cancel := context.WithTimeout(r.Context(), QueryTimeout)
	defer cancel()
	queryCtx = withCopyTrip(queryCtx)

	// Raw-text mode runs like Neon's text query: the extended protocol with
	// untyped text parameters and text results, one statement per query. The
	// simple protocol would run every statement in a query and joins a batch
	// with ';', so a trailing line comment swallowed the next batch item.
	simple := rawText && up.RawTextSimpleProtocol && count != nil
	var mode pgx.QueryExecMode
	switch {
	case simple:
		mode = pgx.QueryExecModeSimpleProtocol
	case rawText:
		mode = pgx.QueryExecModeExec
	}
	conn, err := dial(queryCtx, up, mode)
	if err != nil {
		if onDialError != nil {
			onDialError(err)
		}
		slog.Warn("pgserverless: connect failed", "err", SanitizeError(err.Error()))
		writeDialError(w, err)
		return
	}
	// Deferred in this order, the connection closes before the held response is sent.
	held := newHeldResponse()
	defer held.sendTo(w, writeTimeout)
	defer conn.Close(context.Background())
	w = held

	if up.MaxResponseBytes != 0 {
		maxResponse = up.MaxResponseBytes
	}
	budget := &responseBudget{max: maxResponse}
	switch {
	case req.Queries != nil && simple:
		executeBatchSimple(w, conn, queryCtx, req.Queries, count, arrayMode, rawText, txnOpts, budget)
		return
	case req.Queries != nil:
		executeBatch(w, conn, queryCtx, req.Queries, arrayMode, rawText, txnOpts, budget)
		return
	case simple:
		if err := checkSingleStatement(count, *req.Query, len(req.Params)); err != nil {
			writePgError(w, err)
			return
		}
	}
	executeSingle(w, conn, queryCtx, *req.Query, ConvertParams(req.Params), arrayMode, rawText, budget)
}

// writeJSON writes v as JSON; echo's debug and ?pretty indentation and custom serializer do not apply.
func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("pgserverless: encode response failed", "err", err)
	}
}

func writeAdmissionError(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	if ae, ok := errors.AsType[*AdmissionError](err); ok {
		status = ae.Status
	}
	writeJSON(w, status, ErrorResponse{Message: err.Error()})
}

// decodeSQLRequest reads and validates the request body. Splitting it out of
// serveSQL keeps one function per decision: this one decides whether the
// request is well formed, serveSQL decides what to do with a well formed one.
func decodeSQLRequest(r *http.Request) (*SQLRequest, error) {
	// queries is decoded only in batch mode: Neon skips it unread when query is a string.
	var body struct {
		SQLRequest
		Queries json.RawMessage `json:"queries"`
	}
	dec := json.NewDecoder(r.Body)
	// A float64 rounds an integer past 2^53, so a bigint id sent as a JSON number bound as a different id.
	dec.UseNumber()
	err := dec.Decode(&body)
	if err == nil {
		// Neon's serde_json refuses anything but whitespace after the value; read leniently, a second value was silently dropped.
		if _, terr := dec.Token(); !errors.Is(terr, io.EOF) {
			err = cmp.Or(terr, ErrInvalidRequest)
		}
	}
	if err != nil {
		if _, ok := errors.AsType[*http.MaxBytesError](err); ok {
			return nil, fmt.Errorf("%w (max is %d bytes)", ErrRequestTooLarge, MaxRequestBodyBytes)
		}
		if errors.Is(err, os.ErrDeadlineExceeded) {
			return nil, fmt.Errorf("%w: the body did not arrive in time", ErrInvalidRequest)
		}
		return nil, ErrInvalidRequest
	}
	req := body.SQLRequest

	// Neon parses the body as a single statement before a batch, so a string query wins over queries.
	// An empty query is valid: Neon answers it 200 with no fields and no command.
	if req.Query != nil {
		return &req, nil
	}
	if len(body.Queries) == 0 || string(body.Queries) == "null" {
		return nil, ErrMissingQuery
	}
	items, err := decodeBatchItems(body.Queries)
	if err != nil {
		return nil, err
	}
	req.Queries = items
	return &req, nil
}

func decodeBatchItems(raw json.RawMessage) ([]QueryItem, error) {
	// Each item's query is decoded as a pointer, so an item without one is told apart from an empty one.
	var items []struct {
		QueryItem
		Query *string `json:"query"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&items); err != nil {
		return nil, ErrInvalidRequest
	}
	if len(items) > MaxBatchQueries {
		return nil, fmt.Errorf("batch too large: max %d queries", MaxBatchQueries)
	}
	queries := make([]QueryItem, len(items))
	for i, item := range items {
		// Neon refuses such a body with 400; read as "", the item ran as an empty statement and the batch answered 200.
		if item.Query == nil {
			return nil, fmt.Errorf("%w in queries[%d]", ErrMissingQuery, i)
		}
		queries[i] = QueryItem{Query: *item.Query, Params: item.Params, ArrayMode: item.ArrayMode}
	}
	return queries, nil
}

// requestArrayMode lets a single query's arrayMode override the header either way, as Neon and batchArrayMode do.
func requestArrayMode(h http.Header, req *SQLRequest) bool {
	arrayMode := h.Get("Array-Mode") == "true"
	if req.ArrayMode == nil {
		return arrayMode
	}
	if req.Queries == nil {
		return *req.ArrayMode
	}
	return arrayMode || *req.ArrayMode
}

// headerAliases pairs each Neon-prefixed request header with the plain name the handler reads.
var headerAliases = [][2]string{
	{"Neon-Connection-String", "Connection-String"},
	{"Neon-Raw-Text-Output", "Raw-Text-Output"},
	{"Neon-Array-Mode", "Array-Mode"},
	{"Neon-Pool-Opt-In", "Pool-Opt-In"},
	{"Neon-Batch-Read-Only", "Batch-Read-Only"},
	{"Neon-Batch-Isolation-Level", "Batch-Isolation-Level"},
	{"Neon-Batch-Deferrable", "Batch-Deferrable"},
}

// normalizeHeaders maps the driver's Neon-prefixed request headers onto the
// plain names the handler reads. A header already set under the plain name
// wins, so a caller can drive the handler directly without the prefix.
func normalizeHeaders(h http.Header) {
	for _, m := range headerAliases {
		if h.Get(m[1]) == "" {
			if v := h.Get(m[0]); v != "" {
				h.Set(m[1], v)
			}
		}
	}
}

// NormalizeHeaders is the echo middleware form of the header normalization.
func NormalizeHeaders(next echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		normalizeHeaders(c.Request().Header)
		return next(c)
	}
}

// parseHostPort splits a host:port, defaulting the port to 5432.
func parseHostPort(addr string) (string, string) {
	if host, port, err := net.SplitHostPort(addr); err == nil {
		return host, port
	}
	if ip, err := netip.ParseAddr(strings.Trim(addr, "[]")); err == nil {
		return ip.String(), "5432"
	}
	idx := strings.LastIndex(addr, ":")
	if idx == -1 {
		return addr, "5432"
	}
	return addr[:idx], addr[idx+1:]
}
