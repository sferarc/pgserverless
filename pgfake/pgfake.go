package pgfake

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"io"
	"math/big"
	"net"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/jackc/pgx/v5/pgtype"
)

// PG is an in-process PostgreSQL backend sufficient to satisfy a real
// jackc/pgx client connecting through the serverless /sql handler. It speaks
// the parts of the wire protocol pgx exercises in QueryExecModeCacheStatement
// (the handler's default): SSL negotiation, startup/auth, the extended query
// protocol (Parse/Bind/Describe/Execute/Sync), and SendBatch pipelines.
//
// Rows are stored as native Go values and encoded on the wire honoring the
// result format codes pgx requests in Bind (binary for registered OIDs, text
// otherwise) using pgx's own pgtype.Map. This faithfully mirrors how the real
// upstream PostgreSQL responds, so pgx decodes values back to the same native
// types the production handler relies on (RowsToResult uses Rows.Values()).
//
// Responses are programmable per-SQL via a responder map so conformance tests
// can assert how the handler shapes results into the Neon JSON contract.
type PG struct {
	t         testing.TB
	ln        net.Listener
	tlsCfg    *tls.Config
	typeMap   *pgtype.Map
	mu        sync.Mutex
	respond   map[string]Result // keyed by exact SQL string
	lastBegin string            // most recent BEGIN statement observed (for assertions)

	// lastStartup records the parameters of the most recent StartupMessage
	// (user, database, application_name, ...) for audit-source assertions.
	lastStartup map[string]string
	// simpleSQL records every simple-protocol Query payload in order, so tests
	// can assert which protocol a handler selected and how it split a request.
	simpleSQL []string
	// lastBindParams records the parameter values of the most recent Bind.
	lastBindParams [][]byte
	// committed records every IsWrite statement whose transaction committed.
	committed []string

	// rejectAuth, when set, is returned instead of AuthenticationOk. It exists
	// so a test can exercise the path a wrong credential takes, which is the
	// only one a host's brute-force accounting is fed from.
	rejectAuth *pgproto3.ErrorResponse

	// cancels holds a channel per live session, closed by a CancelRequest carrying its process ID.
	cancels        map[uint32]chan struct{}
	nextPID        uint32
	cancelRequests int

	// dbEncoding is the database's encoding and so a session's client_encoding when its startup names none; empty is UTF8.
	dbEncoding string
}

// Result is the canned response for one query.
type Result struct {
	Cols    []Col
	Rows    [][]any // each row is a slice of native Go column values (nil = NULL)
	Tag     string  // CommandComplete Tag, e.g. "SELECT 1"
	PgError *pgproto3.ErrorResponse
	// IsWrite marks a statement that mutates data. A real PostgreSQL READ ONLY
	// transaction rejects such statements with SQLSTATE 25006; the fake backend
	// mirrors that so the batch read-only conformance test exercises real
	// server-side enforcement rather than a client-side guess.
	IsWrite bool
	// ParamOIDs are the parameter types Describe reports; unset ones are 0 (unknown), so pgx binds text.
	ParamOIDs []uint32
	// WaitForCancel holds the statement until a CancelRequest for its session arrives, then fails it with 57014 as PostgreSQL does.
	WaitForCancel bool
	// Requires is a statement creating an object this one uses; until the session has run it, Parse and Execute fail with 42P01.
	Requires string
}

// Col describes one result column.
type Col struct {
	Name    string
	TypeOID uint32
	// TableOID and AttNum say which table column this is; zero is a computed column, and a system column's AttNum is negative.
	TableOID uint32
	AttNum   int16
}

// New starts a fake backend on a random localhost port and returns it.
func New(t testing.TB) *PG {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("pgfake: %v", err)
	}

	f := &PG{
		t:       t,
		ln:      ln,
		tlsCfg:  selfSignedTLS(t),
		typeMap: pgtype.NewMap(),
		respond: make(map[string]Result),
		cancels: make(map[uint32]chan struct{}),
	}
	go f.serve()
	t.Cleanup(func() { _ = ln.Close() })
	return f
}

