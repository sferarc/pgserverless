# pgserverless protocol spec

The specification for serving PostgreSQL over HTTP and WebSocket to serverless clients, and a conformance corpus that checks any implementation of it over the network.

| File | What it is |
| --- | --- |
| [SPEC.md](SPEC.md) | The protocol specification. It is the only normative text. |
| [CONFORMANCE.md](CONFORMANCE.md) | How an implementation is checked against it, and what a report means |
| [CHANGELOG.md](CHANGELOG.md) | What changed in each spec version |
| [openapi.yaml](openapi.yaml) | OpenAPI description of `POST /sql` and the `/v2` upgrade. SPEC.md wins where the two differ. |
| [schema.json](schema.json) | JSON Schema for the conformance corpus |
| [cases/v1.json](cases/v1.json) | The corpus: 104 cases across all four conformance levels |

## Why this exists

The protocol had no written specification. It was defined by one client implementation plus whatever a server happened to do, and the interesting parts are the ones a reimplementation gets wrong by default: an `int8` that must be a JSON string, a `NaN` that JSON cannot carry at all, an infinite timestamp that must not arrive as the integer 1, a multi-dimensional array that must not arrive flat.

Writing it down is what makes a second implementation possible, and a conformance corpus is what makes "compatible" a number someone else can reproduce rather than a claim.

## Status and versions

SPEC.md is version 0.1.0, a draft. The spec is versioned on its own, independently of the Go module in this repository: SPEC.md's "Versioning" section says what a major, minor or patch change means, and the corpus names the spec version it checks in `spec_version`. The corpus has its own integer `version`, which moves only when an existing case's expectation changes.

The reference implementation, the Go package at the root of this repository, passes every case at levels 1 to 4 against PostgreSQL 17. What the corpus does not yet cover is listed in CONFORMANCE.md section 7.

Six questions could not be settled from the client or the reference implementation. They are in SPEC.md Appendix B and are marked as open rather than guessed.

## Check a server

Run the harness from the repository root. It applies the corpus fixtures through `-dsn`, so point it at a throwaway database you own (one fixture runs `ALTER DATABASE`), and point the server under test at that same database:

```bash
go run ./cmd/pgsconform -cases spec/cases/v1.json \
  -server http://127.0.0.1:4000 \
  -dsn 'postgres://user:pass@127.0.0.1:5432/scratch' \
  -report report.json
```

The report records the spec version, the corpus version, the PostgreSQL version and the levels passed. A level counts only with zero failures and zero skips.
