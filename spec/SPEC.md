# The Postgres serverless HTTP and WebSocket protocol

Version 0.1.0, draft. Changes are listed in [CHANGELOG.md](CHANGELOG.md).

This document specifies how a server exposes a PostgreSQL database over HTTP and over WebSocket to a serverless client. It is written so that a second implementation, in any language, can be built from this document alone and checked against the conformance corpus described in [CONFORMANCE.md](CONFORMANCE.md).

## Status of this document

Before this document the protocol had no published specification. It was defined by one client implementation, `@neondatabase/serverless`, and by whatever a server happened to do. This document is derived from two sources, in descending order of authority:

1. The client, read from the published npm bundle at version 1.1.0. Where this document states what a client sends or does, that is the source.
2. The reference implementation, the Go package in this repository, and its test suite.

Where the reference implementation disagrees with the client, the client wins and the disagreement is recorded in Appendix A.

The protocol is implemented by more than one vendor, which is what makes it worth specifying rather than cloning. Any statement here about a specific vendor's deployment is out of scope; this document describes the wire contract only. Where it cites the reference implementation's choices (a batch limit, a status mapping), it does so as an example of a permitted choice, not as a requirement.

### Versioning

This document is versioned on its own, as MAJOR.MINOR.PATCH, independently of any implementation. The reference implementation's release numbers say nothing about which version of this document it implements; its conformance report does.

- **PATCH** clarifies wording without changing what a conforming server must do.
- **MINOR** adds something a server may implement without breaking a conforming one: an optional feature, a new level, or a SHOULD.
- **MAJOR** changes or removes a requirement, so that a server conforming to the previous version may no longer conform.

While the major version is 0 the document is a draft, and a MINOR change may also change a requirement. The conformance corpus names the version it checks in its `spec_version` field, and a report records it (CONFORMANCE.md, section 5).

## 1. Terminology

**MUST**, **MUST NOT**, **SHOULD** and **MAY** are used in the RFC 2119 sense.

**Client** is the party issuing SQL. **Server** is the party exposing the database. **Upstream** is the PostgreSQL instance the server executes against.

A **conforming server** satisfies at least conformance level 1 (section 10).

## 2. Transports

Two transports carry the same intent and share almost nothing else.

| Transport | Path | Carries | Used by |
| --- | --- | --- | --- |
| HTTP | `POST /sql` | One request, one or more statements, JSON in and out | one-shot queries and non-interactive transactions |
| WebSocket | `GET /v2` | The PostgreSQL frontend/backend protocol, tunnelled | session-oriented clients, interactive transactions, `LISTEN`, cursors |

The two are independent. A server MAY implement HTTP only. A client that needs sessions MUST use WebSocket, because the HTTP transport has no session: each request is executed and the connection is released.

The default paths are what clients derive when not configured otherwise: `https://<host>/sql` for HTTP, and `<host>/v2` for WebSocket.

## 3. Admission

Before any generic handling, a conforming server MUST decide two things:

1. **Whether this request may run at all.**
2. **Which upstream it runs against, and as whom.**

Everything about that decision is deployment policy and is out of scope for this document. A single-tenant server resolves a fixed upstream and admits every request. A multi-tenant server may parse the connection string, resolve a tenant, check that the tenant is not suspended, enforce an IP allowlist, and apply a rate limit, in whatever order it likes.

What the protocol requires is only that admission is a **distinct step that runs first**, and that its refusals use the error shape in section 7. This is the one part of a server that is never portable, so naming it keeps the rest portable. The reference implementation models it as one function returning an upstream or an error, called `Admission`; the name is a suggestion, not a requirement.

An admission refusal SHOULD use the status that describes the reason (401 for authentication, 403 for a suspended or disallowed principal, 404 for an unresolvable target, 429 for a rate limit). Note the client consequence in section 7: only 400 is parsed structurally, so an admission refusal's message text is what the user sees.

## 4. The HTTP request

