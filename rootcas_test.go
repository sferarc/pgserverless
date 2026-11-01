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

// sslmode=verify-ca checks only the issuer, so a certificate naming another host failed where psql connected.
func TestVerifyCAOnlyChecksTheIssuerButNotTheHostName(t *testing.T) {
	clearLibpqEnv(t)
	pg := pgfake.New(t)
	roots := x509.NewCertPool()
	require.True(t, roots.AppendCertsFromPEM(pg.CertificatePEM()))
	other := x509.NewCertPool()
	require.True(t, other.AppendCertsFromPEM(pgfake.New(t).CertificatePEM()))
	ctx := context.Background()

	dials := map[string]func(Upstream) error{
		"POST /sql": func(up Upstream) error {
			up.User, up.Database = "u", "db"
			conn, err := Dial(ctx, up, false)
			if err == nil {
				require.NoError(t, conn.Close(ctx))
			}
			return err
		},
		"WebSocket": func(up Upstream) error {
			conn, err := dialUpstreamRaw(ctx, up)
			if err == nil {
				require.NoError(t, conn.Close())
			}
			return err
		},
	}
	for name, dial := range dials {
		t.Run(name, func(t *testing.T) {
			// The fake's certificate names localhost and 127.0.0.1 only.
			up := Upstream{Addr: pg.Addr(), ServerName: "db.example.test", RootCAs: roots}
			require.Error(t, dial(up), "a full check refuses the name")

			up.VerifyCAOnly = true
			require.NoError(t, dial(up))

			up.RootCAs = other
			require.Error(t, dial(up), "the issuer is still checked")
			up.RootCAs = nil
			require.Error(t, dial(up), "without RootCAs the full check runs")
		})
	}
}

// Without RootCAs, VerifyCAOnly would trust any publicly issued certificate for any host, so the name stays checked.
func TestVerifyCAOnlyWithoutRootCAsStillChecksTheHostName(t *testing.T) {
	cfg := upstreamTLSConfig(Upstream{ServerName: "db.example.test", VerifyCAOnly: true}, "10.0.0.1")
	require.False(t, cfg.InsecureSkipVerify)
	require.Nil(t, cfg.VerifyConnection)
	require.Equal(t, "db.example.test", cfg.ServerName)

	cfg = upstreamTLSConfig(Upstream{ServerName: "db.example.test", VerifyCAOnly: true, RootCAs: x509.NewCertPool()}, "10.0.0.1")
	require.True(t, cfg.InsecureSkipVerify)
	require.NotNil(t, cfg.VerifyConnection)
}
