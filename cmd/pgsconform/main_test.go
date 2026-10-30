package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"testing"
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
