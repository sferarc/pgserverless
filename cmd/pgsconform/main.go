// Command pgsconform runs the pgserverless conformance corpus against a running
// server and writes a report.
//
// It talks to the server over the network rather than linking it, so the same
// corpus checks an implementation in any language. That is the whole reason it
// exists: a suite of Go tests calling a Go handler cannot tell you whether a
// Rust server is correct.
//
// Usage:
//
//	pgsconform -cases spec/cases/v1.json \
//	           -server http://localhost:4000 \
//	           -dsn postgres://user:pass@localhost/db \
//	           -report report.json
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"maps"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/coder/websocket"
	"github.com/jackc/pgx/v5"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "pgsconform: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	var (
		casesPath  = flag.String("cases", "spec/cases/v1.json", "corpus file")
		serverURL  = flag.String("server", "http://localhost:4000", "base URL of the server under test")
		dsn        = flag.String("dsn", "", "PostgreSQL connection string used to apply fixtures")
		reportPath = flag.String("report", "", "write the JSON report here (default stdout summary only)")
		implName   = flag.String("impl", "pgserverless-go", "implementation name recorded in the report")
		connString = flag.String("client-conn", "", "connection string sent as the request header (default: -dsn)")
	)
	flag.Parse()

	corpus, err := loadCorpus(*casesPath)
	if err != nil {
		return err
	}
	if *dsn == "" {
		return errors.New("-dsn is required: the corpus needs a real PostgreSQL to hold its fixtures")
	}
	if *connString == "" {
		*connString = *dsn
	}

	ctx := context.Background()
	pgVersion, err := applyFixtures(ctx, *dsn, corpus)
	if err != nil {
		return fmt.Errorf("apply fixtures: %w", err)
	}

	rep := report{
		Implementation: *implName,
		Spec:           corpus.SpecVersion,
		Corpus:         corpus.Version,
		Postgres:       pgVersion,
		Results:        map[string]*levelResult{},
	}

	runner := &runner{server: strings.TrimRight(*serverURL, "/"), connString: *connString}
	for _, c := range corpus.Cases {
		lvl := strconv.Itoa(c.Level)
		if rep.Results[lvl] == nil {
			rep.Results[lvl] = &levelResult{}
		}
		outcome, detail := runner.runCase(ctx, c)
		switch outcome {
		case outcomePass:
			rep.Results[lvl].Passed++
		case outcomeSkip:
			rep.Results[lvl].Skipped++
			rep.Skips = append(rep.Skips, failure{ID: c.ID, Level: c.Level, Spec: c.Spec, Reason: detail})
		case outcomeFail:
			rep.Results[lvl].Failed++
			rep.Failures = append(rep.Failures, failure{ID: c.ID, Level: c.Level, Spec: c.Spec, Reason: detail})
		}
		if c.Compare == "parses_equal" {
			rep.LooseComparisons++
		}
	}

	// A level is claimed only with zero failures AND zero skips. A report that
	// claims a level whose cases were skipped is the failure mode the corpus
	// design calls out by name.
	for lvl, r := range rep.Results {
		if r.Failed == 0 && r.Skipped == 0 && r.Passed > 0 {
			n, _ := strconv.Atoi(lvl)
			rep.ClaimedLevels = append(rep.ClaimedLevels, n)
		}
	}
	sort.Ints(rep.ClaimedLevels)

	if err := emit(rep, *reportPath); err != nil {
		return err
	}
	if len(rep.Failures) > 0 {
		return fmt.Errorf("%d case(s) failed", len(rep.Failures))
	}
	return nil
}

// ---------- corpus ----------

type corpus struct {
	SpecVersion string    `json:"spec_version"`
	Version     int       `json:"version"`
	Fixtures    []fixture `json:"fixtures"`
	Cases       []kase    `json:"cases"`
}

type fixture struct {
	Name string `json:"name"`
	SQL  string `json:"sql"`
}

type kase struct {
	ID       string          `json:"id"`
	Level    int             `json:"level"`
	Spec     string          `json:"spec"`
	Fixture  string          `json:"fixture"`
	Requires string          `json:"requires"`
	Compare  string          `json:"compare"`
	Request  requestSpec     `json:"request"`
	Expect   json.RawMessage `json:"expect"`
}

