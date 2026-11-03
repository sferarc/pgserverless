# Spec changelog

Versions of [SPEC.md](SPEC.md). These are independent of the Go module's releases, which are in the [module changelog](../CHANGELOG.md). SPEC.md section "Versioning" says what each kind of version change means.

## 0.5.0 (draft)

- Section 7.2 requires a statement that starts a COPY to or from the client (`COPY ... FROM STDIN`, `COPY ... TO STDOUT`) to be answered 400 at once, never by waiting for the request timeout and never by a 200 with the copied rows dropped, and recommends `code` `0A000`, which Neon's proxy does not send. A server that forwarded the copy response unhandled conformed to 0.4.1 and does not conform to this version, the reference implementation included before its 0.7.2 release.
- A corpus case may carry `answered_within_ms`, and the harness fails the case when the server has not answered by then. Three cases use it: `errors/copy-to-stdout-is-400-at-once` and `errors/copy-from-stdin-is-400-at-once` at level 1, and `batch/copy-in-a-batch-is-400-at-once` at level 3. The corpus `version` stays 2, since no existing case's expectation changed.
- A corpus case may carry `then`, a follow-up request with its own expectation that the harness sends once the case's request passed. `batch/copy-in-a-batch-persists-nothing` at level 3 uses it to check that a write before a refused COPY in a batch does not persist. The corpus `version` stays 2, since the case is new.

## 0.4.1 (draft)

- Section 4.4 says quoting an array element escapes only `"` and `\`, so a control character is written as itself. The rule that an element reads back as sent already required this.
- Appendix C is new and lists behaviour of Neon's proxy this document does not adopt. C.1 is its JSON escaping of string array elements, which turns a newline into the letter `n`. The corpus checks it in `params/control-character-in-text-array-element`, and its `version` stays 2.

## 0.4.0 (draft)

- Section 5.1 requires an object-mode row to carry one member per column in column order, a repeated name included, which is what Neon's proxy writes. This settles Appendix B item 1. A client parsing the row sees the last column with the name. The corpus checks it in `core/duplicate-columns-in-object-mode`.
- Section 5.2 requires `format` in a field descriptor to be `"text"`, which is what Neon's proxy writes. This settles Appendix B item 5. The corpus checks it in `core/field-format-is-text`, and a corpus field may now list `format`.
- Section 4.4 requires a JSON array parameter to be rendered as an array literal whatever the parameter's type, as Neon's proxy does: `text` receives the literal and `int4`, `json` and `jsonb` reject it with 22P02. Three `params/array-into-*` cases check it.
- The corpus `version` stays 2, since no existing case's expectation changed.

## 0.3.0 (draft)

- Section 6.4 requires `timestamp` and `timestamptz` in native mode to be emitted as PostgreSQL's text (`"2024-01-01 10:00:00"`, and `"2024-01-01 10:00:00+00"` in a UTC session), which is what Neon's proxy returns. This settles Appendix B item 2. A server emitting RFC 3339 (`"2024-01-01T10:00:00Z"`) conformed to 0.2.0 and does not conform to this version, the reference implementation included until this release.
- The corpus `version` is 2: `native/timestamp-spelling-is-undecided`, which accepted either spelling, is replaced by `native/timestamp-as-text`, which accepts only PostgreSQL's. Two cases are new, `native/timestamp-array-as-text` and `native/timestamp-bc-as-text`.

## 0.2.0 (draft)

- Section 6.4 requires an anonymous `record` to be emitted as PostgreSQL's record text, `"(1,2)"`, and a `record[]` as an array of those strings. Section 6.7 only said a composite must not be re-nested, so a server returning the fields as the JSON array `[1,2]` conformed to 0.1.1 and does not conform to this version. The corpus checks both.

## 0.1.1 (draft)

- Appendix B items 4 and 6 state only what is still open. Item 4 said the reference implementation sets no size limit of its own; it refuses a request body over 10 MiB with 413, and its server refuses with 507 a response whose row values pass 10 MiB. Item 6 said its `time`, `interval` and `tsvector` spellings differ from PostgreSQL's; they now match.

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
