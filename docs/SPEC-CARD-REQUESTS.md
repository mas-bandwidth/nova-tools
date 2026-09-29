# card requests and receipts: specification

`internal/card/request` is the typed input and receipt layer of the card manager.
It is pure data: request shapes, strict decoding, validation, one canonical
encoding, a request hash, receipt types and their one-line renderings. It opens no
store and no network connection, and it applies no lifecycle policy of its own
beyond one table (section 2). The manager above it decides what a valid request
means; this layer decides whether a request is well formed, what its bytes are,
and what a lifecycle input names. The identities, the refusal shape, the canonical
encoder and the bounds are the shared leaf `internal/card`, which the definition
package (SPEC-CARD.md) uses too.

The rule of the layer is **batch always**: every operation takes an array or an
explicit complete scope, and a single card is an array of one.

Two words are kept apart. A **lifecycle input** is a request that may move a card.
A **notification** is what the coordinator is told about a card. Neither is an
"event": the name of the operation `apply_events` is the specification's own, and it
applies lifecycle inputs.

---

## 1. The request

A request is one JSON document. Its envelope:

| Field | Meaning |
| --- | --- |
| `schema` | The integer `1`. |
| `operation` | `admit`, `resolve`, `apply_events`, `record_evidence`, `replace`, `inspect` or `check`. |
| `table` | The table name. |
| `epoch` | The expected epoch: a decimal string bounded as uint64. |
| `expected_table_revision` | The observed table revision: a decimal string bounded as uint64. |
| `operation_id` | Optional. The caller's operation ID. When absent, the request is known by `op-` and the first 16 hex characters of its hash without the operation ID (section 4), so the same request asked again is the same operation. |
| `actor` | Who issues the request. |

Beside the envelope a request carries exactly one payload, the one its operation
names: `admissions` for `admit`, `inputs` for `apply_events`, `evidence` for
`record_evidence`, `replacements` for `replace`, `scope` for `resolve` and
`inspect` (`check` takes an optional `scope`). A payload the operation does not
take refuses as `not-applicable`. An `inspect` and a `check` read only: they carry
`schema`, `operation`, `table` and their scope, and `epoch`, `expected_table_revision`,
`operation_id` and `actor` refuse on them.

Counters are strings, never numbers: `"epoch": "3"`. A counter is `0` or a decimal
integer without a leading zero, at most 20 digits, at most 18446744073709551615. No
value passes through a floating-point number, and a JSON float or exponent refuses
wherever an integer is asked for.

The document is read strictly. An unknown key refuses. A duplicate key at any depth
refuses. Data after the document refuses. Invalid UTF-8, a replacement character
and any control character in a value refuse. Nesting deeper than 8 levels refuses.

**Empty and absent are one thing.** An optional field that is empty (`"head": ""`,
`"depends_on": []`, an empty scope array) has the same canonical bytes as one that
is left out, and so the same hash. A required field that is empty is refused.

### 1.1 Admissions

`admit` creates each card in `waiting`. An entry is the pinned admission of a card
definition (SPEC-CARD.md) and the stream row the card goes into. The card must not
exist; the absence guard is implied.

```json
{
  "schema": 1,
  "operation": "admit",
  "table": "work",
  "epoch": "3",
  "expected_table_revision": "12",
  "operation_id": "op-admit",
  "actor": "coordinator",
  "admissions": [
    {
      "id": "card-01",
      "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "object_id": "cccccccccccccccccccccccccccccccccccccccc",
      "commit": "cccccccccccccccccccccccccccccccccccccccc",
      "repository": "github.com/example/cards",
      "path": "cards/card-01.md",
      "kind": "fix-red",
      "title": "Title of card-01",
      "row": "build",
      "policy_version": "1",
      "policy_digest": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
    },
    {
      "id": "card-02",
      "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "object_id": "cccccccccccccccccccccccccccccccccccccccc",
      "commit": "cccccccccccccccccccccccccccccccccccccccc",
      "repository": "github.com/example/cards",
      "path": "cards/card-02.md",
      "kind": "fix-red",
      "title": "Title of card-02",
      "row": "build",
      "policy_version": "1",
      "policy_digest": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
      "depends_on": [
        "card-01",
        "card-00"
      ],
      "entry": "work/one"
    },
    {
      "id": "card-03",
      "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "object_id": "cccccccccccccccccccccccccccccccccccccccc",
      "commit": "cccccccccccccccccccccccccccccccccccccccc",
      "repository": "github.com/example/cards",
      "path": "cards/card-03.md",
      "kind": "fix-red",
      "title": "Title of card-03",
      "row": "build",
      "policy_version": "1",
      "policy_digest": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
    }
  ]
}
```

