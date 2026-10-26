package pgserverless_test

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/sferarc/pgserverless"
)

// A complete server for one database, with no web framework.
func Example() {
	upstream := pgserverless.Upstream{Addr: "db.internal:5432"}

	h := pgserverless.NewHTTPHandler(
		pgserverless.StaticHTTPUpstream(upstream.Addr),
		// The tunnel carries credentials inside the wire protocol, so it
		// needs an address and nothing else from the request.
		pgserverless.WithHTTPWebSocketAdmission(func(*http.Request) (pgserverless.Upstream, error) {
			return upstream, nil
		}),
	)

	srv := &http.Server{Addr: ":4000", Handler: h}
	_ = srv // srv.ListenAndServe() in a real program
}

// The same server on echo. Echo is an http.Handler, so it mounts on any
// net/http server.
func ExampleNewHandler() {
	upstream := pgserverless.Upstream{Addr: "db.internal:5432"}

	h := pgserverless.NewHandler(
		pgserverless.StaticUpstream(upstream.Addr),
		// The tunnel carries credentials inside the wire protocol, so it
		// needs an address and nothing else from the request.
		pgserverless.WithWebSocketAdmission(func(echo.Context) (pgserverless.Upstream, error) {
			return upstream, nil
		}),
	)

	e := echo.New()
	h.Register(e)          // POST /sql
	h.RegisterWebSocket(e) // GET  /v2

	srv := &http.Server{Addr: ":4000", Handler: e}
	_ = srv // srv.ListenAndServe() in a real program
}

func ExampleParseConnectionString() {
	ci, err := pgserverless.ParseConnectionString("postgres://app:secret@db.internal:6432/orders")
	if err != nil {
		panic(err)
	}
	fmt.Println(ci.Host, ci.Port, ci.User, ci.Database)
	// Output: db.internal 6432 app orders
}

// An Admission refuses a request with the status that fits the reason. The
// refusal happens before any connection to the database is opened.
func ExampleAdmissionError() {
	suspended := func(echo.Context) (pgserverless.Upstream, error) {
		return pgserverless.Upstream{}, &pgserverless.AdmissionError{
			Status:  http.StatusForbidden,
			Message: "tenant suspended",
		}
	}

	e := echo.New()
	pgserverless.NewHandler(suspended).Register(e)

	req := httptest.NewRequest(http.MethodPost, "/sql", strings.NewReader(`{"query":"select 1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	body, _ := io.ReadAll(rec.Body)
	fmt.Println(rec.Code, strings.TrimSpace(string(body)))
	// Output: 403 {"message":"tenant suspended"}
}

// StaticUpstream takes credentials from the request's own connection string,
// so a request without one is refused as a bad request.
func ExampleStaticUpstream() {
	e := echo.New()
	pgserverless.NewHandler(pgserverless.StaticUpstream("db.internal:5432")).Register(e)

	req := httptest.NewRequest(http.MethodPost, "/sql", strings.NewReader(`{"query":"select 1"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	body, _ := io.ReadAll(rec.Body)
	fmt.Println(rec.Code, strings.TrimSpace(string(body)))
	// Output: 400 {"message":"Connection-String header is required"}
}

// The driver's Neon-prefixed headers are accepted alongside the plain names.
func ExampleNormalizeHeaders() {
	e := echo.New()
	e.Use(pgserverless.NormalizeHeaders)
	e.GET("/", func(c echo.Context) error {
		return c.String(http.StatusOK, c.Request().Header.Get("Array-Mode"))
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Neon-Array-Mode", "true")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	fmt.Println(rec.Body.String())
	// Output: true
}
