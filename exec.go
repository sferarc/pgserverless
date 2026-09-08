package pgserverless

import (
	"context"
	"net/http"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/labstack/echo/v4"
)

func QueryRows(ctx context.Context, conn *pgx.Conn, query string, params []any, arrayMode, rawText bool) (*QueryResult, error) {
	rows, err := conn.Query(ctx, query, params...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return RowsToResult(rows, conn.TypeMap(), arrayMode, rawText)
}

// executeSingle executes a single query and returns the result as JSON.
func (h *Handler) executeSingle(c echo.Context, conn *pgx.Conn, ctx context.Context, query string, params []any, arrayMode, rawText bool) error {
	result, err := QueryRows(ctx, conn, query, params, arrayMode, rawText)
	if err != nil {
		return HandlePgError(c, err)
	}
	return c.JSON(http.StatusOK, result)
}

// executeBatch executes multiple queries in a transaction and returns all
// results. When the driver requested explicit transaction characteristics
// (read-only / isolation level / deferrable) the batch is wrapped in an
// explicit BEGIN … COMMIT so PostgreSQL applies and enforces them; otherwise it
// runs as a single implicit-transaction pipeline (pgx.SendBatch).
func (h *Handler) executeBatch(c echo.Context, conn *pgx.Conn, ctx context.Context, queries []QueryItem, arrayMode, rawText bool, opts txnOptions) error {
	if !opts.isDefault() {
		return h.executeBatchTxn(c, conn, ctx, queries, arrayMode, rawText, opts)
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
			return HandlePgError(c, err)
		}
		result, err := RowsToResult(rows, conn.TypeMap(), batchArrayMode(queries[i], arrayMode), rawText)
		rows.Close()
		if err != nil {
			return HandlePgError(c, err)
		}
		results = append(results, *result)
	}

	return c.JSON(http.StatusOK, BatchResponse{Results: results})
}

// executeBatchTxn runs the batch inside an explicit transaction with the
// requested characteristics. The BEGIN/COMMIT bracket the per-statement queries
// in the same pipeline so the whole batch is atomic and PostgreSQL enforces the
// characteristics — notably, a READ ONLY batch rejects any write with SQLSTATE
// 25006, failing closed exactly like the proxy's read-only policy. A failure in
// any statement aborts the transaction (no COMMIT is reached), so partial
// effects never persist.
func (h *Handler) executeBatchTxn(c echo.Context, conn *pgx.Conn, ctx context.Context, queries []QueryItem, arrayMode, rawText bool, opts txnOptions) error {
	batch := &pgx.Batch{}
	batch.Queue(opts.beginSQL())
	for _, q := range queries {
		batch.Queue(q.Query, ConvertParams(q.Params)...)
	}
	batch.Queue("COMMIT")

	br := conn.SendBatch(ctx, batch)
	defer br.Close()

	// Drain the BEGIN result.
	if _, err := br.Exec(); err != nil {
		return HandlePgError(c, err)
	}

	results := make([]QueryResult, 0, len(queries))
	for i := range queries {
		rows, err := br.Query()
		if err != nil {
			return HandlePgError(c, err)
		}
		result, err := RowsToResult(rows, conn.TypeMap(), batchArrayMode(queries[i], arrayMode), rawText)
		rows.Close()
		if err != nil {
			return HandlePgError(c, err)
		}
		results = append(results, *result)
	}

	// Drain the COMMIT result.
	if _, err := br.Exec(); err != nil {
		return HandlePgError(c, err)
	}

	return c.JSON(http.StatusOK, BatchResponse{Results: results})
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

	values, err := rows.Values()
	if err != nil {
		return nil, err
	}
	// RawValues is the same row Values just decoded, kept because the decode
	// flattens a multi-dimensional array and only the raw header still says how
	// many dimensions it had (see reshapeArrayValue).
	raw := rows.RawValues()
	for i, d := range descs {
		values[i] = coerceNeonValue(m, values[i], d.DataTypeOID)
		if i < len(raw) {
			values[i] = reshapeArrayValue(m, values[i], raw[i], d.DataTypeOID, d.Format)
		}
	}
	return values, nil
}