// NewRejectingAuth starts a fake backend that completes TLS, reads the startup
// message and then fails the connection with the given SQLSTATE instead of
// authenticating.
//
// A dial to a closed port fails too, but with a net error, and a host that
// classifies dial failures only counts the ones PostgreSQL attributes to the
// credential (class 28). So a test about that classification needs a backend
// that gets far enough to answer with one.
func NewRejectingAuth(t testing.TB, code, message string) *PG {
	f := New(t)
	f.mu.Lock()
	f.rejectAuth = &pgproto3.ErrorResponse{Severity: "FATAL", Code: code, Message: message}
	f.mu.Unlock()
	return f
}

// SetDatabaseEncoding makes enc the client_encoding of a session whose startup names none, as PostgreSQL defaults it to the database's encoding.
// Only LATIN1 converts row text; anything else is sent as UTF-8.
func (f *PG) SetDatabaseEncoding(enc string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.dbEncoding = enc
}

// addr returns the host:port the fake backend listens on.
func (f *PG) Addr() string { return f.ln.Addr().String() }

// setResult registers a canned result for an exact SQL string.
func (f *PG) SetResult(sql string, r Result) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.respond[sql] = r
}

func (f *PG) resultFor(sql string) (Result, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.respond[sql]
	return r, ok
}

// setLastBegin records the most recent BEGIN statement for test assertions.
func (f *PG) setLastBegin(sql string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastBegin = sql
}

// LastBegin returns the most recent BEGIN statement the backend observed.
func (f *PG) LastBegin() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastBegin
}

// CancelRequests returns how many CancelRequests named a live session.
func (f *PG) CancelRequests() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.cancelRequests
}

func (f *PG) register() (uint32, chan struct{}) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.nextPID++
	ch := make(chan struct{})
	f.cancels[f.nextPID] = ch
	return f.nextPID, ch
}

func (f *PG) unregister(pid uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	delete(f.cancels, pid)
}

func (f *PG) cancel(pid uint32) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if ch, ok := f.cancels[pid]; ok {
		f.cancelRequests++
		close(ch)
		delete(f.cancels, pid)
	}
}

// StartupParam returns a parameter from the most recent StartupMessage.
func (f *PG) StartupParam(Name string) string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastStartup[Name]
}

// CommittedWrites returns the IsWrite statements whose transaction committed,
// so a test can tell a rolled-back write from a persisted one.
func (f *PG) CommittedWrites() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.committed...)
}

// commit persists the session's pending writes.
func (f *PG) commit(s *session) {
	f.mu.Lock()
	f.committed = append(f.committed, s.pending...)
	f.mu.Unlock()
	s.pending = nil
}

// LastBindParams returns the parameter values of the most recent Bind, nil for SQL NULL.
func (f *PG) LastBindParams() [][]byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.lastBindParams
}

// LastSimpleSQL returns the most recent simple-protocol Query payload, "" when
// only the extended protocol was used.
func (f *PG) LastSimpleSQL() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.simpleSQL) == 0 {
		return ""
	}
	return f.simpleSQL[len(f.simpleSQL)-1]
}

// SimpleSQL returns every simple-protocol Query payload, oldest first.
func (f *PG) SimpleSQL() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.simpleSQL)
}

func (f *PG) serve() {
	for {
		conn, err := f.ln.Accept()
		if err != nil {
			return
		}
		go f.handle(conn)
	}
}

// session holds per-connection extended-protocol state. PostgreSQL tracks
// prepared statements by Name and portals (bound statements) separately; pgx
// in cache mode uses named statements and the unnamed portal. We model both so
// pipelined batches (SendBatch) resolve each Execute to the right SQL.
type session struct {
	statements    map[string]string // prepared statement Name -> SQL
	portalSQL     string            // SQL bound to the current (unnamed) portal
	resultFormats []int16           // result format codes from the current Bind

	// Transaction state, used to model READ ONLY enforcement.
	inTxn    bool     // a BEGIN has opened a transaction not yet committed
	readOnly bool     // the open transaction is READ ONLY
	aborted  bool     // an error occurred; skip messages until the next Sync
	pending  []string // IsWrite statements run since the transaction began

	ran map[string]bool // statements this session has run without error

	cancelled chan struct{} // closed by a CancelRequest for this session

	latin1 bool // the session's client_encoding is LATIN1, so row text is converted to it
}