type requestSpec struct {
	Path    string          `json:"path"`
	Method  string          `json:"method"`
	Headers headerSpec      `json:"headers"`
	Body    json.RawMessage `json:"body"`
}

// headerSpec is symbolic. The corpus says what the request means, and this
// program decides which spelling carries it, which is what lets one case test
// both spellings and their precedence.
type headerSpec struct {
	ArrayMode *bool  `json:"array_mode"`
	RawText   *bool  `json:"raw_text"`
	Spelling  string `json:"spelling"`
	// The batch headers carry the raw JSON literal rather than a typed value.
	// The corpus deliberately sends an unparseable one ("yes-please") to check
	// that a server refuses it, and a *bool cannot hold that.
	BatchReadOnly       json.RawMessage `json:"batch_read_only"`
	BatchIsolationLevel json.RawMessage `json:"batch_isolation_level"`
	BatchDeferrable     json.RawMessage `json:"batch_deferrable"`
}

type expectation struct {
	Status      *int             `json:"status"`
	StatusClass string           `json:"status_class"`
	Command     *string          `json:"command"`
	RowCount    *int             `json:"row_count"`
	RowAsArray  *bool            `json:"row_as_array"`
	Fields      []expectedField  `json:"fields"`
	Rows        json.RawMessage  `json:"rows"`
	Error       *expectedError   `json:"error"`
	WebSocket   *expectedWSocket `json:"websocket"`
}

type expectedField struct {
	Name       string `json:"name"`
	DataTypeID *int   `json:"data_type_id"`
}

type expectedError struct {
	Code      string   `json:"code"`
	Severity  string   `json:"severity"`
	HasFields []string `json:"has_fields"`
}

type expectedWSocket struct {
	Upgraded    bool   `json:"upgraded"`
	Subprotocol string `json:"subprotocol"`
}

var specVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

func loadCorpus(path string) (*corpus, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read corpus: %w", err)
	}
	var c corpus
	if err := json.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("parse corpus: %w", err)
	}
	if !specVersionPattern.MatchString(c.SpecVersion) {
		return nil, fmt.Errorf("corpus spec_version %q is not MAJOR.MINOR.PATCH: a report has to say which spec it checked", c.SpecVersion)
	}
	if len(c.Cases) == 0 {
		return nil, errors.New("corpus has no cases")
	}
	for _, k := range c.Cases {
		if k.Compare != "parses_equal" && listsAlternatives(k.Expect) {
			return nil, fmt.Errorf("case %s lists `also` alternatives but is not compare: parses_equal", k.ID)
		}
	}
	return &c, nil
}

// applyFixtures puts the database into the state the cases assume, and returns
// the server version, which the report records because `time`, `interval` and
// `tsvector` spellings vary between them.
func applyFixtures(ctx context.Context, dsn string, c *corpus) (string, error) {
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return "", err
	}
	defer conn.Close(ctx)

	var version string
	if err := conn.QueryRow(ctx, "SHOW server_version").Scan(&version); err != nil {
		return "", err
	}
	for _, f := range c.Fixtures {
		if _, err := conn.Exec(ctx, f.SQL); err != nil {
			return "", fmt.Errorf("fixture %s: %w", f.Name, err)
		}
	}
	return version, nil
}

// ---------- running ----------

type outcome int

const (
	outcomePass outcome = iota
	outcomeFail
	outcomeSkip
)

type runner struct {
	server     string
	connString string
}

func (r *runner) runCase(ctx context.Context, c kase) (outcome, string) {
	var exp expectation
	if err := json.Unmarshal(c.Expect, &exp); err != nil {
		return outcomeFail, fmt.Sprintf("corpus case is malformed: %v", err)
	}

	if c.Requires == "websocket" {
		return r.runWebSocket(ctx, c, exp)
	}
	if c.Requires != "" {
		return outcomeSkip, fmt.Sprintf("harness cannot arrange %q", c.Requires)
	}
	return r.runHTTP(ctx, c, exp)
}

