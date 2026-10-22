package pgserverless

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"

	"github.com/sferarc/pgserverless/pgfake"
)

const benchWideRows = 500

func benchServer(b *testing.B) (*echo.Echo, *pgfake.PG) {
	b.Helper()
	pg := pgfake.New(b)
	static := StaticUpstream(pg.Addr())
	e := echo.New()
	NewHandler(func(c echo.Context) (Upstream, error) {
		up, err := static(c)
		up.InsecureSkipVerify = true // pgfake's certificate is self-signed
		return up, err
	}).Register(e)
	return e, pg
}

func benchPost(b *testing.B, e *echo.Echo, body string, headers map[string]string) {
	b.Helper()
	for range b.N {
		req := httptest.NewRequest(http.MethodPost, "/sql", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Neon-Connection-String", "postgresql://u:p@"+conformanceHost+"/db")
		for k, v := range headers {
			req.Header.Set(k, v)
		}
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			b.Fatalf("status %d: %s", rec.Code, rec.Body.String())
		}
	}
}

func BenchmarkQuerySingle(b *testing.B) {
	e, pg := benchServer(b)
	pg.SetResult("SELECT 1 AS n", pgfake.Result{
		Cols: []pgfake.Col{{Name: "n", TypeOID: oidInt4}},
		Rows: [][]any{{int32(1)}},
		Tag:  "SELECT 1",
	})
	b.ReportAllocs()
	b.ResetTimer()
	benchPost(b, e, `{"query":"SELECT 1 AS n"}`, nil)
}

func BenchmarkQueryBatch(b *testing.B) {
	for _, n := range []int{10, MaxBatchQueries} {
		b.Run(fmt.Sprintf("queries=%d", n), func(b *testing.B) {
			e, pg := benchServer(b)
			pg.SetResult("SELECT 1 AS n", pgfake.Result{
				Cols: []pgfake.Col{{Name: "n", TypeOID: oidInt4}},
				Rows: [][]any{{int32(1)}},
				Tag:  "SELECT 1",
			})
			items := make([]string, n)
			for i := range items {
				items[i] = `{"query":"SELECT 1 AS n"}`
			}
			body := `{"queries":[` + strings.Join(items, ",") + `]}`
			b.ReportAllocs()
			b.ResetTimer()
			benchPost(b, e, body, nil)
		})
	}
}

func BenchmarkQueryBatchTransaction(b *testing.B) {
	e, pg := benchServer(b)
	pg.SetResult("SELECT 1 AS n", pgfake.Result{
		Cols: []pgfake.Col{{Name: "n", TypeOID: oidInt4}},
		Rows: [][]any{{int32(1)}},
		Tag:  "SELECT 1",
	})
	body := `{"queries":[` + strings.Repeat(`{"query":"SELECT 1 AS n"},`, 9) + `{"query":"SELECT 1 AS n"}]}`
	b.ReportAllocs()
	b.ResetTimer()
	benchPost(b, e, body, map[string]string{"Neon-Batch-Isolation-Level": "Serializable", "Neon-Batch-Read-Only": "true"})
}

func BenchmarkQueryMixedTypes(b *testing.B) {
	const sql = "SELECT * FROM wide"
	cols := []pgfake.Col{
		{Name: "id", TypeOID: oidInt8},
		{Name: "small", TypeOID: oidInt4},
		{Name: "name", TypeOID: oidText},
		{Name: "ok", TypeOID: oidBool},
		{Name: "score", TypeOID: oidFloat8},
	}
	rows := make([][]any, benchWideRows)
	for i := range rows {
		rows[i] = []any{int64(i) << 40, int32(i), fmt.Sprintf("name-%d", i), i%2 == 0, float64(i) / 7}
	}
	for _, mode := range []struct{ name, header string }{{"object", ""}, {"array", "true"}} {
		b.Run(mode.name, func(b *testing.B) {
			e, pg := benchServer(b)
			pg.SetResult(sql, pgfake.Result{Cols: cols, Rows: rows, Tag: fmt.Sprintf("SELECT %d", benchWideRows)})
			b.ReportAllocs()
			b.ResetTimer()
			benchPost(b, e, `{"query":"`+sql+`"}`, map[string]string{"Neon-Array-Mode": mode.header})
		})
	}
}

func BenchmarkCoerceNeonValue(b *testing.B) {
	b.ReportAllocs()
	for i := range b.N {
		coerceNeonValue(nil, int64(i), oidInt8)
	}
}

func BenchmarkConvertParamsArray(b *testing.B) {
	params := []any{[]any{1.0, 2.0, "three", nil, []any{"x,y", "NULL"}}, map[string]any{"k": []any{1.0}}, "plain"}
	b.ReportAllocs()
	for range b.N {
		ConvertParams(params)
	}
}
