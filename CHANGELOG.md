# Changelog

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