### 4.1 Headers

A conforming server MUST accept both the vendor-prefixed spelling and the unprefixed spelling of every header below. Where both are present, the unprefixed spelling MUST take precedence.

| Header | Values | Sent by client 1.1.0 | Meaning |
| --- | --- | --- | --- |
| `Neon-Connection-String` / `Connection-String` | a `postgres://` or `postgresql://` URL | always | Identifies the upstream and the credentials. Consumed by admission. |
| `Neon-Raw-Text-Output` / `Raw-Text-Output` | `true` | **always, hardcoded** | Selects raw text response mode (section 5.3). |
| `Neon-Array-Mode` / `Array-Mode` | `true` | **always, hardcoded** | Selects positional rows (section 5.1). |
| `Neon-Batch-Isolation-Level` / `Batch-Isolation-Level` | `ReadUncommitted`, `ReadCommitted`, `RepeatableRead`, `Serializable` | batch only, when set | Section 8. |
| `Neon-Batch-Read-Only` / `Batch-Read-Only` | `true`, `false` | batch only, when set | Section 8. |
| `Neon-Batch-Deferrable` / `Batch-Deferrable` | `true`, `false` | batch only, when set | Section 8. |
| `Authorization` | `Bearer <jwt>` | when the client is configured with an auth token | Consumed by admission. |

Two consequences of the "always, hardcoded" column are easy to miss and both matter:

- **The client never asks for object-keyed rows or for decoded values.** It always requests array mode and raw text, then reshapes and parses on its own side. `arrayMode` and `fullResults` are client-side options that never reach the server.
- Therefore **a server that only implements raw text mode is usable by the client**. Native mode (section 5.3) exists for every other kind of consumer.

A server MAY accept additional request-level options. The reference implementation accepts an `arrayMode` field in the request body, which the client does not send. Servers SHOULD NOT rely on such extensions being present.

CORS, where relevant, MUST allow the vendor-prefixed headers, or a browser client's preflight fails.

### 4.2 Single statement body

```json
{
  "query": "SELECT id, email FROM users WHERE id = $1",
  "params": [42]
}
```

`query` is required and MUST be a string. `params` is optional and positional, binding `$1`, `$2` and so on.

`query` MAY be empty, or hold only whitespace and comments. That is a query with no statement, which PostgreSQL answers with EmptyQueryResponse, and the server MUST answer it 200 with the result object of 5.1 carrying `fields` and `rows` as `[]`, `command` as `""` and `rowCount` as null. Parameters bound to such a query are an error, SQLSTATE `08P01` (`bind message supplies N parameters, but prepared statement "" requires 0`). A missing or null `query` is still a 400.

### 4.3 Batch body

```json
{
  "queries": [
    { "query": "INSERT INTO t (a) VALUES ($1)", "params": [1] },
    { "query": "SELECT count(*) FROM t" }
  ]
}
```

`queries` MUST be a non-empty array. A server MAY impose a maximum batch size and MUST answer 400 when it is exceeded. The reference implementation's limit is 100.

An item MAY have an empty `query`, under the same rule as 4.2: it gets the empty result in its own position, and the items after it keep theirs. A server that joins the items into one simple-protocol query MUST answer such an item itself, because PostgreSQL skips an empty statement inside a joined query and every later result would land on the wrong item.

A body carrying both `query` and `queries` is not produced by any client. A server SHOULD treat the presence of `queries` as selecting batch mode.

### 4.4 Parameter encoding

Parameters arrive as JSON, which cannot express every PostgreSQL type, so a server MUST convert before binding. Two rules are load bearing:

**A JSON array parameter MUST be rendered as a PostgreSQL array literal**, and each element MUST be rendered so PostgreSQL reads back the value the client sent. The failure mode is silent: a JSON number is a double, and rendering `float64(1000000)` with a general-purpose formatter yields `1e+06`, which `int[]` and `int8[]` reject with SQLSTATE 22P02. Use a non-exponential decimal format. A nested JSON array MUST render as a nested array literal (`{{1,2},{3,4}}`).

