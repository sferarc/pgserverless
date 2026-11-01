package pgserverless

import (
	"context"
	"crypto/x509"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// A database with a private CA certificate was reachable only by turning the check off.
func TestRootCAsVerifiesTheUpstreamAgainstAPrivateCA(t *testing.T) {
	clearLibpqEnv(t)
	pg := pgfake.New(t)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(pg.CertificatePEM()))
	ctx := context.Background()

	t.Run("POST /sql", func(t *testing.T) {
		up := Upstream{Addr: pg.Addr(), User: "u", Database: "db"}
		_, err := Dial(ctx, up, false)
		require.Error(t, err, "the system roots do not hold the fake's certificate")

		up.RootCAs = roots
		conn, err := Dial(ctx, up, false)
		require.NoError(t, err)
		require.NoError(t, conn.Close(ctx))
	})

	t.Run("WebSocket", func(t *testing.T) {
		up := Upstream{Addr: pg.Addr()}
		_, err := dialUpstreamRaw(ctx, up)
		require.Error(t, err, "the system roots do not hold the fake's certificate")

		up.RootCAs = roots
		conn, err := dialUpstreamRaw(ctx, up)
		require.NoError(t, err)
		require.NoError(t, conn.Close())
	})

	t.Run("a different root still fails", func(t *testing.T) {
		other := x509.NewCertPool()
		require.True(t, other.AppendCertsFromPEM(pgfake.New(t).CertificatePEM()))
		_, err := Dial(ctx, Upstream{Addr: pg.Addr(), User: "u", Database: "db", RootCAs: other}, false)
		require.Error(t, err)
		_, err = dialUpstreamRaw(ctx, Upstream{Addr: pg.Addr(), RootCAs: other})
		require.Error(t, err)
	})
}
