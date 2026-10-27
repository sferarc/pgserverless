package pgserverless

import (
	"encoding/json"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// json and jsonb keep PostgreSQL's text: exact numbers, the server's key order, and no 500 past float64.
func TestConformance_JSONVerbatim(t *testing.T) {
	cases := []struct {
		name string
		oid  uint32
		in   string
		want string
	}{
		{"jsonb key order", oidJSONB, `{"b": 1, "aa": 2}`, `{"b":1,"aa":2}`},
		{"json key order and duplicates", oidJSON, `{"z":1,"a":2,"z":3}`, `{"z":1,"a":2,"z":3}`},
		{"int8 beyond 2^53", oidJSONB, `{"id": 9223372036854775807}`, `{"id":9223372036854775807}`},
		{"numeric precision", oidJSONB, `[0.1000000000000000055511151231257827, 1.50]`, `[0.1000000000000000055511151231257827,1.50]`},
		{"beyond float64 range", oidJSONB, `{"n": 1e400}`, `{"n":1e400}`},
		{"json null", oidJSONB, `null`, `null`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pg := pgfake.New(t)
			pg.SetResult("SELECT j FROM t", pgfake.Result{
				Cols: []pgfake.Col{{Name: "j", TypeOID: tc.oid}},
				Rows: [][]any{{[]byte(tc.in)}},
				Tag:  "SELECT 1",
			})
			rows := rowsJSON(t, pg, "SELECT j FROM t")
			require.Len(t, rows, 1)
			assert.Equal(t, tc.want, string(rows[0]["j"]))
		})
	}
}

func TestConformance_JSONArrayVerbatim(t *testing.T) {
	big := `{"id": 9223372036854775807}`
	huge := `1e400`
	for _, oid := range []uint32{oidJSONArray, oidJSONBArray} {
		pg := pgfake.New(t)
		pg.SetResult("SELECT a FROM t", pgfake.Result{
			Cols: []pgfake.Col{{Name: "a", TypeOID: oid}, {Name: "n", TypeOID: oidInt8}},
			Rows: [][]any{
				{arrayFixture([]*string{&big, nil, &huge, &big}, 2, 2), int64(1)},
			},
			Tag: "SELECT 1",
		})
		rows := rowsJSON(t, pg, "SELECT a FROM t")
		require.Len(t, rows, 1)
		assert.Equal(t, `[[{"id":9223372036854775807},null],[1e400,{"id":9223372036854775807}]]`, string(rows[0]["a"]), "oid %d", oid)
		assert.Equal(t, `"1"`, string(rows[0]["n"]), "the other columns still get the native coercions")
	}
}

func TestDecodeNativeValue(t *testing.T) {
	m := pgtype.NewMap()

	v, err := decodeNativeValue(m, oidJSONB, pgtype.BinaryFormatCode, append([]byte{1}, `{"n": 1e400}`...))
	require.NoError(t, err)
	assert.Equal(t, json.RawMessage(`{"n": 1e400}`), v, "the binary jsonb version byte is stripped")

	_, err = decodeNativeValue(m, oidJSON, pgtype.TextFormatCode, []byte(`{"n":`))
	require.Error(t, err)

	v, err = decodeNativeValue(m, oidJSON, pgtype.TextFormatCode, nil)
	require.NoError(t, err)
	assert.Nil(t, v)

	v, err = decodeNativeValue(m, oidInt4, pgtype.TextFormatCode, []byte("42"))
	require.NoError(t, err)
	assert.Equal(t, int32(42), v)

	v, err = decodeNativeValue(m, 999999, pgtype.TextFormatCode, []byte("x"))
	require.NoError(t, err)
	assert.Equal(t, "x", v, "an unregistered type stays text, as rows.Values leaves it")
}
