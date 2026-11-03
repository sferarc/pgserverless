package pgserverless

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"testing"
	"testing/iotest"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

func encodeBackend(t *testing.T, msgs ...pgproto3.BackendMessage) []byte {
	t.Helper()
	var out []byte
	for _, m := range msgs {
		var err error
		out, err = m.Encode(out)
		require.NoError(t, err)
	}
	return out
}

func TestCopyGuard_StopsAtCopyResponseOnly(t *testing.T) {
	// 'G' (71) as a value byte and as a length byte must not trip it.
	before := encodeBackend(t,
		&pgproto3.DataRow{Values: [][]byte{bytes.Repeat([]byte("G"), 71-4-2-4)}},
		&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")},
	)
	for _, tc := range []struct {
		name string
		msg  pgproto3.BackendMessage
	}{
		{"copy in", &pgproto3.CopyInResponse{}},
		{"copy out", &pgproto3.CopyOutResponse{}},
		{"copy both", &pgproto3.CopyBothResponse{}},
	} {
		stream := append(append([]byte{}, before...), encodeBackend(t, tc.msg)...)
		for _, wrap := range []struct {
			name string
			r    func(io.Reader) io.Reader
		}{
			{"whole", func(r io.Reader) io.Reader { return r }},
			{"one byte", iotest.OneByteReader},
		} {
			t.Run(tc.name+"/"+wrap.name, func(t *testing.T) {
				g := &copyGuard{r: wrap.r(bytes.NewReader(stream)), trip: &copyTrip{}}
				got, err := io.ReadAll(g)
				assert.ErrorIs(t, err, ErrCopyUnsupported)
				assert.Equal(t, before, got, "every message before the copy response reaches pgconn")
				assert.True(t, g.trip.hit())
			})
		}
	}
}

func TestCopyGuard_PassesOtherStreams(t *testing.T) {
	stream := encodeBackend(t,
		&pgproto3.RowDescription{Fields: []pgproto3.FieldDescription{{Name: []byte("G")}}},
		&pgproto3.DataRow{Values: [][]byte{[]byte("GHW")}},
		&pgproto3.CommandComplete{CommandTag: []byte("SELECT 1")},
		&pgproto3.ReadyForQuery{TxStatus: 'I'},
	)
	g := &copyGuard{r: iotest.OneByteReader(bytes.NewReader(stream)), trip: &copyTrip{}}
	got, err := io.ReadAll(g)
	require.NoError(t, err)
	assert.Equal(t, stream, got)
	assert.False(t, g.trip.hit())
}

func assertCopyRefused(t *testing.T, h http.Handler, body string, headers map[string]string) {
	t.Helper()
	start := time.Now()
	rec := doSQLUntil(t, h, body, headers, 5*time.Second)
	assert.Less(t, time.Since(start), 3*time.Second, "answered at once, not at the request deadline")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	var resp ErrorResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.NotNil(t, resp.Code)
	assert.Equal(t, "0A000", *resp.Code)
	assert.Equal(t, ErrCopyUnsupported.Error(), resp.Message)
}

// Neon's tokio-postgres fails a statement on any copy response, which its proxy answers 400.
func TestCopy_RefusedAtOnce(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("COPY t FROM STDIN", pgfake.Result{CopyIn: true})
	pg.SetResult("COPY t TO STDOUT", pgfake.Result{CopyOut: true, Rows: [][]any{{int32(1)}}})
	pg.SetResult("INSERT INTO t VALUES (1)", pgfake.Result{Tag: "INSERT 0 1", IsWrite: true})
	h := newConformanceServer(t, pg)

	for _, mode := range []struct {
		name    string
		headers map[string]string
	}{
		{"raw text", driverHeaders},
		{"native", nil},
	} {
		t.Run(mode.name+"/from stdin", func(t *testing.T) {
			assertCopyRefused(t, h, `{"query":"COPY t FROM STDIN"}`, mode.headers)
		})
		t.Run(mode.name+"/to stdout", func(t *testing.T) {
			assertCopyRefused(t, h, `{"query":"COPY t TO STDOUT"}`, mode.headers)
		})
		t.Run(mode.name+"/batch", func(t *testing.T) {
			assertCopyRefused(t, h, `{"queries":[{"query":"INSERT INTO t VALUES (1)"},{"query":"COPY t FROM STDIN"}]}`, mode.headers)
			assert.Empty(t, pg.CommittedWrites(), "the batch's earlier write is not committed")
		})
	}
}

func TestCopy_RefusedAtOnceOnSimpleProtocol(t *testing.T) {
	pg := pgfake.New(t)
	pg.SetResult("COPY t FROM STDIN", pgfake.Result{CopyIn: true})
	pg.SetResult("COPY t TO STDOUT", pgfake.Result{CopyOut: true, Rows: [][]any{{int32(1)}}})
	h := newGovernedConformanceServer(pg, semicolonCount)

	assertCopyRefused(t, h, `{"query":"COPY t FROM STDIN"}`, driverHeaders)
	assertCopyRefused(t, h, `{"query":"COPY t TO STDOUT"}`, driverHeaders)
	assertCopyRefused(t, h, `{"queries":[{"query":"COPY t FROM STDIN"}]}`, driverHeaders)
}

func TestErrorResponse_CopyUnsupported(t *testing.T) {
	status, resp := PgErrorResponse(errors.Join(errors.New("conn closed"), ErrCopyUnsupported))
	assert.Equal(t, http.StatusBadRequest, status)
	require.NotNil(t, resp.Code)
	assert.Equal(t, "0A000", *resp.Code)
}
