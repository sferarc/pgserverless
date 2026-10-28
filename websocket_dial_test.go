package pgserverless

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// stalledUpstream accepts one connection, lets answer reply to the SSLRequest, then goes silent until the test ends.
func stalledUpstream(t *testing.T, answer func(net.Conn)) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	done := make(chan struct{})
	t.Cleanup(func() {
		close(done)
		ln.Close()
	})
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		if _, err := io.ReadFull(c, make([]byte, 8)); err != nil {
			return
		}
		answer(c)
		<-done
	}()
	return ln.Addr().String()
}

func TestDialUpstreamRawGivesUpOnAStalledTLSNegotiation(t *testing.T) {
	for name, answer := range map[string]func(net.Conn){
		"no answer to the SSLRequest": func(net.Conn) {},
		"no TLS handshake after S":    func(c net.Conn) { _, _ = c.Write([]byte{'S'}) },
	} {
		t.Run(name, func(t *testing.T) {
			addr := stalledUpstream(t, answer)
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()

			start := time.Now()
			conn, err := dialUpstreamRaw(ctx, Upstream{Addr: addr})
			if conn != nil {
				conn.Close()
			}
			require.Error(t, err)
			assert.Less(t, time.Since(start), 5*time.Second, "the dial budget covers the TLS negotiation, not only the TCP connect")
		})
	}
}

func TestDialUpstreamRawLeavesTheTunnelUsableAfterTheDialBudget(t *testing.T) {
	pg := pgfake.New(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	conn, err := dialUpstreamRaw(ctx, Upstream{Addr: pg.Addr(), InsecureSkipVerify: true})
	require.NoError(t, err)
	defer conn.Close()

	<-ctx.Done()
	_, err = conn.Write(startupBytes(t))
	require.NoError(t, err, "the dial deadline must not outlive the dial")
	msg, err := pgproto3.NewFrontend(conn, conn).Receive()
	require.NoError(t, err)
	assert.NotNil(t, msg)
}