`digest` is the definition's SHA-256. `object_id` and `commit` are git object IDs in
lower-case hexadecimal, 40 or 64 characters. `repository` is a repository identity:
`host[:port]/path`, the host in lower case, no scheme, user, credentials or `.git`;
a refusal about it names the rule and never quotes the value, which may be a URL
that carries a credential. `path` is repository-relative (no empty, `.`, `..` or
`.git` segment, no backslash, no drive letter, no leading dash, at most 512 bytes).
`kind`, `depends_on` (at most 8 card IDs, none the card itself), `entry` and `title`
are the definition's. `policy_version` and `policy_digest` pin the review policy the
card is judged by from admission on; the policy itself is the manager's.

### 1.2 Lifecycle inputs

`apply_events` carries lifecycle inputs, one per card. Each declares the place and
revision it expects. The source state is `expect.place.col`; the destination is not
a field: it is derived from the input type and the source state (section 2), and a
key named `to` or `destination` is an unknown key.

```json
{
  "schema": 1,
  "operation": "apply_events",
  "table": "work",
  "epoch": "3",
  "expected_table_revision": "12",
  "operation_id": "op-apply_events",
  "actor": "coordinator",
  "inputs": [
    {
      "id": "card-01",
      "type": "start",
      "expect": {
        "place": {
          "row": "build",
          "col": "ready"
        },
        "revision": "2"
      },
      "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "issuer": "reader-1",
      "source": "artifact:card-01"
    },
    {
      "id": "card-03",
      "type": "verdict-accept",
      "expect": {
        "place": {
          "row": "build",
          "col": "review"
        },
        "revision": "2"
      },
      "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "issuer": "reader-1",
      "source": "artifact:card-03",
      "head": "cccccccccccccccccccccccccccccccccccccccc"
    },
    {
      "id": "card-09",
      "type": "cancel",
      "expect": {
        "place": {
          "row": "build",
          "col": "waiting"
        },
        "revision": "2"
      },
      "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "issuer": "reader-1",
      "source": "artifact:card-09",
      "reason": "no longer wanted"
    },
    {
      "id": "card-10",
      "type": "landing",
      "expect": {
        "place": {
          "row": "build",
          "col": "merging"
        },
        "revision": "2"
      },
      "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
      "issuer": "reader-1",
      "source": "artifact:card-10",
      "head": "cccccccccccccccccccccccccccccccccccccccc",
      "landing": "land:cccccccccccccccccccccccccccccccccccccccc"
    }
  ]
}
```

