package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const oneCaseCorpus = `{"spec_version":"0.1.0","version":1,"cases":[
	{"id":"core/select-one","level":1,"spec":"5","request":{"path":"/sql","method":"POST","body":{"query":"SELECT 1"}},"expect":{"status":200,"command":"SELECT"}}
]}`

func writeCorpus(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cases.json")
	if err := os.WriteFile(path, []byte(oneCaseCorpus), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// answering serves POST /sql with status and a SELECT result, and any other request with 404 as a real server would.
func answering(t *testing.T, status int) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(`{"command":"SELECT","rowCount":1,"rows":[],"fields":[]}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func fixturesApplied(context.Context, string, *corpus) (string, error) { return "17.0", nil }

func TestRunExitCodes(t *testing.T) {
	cases := writeCorpus(t)
	gone := httptest.NewServer(http.NotFoundHandler())
	gone.Close()
	failingFixtures := func(context.Context, string, *corpus) (string, error) {
		return "", errors.New("connection refused")
	}

	tests := []struct {
		name  string
		args  []string
		apply fixtureApplier
		want  int
	}{
		{"every case passed", []string{"-server", answering(t, http.StatusOK).URL}, fixturesApplied, exitPassed},
		{"a case failed", []string{"-server", answering(t, http.StatusInternalServerError).URL}, fixturesApplied, exitFailed},
		{"unknown flag", []string{"-no-such-flag"}, fixturesApplied, exitSetup},
		{"stray argument", []string{"-server", answering(t, http.StatusOK).URL, "extra"}, fixturesApplied, exitSetup},
		{"missing corpus", []string{"-cases", filepath.Join(t.TempDir(), "none.json")}, fixturesApplied, exitSetup},
		{"fixtures failed", []string{"-server", answering(t, http.StatusOK).URL}, failingFixtures, exitSetup},
		{"server unreachable", []string{"-server", gone.URL}, fixturesApplied, exitSetup},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args := append([]string{"-cases", cases, "-dsn", "postgres://u:p@127.0.0.1:1/db"}, tt.args...)
			var stdout, stderr bytes.Buffer
			if got := run(args, &stdout, &stderr, tt.apply); got != tt.want {
				t.Fatalf("exit %d, want %d\nstdout:\n%s\nstderr:\n%s", got, tt.want, stdout.String(), stderr.String())
			}
		})
	}
}

func TestRunWithoutDSNIsASetupError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if got := run([]string{"-cases", writeCorpus(t)}, &stdout, &stderr, fixturesApplied); got != exitSetup {
		t.Fatalf("exit %d, want %d", got, exitSetup)
	}
	if !strings.Contains(stderr.String(), "-dsn is required") {
		t.Fatalf("stderr %q does not name the missing flag", stderr.String())
	}
}

func TestRunUnreachableDatabaseIsASetupError(t *testing.T) {
	args := []string{"-cases", writeCorpus(t), "-server", answering(t, http.StatusOK).URL,
		"-dsn", "postgres://u:p@127.0.0.1:1/db?sslmode=disable&connect_timeout=5"}
	var stdout, stderr bytes.Buffer
	if got := run(args, &stdout, &stderr, applyFixtures); got != exitSetup {
		t.Fatalf("exit %d, want %d\nstderr:\n%s", got, exitSetup, stderr.String())
	}
}

func TestRunReportToStdout(t *testing.T) {
	args := []string{"-cases", writeCorpus(t), "-dsn", "postgres://u:p@127.0.0.1:1/db",
		"-server", answering(t, http.StatusInternalServerError).URL, "-report", "-"}
	var stdout, stderr bytes.Buffer
	if got := run(args, &stdout, &stderr, fixturesApplied); got != exitFailed {
		t.Fatalf("exit %d, want %d", got, exitFailed)
	}

	var rep report
	dec := json.NewDecoder(&stdout)
	dec.DisallowUnknownFields()
	if err := dec.Decode(&rep); err != nil {
		t.Fatalf("stdout is not one JSON report: %v", err)
	}
	if dec.More() {
		t.Fatal("stdout carries more than the JSON report")
	}
	if len(rep.Failures) != 1 || rep.Failures[0].ID != "core/select-one" {
		t.Fatalf("report failures %+v, want core/select-one", rep.Failures)
	}
	for _, line := range []string{"claimed levels:", "FAIL core/select-one", "1 case(s) failed"} {
		if !strings.Contains(stderr.String(), line) {
			t.Errorf("stderr lacks %q:\n%s", line, stderr.String())
		}
	}
}

func TestRunReportToFileKeepsSummaryOnStdout(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	args := []string{"-cases", writeCorpus(t), "-dsn", "postgres://u:p@127.0.0.1:1/db",
		"-server", answering(t, http.StatusOK).URL, "-report", path}
	var stdout, stderr bytes.Buffer
	if got := run(args, &stdout, &stderr, fixturesApplied); got != exitPassed {
		t.Fatalf("exit %d, want %d\nstderr:\n%s", got, exitPassed, stderr.String())
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !json.Valid(raw) {
		t.Fatalf("report file is not JSON: %s", raw)
	}
	if !strings.Contains(stdout.String(), "claimed levels: [1]") {
		t.Fatalf("stdout lacks the summary:\n%s", stdout.String())
	}
}
