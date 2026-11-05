package pgserverless

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"unicode/utf8"
)

// context7.json sits at the published repository's root, where Context7 reads it.
type context7Config struct {
	Schema         string   `json:"$schema"`
	ProjectTitle   string   `json:"projectTitle"`
	Description    string   `json:"description"`
	ExcludeFolders []string `json:"excludeFolders"`
	Rules          []string `json:"rules"`
}

func readContext7Config(t *testing.T) context7Config {
	t.Helper()
	raw, err := os.ReadFile("context7.json")
	if err != nil {
		t.Fatal(err)
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var cfg context7Config
	if err := dec.Decode(&cfg); err != nil {
		t.Fatalf("context7.json: %v", err)
	}
	return cfg
}

func TestContext7ConfigStaysInsideSchemaLimits(t *testing.T) {
	cfg := readContext7Config(t)
	if cfg.Schema != "https://context7.com/schema/context7.json" {
		t.Errorf("$schema = %q", cfg.Schema)
	}
	if n := utf8.RuneCountInString(cfg.ProjectTitle); n < 1 || n > 100 {
		t.Errorf("projectTitle has %d characters, want 1 to 100", n)
	}
	if n := utf8.RuneCountInString(cfg.Description); n < 10 || n > 500 {
		t.Errorf("description has %d characters, want 10 to 500", n)
	}
	if n := len(cfg.Rules); n < 1 || n > 50 {
		t.Errorf("%d rules, want 1 to 50", n)
	}
	// Context7 drops every rule when one is rejected, so one overlong rule costs all of them.
	for _, rule := range cfg.Rules {
		if n := utf8.RuneCountInString(rule); n < 1 || n > 255 {
			t.Errorf("rule has %d characters, want 1 to 255: %q", n, rule)
		}
	}
	for _, dir := range cfg.ExcludeFolders {
		if info, err := os.Stat(dir); err != nil || !info.IsDir() {
			t.Errorf("excludeFolders names %q, which is not a directory here", dir)
		}
	}
}

func TestContext7RulesStateOnlyWhatTheDocsSay(t *testing.T) {
	rules := strings.Join(readContext7Config(t).Rules, "\n")
	facts := []struct{ fact, file string }{
		{`neonConfig.fetchEndpoint = "http://localhost:4000/sql"`, "README.md"},
		{`neonConfig.wsProxy = "localhost:4000/v2"`, "README.md"},
		{"neonConfig.pipelineConnect = false", "README.md"},
		{"2^53", "README.md"},
		{"HTTPAdmission", "README.md"},
		{"*AdmissionError", "README.md"},
		{"WithHTTPWebSocketAdmission", "README.md"},
		{"TLSMode: pgserverless.TLSDisable", "README.md"},
		{"go install github.com/sferarc/pgserverless/cmd/pgserverless@latest", "cmd/pgserverless/README.md"},
		{"127.0.0.1:4000", "cmd/pgserverless/README.md"},
		{"go run ./cmd/pgsconform", "README.md"},
	}
	for _, f := range facts {
		if !strings.Contains(rules, f.fact) {
			t.Errorf("no rule says %q", f.fact)
		}
		doc, err := os.ReadFile(f.file)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(doc), f.fact) {
			t.Errorf("%s no longer says %q, so the rule that does is stale", f.file, f.fact)
		}
	}
}
