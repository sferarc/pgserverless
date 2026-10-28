package pgserverless

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
)

// txnOptions captures the per-batch transaction characteristics the
// @neondatabase/serverless driver sends for sql.transaction([...], opts).
// They arrive as HTTP headers (already normalized from their Neon-* form):
//
//	Batch-Isolation-Level: ReadUncommitted | ReadCommitted | RepeatableRead | Serializable
//	Batch-Read-Only:       true | false
//	Batch-Deferrable:      true | false
//
// The driver only sends these for array (batch) requests; a single query never
// carries them. We translate them into the SQL-standard transaction
// characteristics applied via BEGIN, so PostgreSQL itself enforces them — most
// importantly, READ ONLY makes the server reject writes with SQLSTATE 25006,
// failing closed exactly like the proxy's own read-only policy.
type txnOptions struct {
	isolationLevel string // canonical SQL form, e.g. "SERIALIZABLE"; "" = server default
	readOnly       bool
	deferrable     bool
}

// parseTxnOptions reads the batch transaction characteristics from the
// (already normalized) request headers. Unset headers yield the server default.
// An unrecognized value in any of them is a 400-worthy error so we fail closed.
func parseTxnOptions(h http.Header) (txnOptions, error) {
	level, err := isolationLevelSQL(h.Get("Batch-Isolation-Level"))
	if err != nil {
		return txnOptions{}, err
	}
	readOnly, err := boolOption(h, "Batch-Read-Only")
	if err != nil {
		return txnOptions{}, err
	}
	deferrable, err := boolOption(h, "Batch-Deferrable")
	if err != nil {
		return txnOptions{}, err
	}
	return txnOptions{
		isolationLevel: level,
		readOnly:       readOnly,
		deferrable:     deferrable,
	}, nil
}

// boolOption reads one boolean batch header. An unset header is false (the
// server default). Anything strconv.ParseBool accepts is honored, so a client
// that writes "True" or "1" gets what it asked for rather than the opposite;
// anything else is an error, for the same reason an unrecognized isolation
// level is one. These headers carry a guarantee the caller asked the server to
// enforce, and READ ONLY is the guarantee that turns a committed write into a
// 25006 rejection, so guessing "not exactly true, therefore read-write" is the
// one wrong answer available.
func boolOption(h http.Header, name string) (bool, error) {
	raw := strings.TrimSpace(h.Get(name))
	if raw == "" {
		return false, nil
	}
	v, err := strconv.ParseBool(raw)
	if err != nil {
		return false, fmt.Errorf("invalid %s value %q: expected true or false", name, raw)
	}
	return v, nil
}

// isolationLevelSQL maps the driver's isolation-level option (camelCase, as in
// the Neon TypeScript enum) to its SQL-standard spelling. The match is
// case-insensitive and tolerant of spaces/underscores so equivalent spellings
// (e.g. "read committed") also resolve. An empty option yields "" (server
// default); an unrecognized value yields an error so we fail closed rather than
// silently dropping an isolation guarantee the caller asked for.
func isolationLevelSQL(opt string) (string, error) {
	norm := strings.ToLower(strings.NewReplacer(" ", "", "_", "", "-", "").Replace(strings.TrimSpace(opt)))
	switch norm {
	case "":
		return "", nil
	case "readuncommitted":
		return "READ UNCOMMITTED", nil
	case "readcommitted":
		return "READ COMMITTED", nil
	case "repeatableread":
		return "REPEATABLE READ", nil
	case "serializable":
		return "SERIALIZABLE", nil
	default:
		return "", fmt.Errorf("invalid isolation level %q", opt)
	}
}

// beginSQL renders the BEGIN statement that opens the batch transaction with the
// requested characteristics. PostgreSQL accepts the characteristics in any
// order; DEFERRABLE only has an effect for a SERIALIZABLE READ ONLY transaction
// (PostgreSQL ignores it otherwise), matching the driver's documented contract.
func (o txnOptions) beginSQL() string {
	var b strings.Builder
	b.WriteString("BEGIN")
	if o.isolationLevel != "" {
		b.WriteString(" ISOLATION LEVEL ")
		b.WriteString(o.isolationLevel)
	}
	if o.readOnly {
		b.WriteString(" READ ONLY")
	}
	if o.deferrable {
		b.WriteString(" DEFERRABLE")
	}
	return b.String()
}

// isolationLevelHeader spells each SQL isolation level as the driver's header value.
var isolationLevelHeader = map[string]string{
	"READ UNCOMMITTED": "ReadUncommitted",
	"READ COMMITTED":   "ReadCommitted",
	"REPEATABLE READ":  "RepeatableRead",
	"SERIALIZABLE":     "Serializable",
}

// setResponseHeaders echoes the set characteristics on a committed batch's 200, as Neon's proxy does.
func (o txnOptions) setResponseHeaders(h http.Header) {
	if o.readOnly {
		h.Set("Neon-Batch-Read-Only", "true")
	}
	if o.deferrable {
		h.Set("Neon-Batch-Deferrable", "true")
	}
	if level, ok := isolationLevelHeader[o.isolationLevel]; ok {
		h.Set("Neon-Batch-Isolation-Level", level)
	}
}

// isDefault reports whether no transaction characteristics were requested, so
// the batch can take the plain implicit-transaction pipeline path.
func (o txnOptions) isDefault() bool {
	return o.isolationLevel == "" && !o.readOnly && !o.deferrable
}
