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

// MarshalJSON writes the row as a JSON object in column order, without HTML escaping as Neon's writer has none, so the response cap counts what Neon counts.
func (r ObjectRow) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	put := func(v any) error {
		if err := enc.Encode(v); err != nil {
			return err
		}
		// Encode ends every value with a newline the object does not carry there.
		b.Truncate(b.Len() - 1)
		return nil
	}
	b.WriteByte('{')
	for i, name := range r.Names {
		if i > 0 {
			b.WriteByte(',')
		}
		if err := put(name); err != nil {
			return nil, err
		}
		b.WriteByte(':')
		var v any
		if i < len(r.Values) {
			v = r.Values[i]
		}
		if err := put(v); err != nil {
			return nil, err
		}
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}
