# AGENTS.md

Instructions for coding agents working in this repository. People start at [README.md](./README.md).

## Commands

Go 1.26 or later. These need no database:

```bash
go vet ./...
go test ./...
PGSERVERLESS_TEST_STDLIB=true go test .    # the same handler tests through NewHTTPHandler
go build ./cmd/pgserverless ./cmd/pgsconform
```

CI runs both `go test` lines with `-race`.

The protocol corpus needs a running server and a scratch PostgreSQL database it can create tables in:

```bash
export UPSTREAM_URL='postgres://user:pass@127.0.0.1:5432/scratch?sslmode=disable'
DATABASE_URL="$UPSTREAM_URL" go run ./cmd/pgserverless &
go run ./cmd/pgsconform -cases spec/cases/v1.json -server http://127.0.0.1:4000 \
  -single-database -dsn "$UPSTREAM_URL"
```

The driver suite needs Node, pnpm and a disposable database, since it creates and drops tables:

```bash
bin="$(mktemp -d)/pgserverless"
go build -o "$bin" ./cmd/pgserverless
cd driver-conformance
pnpm install
UPSTREAM_URL='postgres://user:pass@127.0.0.1:5432/scratch?sslmode=disable' \
  PGSERVERLESS_BIN="$bin" pnpm test:go-server
```

## Layout

- The root package is the handler: `http.go` and `handler.go` serve `POST /sql`, `websocket.go` the `/v2` tunnel, `types*.go` and the `*text.go` files the value coercion, `txn.go` batches.
- `pgfake/` is an in-process PostgreSQL backend the tests run against. It is exported, so its API is public.
- `cmd/pgserverless/` is the reference server in front of one database, with its README, Dockerfile and compose example.
- `cmd/pgsconform/` runs the corpus against any server over the network.
- `spec/` holds SPEC.md, openapi.yaml, schema.json, CONFORMANCE.md, the corpus in `cases/v1.json` and the spec changelog.
- `driver-conformance/` drives the real `@neondatabase/serverless` driver, Drizzle, Kysely and Waddler against a server.
- `llms.txt` lists the docs for agents using the library.

## Rules

- `spec/SPEC.md` is the only normative text. `openapi.yaml` and `schema.json` follow it; where they disagree, fix them, not SPEC.md.
- A change to what a server sends or accepts on the wire lands with a SPEC.md change, an entry in `spec/CHANGELOG.md` versioned as SPEC.md's "Versioning" section says, and a corpus case in `spec/cases/v1.json` that names its `level` and `spec` section. Add a Go test in the package as well.
- When the spec leaves behaviour open, the `@neondatabase/serverless` client decides, then Neon's proxy. Neon behaviour the spec does not adopt goes in Appendix C. Do not settle an Appendix B question by guessing.
- Do not edit `CHANGELOG.md` at the root; release notes are written when a version is cut.
- A renamed or added doc or spec file goes into `llms.txt` in the same change.
