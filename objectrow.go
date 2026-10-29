package pgserverless

import (
	"bytes"
	"encoding/json"
)

// ObjectRow is one object-mode result row: the columns' names and values in
// result order. It marshals as a JSON object keyed by column name in that
// order, a repeated name included, as Neon's proxy writes it. A Go map would
// sort the keys.
type ObjectRow struct {
	Names  []string
	Values []any
}

// MarshalJSON writes the row as a JSON object in column order.
func (r ObjectRow) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, name := range r.Names {
		if i > 0 {
			b.WriteByte(',')
		}
		key, err := json.Marshal(name)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		var v any
		if i < len(r.Values) {
			v = r.Values[i]
		}
		val, err := json.Marshal(v)
		if err != nil {
			return nil, err
		}
		b.Write(val)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
