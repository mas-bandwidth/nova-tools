# card requests and receipts — specification

`internal/card/request` is the typed input and receipt layer of the card
manager. It is pure data: request shapes, strict decoding, validation, one
canonical encoding, a request hash, receipt types and their one-line
renderings. It opens no store and no network connection, and it applies no
lifecycle policy. The manager above it decides what a valid request means;
this layer decides whether a request is well formed, what its bytes are, and
which transition an event names.

The rule of the layer is **batch always**: every operation takes an array or an
explicit complete scope, and a single card is an array of one. There is no
single-card request shape.

---

## 1. The request

A request is one JSON document. Its envelope is the same for every mutating
operation:

| Field | Meaning |
| --- | --- |
| `schema` | The integer `1`. |
| `operation` | `admit`, `resolve`, `apply_events`, `record_evidence`, `replace` or `inspect`. |
| `table` | The table name. |
| `epoch` | The expected epoch: a decimal string bounded as uint64. |
| `expected_table_revision` | The observed table revision: a decimal string bounded as uint64. |
| `operation_id` | The caller's operation ID. |
| `actor` | Who issues the request. |

Beside the envelope a request carries exactly one payload, the one its
operation names: `admissions` for `admit`, `events` for `apply_events`,
`evidence` for `record_evidence`, `replacements` for `replace`, `scope` for
`resolve` and `inspect`. A payload the operation does not take refuses as
`not-applicable`. An `inspect` reads only: it carries `schema`, `operation`,
`table` and `scope`, and `epoch`, `expected_table_revision`, `operation_id` and
`actor` refuse on it.

Counters are strings, never numbers: `"epoch": "3"`. A counter is `0` or a
decimal integer without a leading zero, at most 20 digits, at most
18446744073709551615. No value passes through a floating-point number, and a
JSON float or exponent refuses wherever an integer is asked for.

The document is read strictly. An unknown field refuses. A duplicate key at any
depth refuses. Data after the document refuses. Invalid UTF-8, a replacement
character and any control character in a value refuse. Nesting deeper than 8
levels refuses.

### 1.1 Admissions

`admit` creates each card in `waiting`. Each entry names a committed
definition and the stream row the card is admitted into. The card must not
exist; the absence guard is implied.

```json
{
  "schema": 1,
  "operation": "admit",
  "table": "work",
  "epoch": "3",
  "expected_table_revision": "12",
  "operation_id": "op-admit-1",
  "actor": "coordinator",
  "admissions": [
    {
      "id": "card-alpha",
      "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "object_id": "cccccccccccccccccccccccccccccccccccccccc",
      "commit": "cccccccccccccccccccccccccccccccccccccccc",
      "repository": "example.org/team/repo",
      "path": "cards/card-alpha.md",
      "row": "build"
    }
  ]
}
```

`digest` is the definition's SHA-256. `object_id` and `commit` are git object
IDs in lowercase hexadecimal, 40 or 64 characters. `repository` is an opaque
repository identity with no whitespace. `path` is repository-relative: slash
separated, no leading or trailing slash, no empty, `.` or `..` segment, no
backslash.

### 1.2 Events

`apply_events` carries typed events, one per card. Each event declares the
place and revision it expects. The source state is `expect.place.col`. The
destination is not a field: it is derived from the event type and the source
state (section 2), and a field named `to`, `destination` or the like is an
unknown field.

```json
{
  "schema": 1,
  "operation": "apply_events",
  "table": "work",
  "epoch": "3",
  "expected_table_revision": "12",
  "operation_id": "op-events-1",
  "actor": "coordinator",
  "events": [
    {
      "id": "card-alpha",
      "type": "verdict-accept",
      "expect": {"revision": "4", "place": {"row": "build", "col": "review"}},
      "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "issuer": "reader-1",
      "source": "review/7",
      "head": "cccccccccccccccccccccccccccccccccccccccc"
    }
  ]
}
```

Every event carries `id`, `type`, `expect`, `digest` (the card's definition
digest), `issuer` and `source` (the source artifact identity). Beyond those,
each type requires or allows fields:

