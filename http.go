package pgserverless

import (
	"net/http"
	"time"
)

// HTTPAdmission is Admission for net/http: it sees the plain *http.Request and
// returns where the request should run, or an error that refuses it. The error
// text becomes the response body, so it must be safe to show a client; return
// an *AdmissionError to choose the status.
type HTTPAdmission func(r *http.Request) (Upstream, error)

// HTTPOption configures the handler NewHTTPHandler returns.
type HTTPOption func(*httpHandler)

// WithHTTPDialErrorHook registers a callback invoked when the upstream dial
// fails, the net/http counterpart of WithDialErrorHook.
func WithHTTPDialErrorHook(fn func(r *http.Request, err error)) HTTPOption {
	return func(h *httpHandler) { h.onDialError = fn }
}

// WithHTTPWebSocketAdmission sets the HTTPAdmission consulted on GET /v2, the
// net/http counterpart of WithWebSocketAdmission.
func WithHTTPWebSocketAdmission(admit HTTPAdmission) HTTPOption {
	return func(h *httpHandler) { h.admitWS = admit }
}

// WithHTTPStatementCounter is the net/http counterpart of WithStatementCounter.
func WithHTTPStatementCounter(fn func(sql string) (int, error)) HTTPOption {
	return func(h *httpHandler) { h.countStatements = fn }
}

// WithHTTPMaxResponseBytes is the net/http counterpart of WithMaxResponseBytes.
func WithHTTPMaxResponseBytes(n int64) HTTPOption {
	return func(h *httpHandler) { h.maxResponseBytes = n }
}

// WithHTTPCORSOrigins lets pages on origins call POST /sql from a browser. It
// answers the OPTIONS preflight and marks each response readable, as Neon's
// proxy does for every origin; "*" allows any origin, which is Neon's setting.
// Without it the handler sends no CORS headers and OPTIONS /sql is refused.
func WithHTTPCORSOrigins(origins ...string) HTTPOption {
	return func(h *httpHandler) { h.corsOrigins = origins }
}

type httpHandler struct {
	admit            HTTPAdmission
	admitWS          HTTPAdmission
	onDialError      func(r *http.Request, err error)
	countStatements  func(sql string) (int, error)
	maxResponseBytes int64
	corsOrigins      []string
	startupTimeout   time.Duration
}

// NewHTTPHandler returns an http.Handler serving POST /sql and GET /v2, with
// the driver's Neon-prefixed headers normalized. A WebSocket upgrade is served
// on any path, as Neon's proxy does. It mounts on an
// http.ServeMux or any router, and needs no web framework.
func NewHTTPHandler(admit HTTPAdmission, opts ...HTTPOption) http.Handler {
	h := &httpHandler{admit: admit}
	for _, o := range opts {
		o(h)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sql", h.query)
	if len(h.corsOrigins) > 0 {
		mux.HandleFunc("OPTIONS /sql", func(w http.ResponseWriter, r *http.Request) { servePreflight(w, r, h.corsOrigins) })
	}
	mux.HandleFunc("GET "+WebSocketPath, h.upgrade)
	return NormalizeHTTPHeaders(upgradeAnyPath(mux, h.upgrade))
}

func (h *httpHandler) query(w http.ResponseWriter, r *http.Request) {
	allowOrigin(w, r, h.corsOrigins)
	serveSQL(w, r, func() (Upstream, error) { return h.admit(r) }, h.dialErrorFor(r), h.countStatements, h.maxResponseBytes)
}

func (h *httpHandler) upgrade(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	admit := h.admitWS
	if admit == nil {
		admit = h.admit
	}
	serveWebSocket(w, r, func() (Upstream, error) { return admit(r) }, h.dialErrorFor(r), h.startupTimeout)
}

func (h *httpHandler) dialErrorFor(r *http.Request) func(error) {
	if h.onDialError == nil {
		return nil
	}
	return func(err error) { h.onDialError(r, err) }
}

// NormalizeHTTPHeaders is the net/http middleware form of the header
// normalization NormalizeHeaders applies in echo.
func NormalizeHTTPHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		normalizeHeaders(r.Header)
		next.ServeHTTP(w, r)
	})
}

// StaticHTTPUpstream is StaticUpstream for NewHTTPHandler.
func StaticHTTPUpstream(addr string) HTTPAdmission {
	return func(r *http.Request) (Upstream, error) {
		return staticUpstream(addr, r)
	}
}
