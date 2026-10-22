#!/usr/bin/env bash
# Start the Go pgserverless server in front of a Postgres you supply, run the
# suite against it, then stop the server.
#
#   UPSTREAM_URL=postgres://app:pw@127.0.0.1:5432/app?sslmode=disable pnpm test:go-server [vitest args]
#
# PGSERVERLESS_BIN names the server binary to run, and is required.
set -euo pipefail

: "${UPSTREAM_URL:?set UPSTREAM_URL to the Postgres connection string to serve}"
ADDR_PORT="${SERVER_PORT:-4000}"

HERE="$(cd "$(dirname "$0")/.." && pwd)"
WORK="$(mktemp -d)"
trap 'kill "${SERVER_PID:-}" 2>/dev/null || true; rm -rf "$WORK"' EXIT

: "${PGSERVERLESS_BIN:?set PGSERVERLESS_BIN to a pgserverless server binary}"
BIN="$PGSERVERLESS_BIN"

DATABASE_URL="$UPSTREAM_URL" PGSERVERLESS_ADDR="127.0.0.1:$ADDR_PORT" "$BIN" 2>"$WORK/server.log" &
SERVER_PID=$!

for _ in $(seq 50); do
  curl -fsS "http://127.0.0.1:$ADDR_PORT/health" >/dev/null 2>&1 && break
  kill -0 "$SERVER_PID" 2>/dev/null || { cat "$WORK/server.log" >&2; exit 1; }
  sleep 0.2
done

# The driver sends HTTP to the host in its connection string, so point it at
# the server and keep the credentials and database name.
TEST_URL="$(UPSTREAM_URL="$UPSTREAM_URL" ADDR_PORT="$ADDR_PORT" node -e '
  const u = new URL(process.env.UPSTREAM_URL);
  u.host = "127.0.0.1:" + process.env.ADDR_PORT;
  u.search = "";
  console.log(u.toString());
')"

cd "$HERE"
DATABASE_URL="$TEST_URL" PGSERVERLESS_TLS=false PIPELINE_CONNECT=false pnpm exec vitest run "$@"