func (r *runner) runHTTP(ctx context.Context, c kase, exp expectation) (outcome, string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, r.server+c.Request.Path, bytes.NewReader(c.Request.Body))
	if err != nil {
		return outcomeFail, err.Error()
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Neon-Connection-String", r.connString)
	applyHeaders(req, c.Request.Headers)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return outcomeFail, fmt.Sprintf("request failed: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)

	if problems := checkStatus(exp, resp.StatusCode); problems != "" {
		return outcomeFail, problems + bodyHint(body)
	}
	if exp.Error != nil {
		if p := checkError(exp.Error, body); p != "" {
			return outcomeFail, p
		}
		return outcomePass, ""
	}

	var got map[string]json.RawMessage
	if err := json.Unmarshal(body, &got); err != nil {
		return outcomeFail, fmt.Sprintf("response is not a JSON object: %v%s", err, bodyHint(body))
	}
	if p := checkResult(exp, got, c.Compare == "parses_equal"); p != "" {
		return outcomeFail, p
	}
	return outcomePass, ""
}

// runWebSocket covers level 4, which is the upgrade only. Once the connection
// carries PostgreSQL frames, testing them would be testing PostgreSQL.
func (r *runner) runWebSocket(ctx context.Context, c kase, exp expectation) (outcome, string) {
	url := strings.Replace(r.server, "http", "ws", 1) + c.Request.Path
	dialCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	conn, resp, err := websocket.Dial(dialCtx, url, &websocket.DialOptions{
		Subprotocols: []string{"pg"},
		HTTPHeader:   http.Header{"Neon-Connection-String": []string{r.connString}},
	})
	if err != nil {
		if resp != nil {
			// Closing the handshake body belongs in this branch and nowhere
			// else. On the failure path it is an ordinary HTTP body and the
			// runner loops over the whole corpus, so one held open per failing
			// case adds up. On the success path it is the hijacked connection
			// itself, which conn.CloseNow owns, so closing it here would hang
			// up on the socket we are about to inspect.
			if resp.Body != nil {
				_ = resp.Body.Close()
			}
			return outcomeFail, fmt.Sprintf("upgrade failed with HTTP %d: %v", resp.StatusCode, err)
		}
		return outcomeFail, fmt.Sprintf("upgrade failed: %v", err)
	}
	defer conn.CloseNow()

	if exp.Status != nil && resp.StatusCode != *exp.Status {
		return outcomeFail, fmt.Sprintf("status: want %d, got %d", *exp.Status, resp.StatusCode)
	}
	if exp.WebSocket != nil && exp.WebSocket.Subprotocol != "" {
		if got := conn.Subprotocol(); got != exp.WebSocket.Subprotocol {
			return outcomeFail, fmt.Sprintf("subprotocol: want %q, got %q", exp.WebSocket.Subprotocol, got)
		}
	}
	return outcomePass, ""
}

// applyHeaders turns the corpus's symbolic headers into wire headers.
//
// "both" sends the prefixed and unprefixed spellings with CONFLICTING values,
// which is how a single case checks that the unprefixed one wins.
func applyHeaders(req *http.Request, h headerSpec) {
	set := func(prefixed, plain string, v bool) {
		switch h.Spelling {
		case "prefixed":
			req.Header.Set(prefixed, strconv.FormatBool(v))
		case "both":
			req.Header.Set(prefixed, strconv.FormatBool(!v))
			req.Header.Set(plain, strconv.FormatBool(v))
		default:
			req.Header.Set(plain, strconv.FormatBool(v))
		}
	}
	if h.ArrayMode != nil {
		set("Neon-Array-Mode", "Array-Mode", *h.ArrayMode)
	}
	if h.RawText != nil {
		set("Neon-Raw-Text-Output", "Raw-Text-Output", *h.RawText)
	}
	if len(h.BatchReadOnly) > 0 {
		req.Header.Set("Neon-Batch-Read-Only", headerLiteral(h.BatchReadOnly))
	}
	if len(h.BatchDeferrable) > 0 {
		req.Header.Set("Neon-Batch-Deferrable", headerLiteral(h.BatchDeferrable))
	}
	if len(h.BatchIsolationLevel) > 0 {
		req.Header.Set("Neon-Batch-Isolation-Level", headerLiteral(h.BatchIsolationLevel))
	}
}

// headerLiteral renders a corpus value as the header text to send. A JSON
// string becomes its contents, so "yes-please" is sent as yes-please rather
// than as a quoted literal; anything else is sent as written.
func headerLiteral(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err == nil {
		return s
	}
	return strings.TrimSpace(string(raw))
}

func checkStatus(exp expectation, got int) string {
	if exp.Status != nil && got != *exp.Status {
		return fmt.Sprintf("status: want %d, got %d", *exp.Status, got)
	}
	if exp.StatusClass == "error" && got < 400 {
		return fmt.Sprintf("status: want an error status, got %d", got)
	}
	if exp.StatusClass == "success" && got >= 400 {
		return fmt.Sprintf("status: want a success status, got %d", got)
	}
	return ""
}

func checkError(exp *expectedError, body []byte) string {
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		return fmt.Sprintf("error body is not JSON: %v%s", err, bodyHint(body))
	}
	if exp.Code != "" {
		if s, _ := got["code"].(string); s != exp.Code {
			return fmt.Sprintf("error.code: want %q, got %q", exp.Code, got["code"])
		}
	}
	if exp.Severity != "" {
		if s, _ := got["severity"].(string); s != exp.Severity {
			return fmt.Sprintf("error.severity: want %q, got %q", exp.Severity, got["severity"])
		}
	}
	var missing []string
	for _, f := range exp.HasFields {
		if v, ok := got[f]; !ok || v == nil || v == "" {
			missing = append(missing, f)
		}
	}
	if len(missing) > 0 {
		return fmt.Sprintf("error is missing field(s) the client reads: %s (present: %s)",
			strings.Join(missing, ", "), strings.Join(keysOf(got), ", "))
	}
	return ""
}

