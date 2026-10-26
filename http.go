package pgserverless

import "net/http"

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

type httpHandler struct {
	admit           HTTPAdmission
	admitWS         HTTPAdmission
	onDialError     func(r *http.Request, err error)
	countStatements func(sql string) (int, error)
}

// NewHTTPHandler returns an http.Handler serving POST /sql and GET /v2, with
// the driver's Neon-prefixed headers normalized. It mounts on an
// http.ServeMux or any router, and needs no web framework.
func NewHTTPHandler(admit HTTPAdmission, opts ...HTTPOption) http.Handler {
	h := &httpHandler{admit: admit}
	for _, o := range opts {
		o(h)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /sql", h.query)
	mux.HandleFunc("GET "+WebSocketPath, h.upgrade)
	return NormalizeHTTPHeaders(mux)
}

func (h *httpHandler) query(w http.ResponseWriter, r *http.Request) {
	serveSQL(w, r, func() (Upstream, error) { return h.admit(r) }, h.dialErrorFor(r), h.countStatements)
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
	serveWebSocket(w, r, func() (Upstream, error) { return admit(r) }, h.dialErrorFor(r))
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
