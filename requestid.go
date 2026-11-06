package pgserverless

import (
	"crypto/rand"
	"fmt"
	"net/http"
)

// requestIDHeader carries the id each response is given, the header Neon's proxy puts on every response it writes.
const requestIDHeader = "Neon-Request-Id"

// setRequestID gives w a fresh UUID v4 under requestIDHeader and returns it, so the warnings logged for the request can carry it.
func setRequestID(w http.ResponseWriter) string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	id := fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
	w.Header().Set(requestIDHeader, id)
	return id
}