func checkResult(exp expectation, got map[string]json.RawMessage, loose bool) string {
	if exp.Command != nil {
		var cmd string
		_ = json.Unmarshal(got["command"], &cmd)
		if cmd != *exp.Command {
			return fmt.Sprintf("command: want %q, got %q", *exp.Command, cmd)
		}
	}
	if exp.RowCount != nil {
		var n int
		_ = json.Unmarshal(got["rowCount"], &n)
		if n != *exp.RowCount {
			return fmt.Sprintf("rowCount: want %d, got %d", *exp.RowCount, n)
		}
	}
	if exp.RowAsArray != nil {
		var b bool
		_ = json.Unmarshal(got["rowAsArray"], &b)
		if b != *exp.RowAsArray {
			return fmt.Sprintf("rowAsArray: want %v, got %v", *exp.RowAsArray, b)
		}
	}
	if len(exp.Fields) > 0 {
		if p := checkFields(exp.Fields, got["fields"]); p != "" {
			return p
		}
	}
	if len(exp.Rows) > 0 {
		if p := checkRows(exp.Rows, got["rows"], loose); p != "" {
			return p
		}
	}
	return ""
}

func checkFields(exp []expectedField, raw json.RawMessage) string {
	var fields []struct {
		Name       string `json:"name"`
		DataTypeID int    `json:"dataTypeID"`
	}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Sprintf("fields: %v", err)
	}
	if len(fields) != len(exp) {
		return fmt.Sprintf("fields: want %d, got %d", len(exp), len(fields))
	}
	for i, e := range exp {
		if fields[i].Name != e.Name {
			return fmt.Sprintf("fields[%d].name: want %q, got %q", i, e.Name, fields[i].Name)
		}
		if e.DataTypeID != nil && fields[i].DataTypeID != *e.DataTypeID {
			return fmt.Sprintf("fields[%d].dataTypeID: want %d, got %d", i, *e.DataTypeID, fields[i].DataTypeID)
		}
	}
	return ""
}

