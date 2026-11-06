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

func TestUseTextRecordAsksForText(t *testing.T) {
	m := pgtype.NewMap()
	useTextRecord(m)
	for _, oid := range []uint32{pgtype.RecordOID, pgtype.RecordArrayOID} {
		assert.Equal(t, int16(pgtype.TextFormatCode), m.FormatCodeForOID(oid), oid)
	}

	record, ok := m.TypeForOID(pgtype.RecordOID)
	require.True(t, ok)
	v, err := record.Codec.DecodeValue(m, pgtype.RecordOID, pgtype.TextFormatCode, []byte(`(1,"a b")`))
	require.NoError(t, err)
	assert.Equal(t, `(1,"a b")`, v)

	useTextRecord(m)
	again, _ := m.TypeForOID(pgtype.RecordOID)
	assert.Same(t, record, again, "a second call registers nothing")
	useTextRecord(nil)
}

// Neon returns PostgreSQL's record text, since pg-types registers no parser for
// record, and a record[] as an array of those strings.
func TestConformance_RecordColumnsAsText(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("SELECT r, rs, rs2, empty FROM t", pgfake.Result{
		Cols: []pgfake.Col{
			{Name: "r", TypeOID: oidRecord},
			{Name: "rs", TypeOID: oidRecordArray},
			{Name: "rs2", TypeOID: oidRecordArray},
			{Name: "empty", TypeOID: oidRecordArray},
		},
		Rows: [][]any{{
			pgfake.RawColumn(`(1,"a b",)`),
			pgfake.RawColumn(`{"(1,\"a b\",)",NULL}`),
			pgfake.RawColumn(`{{"(1)"},{"(2)"}}`),
			pgfake.RawColumn(`{}`),
		}},
		Tag: "SELECT 1",
	})

	rows := rowsJSON(t, pg, "SELECT r, rs, rs2, empty FROM t")
	assert.JSONEq(t, `"(1,\"a b\",)"`, string(rows[0]["r"]))
	assert.JSONEq(t, `["(1,\"a b\",)",null]`, string(rows[0]["rs"]))
	assert.JSONEq(t, `[["(1)"],["(2)"]]`, string(rows[0]["rs2"]))
	assert.JSONEq(t, `[]`, string(rows[0]["empty"]))
}

// Native mode must return a record as the text raw-text mode reads, whatever
// its field types and the session settings, and a record[] as those strings.
// Money, timetz and pg_lsn fields used to come back as a JSON array of base64,
// and bytea and float fields ignored bytea_output and extra_float_digits.
func TestRecordsAgainstPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(ctx) })

	const query = `SELECT ROW(0.1::float8 + 0.2, '\x01ff'::bytea, 1.5::money, '12:00+02'::timetz, '1/0'::pg_lsn,
		'2024-01-02 10:00'::timestamp, '1 day 2 hours'::interval, 0.00::numeric, 'a b', NULL::int4, ROW(1, 'x')) AS r,
		ARRAY[ROW(1, 'a b'), NULL, ROW(2, '"q"')] AS rs`
	for _, settings := range [][]string{
		{"RESET extra_float_digits", "RESET bytea_output", "RESET DateStyle", "RESET IntervalStyle"},
		{"SET extra_float_digits = 0", "SET bytea_output = escape", "SET DateStyle = 'SQL, DMY'", "SET IntervalStyle = sql_standard"},
	} {
		for _, s := range settings {
			_, err := conn.Exec(ctx, s)
			require.NoError(t, err, s)
		}
		native, err := QueryRows(ctx, conn, query, nil, true, false)
		require.NoError(t, err, settings)
		text, err := conn.Query(ctx, query, pgx.QueryExecModeExec)
		require.NoError(t, err)
		require.True(t, text.Next())
		wantRecord := string(text.RawValues()[0])
		text.Close()

		got := native.Rows[0].([]any)
		assert.Equal(t, wantRecord, got[0], settings)
		assert.Equal(t, []any{"(1,\"a b\")", nil, `(2,"""q""")`}, got[1], settings)
	}

	rec := httptest.NewRecorder()
	executeBatch(rec, conn, ctx, []QueryItem{
		{Query: "SET extra_float_digits = 1"},
		{Query: `SELECT ROW(0.1::float8 + 0.2, 1.5::money) AS r`},
	}, true, false, txnOptions{}, nil)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())
	var body struct {
		Results []struct {
			Rows [][]json.RawMessage `json:"rows"`
		} `json:"results"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	require.Len(t, body.Results, 2)
	assert.Equal(t, `"(0.30000000000000004,$1.50)"`, string(body.Results[1].Rows[0][0]))
}
