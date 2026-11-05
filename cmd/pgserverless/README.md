# cmd/pgserverless

A server that puts one PostgreSQL database behind `POST /sql` and `GET /v2`, so runtimes without TCP sockets (edge functions, Workers, browsers) can reach it with the unmodified `@neondatabase/serverless` driver.

Clients bring their own database credentials in the connection string. The server never lends `DATABASE_URL`'s user or password to a request.

## Run it

With Go:

```
go install github.com/sferarc/pgserverless/cmd/pgserverless@latest
DATABASE_URL='postgres://db.internal:5432/app' pgserverless
```

It listens on `127.0.0.1:4000` by default.

With Docker, from the root of this repository:

```
docker build -f cmd/pgserverless/Dockerfile -t pgserverless .
docker run --rm -p 127.0.0.1:4000:4000 \
  -e DATABASE_URL='postgres://db.internal:5432/app' \
  -e PGSERVERLESS_ALLOW_REMOTE=true \
  pgserverless
```

Inside the container the server binds `:4000`, which it cannot tell apart from a public interface, so it refuses to start without a token or `PGSERVERLESS_ALLOW_REMOTE=true`. Here `-p 127.0.0.1:4000:4000` is what restricts access, and the variable says so. For anything other machines can reach, set `PGSERVERLESS_AUTH_TOKEN` instead.

To try it end to end without a database of your own:

```
cd cmd/pgserverless/example && docker compose up --build
```

That starts PostgreSQL, this server and a Node script that queries through both transports.

## Point a client at it

```js
import { neon, neonConfig } from "@neondatabase/serverless";

neonConfig.fetchEndpoint = "http://localhost:4000/sql";
const sql = neon("postgres://user:pass@localhost/app");

const rows = await sql`SELECT now()`;
```

`Pool` and `Client` use the WebSocket transport:

```js
import { Pool, neonConfig } from "@neondatabase/serverless";

neonConfig.wsProxy = "localhost:4000/v2";
neonConfig.useSecureWebSocket = false; // true behind TLS
neonConfig.pipelineConnect = false; // required for SCRAM, see below

const pool = new Pool({
  connectionString: "postgres://user:pass@localhost/app",
});
const { rows } = await pool.query("SELECT now()");
```

With `PGSERVERLESS_AUTH_TOKEN` set, every request needs `Authorization: Bearer <token>`. `neon(url, { authToken })` sends it on `POST /sql`. A browser WebSocket cannot send the header, so a browser using `Pool` needs a proxy in front that adds it.

## Settings

| Variable | Default | Meaning |
| --- | --- | --- |
| `DATABASE_URL` | none, required | The database to serve. Only its host, port, `sslmode` and `sslrootcert` are used. |
| `PGSERVERLESS_ADDR` | `127.0.0.1:4000` (`:4000` in the image) | Listen address. |
| `PGSERVERLESS_AUTH_TOKEN` | none | When set, `POST /sql` and `GET /v2` require `Authorization: Bearer <token>`, compared in constant time. |
| `PGSERVERLESS_ALLOW_REMOTE` | `false` | `true` lets the server listen beyond loopback with no token. `PGSERVERLESS_ALLOW_UNAUTHENTICATED=true` is read the same way. |
| `PGSERVERLESS_CORS_ORIGINS` | none | Comma separated origins, or `*`, allowed to call `POST /sql` from a browser. Unset, no cross-origin page can read a response. With no token, these are also the only other origins whose browser WebSocket upgrades are accepted. |
| `PGSERVERLESS_LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error`. |
| `PGSERVERLESS_LOG_FORMAT` | `text` | `text` for `slog` key=value lines, or `json` for one JSON object per line. Logs go to stderr. |

TLS to the database follows `DATABASE_URL`'s `sslmode` as libpq reads it:

- `disable` connects in the clear.
- `allow` and `prefer` try TLS and fall back to the clear.
- `require` encrypts without checking the certificate, unless `sslrootcert` is set.
- `verify-ca` and `verify-full` check the certificate, against `sslrootcert` when it names a file.
- No `sslmode` requires TLS and checks the certificate and host name.

A self-signed database certificate on a trusted network is `sslmode=require`; to check it, set `sslrootcert` to its CA file.

`GET /health` answers `{"status":"ok"}` without touching the database.

On SIGTERM or SIGINT it stops accepting requests and gives running ones, and statements running in WebSocket tunnels, 7 seconds to finish. It then cancels the rest on the database and exits within 3 more seconds, inside Docker's default 10 second stop timeout.

## Exposure

Anyone who can reach this server and knows a database credential can use the database. So:

- It refuses to listen beyond loopback with no token unless `PGSERVERLESS_ALLOW_REMOTE=true` says the network already restricts access, as it does behind a private network or a service mesh.
- It sends no CORS headers unless origins are listed, and a request without its own `Connection-String` is refused, so a cross-site form post cannot run SQL.
- With no token, it refuses a WebSocket upgrade from a browser page on an origin `PGSERVERLESS_CORS_ORIGINS` does not list, since browsers apply no CORS to WebSockets. A page on `http://localhost:3000` that uses `Pool` needs that origin listed.
- With no token on a loopback listener, it refuses a `Host` other than `localhost`, a `.localhost` name or a loopback address, so a DNS rebinding page cannot reach it. To serve another name, set a token.
- It does not terminate TLS. Run it behind something that does, or where the hop is private.

## SCRAM and pipelining

The driver pipelines its first messages by default, assuming the server will ask for a cleartext password. PostgreSQL usually asks for SCRAM instead, so a client on the default hangs. Set `neonConfig.pipelineConnect = false`. This follows from the protocol rather than from this server, and [SPEC.md](../../spec/SPEC.md) records it.

## What it is not

- Not a connection pooler. Each `POST /sql` opens a connection and closes it; put a pooler in front of a database that cannot take that.
- Not a policy engine, cache or rate limiter. Build those on the package's admission function, which decides per request whether it proceeds and which database it reaches.
