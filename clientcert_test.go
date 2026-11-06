package pgserverless

import (
	"context"
	"crypto/tls"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/sferarc/pgserverless/pgfake"
)

// A database that requires a client certificate refused every connection, since Upstream had no way to offer one.
func TestClientCertIsOfferedToAnUpstreamThatRequiresOne(t *testing.T) {
	clearLibpqEnv(t)
	certPEM, keyPEM := pgfake.ClientCertificate(t)
	cert, err := tls.X509KeyPair(certPEM, keyPEM)
	require.NoError(t, err)
	otherPEM, otherKeyPEM := pgfake.ClientCertificate(t)
	other, err := tls.X509KeyPair(otherPEM, otherKeyPEM)
	require.NoError(t, err)

	pg := pgfake.New(t)
	pg.RequireClientCert(certPEM)
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
			up := Upstream{Addr: pg.Addr(), InsecureSkipVerify: true}
			require.Error(t, dial(up), "without a certificate the upstream refuses the handshake")

			up.ClientCert = &other
			require.Error(t, dial(up), "a certificate the upstream does not trust is refused")

			up.ClientCert = &cert
			require.NoError(t, dial(up))
		})
	}
}
