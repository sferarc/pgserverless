package pgserverless

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseConnectionString(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    *ConnInfo
		wantErr error
	}{
		{
			name:  "valid postgres://",
			input: "postgres://user:pass@db.example.test:5432/mydb",
			want: &ConnInfo{
				Host:     "db.example.test",
				Port:     "5432",
				User:     "user",
				Password: "pass",
				Database: "mydb",
			},
		},
		{
			name:  "valid postgresql://",
			input: "postgresql://user:pass@host.example.com/db",
			want: &ConnInfo{
				Host:     "host.example.com",
				Port:     "5432",
				User:     "user",
				Password: "pass",
				Database: "db",
			},
		},
		{
			name:  "default database from user",
			input: "postgres://myuser:pass@host.example.com",
			want: &ConnInfo{
				Host:     "host.example.com",
				Port:     "5432",
				User:     "myuser",
				Password: "pass",
				Database: "myuser",
			},
		},
		{
			name:    "empty string",
			input:   "",
			wantErr: ErrMissingConnectionString,
		},
		{
			name:    "invalid scheme",
			input:   "mysql://user:pass@host/db",
			wantErr: ErrInvalidConnectionString,
		},
		{
			name:    "missing host",
			input:   "postgres://user:pass@/db",
			wantErr: ErrInvalidConnectionString,
		},
		{
			name:    "missing user",
			input:   "postgres://:pass@host/db",
			wantErr: ErrInvalidConnectionString,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseConnectionString(tt.input)
			if tt.wantErr != nil {
				require.Error(t, err)
				assert.True(t, errors.Is(err, tt.wantErr), "expected %v, got %v", tt.wantErr, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestPgErrorStatusMatchesNeon(t *testing.T) {
	tests := []struct {
		code      string
		statement int
		dial      int
	}{
		{"28P01", 400, 400},
		{"3D000", 400, 400},
		{"42601", 400, 500},
		{"23505", 400, 500},
		{"53300", 400, 500},
		{"57014", 400, 500},
		{"08006", 400, 500},
		{"08P01", 400, 500},
		{"XX000", 400, 500},
	}
	for _, tt := range tests {
		t.Run(tt.code, func(t *testing.T) {
			err := &pgconn.PgError{Code: tt.code, Message: "m", Severity: "ERROR"}
			status, resp := PgErrorResponse(err)
			assert.Equal(t, tt.statement, status)
			require.NotNil(t, resp.Code)
			assert.Equal(t, tt.code, *resp.Code)
			status, _ = DialErrorResponse(err)
			assert.Equal(t, tt.dial, status)
		})
	}
}

func TestSanitizeError(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"connection refused", "connection refused"},
		{"postgresql://user:pass@host/db", "upstream connection failed"},
		{"postgres://user:pass@host/db", "upstream connection failed"},
		{"safe message", "safe message"},
		{"password authentication failed for user x", "password authentication failed for user x"},
		{"host=h user=u password=hunter2 dbname=d", "upstream connection failed"},
		{"password=hunter2", "upstream connection failed"},
		{"host=h PASSWORD=hunter2", "upstream connection failed"},
		{"host=h password = hunter2", "upstream connection failed"},
		{"host=h password='two words' dbname=d", "upstream connection failed"},
		{`host=h password="two words"`, "upstream connection failed"},
		{`host=h password='it\'s'`, "upstream connection failed"},
		{"cannot parse `host=h password=xxxxx`", "upstream connection failed"},
		{"host=h sslpassword=hunter2", "upstream connection failed"},
		{"dial failed (password=hunter2)", "upstream connection failed"},
		{"host=h user=u dbname=d", "host=h user=u dbname=d"},
		{"reset password=", "upstream connection failed"},
		{"dsn:password=hunter2", "upstream connection failed"},
		{"[password=hunter2]", "upstream connection failed"},
		{"{password=hunter2}", "upstream connection failed"},
		{"the new_password=1 field", "the new_password=1 field"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			assert.Equal(t, tt.want, SanitizeError(tt.input))
		})
	}
}

func FuzzSanitizeError(f *testing.F) {
	for _, seed := range []string{
		"host=h password=hunter2",
		"host=h password='two words'",
		`host=h password="two words"`,
		"postgres://u:hunter2@h/d",
		"safe message",
		"",
	} {
		f.Add(seed)
	}
	f.Fuzz(func(t *testing.T, msg string) {
		got := SanitizeError(msg)
		if got != msg && got != "upstream connection failed" {
			t.Fatalf("SanitizeError(%q) = %q, want input unchanged or the generic message", msg, got)
		}
		if keywordPassword.MatchString(msg) && got == msg {
			t.Fatalf("SanitizeError(%q) leaked a keyword password", msg)
		}
	})
}

func TestClassifyError(t *testing.T) {
	tests := []struct {
		err       error
		statement int
		dial      int
		code      string
	}{
		{errors.New("connection refused"), 400, 500, ""},
		{errors.New("no such host"), 400, 500, ""},
		{context.DeadlineExceeded, 400, 400, "08P01"},
		{errors.New("timeout occurred"), 400, 400, "08P01"},
		{errors.New("password authentication failed"), 400, 400, ""},
		{ErrResponseTooLarge, 507, 507, ""},
		{errors.New("something unknown"), 500, 500, ""},
	}
	for _, tt := range tests {
		t.Run(tt.err.Error(), func(t *testing.T) {
			status, _, code := classifyError(tt.err, false)
			assert.Equal(t, tt.statement, status)
			assert.Equal(t, tt.code, code)
			status, _, _ = classifyError(tt.err, true)
			assert.Equal(t, tt.dial, status)
		})
	}
}

// TestErrorResponseUsesTheClientsFieldSpellings pins the wire keys against the
// names the driver actually reads.
//
// This is asserted on the raw JSON on purpose. The conformance test for a full
// PostgreSQL error unmarshals the body back into ErrorResponse, which is the
// same struct that produced it, so it round-trips under any spelling and would
// stay green through a rename in either direction. These five are exactly the
// ones that were wrong before (`schemaName`, `tableName`, `columnName`,
// `dataTypeName`, `constraintName`), which a client silently drops, leaving a
// caller with an error object full of undefined.
func TestErrorResponseUsesTheClientsFieldSpellings(t *testing.T) {
	e := echo.New()
	rec := httptest.NewRecorder()
	c := e.NewContext(httptest.NewRequest(http.MethodPost, "/sql", nil), rec)

	require.NoError(t, HandlePgError(c, &pgconn.PgError{
		Severity: "ERROR", Code: "23505", Message: "duplicate key value violates unique constraint",
		Detail: "Key (id)=(1) already exists.", Hint: "use upsert", Where: "PL/pgSQL function f",
		SchemaName: "public", TableName: "users", ColumnName: "id",
		DataTypeName: "int4", ConstraintName: "users_pkey",
		Position: 17, InternalPosition: 3, InternalQuery: "select 1",
		File: "nbtinsert.c", Line: 673, Routine: "_bt_check_unique",
	}))

	var raw map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &raw))

	for key, want := range map[string]any{
		"schema": "public", "table": "users", "column": "id",
		"dataType": "int4", "constraint": "users_pkey",
		"code": "23505", "severity": "ERROR",
		"detail": "Key (id)=(1) already exists.", "hint": "use upsert",
		"where": "PL/pgSQL function f", "internalQuery": "select 1",
		"file": "nbtinsert.c", "routine": "_bt_check_unique",
		// Numeric PostgreSQL fields are decimal strings: the driver copies them
		// onto its error object verbatim and node-postgres reports them as text.
		"position": "17", "internalPosition": "3", "line": "673",
	} {
		assert.Equal(t, want, raw[key], "field %q", key)
	}

	// The PostgreSQL-side spellings must not appear at all. A struct carrying
	// both would satisfy the assertions above while still sending the dead ones.
	for _, dead := range []string{"schemaName", "tableName", "columnName", "dataTypeName", "constraintName"} {
		assert.NotContains(t, raw, dead,
			"%q is a PostgreSQL field name the client does not read; it must not be on the wire", dead)
	}
}

