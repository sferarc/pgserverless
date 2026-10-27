# Changelog

## 0.2.2

### Patch Changes

- 095275f: `"char"`, `tid` and `xid8` columns, and their arrays, are returned as PostgreSQL text on the JSON path. `pg_class.relkind` used to arrive as the number `114` instead of `"r"`, a `ctid` as a JSON object instead of `"(0,1)"`, and an `xid8` as a number that loses precision past 2^53.
- af8b79b: Without `Neon-Raw-Text-Output`, `time` and `interval` values (and their arrays) are now spelled as PostgreSQL prints them under the default IntervalStyle: `12:34:56` instead of `12:34:56.000000`, `1 day` instead of `1 day 00:00:00`, `-1 days +02:00:00` for mixed signs, and `infinity` or `-infinity` for PostgreSQL 17's infinite intervals instead of raw field maxima.
- 3f6ef2c: Without `Neon-Raw-Text-Output`, `tstzrange` and `tstzmultirange` bounds now carry the `+00` offset PostgreSQL prints in a UTC session instead of pgx's `Z`.

## 0.2.1

### Patch Changes

- 32bae0f: Require `github.com/labstack/echo/v4` v4.16.1, which fixes GHSA-cxf3-9h37-7rr8, GHSA-9969-6676-49h5, GHSA-h7wr-284f-5qvc and GHSA-f48p-c9v4-h7xv. The module declared v4.15.4, which is inside the vulnerable range of all four. pgserverless itself does not use the affected `Proxy`, `Decompress` or `BodyDump` middleware, but a consumer that builds on the old floor could resolve a vulnerable echo for its own routes.
- 2dfba67: Number parameters are bound as PostgreSQL number text instead of as Go floats. A fraction bound to an integer parameter (`1.5` into `$1::int4`) now fails with `22P02` instead of being truncated to `1`, matching the same value inside an array. A whole number past the int64 range bound to an integer parameter now fails with PostgreSQL's `22003` (value out of range).

## 0.2.0

### Minor Changes

- 7d0c4b1: `NewHTTPHandler` serves `POST /sql` and `GET /v2` as a plain `net/http` handler, so the module no longer needs echo to be mounted. `HTTPAdmission` is its extension point, with `WithHTTPWebSocketAdmission`, `WithHTTPDialErrorHook`, `StaticHTTPUpstream` and `NormalizeHTTPHeaders` beside it. The echo entry points are unchanged and now wrap the same core. `cmd/pgserverless` is a minimal reference server for one database from `DATABASE_URL`, serving `POST /sql`, `GET /v2` and `GET /health`. The driver conformance suite targets it through `PGSERVERLESS_BIN` and now also covers Prisma through `@prisma/adapter-neon`. `pgfake` identifies itself neutrally.

### Patch Changes

- d6170b6: A single query's `arrayMode: false` in the request body now returns object rows even when `Neon-Array-Mode: true` is set, matching Neon. Before, the body could only turn array mode on.
- 4dd5d09: Raw-text requests, which is every request the Neon driver sends, now run on the extended protocol with untyped text parameters, as Neon does. A batch item ending in a `--` comment no longer comments out the next item (it used to fail the batch with a 500), and a query holding several statements is refused with `42601` instead of running all of them and returning only the first result. `Upstream.RawTextSimpleProtocol` keeps raw-text requests on the simple protocol for an upstream that enforces some statement rules only there.
- fa902b8: Raw-text requests on `Upstream.RawTextSimpleProtocol` now follow Neon's statement rules too, counted by the function passed to the new `WithStatementCounter` (or `WithHTTPStatementCounter`) option, which should be a real PostgreSQL parser; without one, `RawTextSimpleProtocol` is ignored and requests use the extended protocol. A query holding several statements, or one the counter cannot parse, is refused with `42601` before it is sent. A batch is still sent as one joined query, but each item must be one statement the counter can parse (else `42601`) and the `;` separators sit on their own line, so an item ending in a `--` comment no longer comments out the next one.

## 0.1.1

### Patch Changes

- 4709fd3: Error messages that carry a libpq keyword connection string with `password=` or `sslpassword=`, quoted or not, are now replaced with the generic upstream failure message instead of being logged or returned. Only `postgres://` URLs were covered before.

## 0.1.0

First release.

- HTTP transport at `POST /sql`: single queries, array mode (header or body, per query in a batch), raw-text output, and batches of up to 100 queries run as one transaction. Isolation level, read-only and deferrable come from request headers and are enforced by PostgreSQL; an unrecognised value is refused rather than ignored. Each request has a 30 second budget.
- WebSocket transport at `GET /v2`: declines the client's `SSLRequest`, then relays the wire protocol without parsing, so authentication runs end to end between the client and the database.
- One extension point, `Admission`, run before any protocol work on both transports. `AdmissionError` picks the status of a refusal, `WithWebSocketAdmission` gives the tunnel its own policy, and `WithDialErrorHook` reports failed upstream dials. `StaticUpstream` serves a single database.
- Upstream TLS on by default, with `TLSDisable` for databases that do not serve it.
- Value coercion for the driver's native-type contract: `int8` and `numeric` as strings, `NaN` and infinities as PostgreSQL text, infinite timestamps as `infinity` and `-infinity`, `macaddr`, `bit` and `varbit` as text, multi-dimensional arrays with their dimensions, `inet` without a single-host netmask, and ranges and multiranges as PostgreSQL literals.
- Errors carry the field names the driver copies onto its error object.
- The driver's `Neon-` prefixed request headers are accepted alongside the plain names.
- `pgfake`, an in-process PostgreSQL backend for tests: SSL negotiation, startup and authentication, the extended query protocol and `SendBatch` pipelines over a real socket, with a rejecting-auth variant.
- A driver conformance suite in `driver-conformance` that runs `@neondatabase/serverless`, Drizzle, Kysely and Waddler against a server.
