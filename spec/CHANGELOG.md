# Spec changelog

Versions of [SPEC.md](SPEC.md). These are independent of the Go module's releases, which are in the [module changelog](../CHANGELOG.md). SPEC.md section "Versioning" says what each kind of version change means.

## 0.1.0 (draft)

The first published version.

- Sections 1 to 10 and Appendices A and B as first drafted from client 1.1.0 and the reference implementation.
- Section 8 states the batch response shape, `{"results": [...]}` with one result object per statement in order, which the earlier draft left implicit.
- Section 7.2 now requires 400 for every PostgreSQL error a statement raises, whatever its SQLSTATE class, and recommends 400 or 500 for connect errors and 400 with `08P01` for a cancelled or timed out request. This is the mapping Neon's proxy uses, and the only one under which the client always receives the SQLSTATE, since it parses the body on 400 only. The first draft mapped classes `53` and `57` to 503, class `08` to 504 and class `28` to 401.
- Section 7.1 says a missing error field may be omitted or `null`.
- Sections 4.2 and 4.3 allow an empty `query`, in a batch item too, which is answered 200 with empty `fields` and `rows`, `command` `""` and `rowCount` null, as Neon renders PostgreSQL's EmptyQueryResponse. The first draft required a non-empty string.
- Section 11, security considerations, is new.
- A second, independently written draft of the protocol was reconciled into this one and dropped. What it added is in sections 8 and 11. What it got wrong is not carried over: it described one PostgreSQL message per WebSocket frame, which section 9 contradicts, and a level structure that did not match section 10. Its client API section is out of scope for a wire protocol.
- The corpus gains a `spec_version` field naming the version its cases check.