| Type | Requires | Allows |
| --- | --- | --- |
| `start` | | |
| `result` | `result` (`success`, `failure` or `return`) | `head` |
| `verdict-accept` | `head` | |
| `verdict-retry` | `reason` | `head` |
| `verdict-rework` | `reason` | `head` |
| `head` | `head` | |
| `ci-green` | `head` | |
| `ci-red` | `head` | |
| `cancel` | `reason` | |
| `landing` | `head`, `landing` | |
| `external-landing` | `head`, `landing` | |
| `dependency-failed` | `dependency` (a card ID other than the card's own) | |
| `completed` | | |

A field a type neither requires nor allows refuses as `not-applicable`. `head`
is a git object ID; `landing` is an opaque reference; `reason` is one line with
no leading or trailing blank.

The type set is split where the destination depends on the variant, so that
the destination is a pure function of the type and the source state: a verdict
is `verdict-accept`, `verdict-retry` or `verdict-rework`, and a CI result is
`ci-green` or `ci-red`.

### 1.3 Evidence

`record_evidence` carries one entry per card. An entry binds the card's exact
definition digest and revision and holds one to 8 records. A card is listed
once; its several observations are its records.

```json
{
  "schema": 1,
  "operation": "record_evidence",
  "table": "work",
  "epoch": "3",
  "expected_table_revision": "12",
  "operation_id": "op-evidence-1",
  "actor": "coordinator",
  "evidence": [
    {
      "id": "card-alpha",
      "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "expect": {"revision": "4", "place": {"row": "build", "col": "review"}},
      "records": [
        {"evidence_id": "ev-1", "kind": "read", "disposition": "accept", "head": "cccccccccccccccccccccccccccccccccccccccc", "issuer": "reader-1", "source": "review/7"},
        {"evidence_id": "ev-2", "kind": "ci", "disposition": "green", "head": "cccccccccccccccccccccccccccccccccccccccc", "issuer": "ci", "source": "run/9"}
      ]
    }
  ]
}
```

A `read` record has disposition `accept` or `reject` and may carry a `head`. A
`ci` record has disposition `green` or `red` and must carry a `head`. Evidence
IDs are unique across the request.

### 1.4 Replacements

`replace` carries pairs: the old card, with its digest and expected revision
and place, and the new admission that succeeds it. The old card is waiting or
ready. Each card ID appears once across the old and new sides of the whole
request.

```json
{
  "schema": 1,
  "operation": "replace",
  "table": "work",
  "epoch": "3",
  "expected_table_revision": "12",
  "operation_id": "op-replace-1",
  "actor": "coordinator",
  "replacements": [
    {
      "old": {"id": "card-old", "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "expect": {"revision": "2", "place": {"row": "build", "col": "ready"}}},
      "new": {"id": "card-new", "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "object_id": "cccccccccccccccccccccccccccccccccccccccc", "commit": "cccccccccccccccccccccccccccccccccccccccc", "repository": "example.org/team/repo", "path": "cards/card-new.md", "row": "build"}
    }
  ]
}
```

### 1.5 Scope

`resolve` and `inspect` take a scope: an explicit ID array, or a complete
declared selection of a row, a column or both, with the bound the selection may
not exceed.

```json
{
  "schema": 1,
  "operation": "resolve",
  "table": "work",
  "epoch": "3",
  "expected_table_revision": "12",
  "operation_id": "op-resolve-1",
  "actor": "coordinator",
  "scope": {"row": "build", "col": "waiting", "bound": 100}
}
```

`{"ids": ["card-alpha", "card-beta"]}` names cards explicitly. A scope names
IDs or a selection, never both. A selection needs a row or a column and a
`bound` from 1 to 1024.

### 1.6 Value grammar

| Value | Form |
| --- | --- |
| Card ID, evidence ID | Nonempty ASCII letters, digits, underscore and hyphen. |
| Table, row | Letters, digits, underscore, dot and hyphen, starting with a letter, digit or underscore. |
| Operation ID, actor, issuer | The same as a table name. |
| `col`, source state | One of `waiting`, `ready`, `working`, `review`, `merging`, `landed`, `done`. |
| Digest | 64 lowercase hexadecimal characters. |
| Git object ID | 40 or 64 lowercase hexadecimal characters. |
| Revision in `expect` | A decimal counter of at least 1. |

---

## 2. The transition table

`Destination(eventType, sourceState)` returns the destination state and whether
the pair has a listed transition. An event whose type and source state have no
listed transition refuses as `no-transition`. A card in `landed` or `done`
has none.

| Event type | From | To | Outcome |
| --- | --- | --- | --- |
| `start` | ready | working | |
| `result` | working | review | |
| `verdict-accept` | review | merging | |
| `verdict-retry` | review | ready | |
| `verdict-rework` | review | ready | |
| `head` | merging | review | |
| `ci-red` | merging | review | |
| `ci-green` | none | | |
| `cancel` | waiting, ready, working, review, merging | done | cancelled |
| `landing` | merging | landed | |
| `external-landing` | waiting, ready, working | landed | |
| `dependency-failed` | waiting | done | dependency-failed |
| `completed` | review | done | completed |

`OutcomeOf(eventType)` returns the outcome a done card takes. The fourth
outcome, `replaced`, is set by a replacement, never by an event.

---

## 3. Validation

Validation covers the whole request before anything else. It reports every
refusal it finds, at most 64, and counts the rest; a request with any refusal is
refused whole and nothing is partly accepted.

Across the array:

- An empty array refuses (`empty-array`).
- A card ID repeated in one request refuses (`repeated-id`).
- Two events for one card that differ refuse (`conflicting-events`).
- An event whose source state is the destination of another event for the same
  card in the request refuses (`event-chain`). A card's events chain across
  requests, never inside one.

### 3.1 Bounds

| Bound | Limit |
| --- | --- |
| Admissions, events, evidence entries per request | 128 |
| Replacement pairs per request | 64 |
| IDs in a scope, and a selection's declared bound | 1024 |
| Records per evidence entry | 8 |
| Refusals reported | 64 |
| JSON nesting | 8 levels |
| Canonical encoded request | 1 MiB |
| Document read | 4 MiB |
| Card ID, evidence ID | 64 bytes |
| Table, row | 64 bytes |
| Operation ID, actor, issuer | 128 bytes |
| Repository, source, landing reference | 256 bytes |
| Path | 512 bytes |
| Reason | 512 bytes |
| Counter | 20 digits |

An over-limit request refuses whole with the limit and a remedy. It is never
split; a narrower request is a separate operation with its own ID.

### 3.2 Refusals

A refusal is a value: operation, index in the array (-1 for the envelope or
the scope), card ID where known, field, cause, what was found, limit and
remedy. It renders on one line:

```
refused apply_events[1] card=c2 field=expect.place.col: no-transition; found "result@done"; limit source state one of working; remedy: declare a source state the type moves from; the destination is derived, never sent
```

The causes are `syntax`, `trailing-data`, `duplicate-key`, `unknown-field`,
`not-applicable`, `wrong-type`, `too-deep`, `required`, `bad-value`, `invalid-utf8`,
`control-character`, `too-long`, `too-many`, `too-large`, `empty-array`,
`repeated-id`, `conflicting-events`, `event-chain`, `no-transition` and
`not-eligible`.

A syntax error, invalid UTF-8 in the document, an oversized document and
nesting past the limit stop the read; the refusal is then the one reported.
Every other refusal is collected. A value of the wrong JSON type is refused
once; its absence is not refused again.

---

## 4. Canonical encoding and identity

`Canonical(request)` is the request's one deterministic encoding: object keys
sorted bytewise, no whitespace between tokens, integers only, strings escaped
without the HTML escapes, optional fields that are empty omitted, arrays in
their given order. `Parse(Canonical(x))` equals `x`, and the canonical form of a
parsed document is stable. Two documents that differ only in key order,
whitespace or string escapes have the same canonical bytes; two that differ in
any value, or in array order, do not. `Hash(request)` is the SHA-256 of the
canonical bytes, 64 lowercase hexadecimal characters.

An operation's identity is its table, its epoch and its operation ID.
`SameRequest(recordedCanonical, incomingCanonical)` decides whether two
requests are the same by comparing canonical bytes. It takes no digest: a
caller-supplied digest never stands for the bytes. Empty input is never the
same request.

---

## 5. Receipts and rejections

A `Receipt` is the record of an accepted batch: the operation, operation ID,
request hash, table, epoch, the table revision before and after, the actor,
the result (`changed` or `noop`), every changed card's before and after (row,
state, revision and outcome), the declared non-changing selection outcomes
(`blocked` with the named reason, `ineligible`, `missing`) and the counts:
selected, eligible, changed, blocked, ineligible, missing and guards.
`ValidateReceipt` checks it: the table revision advances by one for a changed
batch and for a noop; a changed card's revision advances by one and a created
card starts at 1 in `waiting`; an outcome sits on a done card and nowhere else;
the counts agree with the lists.

A `Rejection` is the refusal of a batch or of one card: the operation and
operation ID, the scope (`batch` or `card`) and the card, the cause, what was
expected and what was observed, whether anything changed, and the next usable
command. `changed` is `no` or `unknown`. A transport failure is `unknown`,
never `no`; every other cause is `no`. `ValidateRejection` enforces both.

`CanonicalReceipt` and `CanonicalRejection` encode them with the rules of
section 4. `Receipt.Line` and `Rejection.Line` render each on one line.

---

## 6. Guarantees and limits

- No input panics. Every refusal is a value with a one-line rendering.
- The package imports no store, network or card-layer package.
- The package holds no lifecycle policy beyond the transition table: whether a
  card's revision is current, whether evidence suffices for a verdict, whether
  a dependency is met and what a replacement terminates are the manager's
  decisions on a valid request.
