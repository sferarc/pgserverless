# pgserverless driver conformance

Checks a pgserverless server against the public [`@neondatabase/serverless`](https://github.com/neondatabase/serverless) driver and the clients built on it. The driver is the de facto client for the protocol, so what it accepts is the practical definition of compatible.

The suite drives one `DATABASE_URL` through three layers: the HTTP `neon()` function, the WebSocket `Client` and `Pool`, and ecosystem clients (Drizzle, Kysely, Waddler). It creates and drops tables, so point it at a disposable database.

It complements the Go corpus in [`spec/pgserverless`](../../../../spec/pgserverless), which pins the wire shapes. This suite pins what the real driver does with them.

## Run it against the Go server

You supply the Postgres. The script builds the server from this repository, starts it in front of that database, runs the suite and stops the server:

```bash
UPSTREAM_URL='postgres://app:pw@127.0.0.1:5432/app?sslmode=disable' pnpm test:go-server
```

Arguments pass through to vitest, for example `pnpm test:go-server tests/http`. Use `PGSERVERLESS_BIN` for a prebuilt binary (required outside the monorepo) and `SERVER_PORT` to move off 4000.

## Run it against any other server

```bash
DATABASE_URL='postgres://user:pass@host/db' pnpm test
```

| Variable | Default | Purpose |
| --- | --- | --- |
| `DATABASE_URL` | none, required | Connection string of the server under test. The host is where the driver sends HTTP and WebSocket traffic. |
| `PORT` | port in the URL, else 443 (80 without TLS) | Port for `/sql` and `/v2`. |
| `PGSERVERLESS_TLS` | `true` | `false` selects `http` and `ws`, for a local server. |
| `PIPELINE_CONNECT` | `password` | `false` disables the driver's connect pipelining, which a SCRAM database needs. |

For a parity run, point `DATABASE_URL` at a Neon database and compare the two results. A failure on both is a wrong expectation in the suite.

## Layout

```
helpers/        connection setup (neonConfig), fixtures, typed result helpers
tests/http/         neon(): queries, templates, transactions, types, errors, modes
tests/websocket/    Client and Pool over /v2
tests/config/       neonConfig variations
tests/integrations/ Drizzle, Kysely, Waddler, driver contract checks
tests/runtimes/     runtime-shaped usage run under Node
tests/performance/  load and latency smoke checks
tests/edge-cases/   payload limits, binary round trips, injection, type and error consistency
```

Tests run in parallel against a live database. A test that creates a table names it with `uniqueTableName()` and drops it in `afterAll`.

## Tooling

```bash
pnpm typecheck
pnpm exec biome check .   # monorepo only, the lint config is not mirrored
```
