package pgserverless

import (
	"context"
	"log/slog"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// catalogArrayQuery mirrors Neon's typeinfo lookup for unknown column types;
// parsePgArray splits only on commas, so other delimiters are left out.
const catalogArrayQuery = `SELECT t.oid, t.typelem FROM pg_catalog.pg_type t ` +
	`JOIN pg_catalog.pg_type e ON e.oid = t.typelem ` +
	`WHERE t.oid = ANY($1::pg_catalog.oid[]) AND t.typcategory = 'A' AND e.typdelim = ','`

// splitCatalogArrays turns an array column whose type exists only in the
// catalog (an enum, domain or extension type's array), which pgx hands back as
// one literal string, into the JSON array Neon returns for it. The pg_type
// lookup runs once per response and only when such a column holds an
// array-shaped value; a failed lookup leaves the strings as they arrived.
func splitCatalogArrays(ctx context.Context, conn *pgx.Conn, results []QueryResult) {
	m := conn.TypeMap()
	var oids []uint32
	seen := make(map[uint32]bool)
	for i := range results {
		for col, f := range results[i].Fields {
			oid := f.DataTypeID
			if seen[oid] || !catalogOnlyType(m, oid) || !columnHasArrayLiteral(&results[i], col) {
				continue
			}
			seen[oid] = true
			oids = append(oids, oid)
		}
	}
	if len(oids) == 0 {
		return
	}

	elems, err := catalogArrayElems(ctx, conn, oids)
	if err != nil {
		slog.Debug("pgserverless: catalog array lookup failed", "err", SanitizeError(err.Error()))
		return
	}
	for i := range results {
		splitResultArrays(&results[i], elems)
	}
}

// catalogOnlyType reports whether a column type is one pgx knows nothing about
// and that no other coercion already splits.
func catalogOnlyType(m *pgtype.Map, oid uint32) bool {
	if oid == 0 {
		return false
	}
	if _, ok := codeclessArrayElemOID[oid]; ok {
		return false
	}
	_, known := m.TypeForOID(oid)
	return !known
}

// catalogArrayElems maps each of oids that the catalog says is an array to its
// element type.
func catalogArrayElems(ctx context.Context, conn *pgx.Conn, oids []uint32) (map[uint32]uint32, error) {
	rows, err := conn.Query(ctx, catalogArrayQuery, oids)
	if err != nil {
		return nil, err
	}
	elems := make(map[uint32]uint32, len(oids))
	var arrOID, elemOID uint32
	_, err = pgx.ForEachRow(rows, []any{&arrOID, &elemOID}, func() error {
		elems[arrOID] = elemOID
		return nil
	})
	if err != nil {
		return nil, err
	}
	return elems, nil
}

func columnHasArrayLiteral(r *QueryResult, col int) bool {
	for _, row := range r.Rows {
		v, ok := resultCell(row, col)
		if !ok {
			continue
		}
		if s, ok := v.(string); ok && (strings.HasPrefix(s, "{") || strings.HasPrefix(s, "[")) {
			return true
		}
	}
	return false
}

func splitResultArrays(r *QueryResult, elems map[uint32]uint32) {
	for col, f := range r.Fields {
		elemOID, ok := elems[f.DataTypeID]
		if !ok {
			continue
		}
		for _, row := range r.Rows {
			v, ok := resultCell(row, col)
			if !ok {
				continue
			}
			if split, ok := textArrayElems(v, elemOID); ok {
				setResultCell(row, col, split)
			}
		}
	}
}

// resultCell reads column col of row.
func resultCell(row any, col int) (any, bool) {
	vals := rowCells(row)
	if col < len(vals) {
		return vals[col], true
	}
	return nil, false
}

func setResultCell(row any, col int, v any) {
	if vals := rowCells(row); col < len(vals) {
		vals[col] = v
	}
}

func rowCells(row any) []any {
	switch row := row.(type) {
	case []any:
		return row
	case ObjectRow:
		return row.Values
	}
	return nil
}