Every input carries `id`, `type`, `expect`, `digest` (the card's definition digest),
`issuer` and `source` (the source artifact identity). Beyond those, each type
requires or allows fields, as the table of section 2 says; a field a type neither
requires nor allows refuses as `not-applicable`. `head` is a git object ID, or for a
card that has no pull request, `sha256:` and the 64-hex digest of its result
artifact. `landing` and `source` are tokens. `reason` is one line with no leading or
trailing blank.

### 1.3 Evidence

`record_evidence` carries one entry per card: the card, its expected place, and one
to 16 records (at most 512 in a request). The expected revision may be left out: it
is a freshness guard, not part of what the evidence says.

```json
{
  "schema": 1,
  "operation": "record_evidence",
  "table": "work",
  "epoch": "3",
  "expected_table_revision": "12",
  "operation_id": "op-record_evidence",
  "actor": "coordinator",
  "evidence": [
    {
      "id": "card-01",
      "expect": {
        "place": {
          "row": "build",
          "col": "review"
        },
        "revision": "4"
      },
      "records": [
        {
          "kind": "read",
          "issuer": "reader-1",
          "disposition": "accept",
          "head": "cccccccccccccccccccccccccccccccccccccccc",
          "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
          "verifier": "forge",
          "artifact": "review:1"
        },
        {
          "kind": "ci",
          "issuer": "ci:unit",
          "disposition": "green",
          "head": "cccccccccccccccccccccccccccccccccccccccc",
          "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
          "verifier": "forge",
          "artifact": "run:9"
        },
        {
          "kind": "sweep",
          "issuer": "sweeper",
          "disposition": "clean",
          "head": "cccccccccccccccccccccccccccccccccccccccc",
          "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
          "verifier": "forge",
          "artifact": "obs:abc"
        }
      ]
    },
    {
      "id": "card-02",
      "expect": {
        "place": {
          "row": "build",
          "col": "merging"
        }
      },
      "records": [
        {
          "kind": "ci",
          "issuer": "ci:unit",
          "disposition": "red",
          "head": "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd",
          "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
          "verifier": "forge",
          "artifact": "run:10"
        }
      ]
    },
    {
      "id": "card-03",
      "expect": {
        "place": {
          "row": "build",
          "col": "working"
        },
        "revision": "7"
      },
      "records": [
        {
          "kind": "landing",
          "issuer": "git",
          "disposition": "landed",
          "head": "cccccccccccccccccccccccccccccccccccccccc",
          "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
          "verifier": "forge",
          "artifact": "land:cccccccccccccccccccccccccccccccccccccccc"
        }
      ]
    }
  ]
}
```

A record is an observation bound to the card's definition digest (`digest`) and the
code head it was observed at (`head`, required): **evidence binds to definition digest
plus head**; `expect.revision` only guards freshness. The kinds are `read` (`accept`
or `reject`), `ci` (`green` or `red`), `sweep` (`clean` or `negative`) and `landing`
(`landed`); a `queue` record is written only by the `queue-rejected` input and is
never submitted. A CI result is evidence, never a lifecycle input.

The exact-head rule for a code card (a read or CI at the card's current head counts,
any other does not) needs the card's kind and current head, which this layer does not
know: it is enforced by the manager policy.

A record has one canonical text and one identity: seven blank-separated tokens,
`v1 <kind> <issuer> <disposition> <head> sha256:<digest> <verifier> <artifact>`, each
token drawn from letters, digits and `. : / @ + _ -`; its ID is the first 16 hex
characters of the SHA-256 of that line. `ParseRecord` reads it back. In one entry at
most one record has a given kind, issuer and head.

### 1.4 Replacements

`replace` carries pairs: the old card, with its digest and expected revision and
place, and the new admission that succeeds it. The old card is waiting or ready.
Each card ID appears once across the old and new sides of the whole request.

```json
{
  "schema": 1,
  "operation": "replace",
  "table": "work",
  "epoch": "3",
  "expected_table_revision": "12",
  "operation_id": "op-replace",
  "actor": "coordinator",
  "replacements": [
    {
      "old": {
        "id": "card-old",
        "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        "expect": {
          "place": {
            "row": "build",
            "col": "ready"
          },
          "revision": "2"
        }
      },
      "new": {
        "id": "card-new",
        "digest": "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
        "object_id": "cccccccccccccccccccccccccccccccccccccccc",
        "commit": "cccccccccccccccccccccccccccccccccccccccc",
        "repository": "github.com/example/cards",
        "path": "cards/card-new.md",
        "kind": "fix-red",
        "title": "Title of card-new",
        "row": "build",
        "policy_version": "1",
        "policy_digest": "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
        "depends_on": [
          "card-01"
        ]
      }
    }
  ]
}
```

### 1.5 Scope

`resolve`, `inspect` and `check` select cards by a scope in exactly one of three
forms: `ids` (explicit card IDs), `rows` (whole rows) or `all` (the whole table). A
scope is complete: it never returns a prefix.

```json
{
  "schema": 1,
  "operation": "resolve",
  "table": "work",
  "epoch": "3",
  "expected_table_revision": "12",
  "operation_id": "op-resolve",
  "actor": "coordinator",
  "scope": {
    "rows": [
      "build",
      "docs"
    ]
  }
}
```

```json
{
  "schema": 1,
  "operation": "inspect",
  "table": "work",
  "scope": {
    "ids": [
      "card-01",
      "card-02"
    ]
  }
}
```

```json
{
  "schema": 1,
  "operation": "check",
  "table": "work"
}
```

### 1.6 Value grammar

| Value | Form |
| --- | --- |
| Card ID, dependency, successor | Nonempty ASCII letters, digits, underscore and hyphen, at most 64 bytes; never `-` or `none`. |
| Table, row | Letters, digits, underscore, dot and hyphen, starting with a letter, digit or underscore. |
| Operation ID, actor, issuer, verifier, source, landing, artifact | A token: letters, digits and `. : / @ + _ -` (printable ASCII with no blank, comma or quote), within its bound. |
| `col`, source state | One of the six states of section 2. |
| Digest | 64 lower-case hexadecimal characters. |
| Git object ID | 40 or 64 lower-case hexadecimal characters. |
| Head | A git object ID, or `sha256:` and 64 hex characters. |
| Revision in `expect` | A decimal counter of at least 1. |
| Reason, title, entry | One line of ordinary text: no control, bidirectional, zero-width or format character, and no line or paragraph separator. |

---

## 2. The lifecycle

The lifecycle lives in one file, `internal/card/request/lifecycle.go`: the states,
the lifecycle inputs, the transition table, the operation moves, the forced moves and
the judgment classification. Nothing else in the card layer names a state or an input
type. It is modelled in TLA+ (`tla/CardManager.tla`): a change to a table in that
file is a change to the state machine, so the model changes in the same PR and TLC
runs. `TestLifecycleAgreesWithTheModel` reads the model when it is in the tree and
compares its states and transitions with the tables, and skips, saying why, when it is
not (the model is on the branch of PR #4599 until it lands).

The states are `waiting`, `ready`, `working`, `review`, `merging` and `landed`.
**Landed is final**: it means the code is on the development branch, a fact observed
from the repository, decided by nobody, and nothing moves a card out of it. There is
no `done` state and no confirmation after landing. A card that stops any other way
(cancelled, replaced, or whose dependency failed) **leaves the table**: it is removed
from its cell (unplaced), its record stays with an outcome (`cancelled`, `replaced`,
`dependency-failed`) and a reason, and a notification is produced.

### 2.1 The transition table

`Destination(inputType, sourceState)` returns the destination and whether the pair has
a listed transition; an input whose type and source state have none refuses as
`no-transition`. `off` is a card that leaves the table with its outcome. Class is
mechanical forward progress or a point where judgment may be required (section 2.3).

| Input | From | To | Class | Requires | Allows |
| --- | --- | --- | --- | --- | --- |
| `start` | ready | working | mechanical |  |  |
| `result` | working | review | mechanical | result | head |
| `verdict-accept` | review | merging | mechanical | head |  |
| `verdict-retry` | review | ready | judgment | reason | head |
| `verdict-rework` | review | ready | judgment | reason | head |
| `head` | review, merging | review | judgment | head |  |
| `queue-rejected` | merging | review | judgment | head, reason |  |
| `cancel` | waiting, ready, working, review, merging | off (cancelled) | judgment | reason |  |
| `landing` | merging | landed | mechanical | head, landing |  |
| `external-landing` | waiting, ready, working | landed | judgment | head, landing |  |
| `dependency-failed` | waiting | off (dependency-failed) | judgment | dependency |  |

A `result` that reports `failure` or `return` is a judgment point; `success` is
mechanical. `head` from `review` keeps the card in review: the acceptance and CI at
the old head no longer count. `landing` is observed from the repository at the exact
head; `external-landing` is landing without the review path.

An admit creates a card in `waiting`. A resolve moves a waiting card to `ready` when
its prerequisites are met (the manager checks). A replace ends a card in `waiting` or
`ready` as `replaced`, leaving the table, and creates its successor in `waiting`.

### 2.2 Forced moves

`ForcedMove(kind, disposition, state)` answers, from data, the one move an observation
forces in the batch that records it: a red CI result recorded for a card in `merging`
returns it to `review`. Every other combination forces nothing. The policy that
applies it is the manager's; this package holds the data and the classification.

### 2.3 Judgment points

Every lifecycle input, forced move, evidence observation and selection outcome is
classified, and a test fails when one is added without a classification.

| Point | Class |
| --- | --- |
| input `start` | mechanical |
| input `result` | mechanical |
| input `verdict-accept` | mechanical |
| input `verdict-retry` | judgment |
| input `verdict-rework` | judgment |
| input `head` | judgment |
| input `queue-rejected` | judgment |
| input `cancel` | judgment |
| input `landing` | mechanical |
| input `external-landing` | judgment |
| input `dependency-failed` | judgment |
| result `success` | mechanical |
| result `failure` | judgment |
| result `return` | judgment |
| forced move: ci red in merging -> review | judgment |
| evidence `read` `accept` | mechanical |
| evidence `read` `reject` | judgment |
| evidence `ci` `green` | mechanical |
| evidence `ci` `red` | judgment |
| evidence `sweep` `clean` | mechanical |
| evidence `sweep` `negative` | judgment |
| evidence `landing` `landed` | mechanical |
| evidence `queue` `reject` | judgment |
| selection outcome `changed` | mechanical |
| selection outcome `blocked` | judgment |
| selection outcome `ineligible` | mechanical |
| selection outcome `missing` | judgment |
| selection outcome `already` | mechanical |
| selection outcome `inapplicable` | mechanical |

A judgment point yields a notification to the coordinator; nothing in this layer
answers one by moving the card again.

---

## 3. Validation

Validation covers the whole request before anything else and reports every refusal it
finds, at most 64, counting the rest; a request with any refusal is refused whole and
nothing is partly accepted.

Across the array: an empty array refuses (`empty-array`); a card ID repeated in one
request refuses (`repeated-id`); two lifecycle inputs for one card that differ refuse
(`conflicting-inputs`); an input whose source state is the destination of another input
for the same card in the request refuses (`input-chain`): a card's inputs chain across
requests, never inside one; two records of one entry with one kind, issuer and head
that differ refuse (`conflicting-records`).

### 3.1 Bounds, and why a valid request fits one table manifest

The table's batch bounds are 128 entries with changes, 1,024 guard-only entries, a
1 MiB manifest, 256-byte member IDs, 64 KiB per field value and 128 set fields per
member. One of the 128 changed entries is the manager's card operation record, so a
card request has at most 127. Every bound of the card layer is set so that any request
valid here fits one manifest:

| Bound | Limit |
| --- | --- |
| Admissions, lifecycle inputs, evidence cards per request | 127 |
| Replacement pairs per request (2 members each) | 63 |
| Cards in a resolve scope by ID | 113 |
| Cards in an inspect or check scope by ID; rows in a scope | 1,024; 64 |
| Dependencies of a card; distinct outside dependencies of an admit | 8; 1,024 |
| Records per evidence entry; records per request | 16; 512 |
| Refusals reported | 64 |
| JSON nesting | 8 levels |
| Canonical request | 1 MiB |
| Document read | 4 MiB |
| Card ID | 64 bytes |
| Table, row; operation ID, actor, issuer | 64 bytes |
| Verifier; source, landing, artifact | 32 bytes; 128 bytes |
| Head; record line | 71 bytes; 391 bytes |
| Repository identity; path | 128 bytes; 512 bytes |
| Reason; title; entry | 256; 160; 128 bytes |
| Counter | 20 digits |

The sums (each worst-case manifest is the entries at their largest encoded size plus 1 KiB
of envelope and the card operation record of 241 KiB; a test builds the worst-case
request of each operation and asserts every sum):

| Operation | Worst case | Bytes |
| --- | --- | --- |
| admit | 1,024 + 127 x 2253 + 246784 | 533939 |
| replace | 1,024 + 63 x 2765 + 246784 | 422003 |
| apply_events | 1,024 + 127 x 4506 + 246784 | 820070 |
| record_evidence | 1,024 + 127 x 3789 + 512 x 615 + 246784 | 1043891 |
| resolve | 1,024 + 113 x 308 + 1,017 x 256 + 246784 | 542964 |

Against the table's 1048576 bytes, the largest is record_evidence with 4685 bytes to spare.

Resolve's guard-only entries are its cards' prerequisites: 113 cards of 8 dependencies
need at most 113 x 9 = 1,017 of the 1,024; 127 admissions of 8 dependencies name at
most 1,016 outside cards. An evidence request has an escaping factor of exactly 1: its
fields are tokens and digests, which hold no quote or backslash; only the free text of a
reason, a title and an entry can double when the request is stored as a string.

An over-limit request refuses whole with the limit and a remedy. It is never split; a
narrower request is a separate operation with its own ID.

### 3.2 Refusals

A refusal is the card layer's one shape: operation, index in the array (-1 for the
envelope or a scope), card ID where known, field, cause, found, limit and next action.
It renders on one line; a value from the input is quoted and at most 48 bytes (then its
length), so a newline, a NUL or a 100,000-byte key can neither forge nor flood a line:

```text
refused apply_events[1] card=c2 field=expect.place.col: no-transition; found "result@landed"; limit source state one of working; next: declare a source state the type moves from; the destination is derived, never sent
```

The causes are one closed vocabulary, shared with the definition package, one name for
one fact:

`syntax`, `trailing-data`, `duplicate-key`, `unknown-key`, `not-applicable`, `wrong-type`, `too-deep`, `required`, `invalid-value`, `reserved-word`, `invalid-utf8`, `control-character`, `too-long`, `too-many`, `too-large`, `empty-array`, `repeated-id`, `conflicting-inputs`, `input-chain`, `conflicting-records`, `no-transition`, `not-eligible`, `invalid-repository`, `invalid-commit`, `invalid-path`, `path-escapes`, `empty-file`, `duplicate-file`, `byte-order-mark`, `carriage-return`, `contract-line`, `contract-sha`, `ambiguous-spelling`, `unsupported-schema`, `stranded`, `id-mismatch`, `empty-value`, `invalid-id`, `invalid-entry`, `invalid-kind`, `unclassified-kind`, `invalid-paths`, `invalid-depends-on`, `invalid-tier`, `invalid-test`, `no-brief`, `self-dependency`, `dependency-cycle`, `too-many-external-dependencies`, `duplicate-path`, `not-repository`, `git-unavailable`, `git-failed`, `timeout`, `unknown-commit`, `not-commit`, `missing-path`, `missing-object`, `symlink`, `not-blob`, `identity-missing`, `invalid-request`, `stale-epoch`, `stale-table-revision`, `stale-card-revision`, `place-mismatch`, `digest-mismatch`, `operation-conflict`, `unknown-row-or-column`, `guard-failed`, `over-limit`, `transport-failure`, `store-error`.

A syntax error, invalid UTF-8 in the document, an oversized document and nesting past the
limit stop the read; the refusal is then the one reported. Every other refusal is
collected. A value of the wrong JSON type is refused once; its absence is not refused
again.

---

## 4. Canonical encoding and identity

`Canonical(request)` is the request's one deterministic encoding, by the card layer's
encoder: object keys sorted bytewise, no whitespace between tokens, integers only, strings
escaped without the HTML escapes and every control character (and U+2028, U+2029, DEL) as a
`\u00xx` escape, empty optional fields left out. Arrays whose order carries no meaning are
sorted by the encoder: entries by card ID, replacements by the old card's ID, records by
record ID, scope IDs and rows, dependencies. Two documents that differ only in key order,
whitespace, string escapes or the order of such arrays have the same canonical bytes and
the same hash; two that differ in any value do not.

`Hash` is the SHA-256 of the canonical bytes, and `HashWithoutOperationID` the same with the
operation ID left out: the hash of what is asked, whatever it is called.

`Validate` and `Parse` return a `Valid`: the request with its canonical bytes and hash, which
has no exported field and no other constructor, so a later layer cannot be handed a request
that was not checked. It satisfies `Checked` for every operation: `Kind`, `OperationID` (given,
or derived), `Table`, `Epoch`, `ObservedRevision`, `Actor`, `Hash`, `HashWithoutOperationID`,
`Canonical` and `Cards`. `Cards` enumerates every card a request names with its role
(`changed`, `guard-only`, `old`, `new`, `dependency`) and its expected place and revision where
given, so no consumer switches on the operation.

An operation's identity is its table, its epoch and its operation ID. `SameRequest(recorded,
incoming)` takes the incoming request as a `Valid` and the recorded canonical bytes, and
checks that the recorded bytes parse and re-canonicalise to themselves before comparing them
with the incoming bytes. It takes no digest: a caller-supplied digest never stands for the
bytes. Empty input is never the same request.

---

## 5. Receipts, results and rejections

A `Receipt` is the record of an accepted batch: the operation, operation ID, request hash,
table, epoch, the table revision before and after, the actor, the result (`changed` or
`noop`), every changed card (`before`, `after`, and per card its `successor` when it left as
replaced, its `landing` identity when it landed, its `reason` when it left the table, and its
notifications), the declared non-changing selection outcomes (`blocked`, `ineligible`,
`missing`, `already`, `inapplicable`) and the counts. Its one-line form carries the request
hash and its own digest, which is its identity.

`ValidateReceipt` refuses a receipt no operation could produce: the table revision advances by
one for a changed batch and for a noop; a card's revision advances by one and a created card
starts at 1 in `waiting`; an admit only creates; a resolve only moves `waiting` to `ready`; an
apply only makes a move some lifecycle input names; recording evidence only leaves the state or
makes the forced move; a replace only ends a `waiting` or `ready` card as replaced and creates
its successor; a card that left the table has no state, an outcome and a reason, and a placed
card has none of them; nothing leaves `landed`; the counts agree with the lists.

A card's state carries its **cycle counters** as decimal strings, an empty one reading zero:
`rework`, `merge_returns`, `head_changes`, `red`, `red_same_head`, `red_heads`, `flaky`,
`reject`, `starts`, `results`, `failed_results` and `lineage`. A receipt never decreases one
(`red_same_head`, the red results at the current head, resets when the head changes).

A **notification** on a changed card has a kind, whether judgment may be required (`yes` or
`no`, consistent with its kind), one bounded line of what it changes about the card's
readiness (filled by the manager), whether the card is `escalated` (only a judgment point) and
the counters after the change. Nothing here produces one.

An `InspectResult` is what an inspect or a check returns: per card its place, pinned definition
identity, evidence standing at the current head, what is missing for the next forward move,
structural drift, counters, escalation and marks. `ValidateInspectResult` checks it.

A `Rejection` is the refusal of a batch or of one card: the operation and operation ID (none
for an inspect or a check), the scope (`batch` or `card`) and the card, the cause, what was
expected and observed, and the next usable command. Whether anything changed is not a field: it
is derived from the cause (`Cause.Changed`): `unknown` for `transport-failure` (the request did
not reach the store or its reply was lost) and for `store-error` (the store returned an error
that does not prove nothing was written), `no` for every other.

`CanonicalReceipt` and `CanonicalRejection` encode them with the rules of section 4; a hostile
value in either renders on one quoted, bounded line.

---

## 6. Guarantees and limits

- No input panics. Every refusal is a value with a one-line rendering.
- The package imports no store, no network and only the leaf `internal/card`.
- The only lifecycle policy in the layer is the one file of section 2. Whether a card's revision
  is current, whether evidence suffices for a verdict, whether a prerequisite is met, what a
  replacement ends and whether a kind needs a pull request are the manager's decisions on a valid
  request.
- The specification's replay rule (an identical request returns the recorded result) is the
  manager's; this layer gives it `SameRequest`, the hash and the operation identity.

## 7. Open questions for the owner

These are not decided here; each is implemented as the stricter reading.

1. **Where does a card that needs no pull request end?** A kind that needs none (`read`, `probe`,
   `text`, `tone`, `report`) has no code to land, and the lifecycle has no state for it and no
   input that ends it: it can be cancelled (it leaves the table) and nothing else.
2. **Cancel from merging.** Allowed today, as the specification lists it; the manager design asks
   whether it should dequeue first.
3. **Code that landed while its card was in review.** `external-landing` from review refuses; review
   must pass `verdict-accept` first.
4. **Replacement of a card in review or merging.** Refused: only `waiting` and `ready` are replaced.
5. **A red CI result forces merging to review; a reader's rejection in merging does not.** The manager
   design forces both; the ruling names only red CI.
