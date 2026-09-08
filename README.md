# pgserverless

Serve PostgreSQL over HTTP and WebSockets, speaking the request and response shapes the `@neondatabase/serverless` driver sends.

```bash
go get github.com/sferarc/pgserverless
```

```go
e := echo.New()
h := pgserverless.NewHandler(pgserverless.StaticUpstream("db.internal:5432"))
h.Register(e)          // POST /sql
h.RegisterWebSocket(e) // GET  /v2
e.Start(":4000")
```

That is a complete, conforming server for a single database. Point the driver at it and nothing on the client side changes:

```js
import { neon } from "@neondatabase/serverless";
const sql = neon("postgres://user:pass@localhost:4000/mydb");
await sql`select now()`;
```

## The problem this solves

A Cloudflare Worker, a Vercel Edge Function, a Deno Deploy isolate: none of them can open a TCP socket, so none of them can speak the PostgreSQL wire protocol. The way around that has settled into a shape — `POST /sql` carrying a query and its parameters as JSON, and `GET /v2` upgrading to a WebSocket that tunnels the real wire protocol — and more than one provider now answers on it.

This package treats that as a shared protocol rather than as one vendor's endpoint. If you are putting PostgreSQL behind a runtime with no sockets, this is the server half.

Most of the work is not the routing. It is the value coercion, which is where an implementation written from the request and response examples goes quietly wrong.

## Value coercion is the hard part

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

Raw-text mode — the driver's primary path, since it runs its own type parsers client-side keyed by `dataTypeID` — bypasses most of this and returns every value as PostgreSQL text.

## The seam

Everything a host has to decide before a query runs (which database, is this caller allowed, is the tenant paying) is one function:

```go
type Admission func(c echo.Context) (Upstream, error)
```

Everything after that decision — parse the body, dial, run, coerce, encode — is this package.

`StaticUpstream` is the whole implementation for a deployment in front of a single database. A multi-tenant platform's version is much larger, and that asymmetry is the point: the protocol is the same either way, and the policy is not something a library can guess.

## Both transports

**`POST /sql`** carries one request and one response, and is what a fetch-based client uses. It handles single queries, array mode, raw text mode, and batches inside a transaction, with isolation level and read-only taken from request headers.

**`GET /v2`** upgrades to a WebSocket carrying the PostgreSQL wire protocol. It declines the client's `SSLRequest`, because the WebSocket already carries the encryption, and then relays without parsing. Authentication runs end to end between the client and the database, so this package never holds a credential on that path.

## Correctness

The conformance suite drives the real HTTP handler end to end against an in-process PostgreSQL backend, using a real `jackc/pgx` client — the same client path a production deployment uses.

That backend is exported as `pgserverless/pgfake` and is useful on its own: it speaks SSL negotiation, startup and authentication, the extended query protocol, and `SendBatch` pipelines, over a real socket with a real TLS certificate, with no container and no `initdb`.

```go
pg := pgfake.New(t)
pg.SetResult("SELECT id FROM users", pgfake.Result{
	Cols: []pgfake.Col{{Name: "id", TypeOID: pgtype.Int4OID}},
	Rows: [][]any{{int32(1)}},
	Tag:  "SELECT 1",
})
```

## Dependencies

`jackc/pgx/v5` for the client and the type map, `labstack/echo/v4` for routing, `coder/websocket` for the upgrade.

## Contributing

Issues and pull requests are welcome here. Changes are synchronised with the repository this package is developed in, so a merged pull request travels back rather than being reapplied by hand.

The package is developed for and used in production by [PgBeam](https://pgbeam.com), which serves this protocol for every project on the platform.

## License

Apache-2.0. See [LICENSE](./LICENSE).