**An element MUST be quoted whenever the bare text would read back as a different value**: when it is empty, when it case-insensitively equals `NULL`, or when it contains any of `{`, `}`, `,`, `"`, `\` or whitespace. Quoting escapes `"` and `\` with a backslash.

## 5. The HTTP response

### 5.1 Result object

A successful single-statement request returns HTTP 200 and one result object. A batch wraps one of these per statement (section 8).

```json
{
  "fields": [ ... ],
  "command": "SELECT",
  "rowCount": 1,
  "rows": [ ... ],
  "rowAsArray": true
}
```

| Field | Type | Rule |
| --- | --- | --- |
| `fields` | array of field descriptors (5.2) | One per result column, in order. |
| `command` | string | The command tag's first word, for example `SELECT`, `INSERT`, `UPDATE`. `""` for a query with no statement (4.2). |
| `rowCount` | integer or null | The count from the command tag. `INSERT 0 5` yields 5. Null when the tag carries no count. |
| `rows` | array | Never null. An empty result set MUST be `[]`. |
| `rowAsArray` | boolean | Echoes whether rows are positional. |

When array mode is on, each row is an array of values in column order. When it is off, each row is an object keyed by column name. A duplicate column name in object mode is unspecified; clients requesting object mode from a query with duplicate names get an implementation-defined result, which is one reason the client always uses array mode.

### 5.2 Field descriptor

```json
{
  "name": "id",
  "tableID": 16385,
  "columnID": 1,
  "dataTypeID": 23,
  "dataTypeSize": 4,
  "dataTypeModifier": -1,
  "format": "text"
}
```

`dataTypeID` is the PostgreSQL type OID and is **required**: in raw text mode it is the only thing telling the client how to parse the value. `tableID` and `columnID` are 0 for a computed column.

### 5.3 Two response modes

This is the part most likely to be implemented once and assumed universal. There are two modes, and they produce different JSON for the same row.

**Raw text mode** (`Raw-Text-Output: true`). Every value is the PostgreSQL text representation as a JSON string, or `null` for SQL NULL. No type-directed conversion happens on the server. The client parses each value using its own parser table keyed by `dataTypeID`. This is what the client always requests.

