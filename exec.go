package pgserverless

import (
	"context"
	"errors"
	"fmt"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
)

func QueryRows(ctx context.Context, conn *pgx.Conn, query string, params []any, arrayMode, rawText bool) (*QueryResult, error) {
	return queryRows(ctx, conn, query, params, arrayMode, rawText, nil)
}

func queryRows(ctx context.Context, conn *pgx.Conn, query string, params []any, arrayMode, rawText bool, budget *responseBudget) (*QueryResult, error) {
	rows, err := conn.Query(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	result, err := rowsToResult(rows, conn.TypeMap(), arrayMode, rawText, budget)
	rows.Close()
	if err != nil || rawText {
		return result, err
	}
	results := []QueryResult{*result}
	splitCatalogArrays(ctx, conn, results)
	return &results[0], nil
}

// executeSingle executes a single query and returns the result as JSON.
func executeSingle(w http.ResponseWriter, conn *pgx.Conn, ctx context.Context, query string, params []any, arrayMode, rawText bool, budget *responseBudget) {
	result, err := queryRows(ctx, conn, query, params, arrayMode, rawText, budget)
	if err != nil {
		writeQueryError(ctx, w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// executeBatch executes multiple queries in a transaction and returns all
// results. When the driver requested explicit transaction characteristics
// (read-only / isolation level / deferrable) the batch is wrapped in an
// explicit BEGIN … COMMIT so PostgreSQL applies and enforces them; otherwise it
// runs as a single implicit-transaction pipeline (pgx.SendBatch). A capped
// batch always takes the explicit path, as Neon's does, so a 507 can roll it back.
func executeBatch(w http.ResponseWriter, conn *pgx.Conn, ctx context.Context, queries []QueryItem, arrayMode, rawText bool, opts txnOptions, budget *responseBudget) {
	if !opts.isDefault() || budget.capped() {
		executeBatchTxn(w, conn, ctx, queries, arrayMode, rawText, opts, budget)
		return
	}

	batch := &pgx.Batch{}
	for _, q := range queries {
		batch.Queue(q.Query, ConvertParams(q.Params)...)
	}

	br := conn.SendBatch(ctx, batch)
	defer br.Close()

	results := make([]QueryResult, 0, len(queries))
	for i := range queries {
		rows, err := br.Query()
		if err != nil {
			writeQueryError(ctx, w, err)
			return
		}
		result, err := rowsToResult(rows, conn.TypeMap(), batchArrayMode(queries[i], arrayMode), rawText, budget)
		rows.Close()
		if err != nil {
			writeQueryError(ctx, w, err)
			return
		}
		results = append(results, *result)
	}

	// The lookup needs the connection, which the batch holds until it is closed.
	if !rawText && br.Close() == nil {
		splitCatalogArrays(ctx, conn, results)
	}
	writeJSON(w, http.StatusOK, BatchResponse{Results: results})
}

// executeBatchSimple runs a simple-protocol batch the way executeBatch does,
// as one simple query that pgx joins with ';', so a policy proxy still sees and
// brackets the whole batch itself. Each item must be one complete statement,
// and a newline on both sides of every ';' keeps a trailing -- comment from
// swallowing the next item.
func executeBatchSimple(w http.ResponseWriter, conn *pgx.Conn, ctx context.Context, queries []QueryItem, count func(string) (int, error), arrayMode, rawText bool, opts txnOptions, budget *responseBudget) {
	// executeBatchTxn queues BEGIN before the items and may queue COMMIT after them.
	txn := !opts.isDefault() || budget.capped()
	joined := make([]QueryItem, len(queries))
	for i, q := range queries {
		if err := checkBatchItem(count, q.Query); err != nil {
			writeQueryError(ctx, w, err)
			return
		}
		if i > 0 || txn {
			q.Query = "\n" + q.Query
		}
		if i < len(queries)-1 || txn {
			q.Query += "\n"
		}
		joined[i] = q
	}
	executeBatch(w, conn, ctx, joined, arrayMode, rawText, opts, budget)
}

// executeBatchTxn runs the batch inside an explicit transaction with the
// requested characteristics. The BEGIN/COMMIT bracket the per-statement queries
// in the same pipeline so the whole batch is atomic and PostgreSQL enforces the
// characteristics — notably, a READ ONLY batch rejects any write with SQLSTATE
// 25006, failing closed exactly like the proxy's read-only policy. A failure in
// any statement aborts the transaction (no COMMIT is reached), so partial
// effects never persist. Under a response cap PostgreSQL may run every
// statement before the handler reads past the cap, so COMMIT is sent only once
// every result fit; a 507 closes the connection with the transaction still
// open, which rolls it back, as Neon does.
func executeBatchTxn(w http.ResponseWriter, conn *pgx.Conn, ctx context.Context, queries []QueryItem, arrayMode, rawText bool, opts txnOptions, budget *responseBudget) {
	deferCommit := budget.capped()
	batch := &pgx.Batch{}
	batch.Queue(opts.beginSQL())
	for _, q := range queries {
		batch.Queue(q.Query, ConvertParams(q.Params)...)
	}
	if !deferCommit {
		batch.Queue("COMMIT")
	}

	br := conn.SendBatch(ctx, batch)
	defer br.Close()

	// Drain the BEGIN result.
	if _, err := br.Exec(); err != nil {
		writeQueryError(ctx, w, err)
		return
	}

	results := make([]QueryResult, 0, len(queries))
	for i := range queries {
		rows, err := br.Query()
		if err != nil {
			writeQueryError(ctx, w, err)
			return
		}
		result, err := rowsToResult(rows, conn.TypeMap(), batchArrayMode(queries[i], arrayMode), rawText, budget)
		if errors.Is(err, ErrResponseTooLarge) {
			// Closing stops an oversized result instead of draining it.
			_ = conn.Close(ctx)
		}
		rows.Close()
		if err != nil {
			writeQueryError(ctx, w, err)
			return
		}
		results = append(results, *result)
	}

	if deferCommit {
		if err := br.Close(); err != nil {
			writeQueryError(ctx, w, err)
			return
		}
		if _, err := conn.Exec(ctx, "COMMIT"); err != nil {
			writeQueryError(ctx, w, err)
			return
		}
	} else if _, err := br.Exec(); err != nil { // Drain the COMMIT result.
		writeQueryError(ctx, w, err)
		return
	}

	if !rawText && br.Close() == nil {
		splitCatalogArrays(ctx, conn, results)
	}
	opts.setResponseHeaders(w.Header())
	writeJSON(w, http.StatusOK, BatchResponse{Results: results})
}

// responseBudget counts the row bytes one response has read against its cap;
// a batch shares one budget across its statements, as Neon's buffer does.
type responseBudget struct {
	max, used int64
}

func (b *responseBudget) capped() bool {
	return b != nil && b.max > 0
}

// add charges one row's values and fails once the response passes the cap.
func (b *responseBudget) add(row [][]byte) error {
	if !b.capped() {
		return nil
	}
	for _, v := range row {
		b.used += int64(len(v))
	}
	if b.used > b.max {
		return fmt.Errorf("%w (max is %d bytes)", ErrResponseTooLarge, b.max)
	}
	return nil
}

// batchArrayMode resolves the effective array mode for one batch item: a
// per-query arrayMode overrides the request-level default.
func batchArrayMode(q QueryItem, arrayMode bool) bool {
	if q.ArrayMode != nil {
		return *q.ArrayMode
	}
	return arrayMode
}

// RowsToResult converts pgx rows to a QueryResult. When rawText is true, values
// are emitted as their raw PostgreSQL text representation (a JSON string per
// column, or null for SQL NULL) — matching what the @neondatabase/serverless
// driver expects so its own type parsers run client-side. Otherwise values are
// pgx-decoded and coerced to the Neon driver's native-type contract. m is the
// connection's type map, which the coercion uses to render back to PostgreSQL
// text the types whose Go decode does not marshal to it.
func RowsToResult(rows pgx.Rows, m *pgtype.Map, arrayMode, rawText bool) (*QueryResult, error) {
	return rowsToResult(rows, m, arrayMode, rawText, nil)
}

func rowsToResult(rows pgx.Rows, m *pgtype.Map, arrayMode, rawText bool, budget *responseBudget) (*QueryResult, error) {
	descs := rows.FieldDescriptions()
	fields := make([]FieldDefinition, len(descs))
	for i, d := range descs {
		fields[i] = FieldDefinition{
			Name:             d.Name,
			TableID:          d.TableOID,
			ColumnID:         uint16(d.TableAttributeNumber),
			DataTypeID:       d.DataTypeOID,
			DataTypeSize:     d.DataTypeSize,
			DataTypeModifier: d.TypeModifier,
			Format:           "text",
		}
	}

	var resultRows []any
	for rows.Next() {
		if err := budget.add(rows.RawValues()); err != nil {
			return nil, err
		}
		values, err := rowValues(rows, descs, m, rawText)
		if err != nil {
			return nil, err
		}

		if arrayMode {
			resultRows = append(resultRows, values)
		} else {
			row := make(map[string]any, len(descs))
			for i, d := range descs {
				row[d.Name] = values[i]
			}
			resultRows = append(resultRows, row)
		}
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	if resultRows == nil {
		resultRows = []any{}
	}

	cmd, rowCount := parseCommandTag(rows.CommandTag().String())

	return &QueryResult{
		Fields:     fields,
		Command:    cmd,
		RowCount:   rowCount,
		Rows:       resultRows,
		RowAsArray: arrayMode,
	}, nil
}

// rowValues extracts the current row's column values. In raw-text mode it reads
// the unparsed text bytes (string per column, nil for SQL NULL) so the response
// carries verbatim PG text. Otherwise it uses pgx's native decode and applies
// the Neon driver's type-coercion contract (int8/numeric → string, etc.).
func rowValues(rows pgx.Rows, descs []pgconn.FieldDescription, m *pgtype.Map, rawText bool) ([]any, error) {
	if rawText {
		raw := rows.RawValues()
		values := make([]any, len(descs))
		for i := range descs {
			if raw[i] == nil {
				values[i] = nil
				continue
			}
			values[i] = string(raw[i])
		}
		return values, nil
	}

	values, err := nativeRowValues(rows, descs, m)
	if err != nil {
		return nil, err
	}
	// RawValues is the same row Values just decoded, kept because the decode
	// flattens a multi-dimensional array and only the raw header still says how
	// many dimensions it had (see reshapeArrayValue).
	raw := rows.RawValues()
	for i, d := range descs {
		if i < len(raw) {
			if v, ok := recordColumnValue(m, raw[i], d.DataTypeOID, d.Format); ok {
				values[i] = v
				continue
			}
		}
		values[i] = coerceNeonValue(m, values[i], d.DataTypeOID)
		if i < len(raw) {
			values[i] = numericScaleValue(m, values[i], raw[i], d.DataTypeOID, d.Format)
			values[i] = reshapeArrayValue(m, values[i], raw[i], d.DataTypeOID, d.Format)
		}
	}
	return values, nil
}
