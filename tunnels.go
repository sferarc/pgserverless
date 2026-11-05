package pgserverless

import (
	"context"
	"encoding/binary"
	"log/slog"
	"sync"
)

// Tunnels tracks the WebSocket tunnels a handler has open, so that a server
// which is stopping can cancel the statements they are running.
//
// http.Server.Shutdown neither waits for nor closes a hijacked connection, and
// a process that exits with a tunnel open leaves its statement running on
// PostgreSQL until the backend next writes to the dead socket. A server that
// passes a Tunnels to WithHTTPTunnels calls CancelRunning before it exits.
//
// The zero value is ready to use, and a nil *Tunnels tracks nothing.
type Tunnels struct {
	mu   sync.Mutex
	open map[*tunnel]struct{}
}

// WithHTTPTunnels registers every tunnel the handler opens with t.
func WithHTTPTunnels(t *Tunnels) HTTPOption {
	return func(h *httpHandler) { h.tunnels = t }
}

// Running reports how many open tunnels have a statement in flight: a Query,
// Sync or FunctionCall that PostgreSQL has not yet answered with ReadyForQuery.
func (t *Tunnels) Running() int {
	n := 0
	for _, tun := range t.snapshot() {
		if _, ok := tun.cancelPacket(); ok {
			n++
		}
	}
	return n
}

// CancelRunning sends PostgreSQL a CancelRequest for the statement each open
// tunnel has in flight, on a new upstream connection as a client cancels its
// own, and returns once every one is delivered or ctx ends. It returns how many
// tunnels it sent one for.
func (t *Tunnels) CancelRunning(ctx context.Context) int {
	var wg sync.WaitGroup
	sent := 0
	for _, tun := range t.snapshot() {
		packet, ok := tun.cancelPacket()
		if !ok {
			continue
		}
		sent++
		wg.Go(func() {
			server, err := dialUpstreamRaw(ctx, tun.up)
			if err != nil {
				slog.Warn("pgserverless: cancelling a tunnel's statement failed", "err", SanitizeError(err.Error()))
				return
			}
			defer server.Close()
			deliverCancel(server, packet)
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
	return sent
}

func (t *Tunnels) snapshot() []*tunnel {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	out := make([]*tunnel, 0, len(t.open))
	for tun := range t.open {
		out = append(out, tun)
	}
	return out
}

// add registers a tunnel whose StartupMessage has just been forwarded to up; a nil t returns nil.
func (t *Tunnels) add(up Upstream) *tunnel {
	if t == nil {
		return nil
	}
	// The forwarded startup is answered with a ReadyForQuery like a statement is.
	tun := &tunnel{up: up, pending: 1}
	tun.fromClient = msgScanner{onMsg: tun.clientMessage}
	tun.fromServer = msgScanner{keep: 'K', onMsg: tun.serverMessage}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.open == nil {
		t.open = make(map[*tunnel]struct{})
	}
	t.open[tun] = struct{}{}
	return tun
}

func (t *Tunnels) remove(tun *tunnel) {
	if t == nil || tun == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.open, tun)
}

// tunnel is what the relay learns about one session from the message framing it forwards.
type tunnel struct {
	up         Upstream
	fromClient msgScanner
	fromServer msgScanner

	mu sync.Mutex
	// key is BackendKeyData's process ID and secret key, which is a CancelRequest's body.
	key []byte
	// pending counts the client's messages that PostgreSQL answers with ReadyForQuery, less the answers seen.
	pending int
}

func (tun *tunnel) clientMessage(typ byte, _ []byte) {
	switch typ {
	case 'Q', 'S', 'F':
		tun.mu.Lock()
		tun.pending++
		tun.mu.Unlock()
	}
}

func (tun *tunnel) serverMessage(typ byte, body []byte) {
	tun.mu.Lock()
	defer tun.mu.Unlock()
	switch typ {
	case 'Z':
		tun.pending--
	case 'K':
		if len(body) >= 8 {
			tun.key = append([]byte(nil), body...)
		}
	}
}

// cancelPacket returns the CancelRequest for the session's statement in flight, if it has one.
func (tun *tunnel) cancelPacket() ([]byte, bool) {
	tun.mu.Lock()
	defer tun.mu.Unlock()
	if tun.key == nil || tun.pending <= 0 || tun.fromClient.broken || tun.fromServer.broken {
		return nil, false
	}
	p := make([]byte, 8, 8+len(tun.key))
	binary.BigEndian.PutUint32(p[0:4], uint32(8+len(tun.key)))
	binary.BigEndian.PutUint32(p[4:8], 80877102)
	return append(p, tun.key...), true
}

// maxKeyData bounds the BackendKeyData body kept: a process ID and a secret key of at most 256 bytes (protocol 3.2).
const maxKeyData = 4 + 256

// msgScanner follows the framing of one direction of a session after startup (a type byte, then a length that
// counts itself) as an io.Writer, keeping only a header and the body of the one message type it is asked for.
type msgScanner struct {
	keep  byte
	onMsg func(typ byte, body []byte)

	head    [5]byte
	headLen int
	left    int
	keeping bool
	body    []byte
	// broken is set by a length no message can have; the scanner then stops, since it can no longer find a boundary.
	broken bool
}

// Write never fails, so a tee into it cannot stop the relay.
func (s *msgScanner) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 && !s.broken {
		if s.headLen < len(s.head) {
			c := copy(s.head[s.headLen:], p)
			s.headLen += c
			p = p[c:]
			if s.headLen < len(s.head) {
				break
			}
			length := binary.BigEndian.Uint32(s.head[1:])
			if length < 4 {
				s.broken = true
				break
			}
			s.left = int(length) - 4
			s.keeping = s.keep != 0 && s.head[0] == s.keep && s.left <= maxKeyData
			s.body = s.body[:0]
		}
		c := min(s.left, len(p))
		if s.keeping {
			s.body = append(s.body, p[:c]...)
		}
		s.left -= c
		p = p[c:]
		if s.left == 0 {
			s.onMsg(s.head[0], s.body)
			s.headLen = 0
		}
	}
	return n, nil
}