Implementation note: the simplest way to guarantee this is to execute in a mode that returns text-format results (PostgreSQL's simple query protocol), and emit the bytes verbatim.

**Native mode** (no raw text header). The server decodes values and emits JSON of the natural type: a JSON number for `int4`, a JSON object for `jsonb`, a JSON array for an array column. This is the mode for consumers that are not the client, and it is where section 6 applies in full.

A conforming server MUST implement raw text mode. Native mode is level 2.

## 6. Value encoding rules for native mode (normative)

Every rule here exists because the natural encoding of a decoded PostgreSQL value is wrong, either against the client's contract or against the data itself. They are stated as: what the column holds, what a naive implementation emits, and what a conforming server MUST emit.

An implementation that skips these will pass a smoke test and corrupt data in production. Four of them are silent value corruption rather than an ugly shape.

Every rule below is checked over the wire by the corpus cases whose `spec` field cites its section. A rule with no case is a gap in the corpus, not a rule a server may skip.

### 6.1 Numbers that do not fit a JSON number

| Type | Naive output | Required output | Why |
| --- | --- | --- | --- |
| `int8` | `9223372036854775807` (JSON number) | `"9223372036854775807"` (string) | A JSON number is a double in JavaScript. Any absolute value above 2^53 silently changes. |
| `numeric` | JSON number | decimal string | Arbitrary precision cannot survive a double. |
| `money` | JSON number or a struct | PostgreSQL's own money text | Same, plus locale formatting. |
| `int8[]`, `numeric[]` | array of numbers | array of strings | Element-wise, same reason. |
| `money[]` | the array literal as one string | array of strings, nested per dimension | Element-wise. An element with group separators is quoted in the literal, so splitting on commas is wrong. |

### 6.2 Values JSON cannot represent at all

| Type                           | Naive output          | Required output |
| ------------------------------ | --------------------- | --------------- |
| `float4`, `float8` holding NaN | serialization failure | `"NaN"`         |
| holding positive infinity      | serialization failure | `"Infinity"`    |
| holding negative infinity      | serialization failure | `"-Infinity"`   |

JSON has no encoding for these, and they are ordinary values of both types. A server that does not handle them does not return a wrong value, it **fails the entire response with a 500 and takes every other row with it**. Finite floats MUST stay JSON numbers. Applies element-wise inside `float4[]` and `float8[]`.

### 6.3 Infinite dates and timestamps

`date`, `timestamp` and `timestamptz` can hold `infinity` and `-infinity`. These MUST be emitted as the words `"infinity"` and `"-infinity"`.

The naive failure here is the worst in this document. A decoder that represents infinity as an enum will emit `1` or `-1`, **a number indistinguishable from a value the column could legitimately hold**. A leaked struct at least looks foreign; this does not.

Applies element-wise inside the array forms.

### 6.4 Values whose decoded form is unreadable

Each of these MUST be emitted as the PostgreSQL text representation.

| Type | Naive output | Required output |
| --- | --- | --- |
| `uuid` | `[160,238,...]`, 16 numbers | `"a0ee...-...."`, canonical dashed |
| `bytea` | `"AQI="`, base64, unmarked | `"\\x0102"` |
| `xml` | base64 | the document text |
| `date` | `"2024-01-01T00:00:00Z"` | `"2024-01-01"` |
| `time`, `interval` | an object of decoder field names | the text form |
| `macaddr`, `macaddr8` | `"CAArAQID"`, base64 | `"08:00:2b:01:02:03"` |
| `bit`, `varbit` | `{"Bytes":"oA==","Len":3}` | `"101"` |
| `lseg`, `path`, `box`, `polygon`, `line`, `circle` | an object of decoder field names | `"[(1,2),(3,4)]"` and so on |
| `tsvector` | nested objects with weights as ASCII codes | `"'a':1"` |

`point` is worth calling out as the exception that needs nothing in at least one decoder, so implementers should check rather than assume.

Applies element-wise inside the array forms. A decoder with no array codec for one of them, as pgx has none for `macaddr8[]`, hands back the array literal as one string; it MUST still be emitted as an array of strings.

### 6.5 inet, and only when it addresses a single host

`inet` holding `192.168.1.1` MUST be emitted as `"192.168.1.1"`, not `"192.168.1.1/32"`.

The netmask MUST be kept for every prefix that is not a single host, and `cidr` MUST always keep it, because PostgreSQL prints it in both those cases. The width is taken from the address, so an IPv6 host is `/128`.

This is not lossy corruption; both spellings parse to the same value. It matters because the value is a short printable identifier that gets logged, compared and joined against text columns, so the extra `/32` silently stops matching.

### 6.6 Ranges and multiranges

`int4range` and friends MUST be emitted as the PostgreSQL literal: `"[1,3)"`, and a multirange as `"{[1,3),[5,7)}"` with the member literals unquoted inside the braces.

Three details a reimplementation gets wrong:

- An **empty** range is the word `"empty"` and carries no bounds. That is a different value from a range with two unbounded ends, `"(,)"`: the first contains nothing, the second contains everything.
- An **unbounded** end is written as nothing at all, not as an empty quoted string. `("",3)` is a range whose lower bound is the empty string.
- A bound MUST be quoted when the bare text would reparse differently, which is when it is empty or contains any of `[`, `]`, `(`, `)`, `,`, `"`, `\` or whitespace. Note this set differs from the array element set in 4.4.

The naive output is both unreadable and lossy: an `int8range` bound above 2^53 emitted as a JSON number is corrupted the same way 6.1 describes.

### 6.7 Multi-dimensional arrays keep their shape

`array[array[1,2],array[3,4]]` MUST be emitted as `[[1,2],[3,4]]`, not as `[1,2,3,4]`.

Many decoders flatten. Nothing in the flattened response says a dimension was lost, so a client reading `rows[0].a[1]` gets element 2 where the column holds `[3,4]`. Since the parameter path in 4.4 accepts `{{1,2},{3,4}}`, without this rule a nested array can be written and cannot be read back in the shape it was written.

Implementations reading dimensions from PostgreSQL's binary array header MUST confirm the value really is an array of that column's type before reshaping. A composite (`row(...)`) decodes to a list too, and its binary layout parses as a plausible array header, so a naive check re-nests records into nonsense. Confirm using the column's type, and check that the dimensions account for exactly the elements present.

### 6.8 Order of operations

The coercions in 6.1 through 6.6 operate on a flat element list. Reshaping (6.7) MUST happen **after** them. Reshaping first hides every element from the element-wise rules, so an `int8[][]` would gain its shape and lose its string contract.

## 7. Errors

### 7.1 Response shape

An error response is a JSON object whose only required member is `message`. When the failure is a PostgreSQL error, the server SHOULD include the fields below.

**The field names are the client's, not PostgreSQL's C struct names.** Client 1.1.0 copies exactly these keys onto the error it throws:

```
severity, code, detail, hint, position, internalPosition, internalQuery,
where, schema, table, column, dataType, constraint, file, line, routine
```

Note `schema`, `table`, `column`, `dataType` and `constraint`, **not** `schemaName`, `tableName`, `columnName`, `dataTypeName`, `constraintName`. A server using the longer spellings sends fields the client silently drops. See Appendix A.1.

`position`, `internalPosition` and `line` are decimal strings. A field PostgreSQL did not send MAY be omitted or sent as `null`: the client reads each key as `body[key] ?? undefined`, so the two are the same to it. Neon's proxy sends every key, `null` where absent; the reference implementation omits them.

### 7.2 Status codes, and the client's asymmetry

**The client parses the structured body only on HTTP 400.** Every other status is surfaced as a generic `Server error (HTTP status N)` carrying the response body as text, with no `code`, no `severity` and no other field populated. This holds in client 1.1.0 and 1.2.0.

So the status decides whether a user sees the SQLSTATE at all, and the rules below are drawn to keep it. They are the mapping Neon's proxy uses, which is what the client is built against.

**Errors raised while running the request's statements.** A PostgreSQL error raised by a statement MUST be answered with 400 and the full body of 7.1, whatever its SQLSTATE class. That includes class `28`, class `53`, class `57`, class `08` and `08P01`. A status that describes the class better, such as 503 for `53300`, costs the client the code.

**Errors opening the upstream connection.** An error the request caused SHOULD be 400: an unknown database (`3D000`) or failed authentication (class `28`). Any other refusal by the upstream, and an upstream that cannot be reached, SHOULD be 500, with the error shape.

**Failures that are not PostgreSQL errors** (7.3):

| Failure | Status |
| --- | --- |
| Malformed body, missing `query`, oversized batch, unusable connection string, a refused batch header (section 8) | 400 |
| The request was cancelled or timed out, while connecting or while running | 400, with `code` `08P01` |
| The connection to the upstream was lost while running | 400 |
| The request body exceeds the deployment's limit | 413 |
| The response exceeds the deployment's limit | 507 |
| Anything else | 500 |

Admission refusals (section 3) are deployment policy and keep the status that describes them.

### 7.3 Non-database failures

Failures that are not PostgreSQL errors (the upstream is unreachable, a timeout, a malformed request) MUST still return the error shape with a `message`.

A server MUST NOT leak credentials in an error message. A connection string in an error string is the common way this happens.

## 8. Batch transactions

A batch runs as a single transaction. When none of the three batch headers is present, a server MAY use an implicit transaction (a single pipeline).

When any of them is present, the server MUST open an explicit transaction with the requested characteristics, run the statements, and commit:

```
BEGIN [ISOLATION LEVEL <level>] [READ ONLY] [DEFERRABLE]
```

The characteristics MUST be applied by PostgreSQL rather than emulated, so that a read-only batch rejects a write with SQLSTATE 25006. Any statement failing aborts the transaction, so partial effects MUST NOT persist.

Isolation level values arrive in the client's camelCase spelling and map to the SQL spelling. A server MUST reject an unrecognized level with 400 rather than silently dropping it: the caller asked the server to enforce a guarantee.

The boolean headers MUST accept the spellings a standard boolean parser accepts (`true`, `True`, `1`), and MUST reject anything else with 400 for the same reason. Defaulting an unparseable value to false is the one wrong answer available, because false means read-write.

`DEFERRABLE` only has an effect for a `SERIALIZABLE READ ONLY` transaction; PostgreSQL ignores it otherwise.

A successful batch returns HTTP 200 and one object, `{"results": [...]}`, holding one result object (5.1) per statement, in the order the statements were sent. A failed batch returns one error response (section 7) for the statement that failed, and no results.

## 9. The WebSocket transport

`GET /v2` upgrades to a WebSocket carrying the PostgreSQL frontend/backend protocol in **binary frames**. The server SHOULD negotiate the `pg` subprotocol.

There is no TLS SNI on this path, so a multi-tenant server resolves the target from the HTTP `Host` header of the upgrade request. A target that cannot be resolved MUST be refused **before** the upgrade, with an ordinary HTTP error response.

Once upgraded, the frames are the PostgreSQL protocol and this document adds almost nothing to it. Three points that are not obvious:

**SSLRequest and GSSENCRequest MUST be declined** with a single `N` byte. The WebSocket already carries the transport encryption, and the client defaults to disabling PostgreSQL-level TLS.

**Startup pipelining.** The client defaults to pipelining its first three messages, assuming the server will ask for a cleartext password. A server that authenticates with SCRAM will not, and a client using the default configuration hangs. A conforming server MUST tolerate a pipelined startup by treating the connection as a continuous stream rather than expecting a message boundary per read. It cannot make a SCRAM handshake work against a client that has already committed to the password flow, so a server using SCRAM MUST document that clients need `pipelineConnect` disabled.

**CancelRequest** MAY be unsupported. Cancellation arrives on a second connection, which an anycast or scale-to-zero deployment may route elsewhere. A server that does not support it SHOULD ignore it rather than erroring.

Authentication MUST NOT be driven before admission. Challenging first takes a secret from a client the server was going to refuse anyway, and can downgrade a credential to a weaker mechanism than the one it is registered with.

## 10. Conformance levels

An implementation claims a level. Each level includes the ones below it.

| Level | Name | Requires |
| --- | --- | --- |
| 1 | **Core HTTP** | `POST /sql`, both header spellings with unprefixed precedence, single statement with parameters, the result object of 5.1 and 5.2, raw text mode, the error shape of 7.1 and the statement error status of 7.2, parameter encoding of 4.4 |
| 2 | **Native types** | Everything in section 6 |
| 3 | **Batch** | Batch bodies, and the transaction characteristics of section 8 |
| 4 | **WebSocket** | Section 9 |

Level 1 is what the client needs. Level 2 is what every other consumer needs. Levels are independent above 1: an implementation may claim 1 and 4 without 2.

An implementation MUST NOT claim a level it does not pass in full, and SHOULD publish its conformance report (CONFORMANCE.md, section 5).

## 11. Security considerations

On the HTTP transport, credentials arrive in headers: the password inside `Connection-String`, and the bearer token in `Authorization`. On the WebSocket transport they travel inside the tunnelled PostgreSQL startup, end to end between client and upstream.

- A server MUST NOT log a credential, in full or in part, and MUST NOT forward a bearer token to anyone but the party that validates it.
- Across any network a client does not control, the transport SHOULD be TLS (`https://` and `wss://`). A server MAY leave TLS to a proxy in front of it, since what matters is that the headers never cross an untrusted network in clear.
- Section 7.3 forbids credentials in error messages, which is the other common way they leak.
- Rate limits and request or response size limits are admission and deployment policy (section 3). A server SHOULD apply them, and SHOULD answer with the error shape of section 7 when it does.

## Appendix A: disagreements found while writing this

Found by reading client 1.1.0 against the reference implementation. A.1 was two defects in the server, both fixed. A.2 to A.4 were errors in that implementation's own compatibility notes, all corrected.

### A.1 The server sent five error fields under names the client ignores (fixed)

The server emitted `schemaName`, `tableName`, `columnName`, `dataTypeName` and `constraintName`. The client reads `schema`, `table`, `column`, `dataType` and `constraint`, so all five arrived as `undefined` on the thrown error. It also declared `position`, `internalPosition`, `internalQuery`, `file`, `line` and `routine` and never populated any of them, although the underlying driver carries them.

Both halves are fixed in the reference implementation: the five now carry the client's spellings, and the six are populated, with the numeric ones rendered as decimal strings and omitted when zero. The remaining fields (`severity`, `code`, `detail`, `hint`, `where`) always matched.

Section 7.1 is the normative statement and `cases/v1.json` checks it over the wire. An implementation's own tests should check the key names on the raw JSON body, because a test that unmarshals the body back into the server's own struct round-trips under either spelling.

### A.2 The notes said the client sends `arrayMode` in the request body (fixed)

It does not, in 1.1.0. `prepareQuery` produces `{query, params}` only, and `arrayMode` is a client-side option applied after the response arrives. The server's support for a body-level `arrayMode` is a valid extension, but the note that "the driver sends both header and body" was wrong.

### A.3 `Neon-Pool-Opt-In` does not appear in client 1.1.0 (fixed)

The server normalizes it. Harmless, but it is not part of the current contract and should not be listed as a supported feature without a version qualifier.

### A.4 The coercion rules are not exercised on the real client path (fixed)

The client always sends `Neon-Raw-Text-Output: true`, which selects raw text mode, where none of section 6 runs. Every rule in section 6 is therefore about consumers that are not the client. That does not make them less important, since they are exactly what a REST endpoint or a hand-written client hits, but the notes read as though they were on the client's path and they are not.

## Appendix B: open questions

These could not be settled from either source and MUST NOT be guessed by an implementer.

1. **Duplicate column names in object mode.** Unspecified. The client never asks for object mode, so no behaviour is pinned.
2. **`timestamp` and `timestamptz` in native mode.** The reference implementation deliberately emits RFC 3339 (`"2024-01-01T10:00:00Z"`) where PostgreSQL prints `2024-01-01 10:00:00`. Which is correct for this protocol is undecided, and the corpus carries the case as a tolerant comparison rather than asserting either.
3. **Whether an implementation may omit raw text mode.** This document requires it at level 1 because the client requires it. If a server exists whose only consumers are native-mode, the level structure may be wrong.
4. **Maximum request and response sizes.** The reference implementation caps a batch at 100 statements and sets no size limit of its own; a deployment in front of it may set either. Neither is specified. A client hitting a smaller server limit gets an error it cannot distinguish from a server fault.
5. **`format` in the field descriptor.** The reference implementation always emits `"text"`. Whether a server may emit `"binary"`, and what a client does with it, is unknown.
6. **Text spellings that differ by decoder.** The reference implementation's `time` carries six fractional digits where PostgreSQL prints none, its `interval` always carries a time part, and a `tsvector` lexeme containing a single quote is backslash escaped where PostgreSQL doubles it. All parse back to the same value. Whether conformance should require PostgreSQL's exact spelling is undecided, and the corpus marks these as tolerant comparisons.