// isControlSQL reports whether sql is a transaction-control statement the fake
// backend interprets directly (BEGIN with optional characteristics, COMMIT or
// ROLLBACK).
func isControlSQL(sql string) bool {
	u := strings.ToUpper(strings.TrimSpace(sql))
	return strings.HasPrefix(u, "BEGIN") || u == "COMMIT" || u == "END" || u == "ROLLBACK"
}

// splitSimpleQuery splits a simple-protocol query string into its individual
// statements on ';'. Test fixtures never embed semicolons in string literals,
// so a plain split (dropping blank trailing fragments) faithfully models how
// PostgreSQL executes a multi-statement simple query.
func splitSimpleQuery(q string) []string {
	parts := strings.Split(q, ";")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if s := strings.TrimSpace(p); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func (f *PG) handle(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))

	// SSL negotiation: pgx sends SSLRequest first. We accept and upgrade.
	tlsConn, err := f.negotiateSSL(conn)
	if err != nil {
		return
	}
	conn = tlsConn

	be := pgproto3.NewBackend(conn, conn)

	// Read startup message (post-TLS).
	startup, err := be.ReceiveStartupMessage()
	if err != nil {
		return
	}
	if cr, ok := startup.(*pgproto3.CancelRequest); ok {
		f.cancel(cr.ProcessID)
		return
	}
	sm, ok := startup.(*pgproto3.StartupMessage)
	if !ok {
		return
	}
	f.mu.Lock()
	f.lastStartup = sm.Parameters
	clientEncoding := sm.Parameters["client_encoding"]
	if clientEncoding == "" {
		clientEncoding = f.dbEncoding
	}
	f.mu.Unlock()
	if clientEncoding == "" {
		clientEncoding = "UTF8"
	}

	f.mu.Lock()
	reject := f.rejectAuth
	f.mu.Unlock()
	if reject != nil {
		be.Send(reject)
		_ = be.Flush()
		return
	}

	// Auth: trust everyone.
	be.Send(&pgproto3.AuthenticationOk{})
	be.Send(&pgproto3.ParameterStatus{Name: "server_version", Value: "16.0 (pgfake)"})
	be.Send(&pgproto3.ParameterStatus{Name: "client_encoding", Value: clientEncoding})
	// pgx requires standard_conforming_strings=on before it will run simple-
	// protocol queries with arguments. A real PostgreSQL server always reports
	// this.
	be.Send(&pgproto3.ParameterStatus{Name: "standard_conforming_strings", Value: "on"})
	pid, cancelled := f.register()
	defer f.unregister(pid)
	be.Send(&pgproto3.BackendKeyData{ProcessID: pid, SecretKey: []byte{0, 0, 0, 1}})
	be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
	if err := be.Flush(); err != nil {
		return
	}

	s := &session{statements: map[string]string{}, cancelled: cancelled, ran: map[string]bool{}, latin1: strings.EqualFold(clientEncoding, "LATIN1")}

	for {
		msg, err := be.Receive()
		if err != nil {
			return
		}

		// After an error in an extended-protocol pipeline, PostgreSQL ignores
		// all messages until the next Sync. We model that so a read-only
		// rejection mid-batch aborts the rest of the transaction.
		if s.aborted {
			if _, ok := msg.(*pgproto3.Sync); ok {
				s.aborted = false
				s.resetTxn()
				be.Send(&pgproto3.ReadyForQuery{TxStatus: 'I'})
				_ = be.Flush()
			} else if _, ok := msg.(*pgproto3.Terminate); ok {
				return
			}
			continue
		}

		switch m := msg.(type) {
		case *pgproto3.Query:
			f.mu.Lock()
			f.simpleSQL = append(f.simpleSQL, m.String)
			f.mu.Unlock()
			// Simple query protocol: always text format. A single Query may
			// carry multiple statements separated by ';' (pgx uses this for
			// SendBatch in simple-protocol mode). Each statement produces its
			// own result set; a single ReadyForQuery terminates the whole
			// simple-query message. On the first error PostgreSQL stops
			// executing the remaining statements.
			for _, stmt := range splitSimpleQuery(m.String) {
				f.writeResult(be, s, stmt, true, nil)
				if s.aborted {
					break
				}
			}
			status := s.txStatus()
			if s.aborted {
				s.pending = nil
			} else if !s.inTxn {
				f.commit(s)
			}
			s.aborted = false // simple-query errors are resolved at ReadyForQuery
			be.Send(&pgproto3.ReadyForQuery{TxStatus: status})
			_ = be.Flush()

		case *pgproto3.Parse:
			if len(splitSimpleQuery(m.Query)) > 1 {
				// PostgreSQL refuses to prepare more than one statement.
				s.aborted = true
				be.Send(&pgproto3.ErrorResponse{
					Severity: "ERROR",
					Code:     "42601",
					Message:  "cannot insert multiple commands into a prepared statement",
				})
				break
			}
			if res, ok := f.resultFor(m.Query); ok && res.Requires != "" && !s.ran[res.Requires] {
				s.aborted = true
				be.Send(missingRelation(res.Requires))
				break
			}
			s.statements[m.Name] = m.Query
			be.Send(&pgproto3.ParseComplete{})

		case *pgproto3.Bind:
			s.portalSQL = s.statements[m.PreparedStatement]
			params := make([][]byte, len(m.Parameters))
			for i, p := range m.Parameters {
				if p != nil {
					params[i] = append([]byte{}, p...)
				}
			}
			f.mu.Lock()
			f.lastBindParams = params
			f.mu.Unlock()
			s.resultFormats = m.ResultFormatCodes
			be.Send(&pgproto3.BindComplete{})

		case *pgproto3.Describe:
			// 'S' describes a prepared statement (by Name); 'P' the open portal.
			sql := s.portalSQL
			if m.ObjectType == 'S' {
				sql = s.statements[m.Name]
				// Parameter description first. We must report the correct
				// parameter count (pgx validates arg count against it); OID 0
				// (unknown) lets pgx infer each type from the bound value.
				oids := make([]uint32, countParams(sql))
				if res, ok := f.resultFor(sql); ok {
					copy(oids, res.ParamOIDs)
				}
				be.Send(&pgproto3.ParameterDescription{ParameterOIDs: oids})
			}
			// Transaction-control statements return no Rows.
			if isControlSQL(sql) {
				be.Send(&pgproto3.NoData{})
				break
			}
			res, ok := f.resultFor(sql)
			if ok && res.PgError == nil && len(res.Cols) > 0 {
				be.Send(rowDescription(res.Cols))
			} else {
				be.Send(&pgproto3.NoData{})
			}

		case *pgproto3.Execute:
			f.writeResult(be, s, s.portalSQL, false, s.resultFormats)

		case *pgproto3.Sync:
			if !s.inTxn {
				f.commit(s)
			}
			be.Send(&pgproto3.ReadyForQuery{TxStatus: s.txStatus()})
			_ = be.Flush()

		case *pgproto3.Terminate:
			return

		default:
			_ = be.Flush()
		}
	}
}

