# Changelog

## 0.2.6

### Patch Changes

- 6823ed8: A `POST /sql` statement whose request ends, by the 30 second query timeout or the client going away, is now cancelled on the database with a CancelRequest instead of being left running after the connection is cut. The response is Neon's: status 400 with `{"message":"Query cancelled, connection was terminated","code":"08P01"}`, where it used to be 504 `upstream connection timed out`. `Dial` connections get the same cancel.
- 2052ba5: A native-mode batch (no `Neon-Raw-Text-Output`) now runs its statements in order inside one transaction, as Neon does, so an item can use a table an earlier item creates. It used to prepare every statement before running the first and failed such a batch with `42P01`. Raw-text batches, which the driver sends, are unchanged.

## 0.2.5

### Patch Changes

- 6c4c013: The WebSocket transport now forwards the bytes a client pipelines after its StartupMessage. The driver's default `pipelineConnect: "password"` sends the password in the same message as the startup, and the transport used to read it into a buffer and drop it, so a connection to a cleartext or md5 upstream hung until the server's authentication timeout. A startup packet longer than PostgreSQL's 10000 byte limit is refused.

## 0.2.4

### Patch Changes

- 87c2858: A batch that commits now answers with the transaction characteristics it ran with, as Neon does: `Neon-Batch-Read-Only: true` and `Neon-Batch-Deferrable: true` when they were set, and `Neon-Batch-Isolation-Level` spelled `ReadUncommitted`, `ReadCommitted`, `RepeatableRead` or `Serializable`. A single query, a failed batch and an option sent as `false` carry none of them.

## 0.2.3

### Patch Changes

