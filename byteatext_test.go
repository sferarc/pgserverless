package pgserverless

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// Under bytea_output = escape PostgreSQL prints byteaout's escape spelling,
// which native mode returns as sent; the fake answers in that spelling only if
// the handler asked for text, since a binary column would be read as raw bytes.
func TestConformance_ByteaInSessionByteaOutput(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT b, arr FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "b", TypeOID: oidBytea}, {Name: "arr", TypeOID: oidByteaArray}},
		Rows: [][]any{{
			pgfake.RawColumn(`A\000\377\\`),
			pgfake.RawColumn(`{{"\\001",NULL},{A,"\\\\"}}`),
		}},
		Tag: "SELECT 1",
	})
	e := newConformanceServer(t, pg)

	rec := doSQL(e, `{"query":"SELECT b, arr FROM t"}`, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	assert.JSONEq(t, `"A\\000\\377\\\\"`, firstColumn(t, rec.Body.Bytes(), "b"))
	assert.JSONEq(t, `[["\\001",null],["A","\\\\"]]`, firstColumn(t, rec.Body.Bytes(), "arr"))
}

func TestUseTextByteaAsksForText(t *testing.T) {
	m := pgtype.NewMap()
	useTextBytea(m)
	assert.Equal(t, int16(pgtype.TextFormatCode), m.FormatCodeForOID(pgtype.ByteaOID))
	assert.Equal(t, int16(pgtype.TextFormatCode), m.FormatCodeForOID(pgtype.ByteaArrayOID))

	v, err := m.Encode(pgtype.ByteaOID, pgtype.BinaryFormatCode, []byte{1, 2}, nil)
	require.NoError(t, err, "a bytea parameter still encodes")
	assert.Equal(t, []byte{1, 2}, v)

	bytea, _ := m.TypeForOID(pgtype.ByteaOID)
	useTextBytea(m)
	again, _ := m.TypeForOID(pgtype.ByteaOID)
	assert.Same(t, bytea, again, "a second call registers nothing")
}

// Checked against a real PostgreSQL in both bytea_output settings and after a SET inside a native batch.
func TestByteaOutputAgainstPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(ctx) })

	const values = `(VALUES ('\x'::bytea), ('\x41'), ('\x00ff5c22'), ('\x7b7d2c20')) v(x)`
	for _, output := range []string{"escape", "hex"} {
		_, err := conn.Exec(ctx, "SET bytea_output = "+output)
		require.NoError(t, err, output)
		native, err := QueryRows(ctx, conn, `SELECT x, ARRAY[x, NULL] AS arr FROM `+values, nil, true, false)
		require.NoError(t, err, output)
		text, err := QueryRows(ctx, conn, `SELECT x::text FROM `+values, nil, true, true)
		require.NoError(t, err, output)
		require.Len(t, native.Rows, len(text.Rows))
		for i := range text.Rows {
			want, got := text.Rows[i].([]any), native.Rows[i].([]any)
			assert.Equal(t, want[0], got[0], output)
			assert.Equal(t, []any{want[0], nil}, got[1], output)
		}
	}

	_, err = conn.Exec(ctx, "SET bytea_output = hex")
	require.NoError(t, err)
	rec := httptest.NewRecorder()
	executeBatch(rec, conn, ctx, []QueryItem{
		{Query: "SET bytea_output = escape"},
		{Query: `SELECT '\x0102'::bytea AS b`},
	}, true, false, txnOptions{}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Results []struct {
			Rows [][]any `json:"rows"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Results, 2)
	assert.Equal(t, `\001\002`, body.Results[1].Rows[0][0])
}
