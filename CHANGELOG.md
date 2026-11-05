# Changelog

## 0.9.0

### Minor Changes

- 7a103ea: `pgsconform` exits 1 only when cases failed and 2 on a usage or setup error (flags, corpus, fixtures, a server under test that does not answer), so CI can tell a failing server from a broken run. `-report -` writes the JSON report to stdout and moves the summary to stderr.
- 69ac0e2: The reference server (`cmd/pgserverless`) reads `PGSERVERLESS_LOG_FORMAT`: `text` (the default) keeps the `slog` text lines, and `json` writes one JSON object per line for log pipelines and container platforms. Any other value stops the server at start with a message naming the accepted values.

## 0.8.3

### Patch Changes

- a0290d8: The repository root has an `AGENTS.md` for coding agents: the build, test and conformance commands, the layout, and the rules a change keeps (SPEC.md is normative, a wire change lands with a spec changelog entry and a corpus case, the client and then Neon's proxy settle open behaviour). The README's Contributing section links it.

## 0.8.2

### Patch Changes

- b0a68b1: The module root has an `llms.txt` that links the README, the server's README, the spec files and the Go package reference, and the README and package documentation now link `spec/openapi.yaml`, which neither linked before.

## 0.8.1

### Patch Changes

- b9590c7: `pgsconform` now checks `row_count` strictly. `null` requires a JSON null, where it used to leave `rowCount` unchecked, and an integer requires that JSON number, where `0` used to pass a server that sent null, a string or no `rowCount` at all. Two level 1 cases pin SPEC 5.1's command tags beyond `SELECT n`: `core/insert-row-count-follows-the-oid` (an `INSERT 0 1` answers 1, not the OID) and `core/tag-without-a-count-has-null-row-count` (`CREATE TABLE` answers `command` `CREATE` and a null `rowCount`). `core/empty-query-is-an-empty-result` now checks its null `rowCount`. The reference implementation already passes.

## 0.8.0

### Minor Changes

- 9df54d2: `Upstream.MaxResponseBytes` lets an Admission override the `POST /sql` response cap for one request: zero keeps the handler's cap, a positive value replaces it and a negative value lifts it. A host can now cap every session and exempt the few whose own limits make the cap redundant.

## 0.7.7

### Patch Changes

- 5150e50: SPEC.md 0.7.0 requires a WebSocket upgrade to be accepted on any path, as Neon's proxy does, with `/v2` only the default; it used to name `GET /v2` alone, so a server built from it failed a client whose `wsProxy` points at `/v1`. The corpus gains `websocket/upgrade-on-any-path`. The reference implementation already behaved this way.

## 0.7.6

### Patch Changes

- 4099269: A `POST /sql` body carrying both a string `query` and `queries` now runs `query` as a single statement and ignores `queries` unread, as Neon's proxy reads it. It used to run the batch. SPEC.md 0.6.0 makes this the 4.3 rule, and the corpus checks it in `core/string-query-wins-over-queries`.

## 0.7.5

### Patch Changes

- 2d34324: A conformance corpus case may now carry `then`, a follow-up `POST /sql` that `pgsconform` sends once the case's own request passed, and the case passes only when both meet their expectations. The new level 3 case `batch/copy-in-a-batch-persists-nothing` uses it to check that an INSERT before a refused COPY in a batch does not persist, which SPEC section 7.2 requires and a single response cannot show. The reference implementation already passes it, since the refused COPY drops the connection and PostgreSQL rolls the batch back.

## 0.7.4

### Patch Changes

- 8baa4df: `pgsconform` now checks `error.message_contains` and `error.must_not_contain`, which the corpus schema allowed but the harness ignored. A case with `message_contains` used to pass for any error message, and one with `must_not_contain` passed a server that leaked the listed text, such as its connection string, in an error. The leak check reads the response body as sent and with its JSON strings decoded. The reference implementation already passes the corpus.

## 0.7.3

### Patch Changes

- 7e67e9f: SPEC.md 0.5.0 requires a statement that starts a COPY to or from the client to be answered 400 at once, and recommends SQLSTATE `0A000`, which Neon's proxy does not send. A corpus case may now carry `answered_within_ms`, which `pgsconform` fails when the server has not answered in time, and three new cases use it so a server that waits for its timeout or answers 200 fails. The reference implementation already behaves this way.

## 0.7.2

### Patch Changes

- deaa5a8: A `POST /sql` statement that starts a COPY to or from the client is now refused at once with 400 and SQLSTATE `0A000`, as Neon's proxy refuses it. `COPY ... FROM STDIN` used to hold its upstream connection until the 30 second query timeout, and `COPY ... TO STDOUT` answered 200 with its rows dropped. The upstream connection is closed, so PostgreSQL aborts the COPY and the rest of a batch's transaction.

## 0.7.1

### Patch Changes

- d3b9a2c: `pgsconform -single-database` reports the cases that need an unresolvable host as not applicable, in their own `not_applicable` count per level and list in the report, and a level with only those left over is still claimed. A server that serves one database resolves every Host to it, so it had no host to pass to `-unresolvable-host`, the case was skipped, and such a server could no longer claim level 4. The flag cannot be combined with `-unresolvable-host`, and a server run without it still has to refuse an unresolvable host.
- 0b3584b: SPEC.md 0.4.0 settles Appendix B items 1 and 5 and the binding of a JSON array to a non-array parameter, each as Neon's proxy behaves: an object-mode row keeps one member per column even when names repeat, a field's `format` is always `"text"`, and a JSON array binds as an array literal whatever the parameter's type. The reference implementation already behaved this way. The corpus gains five cases, and `pgsconform` checks a field's `format` when a case lists it.
- f8aa0d8: SPEC.md 0.4.1 says an array element's quoting escapes only `"` and `\`, so a control character is written as itself, and a new Appendix C records that Neon's proxy JSON-escapes string array elements instead, which turns a newline into the letter `n`. The corpus gains `params/control-character-in-text-array-element`. The reference implementation already behaved this way.

## 0.7.0

### Minor Changes

- 4994f3e: Native mode (no `Neon-Raw-Text-Output`) now returns `timestamp` and `timestamptz` as PostgreSQL prints them, which is what Neon's proxy returns: `"2024-01-01 10:00:00"` and `"2024-01-01 10:00:00+00"`, where it returned RFC 3339 (`"2024-01-01T10:00:00Z"`). Years outside 0 to 9999 follow PostgreSQL too (`"0044-03-15 12:00:00 BC"`, `"10000-01-01 00:00:00"`) instead of the ISO 8601 expanded form. Array elements and record fields are spelled the same way. A client that parses these columns with JavaScript's `Date` or an RFC 3339 parser needs to accept the space separated form. Raw text mode, which `@neondatabase/serverless` uses, is unchanged. SPEC.md 0.3.0 settles Appendix B item 2 this way, and the corpus (`version` 2) checks it.

## 0.6.3

### Patch Changes

- d188bb6: The reference server (`cmd/pgserverless`) with no `PGSERVERLESS_AUTH_TOKEN` now refuses, with 403 before the upgrade, a WebSocket upgrade whose `Origin` is neither its own nor listed in `PGSERVERLESS_CORS_ORIGINS`, and on a loopback listener refuses any request whose `Host` is not `localhost`, a `.localhost` name or a loopback address. Browsers apply no CORS to WebSockets, so any web page could open a tunnel to a local server, and a DNS rebinding name made a page same-origin so CORS never applied to `POST /sql` either. A browser page that uses `Pool` against a tokenless server now needs its origin in `PGSERVERLESS_CORS_ORIGINS`; clients outside a browser send no `Origin` and are unaffected.

## 0.6.2

### Patch Changes

- 709660c: The response cap set by `WithMaxResponseBytes` or `WithHTTPMaxResponseBytes` now charges each row its DataRow framing (7 bytes, plus 4 per column) on top of its values. It counted values only, so a result of NULLs or of rows with no columns cost nothing and could grow without bound under any cap. A capped response now reaches 507 a little sooner than before.
- e8930a7: The conformance corpus checks every type the SPEC section 6 rules name, not one or two per rule: `float4` infinity and `float4[]`, infinite `timestamp` and the `date[]` and `timestamp[]` forms, `time`, `interval` and the array forms of each 6.4 type (including `box[]`, whose array delimiter is a semicolon), `inet[]` and `cidr[]`, and the numeric, date and timestamp ranges with their multiranges. The library's output does not change.

## 0.6.1

### Patch Changes

- c51adcd: The conformance corpus checks that a WebSocket upgrade to an unresolvable host, and one that admission refuses, get an HTTP error with a `message` and no 101 (SPEC section 9). `pgsconform` arranges them with the new `-unresolvable-host` and `-refusing-server` flags and reports them skipped without. Before, level 4 had no case for either, so a server that upgraded first and refused afterwards, which the client reports as a network fault, still passed.
- a4f0358: The conformance corpus checks that a NUL byte in a text parameter is refused with 400 and a SQLSTATE, in native and raw text mode: `22021` when PostgreSQL binds the parameter, as Neon answers, or `08P01` when a server sends it in a simple query. The driver's conformance suite accepts either and fails a server whose error carries no code there. A corpus error expectation can now list accepted codes in `codes`, which `pgsconform` checks.
- b3973e1: SPEC.md 0.2.0 requires an anonymous `record` in native mode to be emitted as PostgreSQL's record text (`"(1,2)"`) and a `record[]` as an array of those strings, and the conformance corpus checks both. The reference implementation already did this, so its output does not change.

## 0.6.0

### Minor Changes

- 9fa766b: The reference server (`cmd/pgserverless`) ships a Dockerfile, a compose example that queries it through both transports, and a README of its settings. It reads `PGSERVERLESS_LOG_LEVEL` (`debug`, `info`, `warn` or `error`; anything else stops it at startup) and accepts `PGSERVERLESS_ALLOW_UNAUTHENTICATED=true` as another spelling of `PGSERVERLESS_ALLOW_REMOTE=true`.

## 0.5.0

### Minor Changes

- c2efa25: The reference server (`cmd/pgserverless`) reads `PGSERVERLESS_AUTH_TOKEN`. When set, `POST /sql` and `GET /v2` answer 401 unless the request carries `Authorization: Bearer <token>`, compared in constant time, and the server may listen beyond loopback without `PGSERVERLESS_ALLOW_REMOTE=true`. The CORS preflight and `GET /health` need no token. Before, the command had no authentication of its own, so a listener reachable by other machines served anyone who knew a database credential.
- b04aafe: Add `Upstream.RootCAs`, which checks the database certificate against the given roots instead of the system's on both transports. The reference server (`cmd/pgserverless`) fills it from the file `DATABASE_URL`'s `sslrootcert` names, as libpq does, and refuses to start if that file cannot be read or holds no PEM certificate. Before, the reference server checked the certificate against the system roots only, so a database with a private CA certificate failed every request even with `sslrootcert` pointing at that CA.
- 46d55a3: New `TLSPrefer` upstream mode: both transports negotiate TLS and, when the upstream refuses it or the handshake fails, dial again in the clear, as libpq's `sslmode=prefer` does. The reference server (`cmd/pgserverless`) now uses it for a `DATABASE_URL` with `sslmode=prefer` or `sslmode=allow`, which it used to read as `require`, so every request against a database without TLS failed even though the URL allowed a plaintext connection. A `DATABASE_URL` with no `sslmode` still requires TLS.
- f186edb: Add `Upstream.VerifyCAOnly`, which checks that one of `Upstream.RootCAs` issued the database certificate but not that it names the host, on both transports, as libpq's `sslmode=verify-ca` does. Without `RootCAs` the name is still checked, since any publicly issued certificate would otherwise pass. The reference server (`cmd/pgserverless`) sets it for `DATABASE_URL`'s `sslmode=verify-ca`, `require`, `prefer` and `allow` with an `sslrootcert` file, as libpq does. Before, those modes also checked the host name, so a database whose certificate names another host (reached by IP address or through a tunnel) failed every request where psql connected. `verify-full`, no `sslmode`, `sslrootcert=system` and `verify-ca` without `sslrootcert` (where libpq fails without `~/.postgresql/root.crt`) still check the name.

### Patch Changes

- 040025f: A `POST /sql` batch item with no `query`, or a null one, is now refused with 400 `query is required in queries[N]`, as a single request without one is and as Neon refuses the body. It used to decode as an empty query, so the item got the empty result, the rest of the batch ran and committed, and the response was 200. An item whose `query` is `""` still gets the empty result.
- eaf54ff: `POST /sql` now waits at most 30 seconds for the request body once its headers have arrived, and answers a body that misses it with 400 `invalid request body: the body did not arrive in time`. A client that sent its headers and trickled the body used to hold the handler and its connection forever on `NewHTTPHandler`, the echo `Handler` and `cmd/pgserverless`. The deadline is cleared once the body is read, so a query that runs longer is not affected, and WebSocket upgrades are untouched.
- 880956a: A `POST /sql` body with anything but whitespace after its JSON value is now refused with 400 `invalid request body`, as Neon refuses it. The handler used to run the first value and ignore the rest, so a body holding two requests answered 200 for the first while the second never ran.
- e623106: The conformance corpus now checks a successful batch. `pgsconform` compares an `expect.results` array against the response's `results`, one entry per statement in the order sent, and eleven new level 3 cases pin result order, a statement seeing a table an earlier statement created, each isolation level, read-only and deferrable as PostgreSQL reports them, `1` and `True` accepted as true, an unknown isolation level refused with 400, and a failing statement answered with its own SQLSTATE. The two earlier batch cases checked only refusals.
- 1a991d5: The conformance corpus checks that a server declines an SSLRequest, and a GSSENCRequest followed by an SSLRequest, with the single byte `N` over the WebSocket transport (SPEC section 9). `pgsconform` used to stop at the upgrade, and the schema's `ssl_request_declined` expectation was accepted but never checked, so a server that answered `S` or declined only the first request still passed level 4.
- 49a024b: The reference server (`cmd/pgserverless`) now checks the database's certificate as libpq does. With `sslmode=require`, `prefer` or `allow` in `DATABASE_URL` it encrypts without checking the certificate, unless `sslrootcert` is set; `verify-ca`, `verify-full` and a URL with no `sslmode` still check it. Before, every mode checked it, so `sslmode=require` against a database with a self-signed or private CA certificate failed where psql connects, and under `sslmode=prefer` the failed check fell back to an unencrypted connection.

## 0.4.0

### Minor Changes

- 5b0967b: Error statuses now follow section 7.2 of the protocol spec, which is the mapping Neon's proxy uses. The driver reads `code` and the other error fields only from a 400, so an error answered with any other status reached the caller as `Server error (HTTP status N)` with no SQLSTATE.

  - `PgErrorResponse` and `HandlePgError` now give the statement mapping only: every PostgreSQL error is a 400. Class `28` was 401, classes `53` and `57` were 503, and class `08` other than `08P01` was 504. `POST /sql` already answered a statement's PostgreSQL error this way since 0.3.0; callers of these two functions now get the same answer.
  - Failures that are not PostgreSQL errors, on `POST /sql` and through `PgErrorResponse`: a refused, reset or unresolvable upstream connection while running goes from 504 `upstream connection failed` to 400 `upstream connection lost`, a timeout goes from 504 `upstream connection timed out` to 400 with code `08P01`, and `password authentication failed` goes from 401 to 400. 507 for an oversized response and 500 for anything else are unchanged.
  - New `DialErrorResponse` and `HandleDialError` give the connect mapping, and `POST /sql` now uses it for its upstream dial. An unknown database (`3D000`) or failed authentication (class `28`) is a 400, where class `28` was 401. Any other PostgreSQL error on connect is a 500, where it took its class's status: 503 for classes `53` and `57` (`53300` too many connections among them), 504 for class `08`, and 400 for the rest, such as `42501` when the role may not connect to the database. An unreachable upstream goes from 504 to 500, and a connect timeout from 504 to 400 with `08P01`. A caller that wrote connect failures through `HandlePgError` should switch to `HandleDialError`, or an upstream it cannot reach is answered 400 rather than 500.
  - The protocol is now published with the module. `spec/SPEC.md` is the normative text at spec version 0.1.0, versioned separately in `spec/CHANGELOG.md`, with `spec/openapi.yaml` and `spec/schema.json`. `spec/cases/v1.json` is the language-neutral conformance corpus, 104 cases including four new `errors/*-is-400` cases, and `spec/CONFORMANCE.md` describes its format and levels. `cmd/pgsconform` runs the corpus against any server over HTTP.

### Patch Changes

- d5bd64c: A raw-text batch with no `Neon-Batch-*` header now runs inside `BEGIN` and `COMMIT`, as Neon's does, so `LOCK TABLE`, `SAVEPOINT` and `DECLARE CURSOR` work in `sql.transaction([...])`. It used to run as an implicit-transaction pipeline, which PostgreSQL refuses those statements in with `25P01`. The governed simple-protocol path is unchanged.

## 0.3.2

### Patch Changes

- 1da3024: `POST /sql` with an empty `query` now answers 200 with `fields` and `rows` empty, `command` `""` and `rowCount` null, as Neon renders PostgreSQL's EmptyQueryResponse. It answered 400 `query is required`, so `sql.query("")` threw where it returns no rows on Neon. A batch item with no statement gets the same result in its own position, including in a simple-protocol batch, which used to refuse it with `empty query in batch`. Parameters bound to an empty query are refused with `08P01`, as PostgreSQL refuses them. A missing or null `query` is still 400.

## 0.3.1

### Patch Changes

- c9e482c: The reference server (`cmd/pgserverless`) now honors `sslmode=disable` in `DATABASE_URL` when the password holds a `%` that starts no escape (`50%off`). `ParseConnectionString` accepts such a password since it reads userinfo like Neon, but the sslmode lookup still went through `net/url`, which refuses it, so the server required TLS and every request against a database without TLS failed.

## 0.3.0

### Minor Changes

- 3a7804a: An object-mode row's keys now come back in column order, as Neon writes them, instead of sorted by name. `select 2 as b, 1 as a` returned `{"a":1,"b":2}` and now returns `{"b":2,"a":1}`; a repeated column name is written twice, as on Neon. An object-mode row in `QueryResult.Rows` is an `ObjectRow` rather than a `map[string]any`.

### Patch Changes

- 2f656fc: `POST /sql` now opens its upstream session with `client_encoding=UTF8`. Without it PostgreSQL used the role's or database's encoding, so against a LATIN1 database every non-ASCII character in a result came back as U+FFFD and a non-ASCII parameter was stored garbled. A UTF8 database behaves as before.
- 5c54033: `ParseConnectionString` reads the user, password and database the way Neon's proxy and libpq do. A password holding a `%` that starts no escape (`50%off`), or a character such as `^`, `{` or a non-ASCII letter left unencoded, used to fail the whole request with 400 `invalid connection string`, and that error quoted the password back. An escaped NUL (`%00`) is now refused, as libpq refuses it.
- e3f8ca3: The `POST /sql` upstream dial now ignores the server process's own libpq environment. It built its configuration with `pgx.ParseConfig("")`, so `PGHOST` and `PGPORT` left fallback hosts that were tried when the real upstream failed, running the request on a different server, and `PGTZ`, `PGOPTIONS` and `PGAPPNAME` were sent as startup parameters on every connection.
- 897a06d: A result field's `columnID` is now signed, as PostgreSQL sends it and Neon reports it. A system column such as `ctid` used to arrive as `65535` instead of `-1`, because the attribute number was read as an unsigned 16 bit integer. `FieldDefinition.ColumnID` is an `int16` rather than a `uint16`.
- 4fd36b7: A single `POST /sql` statement whose result passes the response cap now answers 507 at once, as Neon does. It read the rest of the result before answering, so the database kept sending rows nobody would see, and a result still streaming when the request ended answered 400 `Query cancelled, connection was terminated` instead of 507.
- fec4a8e: A `POST /sql` statement that PostgreSQL fails now answers 400 with its SQLSTATE whatever the class, as Neon does. A `57014` statement timeout, an out of memory `53200` or a `57P01` shutdown came back as 503 or 504, and the driver reads the error body only from a 400, so it threw `Server error (HTTP status 503)` with no `code`. A failed connect keeps its status (401 for rejected credentials).
- 8bba717: The WebSocket transport now upgrades on any path, as Neon's proxy does, not only on `/v2`. The client's `wsProxy` setting chooses the path, and its own documentation configures `/v1` (or a string that gets `?address=host:port` appended) for a self-hosted proxy, so those clients got a 404 instead of a tunnel. A request that is not a WebSocket upgrade still gets a 404 on a path nothing serves, and `RegisterWebSocket` only takes paths the Echo instance has no route for.
- 26247d4: The WebSocket transport now forwards a `CancelRequest` to the upstream, as Neon does, so a client cancelling a statement over the tunnel stops it on the database. It used to be logged and dropped, and the statement ran to completion. Nothing is sent back to the cancelling connection, as with PostgreSQL itself.
- d052a58: The WebSocket transport now closes a tunnel whose client does not send its StartupMessage within 30 seconds of the upgrade. A client that upgraded and stayed silent used to hold the connection and its goroutine until it hung up. The deadline ends with the startup, so an open session still has no time limit.

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
