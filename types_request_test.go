package pgserverless

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSQLRequestJSON(t *testing.T) {
	tests := []struct {
		name      string
		input     string
		wantQuery *string
		wantBatch int
		wantArray *bool
	}{
		{
			name:      "single query",
			input:     `{"query":"SELECT 1"}`,
			wantQuery: new("SELECT 1"),
		},
		{
			name:      "single query with params",
			input:     `{"query":"SELECT $1","params":[42]}`,
			wantQuery: new("SELECT $1"),
		},
		{
			name:      "batch mode",
			input:     `{"queries":[{"query":"SELECT 1"},{"query":"SELECT 2"}]}`,
			wantBatch: 2,
		},
		{
			name:      "array mode true",
			input:     `{"query":"SELECT 1","arrayMode":true}`,
			wantQuery: new("SELECT 1"),
			wantArray: new(true),
		},
		{
			name:      "array mode false",
			input:     `{"query":"SELECT 1","arrayMode":false}`,
			wantQuery: new("SELECT 1"),
			wantArray: new(false),
		},
		{
			name:      "omitted optional fields",
			input:     `{"query":"SELECT 1"}`,
			wantQuery: new("SELECT 1"),
		},
		{
			name:  "empty object",
			input: `{}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var req SQLRequest
			err := json.Unmarshal([]byte(tt.input), &req)
			require.NoError(t, err)

			assert.Equal(t, tt.wantQuery, req.Query)
			assert.Len(t, req.Queries, tt.wantBatch)
			assert.Equal(t, tt.wantArray, req.ArrayMode)
		})
	}
}

func TestSQLRequestRoundTrip(t *testing.T) {
	q := "SELECT $1"
	am := true
	original := SQLRequest{
		Query:     &q,
		Params:    []any{float64(1), "hello"},
		ArrayMode: &am,
	}

	data, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded SQLRequest
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, original.Query, decoded.Query)
	assert.Equal(t, original.Params, decoded.Params)
	assert.Equal(t, original.ArrayMode, decoded.ArrayMode)
}

func TestSQLRequestOmitEmpty(t *testing.T) {
	// Query=nil, Params=nil, Queries=nil, ArrayMode=nil should all be omitted
	req := SQLRequest{}
	data, err := json.Marshal(req)
	require.NoError(t, err)

	var m map[string]any
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)

	assert.NotContains(t, m, "query")
	assert.NotContains(t, m, "params")
	assert.NotContains(t, m, "queries")
	assert.NotContains(t, m, "arrayMode")
}

func TestQueryItemJSON(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantQuery  string
		wantParams int
		wantArray  *bool
	}{
		{
			name:      "simple query",
			input:     `{"query":"SELECT 1"}`,
			wantQuery: "SELECT 1",
		},
		{
			name:       "query with params",
			input:      `{"query":"INSERT INTO t VALUES($1,$2)","params":["a",1]}`,
			wantQuery:  "INSERT INTO t VALUES($1,$2)",
			wantParams: 2,
		},
		{
			name:      "per-query array mode",
			input:     `{"query":"SELECT 1","arrayMode":true}`,
			wantQuery: "SELECT 1",
			wantArray: new(true),
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var item QueryItem
			err := json.Unmarshal([]byte(tt.input), &item)
			require.NoError(t, err)

			assert.Equal(t, tt.wantQuery, item.Query)
			assert.Len(t, item.Params, tt.wantParams)
			assert.Equal(t, tt.wantArray, item.ArrayMode)
		})
	}
}

func TestQueryResultJSON(t *testing.T) {
	rowCount := 2
	result := QueryResult{
		Fields: []FieldDefinition{
			{
				Name:             "id",
				TableID:          16384,
				ColumnID:         1,
				DataTypeID:       23,
				DataTypeSize:     4,
				DataTypeModifier: -1,
				Format:           "text",
			},
		},
		Command:    "SELECT",
		RowCount:   &rowCount,
		Rows:       []any{map[string]any{"id": float64(1)}, map[string]any{"id": float64(2)}},
		RowAsArray: false,
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var decoded QueryResult
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, result.Command, decoded.Command)
	assert.Equal(t, result.RowCount, decoded.RowCount)
	assert.Len(t, decoded.Fields, 1)
	assert.Equal(t, "id", decoded.Fields[0].Name)
	assert.Equal(t, uint32(23), decoded.Fields[0].DataTypeID)
	assert.Len(t, decoded.Rows, 2)
	assert.Equal(t, result.RowAsArray, decoded.RowAsArray)
}

func TestQueryResultNilRowCount(t *testing.T) {
	result := QueryResult{
		Fields:   []FieldDefinition{},
		Command:  "CREATE",
		RowCount: nil,
		Rows:     []any{},
	}

	data, err := json.Marshal(result)
	require.NoError(t, err)

	var m map[string]any
	err = json.Unmarshal(data, &m)
	require.NoError(t, err)

	// rowCount should be present but null (not omitempty)
	val, ok := m["rowCount"]
	assert.True(t, ok, "rowCount key should be present")
	assert.Nil(t, val, "rowCount should be null")
}

func TestBatchResponseJSON(t *testing.T) {
	rc1 := 1
	rc2 := 3
	batch := BatchResponse{
		Results: []QueryResult{
			{
				Fields:   []FieldDefinition{{Name: "a", Format: "text"}},
				Command:  "SELECT",
				RowCount: &rc1,
				Rows:     []any{map[string]any{"a": float64(1)}},
			},
			{
				Fields:   []FieldDefinition{{Name: "b", Format: "text"}},
				Command:  "UPDATE",
				RowCount: &rc2,
				Rows:     []any{},
			},
		},
	}

	data, err := json.Marshal(batch)
	require.NoError(t, err)

	var decoded BatchResponse
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	require.Len(t, decoded.Results, 2)
	assert.Equal(t, "SELECT", decoded.Results[0].Command)
	assert.Equal(t, "UPDATE", decoded.Results[1].Command)
}

func TestErrorResponseJSON(t *testing.T) {
	t.Run("minimal error", func(t *testing.T) {
		resp := ErrorResponse{Message: "something went wrong"}
		data, err := json.Marshal(resp)
		require.NoError(t, err)

		var m map[string]any
		err = json.Unmarshal(data, &m)
		require.NoError(t, err)

		assert.Equal(t, "something went wrong", m["message"])
		// Optional fields with omitempty should not be present
		assert.NotContains(t, m, "code")
		assert.NotContains(t, m, "severity")
		assert.NotContains(t, m, "detail")
		assert.NotContains(t, m, "hint")
	})

	t.Run("full PG error", func(t *testing.T) {
		resp := ErrorResponse{
			Message:        "duplicate key value violates unique constraint",
			Code:           new("23505"),
			Severity:       new("ERROR"),
			Detail:         new("Key (id)=(1) already exists."),
			Hint:           new("Check your data"),
			SchemaName:     new("public"),
			TableName:      new("users"),
			ConstraintName: new("users_pkey"),
		}
		data, err := json.Marshal(resp)
		require.NoError(t, err)

		var decoded ErrorResponse
		err = json.Unmarshal(data, &decoded)
		require.NoError(t, err)

		assert.Equal(t, resp.Message, decoded.Message)
		assert.Equal(t, resp.Code, decoded.Code)
		assert.Equal(t, resp.Detail, decoded.Detail)
		assert.Equal(t, resp.SchemaName, decoded.SchemaName)
		assert.Equal(t, resp.TableName, decoded.TableName)
		assert.Equal(t, resp.ConstraintName, decoded.ConstraintName)
	})
}

func TestFieldDefinitionJSON(t *testing.T) {
	fd := FieldDefinition{
		Name:             "total",
		TableID:          16385,
		ColumnID:         3,
		DataTypeID:       1700,
		DataTypeSize:     -1,
		DataTypeModifier: 655366,
		Format:           "text",
	}

	data, err := json.Marshal(fd)
	require.NoError(t, err)

	var decoded FieldDefinition
	err = json.Unmarshal(data, &decoded)
	require.NoError(t, err)

	assert.Equal(t, fd, decoded)
}

func TestConnInfoFields(t *testing.T) {
	ci := ConnInfo{
		Host:     "abc.proxy.pgbeam.app",
		Port:     "5432",
		User:     "admin",
		Password: "secret",
		Database: "mydb",
	}

	assert.Equal(t, "abc.proxy.pgbeam.app", ci.Host)
	assert.Equal(t, "5432", ci.Port)
	assert.Equal(t, "admin", ci.User)
	assert.Equal(t, "secret", ci.Password)
	assert.Equal(t, "mydb", ci.Database)
}

//go:fix inline
func boolPtr(b bool) *bool { return new(b) }