- 1c5f6f8: Boolean parameters are bound as the text `true` or `false` instead of as Go bools. A JSON `true` bound to a `text`, `varchar` or integer parameter used to fail with a 500 in native mode because pgx cannot encode a bool there; it now reaches PostgreSQL, which returns `true` for text and its own `22P02` for an integer. Raw text mode bound the same value as `t`, so `SELECT $1::text` now returns `true` there too.
- 934866b: Without `Neon-Raw-Text-Output`, an array of an enum, a domain or an extension type is now a JSON array of strings, nested per dimension, as Neon returns it, instead of one array literal string. The type is looked up in `pg_type` once per response, and only when such a column holds an array value.
- 7fa9b4d: Without `Neon-Raw-Text-Output`, built-in arrays pgx has no codec for (`timetz[]`, `pg_lsn[]`, `tsquery[]`, `refcursor[]`, the `reg*[]` types, `txid_snapshot[]`, `pg_snapshot[]` and the multirange arrays) are now JSON arrays of strings, nested per dimension, as Neon returns them, instead of one array literal string.
- c4473bf: `WithHTTPCORSOrigins` lets browser pages call `POST /sql` on `NewHTTPHandler`: `OPTIONS /sql` answers the preflight as Neon does (200, `Allow: OPTIONS, POST`, every request header the handler reads, `Access-Control-Max-Age: 86400`), and every `POST /sql` response, errors included, carries `Access-Control-Allow-Origin`. `"*"` allows any origin, Neon's setting; a list allows only those origins. Without the option nothing changes: no CORS headers are sent and the preflight is refused with 405. `cmd/pgserverless` reads the origins from `PGSERVERLESS_CORS_ORIGINS`.
- 1007b93: A JSON integer parameter is bound with the digits the client sent. The request body was decoded into float64, so `9007199254740993` bound to `$1::int8` (or inside an array, or as a member of a `jsonb` object) arrived as `9007199254740992`, and a lookup by a bigint id past 2^53 matched a different row. Numbers with a fraction or exponent bind as before. A number outside float64's range, such as `1e400`, used to fail the whole request with a 400 and now reaches PostgreSQL, which stores it in `numeric` and rejects it for `float8`.
- b393ffe: Without `Neon-Raw-Text-Output`, the geometric types (`point`, `lseg`, `box`, `path`, `polygon`, `line`, `circle` and their arrays) now print each coordinate as PostgreSQL's float8out does: `(1e+20,1e-05)` instead of `(100000000000000000000,0.00001)`, `(1234567,0)` instead of `(1.234567e+06,0)` for a point, and `Infinity` instead of `+Inf`.
- 109b09c: Without `Neon-Raw-Text-Output`, `json` and `jsonb` values (and their arrays) are now returned as the JSON text PostgreSQL sent. Numbers keep their exact digits instead of being rounded through a float64, object keys keep PostgreSQL's order, and a number past the float64 range such as `'{"n": 1e400}'::jsonb` no longer fails the whole response.
- 894d66a: Without `Neon-Raw-Text-Output`, a `numeric` zero now keeps its scale as PostgreSQL prints it: a `numeric(10,2)` column holding zero returns `"0.00"` instead of `"0"`, in scalars, `numeric[]` (including multi-dimensional arrays) and record fields.
- 0613065: Without `Neon-Raw-Text-Output`, a `numrange` or `nummultirange` bound that is a numeric zero now keeps its scale as PostgreSQL prints it: `numrange(0.00, 1.50)` returns `"[0.00,1.50)"` instead of `"[0,1.50)"`, in scalars, `numrange[]` and record fields.
- 5c7000c: JSON object parameters are bound without HTML escaping. An object such as `{"html":"<p>"}` used to bind as `{"html":"\u003cp\u003e"}`, which a `json` or `text` column stored as written; it now binds as the client sent it, with sorted keys as Neon binds it. The same applies to an object inside an array parameter.
- 3591007: Without `Neon-Raw-Text-Output`, `oid`, `xid` and `cid` columns (and their arrays) are now returned as decimal strings, as Neon's server returns them, instead of JSON numbers. `pg_class.oid` used to arrive as `1259` and now arrives as `"1259"`.
- 0c00561: Without `Neon-Raw-Text-Output`, a `record` column now spells its fields exactly as PostgreSQL's record text does. A `timestamptz` field ends in `+00` rather than `Z`, a timestamp outside years 0 to 9999 keeps PostgreSQL's spelling, and an array field renders each element in its own type's PostgreSQL text (`{01:00:00}`, `{1e+20,0.5}`, `{0.00}`) instead of pgx's. A `record[]` or range array field no longer leaves the whole record undecoded.
- a3be49f: `POST /sql` bodies over 10 MiB (`MaxRequestBodyBytes`) are refused with 413 and `request is too large (max is 10485760 bytes)`, Neon's limit and message, before the upstream is dialed. The handler used to read a body of any size, so a server built on `NewHTTPHandler` or `NewHandler` without its own limit could be made to buffer an unbounded request.
- 8969a29: `WithMaxResponseBytes` (and `WithHTTPMaxResponseBytes`) caps a `POST /sql` response: once the row values PostgreSQL sent for it pass the limit, the request fails with 507 and `response is too large (max is N bytes)`, Neon's status and message. A batch counts all its statements against one limit. A capped batch runs in one transaction that commits only after every result fit, so a 507 rolls back all of its statements. `MaxResponseBytes` is Neon's default of 10 MiB. Responses stay uncapped unless the option is set; `cmd/pgserverless` sets it. `ErrResponseTooLarge` now reads `response is too large`.
- b7cc7d3: Without `Neon-Raw-Text-Output`, a `timestamp` or `timestamptz` dated before 1 BC or after 9999 (and the same values inside their arrays) no longer empties the whole response. Such values are written in the ISO 8601 expanded year form JavaScript's `Date` reads, for example `-000043-03-15T12:00:00Z` and `+010000-01-01T00:00:00Z`. Years 0 to 9999 keep their existing spelling.
- 17ae524: When the WebSocket transport cannot reach the upstream (the dial fails, or the upstream refuses TLS), it now sends `FATAL 08006 upstream unavailable` through the tunnel before closing, so a PostgreSQL client reports why the connection failed instead of "Connection terminated unexpectedly".
- 87068ff: The WebSocket transport's 10 second upstream dial budget now covers the SSLRequest exchange and the TLS handshake, not only the TCP connect. An upstream that accepted the connection and then never answered used to hold the tunnel open forever with the client waiting in `connect()`; it now fails with `FATAL 08006 upstream unavailable` like any other dial failure.

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
