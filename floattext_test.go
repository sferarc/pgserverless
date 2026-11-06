package pgserverless

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// Under extra_float_digits = 0 PostgreSQL prints 0.1::float8 + 0.2 as 0.3, which
// Neon parses and returns; the fake answers in that spelling only if the handler
// asked for text, since a binary column would be read as the stored value.
func TestConformance_FloatInSessionExtraFloatDigits(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT f8, f4, arr FROM t", pgfake.Result{
		Cols: []pgfake.Col{
			{Name: "f8", TypeOID: oidFloat8},
			{Name: "f4", TypeOID: oidFloat4},
			{Name: "arr", TypeOID: oidFloat8Array},
		},
		Rows: [][]any{{
			pgfake.RawColumn(`0.3`),
			pgfake.RawColumn(`0.333333`),
			pgfake.RawColumn(`{{0.3,NULL},{NaN,-Infinity}}`),
		}},
		Tag: "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT f8, f4, arr FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `0.3`, firstColumn(t, rec.Body.Bytes(), "f8"))
	assert.JSONEq(t, `0.333333`, firstColumn(t, rec.Body.Bytes(), "f4"))
	assert.JSONEq(t, `[[0.3,null],["NaN","-Infinity"]]`, firstColumn(t, rec.Body.Bytes(), "arr"))
}

func TestUseTextFloatAsksForText(t *testing.T) {
	m := pgtype.NewMap()
	useTextFloat(m)
	for _, oid := range []uint32{pgtype.Float4OID, pgtype.Float8OID, pgtype.Float4ArrayOID, pgtype.Float8ArrayOID} {
		assert.Equal(t, int16(pgtype.TextFormatCode), m.FormatCodeForOID(oid), oid)
	}

	v, err := m.Encode(pgtype.Float8OID, pgtype.BinaryFormatCode, 1.5, nil)
	require.NoError(t, err, "a float8 parameter still encodes")
	assert.Len(t, v, 8)
	float4, ok := m.TypeForOID(pgtype.Float4OID)
	require.True(t, ok)
	dec, err := float4.Codec.DecodeValue(m, pgtype.Float4OID, pgtype.TextFormatCode, []byte("0.100000001"))
	require.NoError(t, err)
	assert.Equal(t, 0.100000001, dec, "float4 text keeps the digits PostgreSQL printed")

	float8, _ := m.TypeForOID(pgtype.Float8OID)
	useTextFloat(m)
	again, _ := m.TypeForOID(pgtype.Float8OID)
	assert.Same(t, float8, again, "a second call registers nothing")
}

func TestTextFloatArrayElemsSpellsNonFiniteAtEveryDepth(t *testing.T) {
	elems, ok := textFloatArrayElems(`{{1.5,NaN},{Infinity,NULL}}`)
	require.True(t, ok)
	assert.Equal(t, []any{[]any{1.5, "NaN"}, []any{"Infinity", nil}}, elems)
	_, ok = textFloatArrayElems([]any{1.5})
	assert.False(t, ok, "an already decoded array is left to the caller")
}

// Checked against a real PostgreSQL, whose text Neon parses with Rust's f64 parser,
// under extra_float_digits 0, 1 (the default) and 3, and after a SET inside a native batch.
func TestExtraFloatDigitsAgainstPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(ctx) })

	const values = `(VALUES (0.1::float8 + 0.2, (1::float4 / 3)), (1e300, 0.1), ('NaN', '-Infinity'), (-0.0, 3.4028235e38)) v(d, r)`
	for _, digits := range []string{"0", "1", "3", "-5"} {
		_, err := conn.Exec(ctx, "SET extra_float_digits = "+digits)
		require.NoError(t, err, digits)
		native, err := QueryRows(ctx, conn, `SELECT d, r, ARRAY[d, NULL] AS arr FROM `+values, nil, true, false)
		require.NoError(t, err, digits)
		text, err := QueryRows(ctx, conn, `SELECT d, r FROM `+values, nil, true, true)
		require.NoError(t, err, digits)
		require.Len(t, native.Rows, len(text.Rows))
		for i := range text.Rows {
			want, got := text.Rows[i].([]any), native.Rows[i].([]any)
			for col := range 2 {
				assert.Equal(t, neonFloat(want[col].(string)), got[col], "%s row %d col %d", digits, i, col)
			}
			assert.Equal(t, []any{neonFloat(want[0].(string)), nil}, got[2], digits)
		}
	}

	_, err = conn.Exec(ctx, "SET extra_float_digits = 1")
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	executeBatch(rec, conn, ctx, []QueryItem{
		{Query: "SET extra_float_digits = 0"},
		{Query: `SELECT 0.1::float8 + 0.2 AS f`},
	}, true, false, txnOptions{}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Results []struct {
			Rows [][]json.RawMessage `json:"rows"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Results, 2)
	assert.Equal(t, `0.3`, string(body.Results[1].Rows[0][0]))
}

// neonFloat is what Neon's pg_text_to_json makes of a float column's text.
func neonFloat(s string) any {
	f, err := strconv.ParseFloat(s, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return s
	}
	return f
}
