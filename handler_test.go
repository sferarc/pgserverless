package pgserverless

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStaticUpstreamVerifiesTLSByDefault(t *testing.T) {
	e := echo.New()
	req := httptest.NewRequest("POST", "/sql", nil)
	req.Header.Set("Connection-String", "postgres://user:pass@tenant.example.com/db")
	c := e.NewContext(req, httptest.NewRecorder())

	up, err := StaticUpstream("127.0.0.1:5432")(c)
	require.NoError(t, err)
	assert.False(t, up.InsecureSkipVerify)
}

func TestParseHostPort(t *testing.T) {
	tests := []struct {
		addr     string
		wantHost string
		wantPort string
	}{
		{"127.0.0.1:5432", "127.0.0.1", "5432"},
		{":5432", "", "5432"},
		{"localhost:20533", "localhost", "20533"},
		{"localhost", "localhost", "5432"},
		{"[::1]:5432", "::1", "5432"},
		{"[2001:db8::1]:6543", "2001:db8::1", "6543"},
		{"::1", "::1", "5432"},
		{"[::1]", "::1", "5432"},
	}
	for _, tt := range tests {
		t.Run(tt.addr, func(t *testing.T) {
			host, port := parseHostPort(tt.addr)
			assert.Equal(t, tt.wantHost, host)
			assert.Equal(t, tt.wantPort, port)
		})
	}
}

// TestDialErrorHookFiresOnDialFailure pins the wiring, not the hook's own logic.
//
// A host in front of many tenants feeds its brute-force accounting from this
// callback, because the dial that reveals a bad credential happens after
// Admission has already returned. The callback is an Option, so a Handler built
// without it is a valid Handler that silently stops reporting: nothing fails to
// compile and no other test notices. This asserts the call site exists.
func TestDialErrorHookFiresOnDialFailure(t *testing.T) {
	var got error
	calls := 0
	// Port 1 on loopback refuses, so Dial fails without needing a database.
	h := NewHandler(StaticUpstream("127.0.0.1:1"), WithDialErrorHook(func(_ echo.Context, err error) {
		calls++
		got = err
	}))

	e := echo.New()
	req := httptest.NewRequest("POST", "/sql", strings.NewReader(`{"query":"select 1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection-String", "postgres://user:pass@tenant.example.com/db")
	rec := httptest.NewRecorder()

	require.NoError(t, h.Query(e.NewContext(req, rec)))
	assert.Equal(t, 1, calls, "the dial failed, so the host's dial-error hook must have been told")
	require.Error(t, got)
}

// TestDialErrorHookIsOptional is the other half: a Handler built without the
// option must not panic on the same path.
func TestDialErrorHookIsOptional(t *testing.T) {
	h := NewHandler(StaticUpstream("127.0.0.1:1"))
	e := echo.New()
	req := httptest.NewRequest("POST", "/sql", strings.NewReader(`{"query":"select 1"}`))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Connection-String", "postgres://user:pass@tenant.example.com/db")
	assert.NotPanics(t, func() {
		_ = h.Query(e.NewContext(req, httptest.NewRecorder()))
	})
}