// resetTxn clears any open-transaction state, discarding uncommitted writes.
func (s *session) resetTxn() {
	s.inTxn = false
	s.readOnly = false
	s.pending = nil
}

// txStatus is the ReadyForQuery transaction-status byte: 'I' idle, 'T' in a
// transaction block, 'E' failed transaction block.
func (s *session) txStatus() byte {
	switch {
	case s.aborted:
		return 'E'
	case s.inTxn:
		return 'T'
	default:
		return 'I'
	}
}

// writeResult emits the canned DataRows + CommandComplete (or ErrorResponse)
// for the given SQL. When sendRowDesc is true (simple protocol) it also emits
// the RowDescription first. resultFormats are the per-column format codes from
// the Bind message (nil => all text). The session carries transaction state so
// BEGIN/COMMIT and READ ONLY enforcement behave like a real backend.
func (f *PG) writeResult(be *pgproto3.Backend, s *session, sql string, sendRowDesc bool, resultFormats []int16) {
	// Transaction control: interpret BEGIN (with characteristics) and COMMIT
	// directly rather than from the responder map.
	if isControlSQL(sql) {
		u := strings.ToUpper(strings.TrimSpace(sql))
		switch {
		case strings.HasPrefix(u, "BEGIN"):
			s.inTxn = true
			s.readOnly = strings.Contains(u, "READ ONLY")
			f.setLastBegin(sql)
			be.Send(&pgproto3.CommandComplete{CommandTag: []byte("BEGIN")})
		case u == "ROLLBACK":
			s.resetTxn()
			be.Send(&pgproto3.CommandComplete{CommandTag: []byte("ROLLBACK")})
		default: // COMMIT / END
			f.commit(s)
			s.resetTxn()
			be.Send(&pgproto3.CommandComplete{CommandTag: []byte("COMMIT")})
		}
		return
	}

	res, ok := f.resultFor(sql)
	if !ok {
		s.aborted = true
		be.Send(&pgproto3.ErrorResponse{
			Severity: "ERROR",
			Code:     "42P01",
			Message:  "relation does not exist (no fake result registered)",
		})
		return
	}

	// READ ONLY enforcement: a write inside a read-only transaction is rejected
	// with SQLSTATE 25006, exactly as PostgreSQL would.
	if s.inTxn && s.readOnly && res.IsWrite {
		s.aborted = true
		be.Send(&pgproto3.ErrorResponse{
			Severity: "ERROR",
			Code:     "25006",
			Message:  "cannot execute " + writeVerb(sql) + " in a read-only transaction",
		})
		return
	}

	if res.PgError != nil {
		s.aborted = true
		be.Send(res.PgError)
		return
	}
	if res.Requires != "" && !s.ran[res.Requires] {
		s.aborted = true
		be.Send(missingRelation(res.Requires))
		return
	}
	if res.WaitForCancel {
		_ = be.Flush()
		select {
		case <-s.cancelled:
		case <-time.After(10 * time.Second):
		}
		s.aborted = true
		be.Send(&pgproto3.ErrorResponse{Severity: "ERROR", Code: "57014", Message: "canceling statement due to user request"})
		return
	}
	if res.IsWrite {
		s.pending = append(s.pending, sql)
	}
	s.ran[sql] = true
	if sendRowDesc && len(res.Cols) > 0 {
		be.Send(rowDescription(res.Cols))
	}
	for _, row := range res.Rows {
		dr := f.encodeRow(res.Cols, row, resultFormats)
		if s.latin1 {
			for i, v := range dr.Values {
				if convertsEncoding(res.Cols[i].TypeOID, formatForCol(resultFormats, i)) {
					dr.Values[i] = toLatin1(v)
				}
			}
		}
		be.Send(dr)
	}
	Tag := res.Tag
	if Tag == "" {
		Tag = "SELECT " + itoa(len(res.Rows))
	}
	be.Send(&pgproto3.CommandComplete{CommandTag: []byte(Tag)})
}

