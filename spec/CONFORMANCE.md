# Conformance suite design

How to check any implementation of [SPEC.md](SPEC.md), in any language, against the same corpus.

The schema is in [schema.json](schema.json) and the corpus so far is in [cases/v1.json](cases/v1.json). The harness is [`cmd/pgsconform`](../cmd/pgsconform), and it has been run against the reference implementation in this repository.

## 1. Why an implementation's own tests cannot be reused

The reference implementation's own suite is Go test files driving the handler in-process against an in-process fake PostgreSQL (`pgfake`). It is good at what it does, which is pinning the value layer's behaviour one rule at a time.

It cannot test a Rust or a TypeScript server, for three reasons that are all structural rather than incidental:

1. The cases are Go function bodies, so they are unreadable to any other implementation.
2. It calls the handler directly, so there is no network boundary to point at another process.
3. The fake PostgreSQL returns canned column values by OID. That is exactly right for testing coercion in isolation and useless as a definition of correct behaviour, because the canned values are the test author's belief about what PostgreSQL sends.

Point 3 is the one that matters most. A corpus that is going to be the definition of the protocol has to be checkable against a real database, or it encodes one implementation's assumptions and calls them a standard.

## 2. Shape of the replacement

Three parts.

**A corpus.** JSON, versioned, language-neutral. Each case is one request against a database in a known state, plus the response required. The cases were derived from an implementation and then hold that implementation to them, and every other implementation to the same.

**A fixture.** SQL that puts a real PostgreSQL into the state the cases assume. Cases reference it by name; they never create their own tables inline, because then a failing case cannot be rerun in isolation.

**A harness.** Any program that reads the corpus, issues each request over the network to a server under test, compares, and emits a report. One harness in one language is enough, since it talks to servers over HTTP rather than linking them. Writing a second harness is a way to check the corpus, not a requirement.

## 3. What a case says

A case is deliberately not "call this function and expect this value". It is a complete HTTP exchange, because that is the only thing every implementation shares.

```json
{
  "id": "native/int8-as-string",
  "level": 2,
  "spec": "6.1",
  "fixture": "types",
  "request": {
    "path": "/sql",
    "headers": { "array_mode": false, "raw_text": false },
    "body": { "query": "SELECT big FROM t_int8" }
  },
  "expect": {
    "status": 200,
    "rows": [{ "big": { "json": "\"9223372036854775807\"" } }]
  }
}
```

Four things about that shape are deliberate.

**`headers` is symbolic, not literal.** A case says `raw_text: false`, and the harness decides whether to send `Neon-Raw-Text-Output` or `Raw-Text-Output` or neither. That lets the same corpus test both spellings and the precedence rule without duplicating every case, and it keeps vendor prefixes out of the corpus.

**Expected values are compared as parsed JSON with the encoding pinned.** The `{"json": "\"9223372036854775807\""}` wrapper exists because the single most important rule in the spec is that a value is the _string_ `"9223372036854775807"` and not the _number_ `9223372036854775807`. A comparison that parses both sides into a language's native types loses exactly that distinction in most languages. So the expected value carries the JSON text, and the harness compares the serialized form of the actual value against it.

**`level` and `spec` are on every case.** `level` is what makes a partial implementation reportable rather than failing. `spec` cites the section, so a failure points at the rule rather than at a test name.

**A case may declare tolerance.** Appendix B of the spec lists spellings that differ by decoder and are not settled. Those cases carry `"compare": "parses_equal"` instead of an exact match and list the alternative spellings they accept in `also`, and the report says how many cases were compared loosely.

## 4. Fixtures and the database

Cases reference a fixture by name. A fixture is a SQL file that creates and populates its tables, and it MUST be idempotent, since the practical way to run this is against a scratch database that several runs share.

The corpus needs a real PostgreSQL. Three consequences worth stating before anyone builds this:

- The version matters. `time` spelling, `interval` spelling and `tsvector` escaping all vary. The report MUST record the server version it ran against.
- Rendering settings matter too. `money` follows `lc_monetary`, and timestamps and intervals follow `DateStyle` and `IntervalStyle`. The corpus pins all three with the `session_settings` fixture, which sets them on the database and so reaches sessions opened afterwards. `timestamptz` renders in the session `TimeZone`, which the corpus does not pin, so it is left out of the tolerant cases.
- Some cases cannot be expressed as a fixture plus a query, because they are about connection-level state or failure. Admission refusals (section 3) and upstream-unreachable errors (7.3) are configuration, not data. Those cases carry `"requires": "unreachable_upstream"` and a harness that cannot arrange the condition MUST report them as skipped rather than passed.

Skipped is a first-class outcome. A report claiming 100% where a third of the cases were skipped is worse than one that says so.

## 5. The report

```json
{
  "implementation": "pgserverless-go 0.1.0",
  "spec": "0.1.0",
  "corpus": 1,
  "postgres": "17.4",
  "claimed_levels": [1, 2, 3],
  "results": {
    "1": { "passed": 24, "failed": 0, "skipped": 0 },
    "2": { "passed": 41, "failed": 0, "skipped": 2 },
    "3": { "passed": 9, "failed": 0, "skipped": 0 },
    "4": { "passed": 0, "failed": 0, "skipped": 11 }
  },
  "loose_comparisons": 3,
  "failures": []
}
```

`spec` is the SPEC.md version the corpus declares in `spec_version`, and `corpus` is the corpus's own `version`. The two move separately: a corpus can gain cases for a spec version that has not changed, and an expectation changes only with a corpus version bump. A report without both cannot be reproduced.

An implementation claims a level only when that level has zero failures and zero skips. The report is what an implementation publishes; the point is that "compatible" stops being a marketing word and becomes a number someone else can reproduce.

## 6. What this cannot check

Worth writing down so the corpus is not oversold.

- **Performance and connection behaviour.** Nothing here measures latency, pooling, or what happens under concurrency.
- **The WebSocket transport beyond the handshake.** Once the connection is upgraded, the frames are the PostgreSQL protocol, and testing that is testing PostgreSQL. Level 4 cases cover the upgrade, subprotocol negotiation, host resolution, refusal before upgrade, and the SSLRequest decline. They do not cover query execution over the tunnel, which is a passthrough.
- **Anything about admission.** By design (spec section 3) it is deployment policy. The corpus can check that a refusal has the right shape, only if the harness can arrange a refusal.
- **Whether the server is talking to the database it claims.** The corpus checks the wire contract, not the semantics of the data.

## 7. Build order

1. Fixtures plus the level 1 cases. Done. That is enough to tell whether a new implementation is worth continuing.
2. The harness, against the reference implementation. Done: levels 1 to 4 pass. Any failure at this stage is a corpus bug, since the corpus is derived from that server.
3. The level 2 cases, which is the bulk of the work and the bulk of the value. Done for every family in SPEC section 6 (6.1 to 6.8) and the Appendix B spellings, which are marked `parses_equal` with the alternatives listed in `also`. The parameter path (SPEC 4.4) is covered by the `params/` cases. Not done: per-type breadth beyond one or two representatives per rule.
4. Level 4. One case (the upgrade handshake) exists; the rest of the level is not written.

Step 2 is the honest checkpoint. If the corpus cannot make the implementation it was derived from pass, it is wrong.
