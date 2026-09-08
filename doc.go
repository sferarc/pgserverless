// Package pgserverless serves PostgreSQL over HTTP for serverless environments,
// speaking the request and response shapes the @neondatabase/serverless driver
// sends in its HTTP (fetch) mode.
//
// That protocol has become a de facto way to reach PostgreSQL from a runtime
// with no TCP sockets, and more than one provider implements it, so this
// package treats it as a shared protocol rather than as one vendor's endpoint.
//
// # Scope
//
// Both transports. POST /sql carries one request and one response, and is what
// a fetch-based client uses. GET /v2 upgrades to a WebSocket carrying the
// PostgreSQL wire protocol, which the client's Pool and Client types use.
//
// The tunnel is a byte pipe once its startup phase is over. It declines the
// client's SSLRequest, because the WebSocket already carries the encryption,
// and then relays without parsing. Authentication runs end to end between the
// client and the database, so this package never holds a credential on that
// path.
//
// # The seam
//
// Everything a host has to decide before a query runs (which database, is this
// caller allowed, is the tenant paying) is one function:
//
//	type Admission func(c echo.Context) (Upstream, error)
//
// Everything after that decision (parse the body, dial, run, coerce values,
// encode the response) is this package. StaticUpstream is the whole
// implementation for a deployment in front of a single database; a multi-tenant
// platform's version is much larger, and that asymmetry is the point.
//
// # Value coercion
//
// Most of this package is the rules for turning PostgreSQL values into the JSON
// the driver expects, which is where a naive implementation goes wrong. The
// rules are not arbitrary: each one exists because the obvious encoding loses
// information or produces something the driver cannot parse.
//
//   - int8 (bigint) and numeric are strings, because JSON numbers are float64
//     and lose precision beyond 2^53.
//   - Non-finite floats (NaN, Infinity, -Infinity) are PostgreSQL's own text,
//     because encoding/json cannot marshal them at all and fails the response.
//   - Infinite timestamps are the words infinity and -infinity, not the integers
//     a pgtype.InfinityModifier would marshal to, which are indistinguishable
//     from data.
//   - macaddr, bit and varbit are text, not the base64 their Go representations
//     marshal to.
//   - Multi-dimensional arrays keep their dimensions, re-nested from the value's
//     own binary array header, because a flat decode loses the shape.
//   - inet drops the netmask when it covers a single host, matching what
//     PostgreSQL prints; a real netmask and every cidr pass through unchanged.
//   - Ranges and multiranges are PostgreSQL literals, not the Go structs behind
//     them.
//
// Raw-text mode (the driver's primary path, since it runs its own type parsers
// client-side keyed by dataTypeID) bypasses most of this by returning every
// value as PostgreSQL text.
package pgserverless
