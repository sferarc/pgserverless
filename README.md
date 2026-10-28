# pgserverless

Serve PostgreSQL over HTTP and WebSockets from Go, speaking the request and response shapes the `@neondatabase/serverless` driver sends.

A Cloudflare Worker, a Vercel Edge Function or a Deno Deploy isolate cannot open a TCP socket, so it cannot speak the PostgreSQL wire protocol. The way around that has settled into a shape: `POST /sql` carries a query and its parameters as JSON, and `GET /v2` upgrades to a WebSocket that tunnels the wire protocol. More than one provider answers on it. This package is the server half, written against the shape rather than one vendor's endpoint.

```bash
go get github.com/sferarc/pgserverless
```

## Quick start

Requires Go 1.26 or later.

With `net/http`:

```go
package main

import (
	"log"
	"net/http"

	"github.com/sferarc/pgserverless"
)

func main() {
	const db = "db.internal:5432"

	h := pgserverless.NewHTTPHandler(
		pgserverless.StaticHTTPUpstream(db),
		// The tunnel carries credentials inside the wire protocol, so it needs
		// an address and nothing else from the request.
		pgserverless.WithHTTPWebSocketAdmission(func(*http.Request) (pgserverless.Upstream, error) {
			return pgserverless.Upstream{Addr: db}, nil
		}),
	)

	mux := http.NewServeMux()
	mux.Handle("/", h) // POST /sql and GET /v2
	log.Fatal(http.ListenAndServe(":4000", mux))
}
```

With Echo, which the handler also supports:

```go
package main

import (
	"github.com/labstack/echo/v4"
	"github.com/sferarc/pgserverless"
)

func main() {
	const db = "db.internal:5432"

	h := pgserverless.NewHandler(
		pgserverless.StaticUpstream(db),
		pgserverless.WithWebSocketAdmission(func(echo.Context) (pgserverless.Upstream, error) {
			return pgserverless.Upstream{Addr: db}, nil
		}),
	)

	e := echo.New()
	h.Register(e)          // POST /sql
	h.RegisterWebSocket(e) // GET  /v2
	e.Logger.Fatal(e.Start(":4000"))
}
```

Point the driver at it:

```js
import { neon, neonConfig } from "@neondatabase/serverless";

neonConfig.fetchEndpoint = "http://localhost:4000/sql";
const sql = neon("postgres://user:pass@localhost/mydb");
await sql`select now()`;
```

For `Pool` and `Client`, which use the WebSocket transport:

```js
import { Pool, neonConfig } from "@neondatabase/serverless";

neonConfig.wsProxy = "localhost:4000/v2";
neonConfig.useSecureWebSocket = false; // true behind TLS
neonConfig.pipelineConnect = false; // required for SCRAM, see below

const pool = new Pool({
  connectionString: "postgres://user:pass@localhost/mydb",
});
const { rows } = await pool.query("select now()");
```

Upstream TLS is on by default. For a database that does not serve TLS, return an `Upstream` with `TLSMode: pgserverless.TLSDisable` from your own `Admission`.

## Compatibility

What the [driver conformance suite](./driver-conformance) exercises, at the versions it pins:

