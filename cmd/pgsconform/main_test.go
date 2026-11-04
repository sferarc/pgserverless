package main

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestEqualJSONTextExact(t *testing.T) {
	tests := []struct {
		name string
		want string
		got  string
		eq   bool
	}{
		{"number is not string", `1`, `"1"`, false},
		{"escaped angle bracket", `"<a>"`, `"\u003ca\u003e"`, true},
		{"whitespace", `[1, 2]`, `[1,2]`, true},
		{"key order matters", `{"b":1,"a":2}`, `{"a":2,"b":1}`, false},
		{"duplicate keys matter", `{"a":1,"a":2}`, `{"a":2}`, false},
		{"trailing data", `"x"`, `"x" junk`, false},
		{"number spelling", `1.0`, `1`, false},
		{"big integer kept", `9223372036854775807`, `9223372036854775807`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := equalJSONText(tt.want, json.RawMessage(tt.got), false); got != tt.eq {
				t.Fatalf("equalJSONText(%s, %s) = %v, want %v", tt.want, tt.got, got, tt.eq)
			}
		})
	}
}

func TestListsAlternatives(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want bool
	}{
		{"alternatives", `{"rows":[{"v":{"json":"1","also":["2"]}}]}`, true},
		{"column named also", `{"rows":[{"also":{"json":"1"}}]}`, false},
		{"no alternatives", `{"rows":[{"v":{"json":"1"}}]}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := listsAlternatives(json.RawMessage(tt.raw)); got != tt.want {
				t.Fatalf("listsAlternatives(%s) = %v, want %v", tt.raw, got, tt.want)
			}
		})
	}
}

func TestCheckRowsAlso(t *testing.T) {
	exp := json.RawMessage(`[{"v":{"json":"\"2024-01-01 00:00:00\"","also":["\"2024-01-01T00:00:00\""]}}]`)
	got := json.RawMessage(`[{"v":"2024-01-01T00:00:00"}]`)

	if msg := checkRows(exp, got, true); msg != "" {
		t.Fatalf("loose: %s", msg)
	}
	if msg := checkRows(exp, got, false); msg == "" {
		t.Fatal("exact comparison accepted an alternative spelling")
	}
}

func TestLoadCorpusRejectsAlsoOnExactCase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	body := `{"spec_version":"0.1.0","version":1,"cases":[{"id":"x","expect":{"rows":[{"v":{"json":"1","also":["2"]}}]}}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCorpus(path); err == nil {
		t.Fatal("want an error for also on an exact case")
	}
}

func TestLoadCorpusRequiresSpecVersion(t *testing.T) {
	for _, v := range []string{``, `"spec_version":"0.1",`, `"spec_version":"v0.1.0",`} {
		path := filepath.Join(t.TempDir(), "c.json")
		body := `{` + v + `"version":1,"cases":[{"id":"x","expect":{}}]}`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadCorpus(path); err == nil {
			t.Fatalf("want an error for %q", v)
		}
	}
}

// The corpus names the spec version its cases check, so the two cannot drift.
func TestPublishedCorpusMatchesSpecVersion(t *testing.T) {
	c, err := loadCorpus(filepath.Join("..", "..", "spec", "cases", "v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	spec, err := os.ReadFile(filepath.Join("..", "..", "spec", "SPEC.md"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^Version (\S+), `).FindSubmatch(spec)
	if m == nil {
		t.Fatal("SPEC.md has no \"Version X.Y.Z, ...\" line")
	}
	if got := string(m[1]); got != c.SpecVersion {
		t.Fatalf("SPEC.md is version %s but cases/v1.json declares spec_version %s", got, c.SpecVersion)
	}
}

func TestCheckResults(t *testing.T) {
	var exp expectation
	if err := json.Unmarshal([]byte(`{"status":200,"results":[{"rows":[{"v":{"json":"\"one\""}}]},{"command":"SELECT","rows":[{"v":{"json":"\"two\""}}]}]}`), &exp); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name string
		body string
		ok   bool
	}{
		{"in order", `{"results":[{"rows":[{"v":"one"}]},{"command":"SELECT","rows":[{"v":"two"}]}]}`, true},
		{"swapped", `{"results":[{"rows":[{"v":"two"}]},{"command":"SELECT","rows":[{"v":"one"}]}]}`, false},
		{"one short", `{"results":[{"rows":[{"v":"one"}]}]}`, false},
		{"single result instead", `{"command":"SELECT","rows":[{"v":"one"}]}`, false},
		{"null", `{"results":null}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tt.body), &got); err != nil {
				t.Fatal(err)
			}
			if msg := checkResult(exp, got, false); (msg == "") != tt.ok {
				t.Fatalf("checkResult(%s) = %q, want ok=%v", tt.body, msg, tt.ok)
			}
		})
	}
}

func TestCheckFieldsFormat(t *testing.T) {
	text := "text"
	exp := []expectedField{{Name: "id", Format: &text}}
	tests := []struct {
		body string
		ok   bool
	}{
		{`[{"name":"id","dataTypeID":23,"format":"text"}]`, true},
		{`[{"name":"id","dataTypeID":23,"format":"binary"}]`, false},
		{`[{"name":"id","dataTypeID":23}]`, false},
	}
	for _, tt := range tests {
		if msg := checkFields(exp, json.RawMessage(tt.body)); (msg == "") != tt.ok {
			t.Errorf("checkFields(%s) = %q, want ok=%v", tt.body, msg, tt.ok)
		}
	}
	if msg := checkFields([]expectedField{{Name: "id"}}, json.RawMessage(`[{"name":"id","format":"binary"}]`)); msg != "" {
		t.Errorf("format checked without being expected: %q", msg)
	}
}

func TestCheckResultRowCount(t *testing.T) {
	tests := []struct {
		name   string
		expect string
		body   string
		ok     bool
	}{
		{"null is null", `{"row_count":null}`, `{"rowCount":null}`, true},
		{"null is not 0", `{"row_count":null}`, `{"rowCount":0}`, false},
		{"null needs the member", `{"row_count":null}`, `{}`, false},
		{"count matches", `{"row_count":1}`, `{"rowCount":1}`, true},
		{"0 is not null", `{"row_count":0}`, `{"rowCount":null}`, false},
		{"0 needs the member", `{"row_count":0}`, `{}`, false},
		{"0 is not a string", `{"row_count":0}`, `{"rowCount":"0"}`, false},
		{"OID read as the count", `{"row_count":1}`, `{"rowCount":0}`, false},
		{"unchecked", `{}`, `{"rowCount":"anything"}`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var exp expectation
			if err := json.Unmarshal([]byte(tt.expect), &exp); err != nil {
				t.Fatal(err)
			}
			var got map[string]json.RawMessage
			if err := json.Unmarshal([]byte(tt.body), &got); err != nil {
				t.Fatal(err)
			}
			if msg := checkResult(exp, got, false); (msg == "") != tt.ok {
				t.Fatalf("checkResult(%s) against %s = %q, want ok=%v", tt.body, tt.expect, msg, tt.ok)
			}
		})
	}
}

func TestCheckErrorCodes(t *testing.T) {
	exp := &expectedError{Codes: []string{"22021", "08P01"}}
	tests := []struct {
		name string
		body string
		ok   bool
	}{
		{"first", `{"code":"22021"}`, true},
		{"second", `{"code":"08P01"}`, true},
		{"other", `{"code":"XX000"}`, false},
		{"missing", `{"message":"bad"}`, false},
		{"null", `{"code":null}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if msg := checkError(exp, []byte(tt.body)); (msg == "") != tt.ok {
				t.Fatalf("checkError(%s) = %q, want ok=%v", tt.body, msg, tt.ok)
			}
		})
	}
}

func TestCheckErrorMessageContains(t *testing.T) {
	exp := &expectedError{MessageContains: "Read-Only"}
	tests := []struct {
		name string
		body string
		ok   bool
	}{
		{"contains", `{"message":"invalid Batch-Read-Only value"}`, true},
		{"other message", `{"message":"bad request"}`, false},
		{"case differs", `{"message":"invalid batch-read-only value"}`, false},
		{"only in another field", `{"message":"bad","detail":"Read-Only"}`, false},
		{"missing", `{"code":"08P01"}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if msg := checkError(exp, []byte(tt.body)); (msg == "") != tt.ok {
				t.Fatalf("checkError(%s) = %q, want ok=%v", tt.body, msg, tt.ok)
			}
		})
	}
}

func TestCheckErrorMustNotContain(t *testing.T) {
	exp := &expectedError{MustNotContain: []string{"s3cret", "user@db"}}
	tests := []struct {
		name string
		body string
		ok   bool
	}{
		{"clean", `{"message":"connection refused"}`, true},
		{"in message", `{"message":"cannot reach postgres://u:s3cret@db"}`, false},
		{"in another field", `{"message":"bad","detail":"user@db"}`, false},
		{"json escaped", `{"message":"cannot reach user\u0040db"}`, false},
		{"in a key", `{"message":"bad","s3cret":1}`, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if msg := checkError(exp, []byte(tt.body)); (msg == "") != tt.ok {
				t.Fatalf("checkError(%s) = %q, want ok=%v", tt.body, msg, tt.ok)
			}
		})
	}
}

func TestLoadCorpusRejectsAmbiguousErrorCodes(t *testing.T) {
	for _, e := range []string{
		`{"code":"22021","codes":["22021","08P01"]}`,
		`{"codes":["22021"]}`,
		`{"codes":[]}`,
	} {
		path := filepath.Join(t.TempDir(), "c.json")
		body := `{"spec_version":"0.1.0","version":1,"cases":[{"id":"x","expect":{"error":` + e + `}}]}`
		if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadCorpus(path); err == nil {
			t.Fatalf("want an error for %s", e)
		}
	}
}

// A refusal answered only at the server's own timeout must fail a case that asks for an answer at once.
func TestAnsweredWithinFailsALateAnswer(t *testing.T) {
	tests := []struct {
		name       string
		headerWait time.Duration
		bodyWait   time.Duration
		ok         bool
	}{
		{"at once", 0, 0, true},
		{"late status", 300 * time.Millisecond, 0, false},
		{"late body", 0, 300 * time.Millisecond, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				time.Sleep(tt.headerWait)
				w.WriteHeader(http.StatusBadRequest)
				w.(http.Flusher).Flush()
				time.Sleep(tt.bodyWait)
				_, _ = w.Write([]byte(`{"message":"refused"}`))
			}))
			defer srv.Close()
			c := kase{ID: "x", Request: requestSpec{Path: "/sql", Body: json.RawMessage(`{"query":"COPY (SELECT 1) TO STDOUT"}`)}}
			status := http.StatusBadRequest
			exp := expectation{Status: &status, WithinMS: 100, Error: &expectedError{}}
			got, detail := (&runner{server: srv.URL}).runHTTP(context.Background(), c, exp, target{server: srv.URL})
			if (got == outcomePass) != tt.ok || (!tt.ok && !strings.Contains(detail, "within 100 ms")) {
				t.Fatalf("runHTTP = %v %q, want ok=%v", got, detail, tt.ok)
			}
		})
	}
}

// A batch refused for COPY must fail its case when the follow-up request still sees the batch's write.
func TestThenObservesWhatTheRequestLeftBehind(t *testing.T) {
	tests := []struct {
		name      string
		committed bool
		ok        bool
	}{
		{"rolled back", false, true},
		{"committed", true, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, _ := io.ReadAll(r.Body)
				if strings.Contains(string(body), "queries") {
					w.WriteHeader(http.StatusBadRequest)
					_, _ = w.Write([]byte(`{"message":"refused"}`))
					return
				}
				result := `{"command":"SELECT","rowCount":0,"rows":[]}`
				if tt.committed {
					result = `{"command":"SELECT","rowCount":1,"rows":[["4"]]}`
				}
				_, _ = w.Write([]byte(result))
			}))
			defer srv.Close()
			c := kase{
				ID:      "x",
				Request: requestSpec{Path: "/sql", Body: json.RawMessage(`{"queries":[{"query":"INSERT"},{"query":"COPY"}]}`)},
				Expect:  json.RawMessage(`{"status":400,"error":{}}`),
				Then: &followUp{
					Request: requestSpec{Path: "/sql", Body: json.RawMessage(`{"query":"SELECT"}`)},
					Expect:  json.RawMessage(`{"status":200,"row_count":0,"rows":[]}`),
				},
			}
			got, detail := (&runner{server: srv.URL}).runCase(context.Background(), c)
			if (got == outcomePass) != tt.ok || (!tt.ok && !strings.HasPrefix(detail, "then: ")) {
				t.Fatalf("runCase = %v %q, want ok=%v", got, detail, tt.ok)
			}
		})
	}
}

func TestLoadCorpusRefusesThenOnAWebSocketCase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.json")
	body := `{"spec_version":"0.5.0","version":2,"cases":[{"id":"x","request":{"path":"/v2","method":"GET"},"expect":{"status":101},"then":{"request":{"path":"/sql"},"expect":{"status":200}}}]}`
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := loadCorpus(path); err == nil || !strings.Contains(err.Error(), "then") {
		t.Fatalf("loadCorpus = %v, want a then error", err)
	}
}