// checkRows compares the SERIALIZED form of each value against the corpus's
// recorded JSON text.
//
// That is the single most important thing this program does. The rule that
// int8 is the string "9223372036854775807" and not the number
// 9223372036854775807 disappears the moment both sides are parsed into a
// language's native types, because most languages have one numeric type and no
// memory of how the value was written.
func checkRows(expRaw, gotRaw json.RawMessage, loose bool) string {
	var exp, got []json.RawMessage
	if err := json.Unmarshal(expRaw, &exp); err != nil {
		return fmt.Sprintf("corpus rows: %v", err)
	}
	if err := json.Unmarshal(gotRaw, &got); err != nil {
		return fmt.Sprintf("rows is not an array: %v", err)
	}
	if len(exp) != len(got) {
		return fmt.Sprintf("rows: want %d, got %d", len(exp), len(got))
	}

	for i := range exp {
		// A row is an object in object mode and an array in array mode. Both
		// reduce to an ordered list of (key, expected JSON text).
		expCells, err := cellsOf(exp[i])
		if err != nil {
			return fmt.Sprintf("corpus rows[%d]: %v", i, err)
		}
		gotCells, err := rawCellsOf(got[i])
		if err != nil {
			return fmt.Sprintf("rows[%d]: %v", i, err)
		}
		for key, wantText := range expCells {
			gotText, ok := gotCells[key]
			if !ok {
				return fmt.Sprintf("rows[%d]: missing %s (got %s)", i, key, strings.Join(keysOfRaw(gotCells), ", "))
			}
			if equalJSONText(wantText.JSON, gotText, loose) {
				continue
			}
			if loose && slices.ContainsFunc(wantText.Also, func(alt string) bool { return equalJSONText(alt, gotText, true) }) {
				continue
			}
			return fmt.Sprintf("rows[%d].%s: want %s, got %s", i, key, wantText.JSON, gotText)
		}
	}
	return ""
}

// listsAlternatives reports whether any wrapper in the expectation carries
// `also`, so a result column that happens to be named also is not mistaken.
func listsAlternatives(raw json.RawMessage) bool {
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return false
	}
	return hasAlso(v)
}

func hasAlso(v any) bool {
	switch t := v.(type) {
	case map[string]any:
		if _, ok := t["json"].(string); ok {
			if also, ok := t["also"].([]any); ok && len(also) > 0 {
				return true
			}
		}
		for _, c := range t {
			if hasAlso(c) {
				return true
			}
		}
	case []any:
		if slices.ContainsFunc(t, hasAlso) {
			return true
		}
	}
	return false
}

type wantCell struct {
	JSON string   `json:"json"`
	Also []string `json:"also"`
}

// cellsOf reads a corpus row, whose values are {"json": "<text>"} wrappers.
func cellsOf(raw json.RawMessage) (map[string]wantCell, error) {
	out := map[string]wantCell{}

	var asObject map[string]wantCell
	if err := json.Unmarshal(raw, &asObject); err == nil {
		maps.Copy(out, asObject)
		return out, nil
	}
	var asArray []wantCell
	if err := json.Unmarshal(raw, &asArray); err != nil {
		return nil, fmt.Errorf("row is neither an object nor an array: %w", err)
	}
	for i, v := range asArray {
		out[strconv.Itoa(i)] = v
	}
	return out, nil
}

// rawCellsOf reads a response row, keeping each value as its raw JSON text.
func rawCellsOf(raw json.RawMessage) (map[string]json.RawMessage, error) {
	out := map[string]json.RawMessage{}

	var asObject map[string]json.RawMessage
	if err := json.Unmarshal(raw, &asObject); err == nil {
		return asObject, nil
	}
	var asArray []json.RawMessage
	if err := json.Unmarshal(raw, &asArray); err != nil {
		return nil, fmt.Errorf("row is neither an object nor an array: %w", err)
	}
	for i, v := range asArray {
		out[strconv.Itoa(i)] = v
	}
	return out, nil
}

// equalJSONText compares two pieces of JSON text. Exact by default, because the
// encoding is the point. Loose comparison parses both sides, and is used only
// for cases the corpus marks as unsettled in Appendix B of the spec.
func equalJSONText(want string, got json.RawMessage, loose bool) bool {
	if compact(want) == compact(string(got)) {
		return true
	}
	if !loose {
		return false
	}
	var a, b any
	if json.Unmarshal([]byte(want), &a) != nil || json.Unmarshal(got, &b) != nil {
		return false
	}
	aj, _ := json.Marshal(a)
	bj, _ := json.Marshal(b)
	return string(aj) == string(bj)
}

