package pgserverless

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgproto3"
)

// The startup phase is the only part of the tunnel that inspects bytes.
// Everything after it is a byte pipe, so these tests are the whole of what can
// go wrong in this file without a live database.

func TestStartupDeclinesSSLRequestThenReadsStartup(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })

	type result struct {
		startup []byte
		err     error
	}
	done := make(chan result, 1)
	go func() {
		b, err := declineEncryptionAndReadStartup(server)
		done <- result{b, err}
	}()

	ssl, _ := (&pgproto3.SSLRequest{}).Encode(nil)
	if _, err := client.Write(ssl); err != nil {
		t.Fatalf("write SSLRequest: %v", err)
	}

	// net.Pipe is unbuffered and synchronous, so the decline has to be read
	// here before anything else is written, and only one goroutine may read
	// this end. An earlier version of this test had a second reader racing for
	// the same byte, and the two deadlocked waiting for each other.
	buf := make([]byte, 1)
	if _, err := io.ReadFull(client, buf); err != nil {
		t.Fatalf("read decline: %v", err)
	}
	if buf[0] != 'N' {
		t.Fatalf("SSLRequest: want decline 'N', got %q", buf[0])
	}

	enc, _ := (&pgproto3.StartupMessage{
		ProtocolVersion: pgproto3.ProtocolVersionNumber,
		Parameters:      map[string]string{"user": "alice", "database": "app"},
	}).Encode(nil)
	if _, err := client.Write(enc); err != nil {
		t.Fatalf("write StartupMessage: %v", err)
	}

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("startup: %v", r.err)
		}
		// The returned bytes are replayed to the upstream verbatim, so they
		// have to decode as a StartupMessage with the parameters intact.
		var got pgproto3.StartupMessage
		if err := got.Decode(r.startup[4:]); err != nil {
			t.Fatalf("returned bytes are not a StartupMessage: %v", err)
		}
		if got.Parameters["user"] != "alice" || got.Parameters["database"] != "app" {
			t.Fatalf("parameters not preserved: %v", got.Parameters)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startup phase did not finish")
	}
}

// TestStartupDeclinesTwiceBeforeStartup covers a client that asks for GSSAPI
// encryption and then for TLS. Declining once and expecting a StartupMessage
// next would leave the second request unanswered and the connection hung.
func TestStartupDeclinesTwiceBeforeStartup(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })

	done := make(chan error, 1)
	go func() {
		_, err := declineEncryptionAndReadStartup(server)
		done <- err
	}()

	send := func(b []byte) { _, _ = client.Write(b) }
	readOne := func() byte {
		buf := make([]byte, 1)
		_, _ = io.ReadFull(client, buf)
		return buf[0]
	}

	gss, _ := (&pgproto3.GSSEncRequest{}).Encode(nil)
	send(gss)
	if got := readOne(); got != 'N' {
		t.Fatalf("GSSEncRequest: want decline 'N', got %q", got)
	}

	ssl, _ := (&pgproto3.SSLRequest{}).Encode(nil)
	send(ssl)
	if got := readOne(); got != 'N' {
		t.Fatalf("SSLRequest: want decline 'N', got %q", got)
	}

	enc, _ := (&pgproto3.StartupMessage{
		ProtocolVersion: pgproto3.ProtocolVersionNumber,
		Parameters:      map[string]string{"user": "bob"},
	}).Encode(nil)
	send(enc)

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("startup after two declines: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startup did not finish after two declines")
	}
}

// TestStartupReportsCancelRequest pins the contract that a CancelRequest
// returns no startup and no error, which is how the caller tells "ignore this
// connection" from "something went wrong".
func TestStartupReportsCancelRequest(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })

	done := make(chan struct {
		b   []byte
		err error
	}, 1)
	go func() {
		b, err := declineEncryptionAndReadStartup(server)
		done <- struct {
			b   []byte
			err error
		}{b, err}
	}()

	enc, _ := (&pgproto3.CancelRequest{ProcessID: 42, SecretKey: []byte{0, 0, 0, 7}}).Encode(nil)
	_, _ = client.Write(enc)

	select {
	case r := <-done:
		if r.err != nil {
			t.Fatalf("cancel request reported an error: %v", r.err)
		}
		if r.b != nil {
			t.Fatalf("cancel request returned a startup message: %q", r.b)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("cancel request did not finish")
	}
}

// TestStartupRefusesEndlessEncryptionRequests stops a client from holding the
// phase open forever by never sending a StartupMessage. Without the bound this
// loops for as long as the peer keeps writing.
func TestStartupRefusesEndlessEncryptionRequests(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })

	done := make(chan error, 1)
	go func() {
		_, err := declineEncryptionAndReadStartup(server)
		done <- err
	}()

	go func() {
		ssl, _ := (&pgproto3.SSLRequest{}).Encode(nil)
		buf := make([]byte, 1)
		for range 6 {
			if _, err := client.Write(ssl); err != nil {
				return
			}
			if _, err := io.ReadFull(client, buf); err != nil {
				return
			}
		}
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("endless encryption requests were accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startup phase never gave up")
	}
}

// TestStartupRefusesOversizedPacket keeps a client from making the server allocate a length it chose; PostgreSQL caps it too.
func TestStartupRefusesOversizedPacket(t *testing.T) {
	t.Parallel()

	client, server := net.Pipe()
	t.Cleanup(func() { client.Close(); server.Close() })

	done := make(chan error, 1)
	go func() {
		_, err := declineEncryptionAndReadStartup(server)
		done <- err
	}()

	go func() { _, _ = client.Write([]byte{0, 0, 0x27, 0x11}) }() // 10001 bytes

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("an oversized startup packet was accepted")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("startup phase waited for an oversized packet's body")
	}
}