// missingRelation is PostgreSQL's answer for a statement naming an object that does not exist yet.
func missingRelation(requires string) *pgproto3.ErrorResponse {
	return &pgproto3.ErrorResponse{Severity: "ERROR", Code: "42P01", Message: "relation does not exist (created by " + requires + ")"}
}

// writeVerb extracts the leading SQL verb (INSERT/UPDATE/DELETE/...) for the
// read-only rejection message, mirroring PostgreSQL's wording.
func writeVerb(sql string) string {
	fields := strings.Fields(strings.ToUpper(strings.TrimSpace(sql)))
	if len(fields) == 0 {
		return "statement"
	}
	return fields[0]
}

// formatForCol returns the wire format code (0 text, 1 binary) for column i.
func formatForCol(resultFormats []int16, i int) int16 {
	switch len(resultFormats) {
	case 0:
		return 0 // text
	case 1:
		return resultFormats[0] // single code applies to all columns
	default:
		if i < len(resultFormats) {
			return resultFormats[i]
		}
		return 0
	}
}

// RawColumn is a column value already in PostgreSQL's wire encoding, written to
// the DataRow verbatim. It exists for the types pgx can decode but not encode
// (record, whose codec is decode-only), where a test has to state the server's
// bytes itself.
type RawColumn []byte

