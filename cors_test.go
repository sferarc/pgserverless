package pgserverless

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func refuseAll(*http.Request) (Upstream, error) {
	return Upstream{}, &AdmissionError{Status: http.StatusForbidden, Message: "suspended"}
}

func preflight(h http.Handler, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodOptions, "/sql", nil)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "neon-connection-string,neon-raw-text-output,neon-array-mode")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func postFrom(h http.Handler, origin string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, "/sql", strings.NewReader(`{"query":"SELECT 1"}`))
	req.Header.Set("Origin", origin)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

// The driver sends Neon-Connection-String, which no browser sends cross-origin without a preflight.
func TestCORSAnyOriginAnswersLikeNeon(t *testing.T) {
	h := NewHTTPHandler(refuseAll, WithHTTPCORSOrigins("*"))

	rec := preflight(h, "https://app.example.test")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "OPTIONS, POST", rec.Header().Get("Allow"))
	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "86400", rec.Header().Get("Access-Control-Max-Age"))
	allowed := strings.Split(rec.Header().Get("Access-Control-Allow-Headers"), ", ")
	for _, name := range []string{
		"Authorization", "Neon-Connection-String", "Neon-Raw-Text-Output", "Neon-Array-Mode",
		"Neon-Batch-Isolation-Level", "Neon-Batch-Read-Only", "Neon-Batch-Deferrable",
	} {
		assert.Contains(t, allowed, name, "the driver sends %s", name)
	}

	// Neon marks error responses readable too, so the driver can show the message.
	rec = postFrom(h, "https://app.example.test")
	require.Equal(t, http.StatusForbidden, rec.Code)
	assert.Equal(t, "*", rec.Header().Get("Access-Control-Allow-Origin"))
}

func TestCORSListedOriginsOnly(t *testing.T) {
	h := NewHTTPHandler(refuseAll, WithHTTPCORSOrigins("https://app.example.test"))

	rec := preflight(h, "https://app.example.test")
	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "https://app.example.test", rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "Origin", rec.Header().Get("Vary"))
	assert.NotEmpty(t, rec.Header().Get("Access-Control-Allow-Headers"))

	rec = postFrom(h, "https://app.example.test")
	assert.Equal(t, "https://app.example.test", rec.Header().Get("Access-Control-Allow-Origin"))

	for _, rec := range []*httptest.ResponseRecorder{preflight(h, "https://evil.example.test"), postFrom(h, "https://evil.example.test")} {
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
		assert.Empty(t, rec.Header().Get("Access-Control-Allow-Headers"))
		assert.Equal(t, "Origin", rec.Header().Get("Vary"))
	}
}

func TestCORSOffByDefault(t *testing.T) {
	h := NewHTTPHandler(refuseAll)

	rec := preflight(h, "https://app.example.test")
	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))

	rec = postFrom(h, "https://app.example.test")
	assert.Equal(t, http.StatusForbidden, rec.Code)
	assert.Empty(t, rec.Header().Get("Access-Control-Allow-Origin"))
	assert.Empty(t, rec.Header().Get("Vary"))
}
