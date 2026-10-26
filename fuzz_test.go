package pgserverless

import (
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"
)

func FuzzDecodeSQLRequest(f *testing.F) {
	for _, s := range []string{
		`{"query":"select 1"}`,
		`{"query":"select $1","params":[1,"a",null,[1,2],{"k":"v"}],"arrayMode":true}`,
		`{"queries":[{"query":"select 1"},{"query":"select 2","arrayMode":false}]}`,
		`{"queries":[]}`,
		`{"query":""}`,
		`[]`,
		`{`,
		``,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, body string) {
		req := httptest.NewRequest(http.MethodPost, "/sql", strings.NewReader(body))

		got, err := decodeSQLRequest(req)
		if err != nil {
			if got != nil {
				t.Fatalf("error %v returned with a request", err)
			}
			return
		}
		if got.Queries == nil && (got.Query == nil || *got.Query == "") {
			t.Fatal("accepted a request with neither a query nor a batch")
		}
		if len(got.Queries) > MaxBatchQueries {
			t.Fatalf("accepted a batch of %d queries", len(got.Queries))
		}

		// Params reach pgx through ConvertParams, so a decoded body must survive it.
		ConvertParams(got.Params)
		for _, q := range got.Queries {
			ConvertParams(q.Params)
		}
	})
}

func FuzzSQLRequestRoundTrip(f *testing.F) {
	f.Add(`{"query":"select 1","params":[1.5,"x",null,true],"arrayMode":true}`)
	f.Add(`{"queries":[{"query":"q","params":[[1,[2]]]}]}`)
	f.Fuzz(func(t *testing.T, body string) {
		var first SQLRequest
		if json.Unmarshal([]byte(body), &first) != nil {
			return
		}
		b, err := json.Marshal(first)
		if err != nil {
			t.Fatalf("marshal a decoded request: %v", err)
		}
		var second SQLRequest
		if err := json.Unmarshal(b, &second); err != nil {
			t.Fatalf("decode own output %s: %v", b, err)
		}
		// omitempty folds an empty slice into an absent one, so compare after the same fold.
		first.Params, first.Queries = foldEmpty(first.Params), foldEmptyItems(first.Queries)
		if !reflect.DeepEqual(first, second) {
			t.Fatalf("round trip changed the request: %+v != %+v", first, second)
		}
	})
}

func foldEmpty(p []any) []any {
	if len(p) == 0 {
		return nil
	}
	return p
}

func foldEmptyItems(q []QueryItem) []QueryItem {
	if len(q) == 0 {
		return nil
	}
	for i := range q {
		q[i].Params = foldEmpty(q[i].Params)
	}
	return q
}

// arrayElems decodes a JSON array into the shapes encoding/json gives the handler.
func arrayElems(s string) ([]any, bool) {
	var elems []any
	if json.Unmarshal([]byte(s), &elems) != nil {
		return nil, false
	}
	return elems, true
}

func FuzzFormatPgArray(f *testing.F) {
	for _, s := range []string{
		`[]`,
		`["a","b"]`,
		`["", "NULL", "null", " pad ", "a,b", "{x}", "q\"uote", "back\\slash"]`,
		`[1, 2.5, null, 1e21]`,
		`[[1,2],[3,4]]`,
		`[{"a":[1]}]`,
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		elems, ok := arrayElems(s)
		if !ok {
			return
		}
		lit := formatPgArray(elems)
		if lit == "" || lit[0] != '{' || lit[len(lit)-1] != '}' {
			t.Fatalf("not an array literal: %q", lit)
		}

		flat := true
		for _, e := range elems {
			switch e.(type) {
			case nil, string:
			default:
				flat = false
			}
		}
		if !flat {
			return
		}
		// A flat array of strings and NULLs is what the quoting rules exist to
		// carry intact, so parsing it back must give the same elements.
		got, err := parsePgArray([]byte(lit), 25)
		if err != nil {
			t.Fatalf("parse own output %q: %v", lit, err)
		}
		if len(elems) == 0 {
			if len(got) != 0 {
				t.Fatalf("empty array came back as %v", got)
			}
			return
		}
		if !reflect.DeepEqual(got, elems) {
			t.Fatalf("round trip of %q: %#v != %#v", lit, got, elems)
		}
	})
}