func TestPgErrorResponse_ParamCountMismatch(t *testing.T) {
	for _, msg := range []string{"expected 1 arguments, got 3", "unused argument: 1"} {
		status, resp := PgErrorResponse(errors.New(msg))
		assert.Equal(t, 400, status)
		require.NotNil(t, resp.Code)
		assert.Equal(t, "08P01", *resp.Code)
	}
}

func TestParseConnectionString_UnparsableURL(t *testing.T) {
	_, err := ParseConnectionString("postgres://user:pass@host:notaport/db")
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrInvalidConnectionString))
}

func TestPgErrorResponse_OptionalFieldsAppearOnlyWhenSent(t *testing.T) {
	bare := &pgconn.PgError{Severity: "ERROR", Code: "XX000", Message: "boom"}
	_, resp := PgErrorResponse(bare)
	assert.Nil(t, resp.Detail)
	assert.Nil(t, resp.Position)
	assert.Nil(t, resp.Line)

	full := &pgconn.PgError{
		Severity: "ERROR", Code: "42601", Message: "syntax error", Hint: "h", Where: "w",
		InternalQuery: "iq", File: "f.c", Routine: "r", Position: 8, InternalPosition: 3, Line: 12,
		SchemaName: "s", TableName: "t", ColumnName: "c", DataTypeName: "d", ConstraintName: "k",
	}
	status, resp := PgErrorResponse(fmt.Errorf("wrapped: %w", full))
	assert.Equal(t, http.StatusBadRequest, status, "a wrapped PgError is still recognized")
	assert.Equal(t, "8", *resp.Position)
	assert.Equal(t, "3", *resp.InternalPosition)
	assert.Equal(t, "12", *resp.Line)
	assert.Equal(t, "iq", *resp.InternalQuery)
	assert.Equal(t, "d", *resp.DataTypeName)
}

func TestSanitizeError_SchemeCase(t *testing.T) {
	for _, msg := range []string{
		"dial POSTGRES://u:secret@h/db failed",
		"cannot reach PostgreSQL://u:secret@h/db",
	} {
		assert.Equal(t, "upstream connection failed", SanitizeError(msg), msg)
	}
}
