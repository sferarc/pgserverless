package pgserverless

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"sync/atomic"

	"github.com/jackc/pgx/v5/pgproto3"
)

// ErrCopyUnsupported refuses a statement that starts a COPY to or from the client, which a JSON request cannot carry.
var ErrCopyUnsupported = errors.New("COPY to or from the client is not supported over HTTP")

// copyTrip records that a connection's upstream entered copy mode. pgx reports
// the broken stream differently on each path (a batch says "conn closed"), so
// writeQueryError asks the trip rather than matching the error.
type copyTrip struct{ tripped atomic.Bool }

type copyTripKey struct{}

// withCopyTrip returns ctx carrying a trip that dial wires into the connection.
func withCopyTrip(ctx context.Context) context.Context {
	return context.WithValue(ctx, copyTripKey{}, &copyTrip{})
}

func copyTripFrom(ctx context.Context) *copyTrip {
	t, _ := ctx.Value(copyTripKey{}).(*copyTrip)
	return t
}

func (t *copyTrip) hit() bool { return t != nil && t.tripped.Load() }

// frontend is a pgconn BuildFrontendFunc; pgconn calls it once per connection attempt.
func (t *copyTrip) frontend(r io.Reader, w io.Writer) *pgproto3.Frontend {
	return pgproto3.NewFrontend(&copyGuard{r: r, trip: t}, w)
}

// copyGuard follows the backend message framing and ends the stream at a
// CopyInResponse, CopyOutResponse or CopyBothResponse. pgconn ignores those,
// so COPY FROM STDIN waited for a message PostgreSQL never sends.
type copyGuard struct {
	r    io.Reader
	trip *copyTrip
	hdr  [5]byte
	nhdr int
	body int
}

func (g *copyGuard) Read(p []byte) (int, error) {
	if g.trip.tripped.Load() {
		return 0, ErrCopyUnsupported
	}
	n, err := g.r.Read(p)
	for i := 0; i < n; {
		if g.body > 0 {
			k := min(g.body, n-i)
			g.body -= k
			i += k
			continue
		}
		if g.nhdr == 0 && isCopyResponse(p[i]) {
			g.trip.tripped.Store(true)
			if i == 0 {
				return 0, ErrCopyUnsupported
			}
			// The messages before this one still reach pgconn; the next Read fails.
			return i, nil
		}
		g.hdr[g.nhdr] = p[i]
		g.nhdr++
		i++
		if g.nhdr == len(g.hdr) {
			g.body = int(binary.BigEndian.Uint32(g.hdr[1:])) - 4
			g.nhdr = 0
		}
	}
	return n, err
}

func isCopyResponse(t byte) bool {
	return t == 'G' || t == 'H' || t == 'W'
}