func FuzzParsePgArray(f *testing.F) {
	for _, s := range []string{`{}`, `{1,2,3}`, `{"a b",NULL}`, `{{1,2},{3,4}}`, `{"unterminated}`, `{\`, `x`} {
		f.Add(s, oidInt4)
	}
	oids := []uint32{oidBool, oidInt2, oidInt4, oidInt8, oidFloat4, oidFloat8, oidNumeric, oidJSON, oidJSONB, 25}
	f.Fuzz(func(t *testing.T, s string, pick uint32) {
		oid := oids[int(pick)%len(oids)]
		if _, err := parsePgArray([]byte(s), oid); err != nil {
			return
		}
		convertTextValue([]byte(s), oid)
	})
}

func FuzzQuoting(f *testing.F) {
	for _, s := range []string{"", "plain", "NULL", "a b", `a"b`, `a\b`, "{}", "[1,2)", "é"} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		for name, q := range map[string]string{"array": quotePgArrayElem(s), "range": quoteRangeBound(s)} {
			if q == s {
				if s == "" || (strings.EqualFold(s, "NULL") && name == "array") {
					t.Fatalf("%s: %q was left bare", name, s)
				}
				continue
			}
			if len(q) < 2 || q[0] != '"' || q[len(q)-1] != '"' {
				t.Fatalf("%s: %q quoted as %q", name, s, q)
			}
		}
	})
}

func FuzzCoerceNeonValue(f *testing.F) {
	f.Add(uint8(0), int64(math.MaxInt64), "", oidInt8)
	f.Add(uint8(1), int64(0), "12345.6789", oidNumeric)
	f.Add(uint8(2), int64(0x7ff8000000000001), "", oidFloat8)
	f.Add(uint8(3), int64(0), "", oidUUID)
	f.Add(uint8(4), int64(0), "10.1.2.3/32", oidInet)
	f.Add(uint8(5), int64(0), "{$1.00,\"$1,000.00\"}", oidMoneyArray)
	f.Add(uint8(6), int64(1), "x", oidInt8Array)

	oids := []uint32{
		oidBool, oidInt2, oidInt4, oidInt8, oidFloat4, oidFloat8, oidMoney, oidNumeric,
		oidJSON, oidUUID, oidUUIDArray, oidMoneyArray, oidInt8Array, oidNumericArr,
		oidInet, oidInetArray, oidBytea, oidDate, oidTimestamp, oidInterval, oidMacaddr,
		oidBit, oidTSVector, oidRecord, 25, 0,
	}
	m := pgtype.NewMap()
	f.Fuzz(func(t *testing.T, kind uint8, n int64, s string, pick uint32) {
		oid := oids[int(pick)%len(oids)]
		var v any
		switch kind % 8 {
		case 0:
			v = n
		case 1:
			var num pgtype.Numeric
			if num.Scan(s) != nil {
				return
			}
			v = num
		case 2:
			v = math.Float64frombits(uint64(n))
		case 3:
			var u [16]byte
			for i := range u {
				u[i] = byte(n >> (8 * (i % 8)))
			}
			v = u
		case 4:
			p, err := netip.ParsePrefix(s)
			if err != nil {
				return
			}
			v = p
		case 5:
			v = s
		case 6:
			v = []any{n, s, nil}
		default:
			v = pgtype.InfinityModifier(n)
		}

		for _, tm := range []*pgtype.Map{m, nil} {
			out := coerceNeonValue(tm, v, oid)
			if kind%8 == 2 && (oid == oidFloat4 || oid == oidFloat8) {
				if _, err := json.Marshal(out); err != nil {
					t.Fatalf("float %v did not marshal after coercion: %v", v, err)
				}
			}
		}

		out := coerceNeonValue(m, v, oid)
		switch {
		case kind%8 == 0 && oid == oidInt8:
			if out != strconv.FormatInt(n, 10) {
				t.Fatalf("int8 %d coerced to %v", n, out)
			}
		case kind%8 == 1 && (oid == oidNumeric || oid == oidMoney):
			got, ok := out.(string)
			if !ok {
				t.Fatalf("numeric %v coerced to %T", v, out)
			}
			var back pgtype.Numeric
			if err := back.Scan(got); err != nil {
				t.Fatalf("numeric text %q does not parse: %v", got, err)
			}
			again, _ := back.Value()
			if again != got {
				t.Fatalf("numeric text %q reprinted as %v", got, again)
			}
		case kind%8 == 3 && oid == oidUUID:
			if got, ok := out.(string); !ok || len(got) != 36 {
				t.Fatalf("uuid coerced to %#v", out)
			}
		case kind%8 == 4 && oid == oidInet:
			got, ok := out.(string)
			if !ok {
				return
			}
			p := v.(netip.Prefix)
			if p.Bits() == p.Addr().BitLen() {
				if got != p.Addr().String() {
					t.Fatalf("host %v rendered as %q", p, got)
				}
			}
		}
	})
}

