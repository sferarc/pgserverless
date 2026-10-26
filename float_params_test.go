package pgserverless

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// A JSON number is a float64, and pgx binds a float64 to a described int4 parameter by truncating it, so 1.5 ran as 1.
func TestConformance_FloatParamsBindAsText(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"native", nil},
		{"raw text", driverHeaders},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT $1::int4 AS v", pgfake.Result{
				Cols:      []pgfake.Col{{Name: "v", TypeOID: oidInt4}},
				Rows:      [][]any{{int32(1)}},
				Tag:       "SELECT 1",
				ParamOIDs: []uint32{oidInt4},
			})
			e := newConformanceServer(t, pg)

			rec := doSQL(e, `{"query":"SELECT $1::int4 AS v","params":[1.5]}`, tc.headers)

			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
			assert.Equal(t, [][]byte{[]byte("1.5")}, pg.LastBindParams(), "PostgreSQL must parse the number the client sent")
		})
	}
}

// Integers keep binding too; the native path used to send them in binary.
func TestConformance_WholeFloatParamBindsDigits(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT $1::int8 AS v", pgfake.Result{
		Cols:      []pgfake.Col{{Name: "v", TypeOID: oidInt8}},
		Rows:      [][]any{{int64(1000000)}},
		Tag:       "SELECT 1",
		ParamOIDs: []uint32{oidInt8},
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT $1::int8 AS v","params":[1e6]}`, nil)

	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.Equal(t, [][]byte{[]byte("1000000")}, pg.LastBindParams())
}