// compact canonicalises JSON string escapes and whitespace, nothing else: key
// order, duplicate keys and number spelling survive, so "<" and "\u003c" match
// while 1 and "1" do not. Text that is not exactly one JSON value is returned
// trimmed and so only matches itself.
func compact(s string) string {
	dec := json.NewDecoder(strings.NewReader(s))
	dec.UseNumber()
	var buf bytes.Buffer
	if err := writeValue(dec, &buf); err != nil {
		return strings.TrimSpace(s)
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return strings.TrimSpace(s)
	}
	return buf.String()
}

func writeValue(dec *json.Decoder, buf *bytes.Buffer) error {
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	delim, isDelim := tok.(json.Delim)
	if !isDelim {
		return writeScalar(tok, buf)
	}
	closer := byte(']')
	if delim == '{' {
		closer = '}'
	}
	buf.WriteByte(byte(delim))
	for first := true; dec.More(); first = false {
		if !first {
			buf.WriteByte(',')
		}
		if delim == '{' {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			if err := writeScalar(key, buf); err != nil {
				return err
			}
			buf.WriteByte(':')
		}
		if err := writeValue(dec, buf); err != nil {
			return err
		}
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	buf.WriteByte(closer)
	return nil
}

func writeScalar(tok json.Token, buf *bytes.Buffer) error {
	if n, ok := tok.(json.Number); ok {
		buf.WriteString(n.String())
		return nil
	}
	enc := json.NewEncoder(buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(tok); err != nil {
		return err
	}
	buf.Truncate(buf.Len() - 1)
	return nil
}

// ---------- report ----------

type report struct {
	Implementation   string                  `json:"implementation"`
	Spec             string                  `json:"spec"`
	Corpus           int                     `json:"corpus"`
	Postgres         string                  `json:"postgres"`
	ClaimedLevels    []int                   `json:"claimed_levels"`
	Results          map[string]*levelResult `json:"results"`
	LooseComparisons int                     `json:"loose_comparisons"`
	Failures         []failure               `json:"failures"`
	Skips            []failure               `json:"skips,omitempty"`
}

type levelResult struct {
	Passed  int `json:"passed"`
	Failed  int `json:"failed"`
	Skipped int `json:"skipped"`
}

type failure struct {
	ID     string `json:"id"`
	Level  int    `json:"level"`
	Spec   string `json:"spec"`
	Reason string `json:"reason"`
}

func emit(rep report, path string) error {
	out, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		return err
	}
	if path != "" {
		if err := os.WriteFile(path, append(out, '\n'), 0o644); err != nil { //nolint:gosec // a report is not a secret
			return err
		}
	}

	fmt.Printf("implementation %s, spec %s, corpus v%d, postgres %s\n", rep.Implementation, rep.Spec, rep.Corpus, rep.Postgres)
	for _, lvl := range sortedKeys(rep.Results) {
		r := rep.Results[lvl]
		fmt.Printf("  level %s: %d passed, %d failed, %d skipped\n", lvl, r.Passed, r.Failed, r.Skipped)
	}
	fmt.Printf("claimed levels: %v\n", rep.ClaimedLevels)
	for _, f := range rep.Failures {
		fmt.Printf("  FAIL %-46s (spec %s) %s\n", f.ID, f.Spec, f.Reason)
	}
	for _, s := range rep.Skips {
		fmt.Printf("  SKIP %-46s %s\n", s.ID, s.Reason)
	}
	return nil
}

func sortedKeys(m map[string]*levelResult) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func keysOf(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func keysOfRaw(m map[string]json.RawMessage) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func bodyHint(body []byte) string {
	s := strings.TrimSpace(string(body))
	if s == "" {
		return ""
	}
	if len(s) > 300 {
		s = s[:300] + "..."
	}
	return "\n      body: " + s
}