func FuzzParseConnectionString(f *testing.F) {
	for _, s := range []string{
		"postgres://u:p@h:5432/db",
		"postgresql://u@h",
		"postgres://u:p%40x@h/d?sslmode=require",
		"postgres://[::1]:5432/db",
		"mysql://u@h",
		"",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, s string) {
		ci, err := ParseConnectionString(s)
		if err != nil {
			if ci != nil {
				t.Fatal("error returned with a result")
			}
			if !errors.Is(err, ErrMissingConnectionString) && !errors.Is(err, ErrInvalidConnectionString) {
				t.Fatalf("unclassified error %v", err)
			}
			return
		}
		if ci.Host == "" || ci.User == "" || ci.Port == "" || ci.Database == "" {
			t.Fatalf("accepted an incomplete connection %+v from %q", ci, s)
		}
	})
}

func FuzzTxnOptions(f *testing.F) {
	f.Add("Serializable", "true", "false")
	f.Add("read committed", "1", "")
	f.Add("", "", "")
	f.Add("bogus", "yes", "x")
	f.Fuzz(func(t *testing.T, level, ro, def string) {
		h := http.Header{}
		h.Set("Batch-Isolation-Level", level)
		h.Set("Batch-Read-Only", ro)
		h.Set("Batch-Deferrable", def)
		opts, err := parseTxnOptions(h)
		if err != nil {
			return
		}
		sql := opts.beginSQL()
		if !strings.HasPrefix(sql, "BEGIN") {
			t.Fatalf("begin statement %q", sql)
		}
		for _, r := range sql {
			if r != ' ' && (r < 'A' || r > 'Z') {
				t.Fatalf("input leaked into the statement: %q", sql)
			}
		}
		if opts.isDefault() != (sql == "BEGIN") {
			t.Fatalf("isDefault disagrees with %q", sql)
		}
	})
}

func FuzzErrorHandling(f *testing.F) {
	for _, s := range []string{
		"expected 2 arguments, got 1",
		"dial tcp: connection refused",
		"postgres://u:secret@h/db failed",
		"password for user@host rejected",
		"",
	} {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, msg string) {
		status, resp := PgErrorResponse(errors.New(msg))
		if status < 400 || status > 599 {
			t.Fatalf("status %d for %q", status, msg)
		}
		if _, err := json.Marshal(resp); err != nil {
			t.Fatalf("response did not marshal: %v", err)
		}
		if got := SanitizeError(msg); strings.Contains(strings.ToLower(got), "postgres://") || strings.Contains(strings.ToLower(got), "postgresql://") {
			t.Fatalf("connection string survived: %q", got)
		}
		parseCommandTag(msg)
		parseHostPort(msg)
	})
}
