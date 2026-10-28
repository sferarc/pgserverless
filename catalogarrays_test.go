package pgserverless

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// OIDs a real database assigns at CREATE TYPE time; pgx knows none of them.
const (
	testEnumOID        uint32 = 16385
	testEnumArrayOID   uint32 = 16390
	testDomainOID      uint32 = 16399
	testDomainArrayOID uint32 = 16400
	oidOIDArrayParam   uint32 = 1028
)

// catalogArrayFake answers the pg_type lookup the way PostgreSQL does for the
// two test array types.
func catalogArrayFake(t *testing.T) *pgfake.PG {
	t.Helper()
	pg := pgfake.New(t)
	pg.SetResult(catalogArrayQuery, pgfake.Result{
		Cols: []pgfake.Col{{Name: "oid", TypeOID: oidOID}, {Name: "typelem", TypeOID: oidOID}},
		Rows: [][]any{
			{testEnumArrayOID, testEnumOID},
			{testDomainArrayOID, testDomainOID},
		},
		ParamOIDs: []uint32{oidOIDArrayParam},
	})
	return pg
}

func catalogColumn(t *testing.T, pg *pgfake.PG, typeOID uint32, value any, headers map[string]string) string {
	t.Helper()
	pg.SetResult("SELECT a FROM t", pgfake.Result{
		Cols: []pgfake.Col{{Name: "a", TypeOID: typeOID}},
		Rows: [][]any{{value}},
	})
	rec := doSQL(newConformanceServer(t, pg), `{"query":"SELECT a FROM t"}`, headers)
	require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

	var resp struct {
		Rows []map[string]json.RawMessage `json:"rows"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Len(t, resp.Rows, 1)
	return string(resp.Rows[0]["a"])
}

// Each literal is what PostgreSQL prints for the array, and want is what Neon
// returns once its typeinfo query has said the column is an array.
func TestConformance_CatalogArraysAsJSONArrays(t *testing.T) {
	tests := []struct {
		name    string
		typeOID uint32
		literal string
		want    string
	}{
		{"enum[] with a quoted label and a NULL", testEnumArrayOID, `{active,"on hold",NULL}`, `["active","on hold",null]`},
		{"two-dimensional enum[]", testEnumArrayOID, `{{a,b},{c,d}}`, `[["a","b"],["c","d"]]`},
		{"empty enum[]", testEnumArrayOID, `{}`, `[]`},
		{"enum[] with explicit bounds", testEnumArrayOID, `[0:1]={a,b}`, `["a","b"]`},
		{"domain over int4, elements stay text like Neon", testDomainArrayOID, `{1,2}`, `["1","2"]`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.JSONEq(t, tc.want, catalogColumn(t, catalogArrayFake(t), tc.typeOID, tc.literal, nil))
		})
	}
}

func TestConformance_CatalogArrayRawTextUnchanged(t *testing.T) {
	got := catalogColumn(t, catalogArrayFake(t), testEnumArrayOID, `{a,b}`, map[string]string{"Neon-Raw-Text-Output": "true"})
	assert.JSONEq(t, `"{a,b}"`, got)
}

// A scalar enum value does not look like an array literal, so no lookup is sent.
func TestConformance_CatalogScalarSkipsTheLookup(t *testing.T) {
	pg := catalogArrayFake(t)
	assert.JSONEq(t, `"active"`, catalogColumn(t, pg, testEnumOID, "active", nil))
	assert.Empty(t, pg.LastBindParams(), "the lookup bound its oid[] parameter")
}

// An extension type whose text happens to start with a brace is not an array.
func TestConformance_CatalogNonArrayTypeUnchanged(t *testing.T) {
	assert.JSONEq(t, `"{x}"`, catalogColumn(t, catalogArrayFake(t), 16500, "{x}", nil))
}

func TestConformance_CatalogArrayLookupFailureKeepsTheString(t *testing.T) {
	assert.JSONEq(t, `"{a,b}"`, catalogColumn(t, pgfake.New(t), testEnumArrayOID, `{a,b}`, nil))
}

func TestConformance_CatalogArraysInBatches(t *testing.T) {
	for _, tc := range []struct {
		name    string
		headers map[string]string
	}{
		{"implicit transaction", nil},
		{"explicit transaction", map[string]string{"Neon-Batch-Read-Only": "true"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pg := catalogArrayFake(t)
			pg.SetResult("SELECT 1 AS one", pgfake.Result{
				Cols: []pgfake.Col{{Name: "one", TypeOID: oidInt4}},
				Rows: [][]any{{int32(1)}},
			})
			pg.SetResult("SELECT a FROM t", pgfake.Result{
				Cols: []pgfake.Col{{Name: "a", TypeOID: testEnumArrayOID}},
				Rows: [][]any{{`{a,b}`}},
			})
			body := `{"queries":[{"query":"SELECT 1 AS one"},{"query":"SELECT a FROM t","arrayMode":true}]}`
			rec := doSQL(newConformanceServer(t, pg), body, tc.headers)
			require.Equal(t, http.StatusOK, rec.Code, rec.Body.String())

			var resp struct {
				Results []struct {
					Rows []json.RawMessage `json:"rows"`
				} `json:"results"`
			}
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			require.Len(t, resp.Results, 2)
			assert.JSONEq(t, `[{"one":1}]`, "["+string(resp.Results[0].Rows[0])+"]")
			assert.JSONEq(t, `[["a","b"]]`, string(resp.Results[1].Rows[0]))
		})
	}
}

// In object mode the later of two same-named columns is the one in the row.
func TestResultCellSkipsAShadowedColumn(t *testing.T) {
	r := &QueryResult{Fields: []FieldDefinition{{Name: "a"}, {Name: "a"}}}
	row := map[string]any{"a": "{x}"}
	_, ok := resultCell(r, row, 0)
	assert.False(t, ok)
	v, ok := resultCell(r, row, 1)
	assert.True(t, ok)
	assert.Equal(t, "{x}", v)
}

// PostgreSQL describes a domain column as its base type, so a domain over an
// array reaches pgx (and Neon) as that array and is never looked up, while an
// array of a domain keeps its catalog-only OID and its elements stay text.
func TestCatalogArraysAgainstPostgres(t *testing.T) {
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	conn, err := pgx.Connect(ctx, url)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close(ctx) })

	for _, stmt := range []string{
		`CREATE TYPE pg_temp.mood AS ENUM ('active', 'on hold')`,
		`CREATE DOMAIN pg_temp.int_d AS int4`,
		`CREATE DOMAIN pg_temp.int_list AS int4[]`,
		`CREATE DOMAIN pg_temp.mood_list AS pg_temp.mood[]`,
	} {
		_, err := conn.Exec(ctx, stmt)
		require.NoError(t, err, stmt)
	}

	res, err := QueryRows(ctx, conn, `SELECT '{1,2}'::pg_temp.int_list AS int_list, `+
		`'{active,"on hold"}'::pg_temp.mood_list AS mood_list, `+
		`'{active,"on hold",NULL}'::pg_temp.mood[] AS moods, `+
		`'{1,2}'::pg_temp.int_d[] AS int_ds, `+
		`'{1,2}'::int4[] AS ints, `+
		`'active'::pg_temp.mood AS mood`, nil, false, false)
	require.NoError(t, err)
	require.Len(t, res.Rows, 1)
	got, err := json.Marshal(res.Rows[0])
	require.NoError(t, err)
	assert.JSONEq(t, `{
		"int_list": [1, 2],
		"mood_list": ["active", "on hold"],
		"moods": ["active", "on hold", null],
		"int_ds": ["1", "2"],
		"ints": [1, 2],
		"mood": "active"
	}`, string(got))
	assert.Equal(t, uint32(1007), res.Fields[0].DataTypeID, "a domain over int4[] is described as int4[]")
	assert.Equal(t, res.Fields[2].DataTypeID, res.Fields[1].DataTypeID, "a domain over mood[] is described as mood[]")
}