| Client | Version | Transport | Status |
| --- | --- | --- | --- |
| `@neondatabase/serverless`, `neon()` | 1.2.0 | HTTP `/sql` | Covered: queries, templates, array and raw-text modes, transactions with isolation level and read-only, errors, data types |
| `@neondatabase/serverless`, `Client`, `Pool` | 1.2.0 | WebSocket `/v2` | Covered |
| Drizzle ORM, `drizzle-orm/neon-http` | 0.45.4 | HTTP `/sql` | Covered, including `db.batch` (Drizzle's neon-http driver has no interactive transactions) |
| Drizzle ORM, `drizzle-orm/neon-serverless` | 0.45.4 | WebSocket `/v2` | Covered: CRUD, data types, errors, transactions with commit and rollback |
| Kysely with `kysely-neon` | 0.29.6, 3.0.0 | HTTP | Covered |
| Waddler, `waddler/neon-http` | 0.1.1 | HTTP | Covered |
| Prisma, `@prisma/adapter-neon`: `PrismaNeon`, `PrismaNeonHttp` | 7.10.1 | WebSocket, HTTP | Covered through the adapter interface, with no generated client: raw queries, typed arguments, errors and WebSocket transactions. No HTTP transactions |

Not covered, so not claimed:

- Real edge runtimes. The runtime files simulate Workers, Vercel and browser conditions under Node.

## The problem this solves

Most of the work is not the routing. It is the value coercion, which is where an implementation written from the request and response examples goes quietly wrong.

### Value coercion

Every rule below exists because the obvious encoding either loses information or produces something the driver cannot parse:

|  |  |
| --- | --- |
| `int8`, `numeric` | Strings. JSON numbers are float64 and lose precision beyond 2^53. |
| `NaN`, `Infinity`, `-Infinity` | PostgreSQL's own text. `encoding/json` cannot marshal them at all and fails the whole response. |
| Infinite timestamps | The words `infinity` and `-infinity`, not the integers a `pgtype.InfinityModifier` marshals to, which are indistinguishable from data. |
| `macaddr`, `bit`, `varbit` | Text, not the base64 their Go representations marshal to. |
| Multi-dimensional arrays | Re-nested from the value's own binary array header. A flat decode loses the shape. |
| `inet` | Drops the netmask when it covers a single host, matching what PostgreSQL prints. A real netmask and every `cidr` pass through unchanged. |
| Ranges, multiranges | PostgreSQL literals, not the Go structs behind them. |

Raw-text mode, the driver's primary path since it runs its own type parsers client-side keyed by `dataTypeID`, bypasses most of this and returns every value as PostgreSQL text.

## The seam

Everything a host has to decide before a query runs (which database, is this caller allowed, is the tenant paying) is one function:

```go
type HTTPAdmission func(r *http.Request) (Upstream, error)
```

Everything after that decision (parse the body, dial, run, coerce, encode) is this package. Return an `*AdmissionError` to choose the HTTP status of a refusal, for example 403 for a suspended tenant. `StaticHTTPUpstream` (`StaticUpstream` for echo) is the whole implementation for a deployment in front of a single database. A multi-tenant platform's version is much larger, and that asymmetry is the point: the protocol is the same either way, and the policy is not something a library can guess.

## Transports

**`POST /sql`** carries one request and one response. It handles single queries, array mode, raw text mode, and batches of up to 100 queries run in a transaction, with isolation level, read-only and deferrable taken from request headers. Each request has a 30 second budget.

**`GET /v2`** upgrades to a WebSocket carrying the PostgreSQL wire protocol. It declines the client's `SSLRequest`, because the WebSocket already carries the encryption, and then relays without parsing. Authentication runs end to end between the client and the database, so this package never holds a credential on that path. Admission runs before the upgrade, so a refusal is an ordinary HTTP error.

## Known behaviour

- Only HTTP and WebSocket are served. There is no raw TCP listener for the PostgreSQL wire protocol.
- `NewHandler` and its options take an `echo.Context`, and the module still depends on Echo. `NewHTTPHandler` is the `net/http` form of the same handler; both run the same code.
- `StaticHTTPUpstream` and `StaticUpstream` read credentials from the request's connection string header, which the WebSocket path does not carry. Give the tunnel its own admission with `WithHTTPWebSocketAdmission` or `WithWebSocketAdmission`.
- On the WebSocket path only `Addr`, `ServerName`, `InsecureSkipVerify` and `TLSMode` of the returned `Upstream` are used. User, password and database come from the client's own startup message.
- Each HTTP request opens a connection to the database and closes it. There is no pooling; put a pooler in front of the database if it cannot take that. The driver's `Pool-Opt-In` header is accepted and ignored.
- The upstream dial uses TLS unless `Upstream.TLSMode` is `TLSDisable`. `StaticUpstream` does not set it.
- A WebSocket `CancelRequest` is ignored. Cancellation arrives on a second connection that a scaled-out deployment may route elsewhere.
- The tunnel does not check the `Origin` header. `Admission` is the authentication point.
- The tunnel bounds only the upstream dial (10 seconds). An open session has no time limit.
- A `POST /sql` body over `MaxRequestBodyBytes` (10 MiB, Neon's default) is refused with 413 and `request is too large (max is 10485760 bytes)`, before the upstream is dialed.
- Responses are uncapped unless `WithMaxResponseBytes` or `WithHTTPMaxResponseBytes` is set. With a cap, a response whose row values pass it fails with 507 and `response is too large (max is N bytes)`, and a batch counts all its statements together. `MaxResponseBytes` is Neon's default of 10 MiB, which `cmd/pgserverless` uses. The cap counts the bytes PostgreSQL sent, so the JSON can be somewhat larger.
- SCRAM databases need `neonConfig.pipelineConnect = false` on the client. The driver's default pipelining assumes a cleartext password request and hangs otherwise.

## Correctness

The package's own tests drive the real HTTP handler end to end against an in-process PostgreSQL backend, using a real `jackc/pgx` client, the same client path a production deployment uses.

That backend is exported as `pgserverless/pgfake` and is useful on its own: it speaks SSL negotiation, startup and authentication, the extended query protocol and `SendBatch` pipelines, over a real socket with a real TLS certificate, with no container and no `initdb`.

```go
pg := pgfake.New(t)
pg.SetResult("SELECT id FROM users", pgfake.Result{
	Cols: []pgfake.Col{{Name: "id", TypeOID: pgtype.Int4OID}},
	Rows: [][]any{{int32(1)}},
	Tag:  "SELECT 1",
})
```

Run the Go tests with `go test ./...`.

## Run the conformance suite against your own server

The suite in [`driver-conformance`](./driver-conformance) drives the real `@neondatabase/serverless` driver, Drizzle, Kysely and Waddler against one `DATABASE_URL`. It creates and drops tables, so point it at a disposable database. It needs Node and pnpm.

Against a server you already run, with `DATABASE_URL` set to a connection string whose host is that server:

```bash
cd driver-conformance
DATABASE_URL='postgres://user:pass@host/db' pnpm test
```

| Variable | Default | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | none, required | Connection string of the server under test. The host is where the driver sends HTTP and WebSocket traffic. |
| `PORT` | port in the URL, else 443 (80 without TLS) | Port for `/sql` and `/v2`. |
| `PGSERVERLESS_TLS` | `true` | `false` selects `http` and `ws`, for a local server. |
| `PIPELINE_CONNECT` | `password` | `false` disables the driver's connect pipelining, which a SCRAM database needs. |

To have the suite start your server for you, build one from this package and supply the database it should front:

```bash
UPSTREAM_URL='postgres://app:pw@127.0.0.1:5432/app?sslmode=disable' \
PGSERVERLESS_BIN=/path/to/your-server \
pnpm test:go-server
```

The binary receives `DATABASE_URL`, `PGSERVERLESS_ADDR` and must answer `GET /health`. Arguments after the script name pass through to vitest, for example `pnpm test:go-server tests/http`. Use `SERVER_PORT` to move off 4000.

For a parity run, point `DATABASE_URL` at a Neon database and compare the two results. A failure on both is a wrong expectation in the suite.

## Dependencies

`jackc/pgx/v5` for the client and the type map, `labstack/echo/v4` for the echo entry points, `coder/websocket` for the upgrade.

## Contributing

Issues and pull requests are welcome here. `go test ./...` runs the unit and conformance tests with no database; the driver suite above needs one.

The package is developed for and used in production by [PgBeam](https://pgbeam.com), which serves this protocol for every project on the platform.

## License

Apache-2.0. See [LICENSE](./LICENSE).
