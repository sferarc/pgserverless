package pgserverless

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The boolean batch headers must behave like the isolation level: a value the
// caller meant as "yes" is honored whatever its spelling, and a value we cannot
// read is a 400 rather than a silently dropped guarantee. READ ONLY is the one
// that matters most, because dropping it turns a batch PostgreSQL would have
// rejected with 25006 into one that commits its writes.
func TestParseTxnOptions_BooleanHeaders(t *testing.T) {
	for _, tc := range []struct {
		name    string
		value   string
		want    bool
		wantErr bool
	}{
		{"unset", "", false, false},
		{"lowercase true", "true", true, false},
		{"capitalized true", "True", true, false},
		{"uppercase true", "TRUE", true, false},
		{"numeric true", "1", true, false},
		{"lowercase false", "false", false, false},
		{"capitalized false", "False", false, false},
		{"numeric false", "0", false, false},
		{"unreadable", "yes", false, true},
		{"garbage", "read-only please", false, true},
	} {
		t.Run("read-only "+tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.value != "" {
				h.Set("Batch-Read-Only", tc.value)
			}
			opts, err := parseTxnOptions(h)
			if tc.wantErr {
				require.Error(t, err, "an unreadable value must fail closed, not default to read-write")
				assert.Contains(t, err.Error(), "Batch-Read-Only")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, opts.readOnly)
		})

		t.Run("deferrable "+tc.name, func(t *testing.T) {
			h := http.Header{}
			if tc.value != "" {
				h.Set("Batch-Deferrable", tc.value)
			}
			opts, err := parseTxnOptions(h)
			if tc.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), "Batch-Deferrable")
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, opts.deferrable)
		})
	}
}

// A capitalized read-only header still reaches the BEGIN, so isDefault does not
// send the batch down the plain implicit-transaction path.
func TestParseTxnOptions_CapitalizedReadOnlyIsNotDefault(t *testing.T) {
	h := http.Header{}
	h.Set("Batch-Read-Only", "True")

	opts, err := parseTxnOptions(h)
	require.NoError(t, err)
	assert.False(t, opts.isDefault())
	assert.Equal(t, "BEGIN READ ONLY", opts.beginSQL())
}
