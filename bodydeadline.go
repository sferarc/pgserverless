package pgserverless

import (
	"errors"
	"io"
	"net/http"
	"time"
)

// bodyReadTimeout bounds how long POST /sql waits for its request body once the headers have arrived.
const bodyReadTimeout = 30 * time.Second

// limitBodyRead bounds the body read with a connection deadline, per request because http.Server.ReadTimeout outlives Hijack and would cut a WebSocket.
func limitBodyRead(w http.ResponseWriter, r *http.Request, d time.Duration) {
	// With no body, net/http already has a background read pending that a deadline would end, cancelling the request context.
	if r.ContentLength == 0 {
		return
	}
	if d <= 0 {
		d = bodyReadTimeout
	}
	rc := http.NewResponseController(w)
	if rc.SetReadDeadline(time.Now().Add(d)) != nil {
		return
	}
	r.Body = &deadlineBody{ReadCloser: r.Body, rc: rc}
}

// deadlineBody clears the read deadline once the body is consumed, so net/http's background read cannot then cancel a long query.
type deadlineBody struct {
	io.ReadCloser
	rc *http.ResponseController
}

func (b *deadlineBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if errors.Is(err, io.EOF) {
		_ = b.rc.SetReadDeadline(time.Time{})
	}
	return n, err
}

func (b *deadlineBody) Close() error {
	err := b.ReadCloser.Close()
	_ = b.rc.SetReadDeadline(time.Time{})
	return err
}
