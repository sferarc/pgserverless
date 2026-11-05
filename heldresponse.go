package pgserverless

import (
	"bytes"
	"cmp"
	"maps"
	"net/http"
)

// heldResponse keeps a POST /sql response in memory until the upstream connection is closed, so a client that reads it slowly cannot hold a PostgreSQL backend.
type heldResponse struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func newHeldResponse() *heldResponse { return &heldResponse{header: http.Header{}} }

func (h *heldResponse) Header() http.Header { return h.header }

func (h *heldResponse) WriteHeader(status int) {
	if h.status == 0 {
		h.status = status
	}
}

func (h *heldResponse) Write(b []byte) (int, error) {
	h.WriteHeader(http.StatusOK)
	return h.body.Write(b)
}

func (h *heldResponse) sendTo(w http.ResponseWriter) {
	maps.Copy(w.Header(), h.header)
	w.WriteHeader(cmp.Or(h.status, http.StatusOK))
	_, _ = w.Write(h.body.Bytes())
}