// encodeRow encodes a native-value row into a DataRow honoring per-column
// format codes, using pgx's own type map (so pgx decodes it back natively).
func (f *PG) encodeRow(Cols []Col, row []any, resultFormats []int16) *pgproto3.DataRow {
	values := make([][]byte, len(Cols))
	for i, col := range Cols {
		v := row[i]
		if v == nil {
			values[i] = nil // SQL NULL
			continue
		}
		if b, ok := v.(RawColumn); ok {
			values[i] = b
			continue
		}
		format := formatForCol(resultFormats, i)
		encoded, err := f.typeMap.Encode(col.TypeOID, format, v, nil)
		if err != nil {
			// Fall back to a text rendering pgx can still parse for simple cases.
			encoded = []byte(itoaAny(v))
		}
		values[i] = encoded
	}
	return &pgproto3.DataRow{Values: values}
}

// convertsEncoding reports whether PostgreSQL converts a column to the client encoding: every text-format value, and the string types in binary too.
func convertsEncoding(oid uint32, format int16) bool {
	switch oid {
	case pgtype.TextOID, pgtype.VarcharOID, pgtype.BPCharOID, pgtype.NameOID:
		return true
	}
	return format == 0
}

// toLatin1 converts UTF-8 text to LATIN1 as PostgreSQL's output conversion does; the fixtures hold no character LATIN1 lacks.
func toLatin1(b []byte) []byte {
	if b == nil {
		return nil
	}
	out := make([]byte, 0, len(b))
	for _, r := range string(b) {
		out = append(out, byte(r))
	}
	return out
}

// rowDescription builds a RowDescription whose format codes match how values
// will be encoded. pgx ignores the format in RowDescription for extended-query
// results (it uses the Bind result formats), but we set text=0 which is correct
// for the simple-protocol path.
func rowDescription(Cols []Col) *pgproto3.RowDescription {
	fields := make([]pgproto3.FieldDescription, len(Cols))
	for i, c := range Cols {
		fields[i] = pgproto3.FieldDescription{
			Name:                 []byte(c.Name),
			TableOID:             c.TableOID,
			TableAttributeNumber: uint16(c.AttNum),
			DataTypeOID:          c.TypeOID,
			DataTypeSize:         -1,
			TypeModifier:         -1,
			Format:               0,
		}
	}
	return &pgproto3.RowDescription{Fields: fields}
}

// negotiateSSL handles the pgx SSLRequest and upgrades the connection to TLS.
func (f *PG) negotiateSSL(conn net.Conn) (net.Conn, error) {
	// SSLRequest is 8 bytes: int32 length (8) + int32 code (80877103).
	buf := make([]byte, 8)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return nil, err
	}
	code := int32(buf[4])<<24 | int32(buf[5])<<16 | int32(buf[6])<<8 | int32(buf[7])
	const sslRequestCode = 80877103
	if code != sslRequestCode {
		return nil, errors.New("expected SSLRequest")
	}
	// Accept SSL.
	if _, err := conn.Write([]byte{'S'}); err != nil {
		return nil, err
	}
	tlsConn := tls.Server(conn, f.tlsCfg)
	if err := tlsConn.Handshake(); err != nil {
		return nil, err
	}
	return tlsConn, nil
}

// selfSignedTLS produces a throwaway TLS config for the fake backend.
func selfSignedTLS(t testing.TB) *tls.Config {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("pgfake: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "pgfake"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("pgfake: %v", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		t.Fatalf("pgfake: %v", err)
	}

	return &tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12}
}

// itoa is a tiny helper to avoid importing strconv just for command tags.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b strings.Builder
	neg := n < 0
	if neg {
		n = -n
	}
	var digits [20]byte
	i := len(digits)
	for n > 0 {
		i--
		digits[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		b.WriteByte('-')
	}
	b.Write(digits[i:])
	return b.String()
}

// countParams returns the number of distinct $N placeholders in sql (the max
// N), which is what PostgreSQL reports in ParameterDescription. It is a small
// scanner sufficient for test fixtures (placeholders outside string literals).
func countParams(sql string) int {
	maxN := 0
	for i := 0; i < len(sql); i++ {
		if sql[i] != '$' {
			continue
		}
		j := i + 1
		n := 0
		for j < len(sql) && sql[j] >= '0' && sql[j] <= '9' {
			n = n*10 + int(sql[j]-'0')
			j++
		}
		if j > i+1 && n > maxN {
			maxN = n
		}
		i = j - 1
	}
	return maxN
}

// itoaAny renders a value as text for the rare encode-fallback path.
func itoaAny(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case int:
		return itoa(t)
	default:
		return ""
	}
}
