# SPEC-CARD: card definitions

A card is a unit of work held as plain data. Its logic is a set of functions over
arrays of cards; a single card is an array of one, and no function takes one card
alone. Nothing in the layer executes or interprets a card's prose.

This file states what a card definition is and how a set of them becomes admission
records. The code is `internal/card/definition`, on the shared leaf
`internal/card` (identities, the refusal shape, the canonical encoder and the
bounds, which `internal/card/request` uses too). The requests that carry an
admission are in [SPEC-CARD-REQUESTS.md](SPEC-CARD-REQUESTS.md). This profile is a
new card format, not the format the swarm cards carry (see "Adoption").

## The one entry point

`Admissions(ctx, repoDir, commit, paths, opts...)` is the function from a git
repository to admission records. It pins the committed blobs of the paths at the
commit, parses the pinned bytes itself, validates the array and returns one record
per card in the order of the paths, or no record and every refusal of the first
stage that refused (pin, then parse, then validate; at most 64 refusals, the rest
counted). It never takes a definition or a digest from its caller: a record is
always what the committed bytes say. `Parse`, `Validate` and `Pin` are the
package's own steps and are not exported: nothing outside needs them.

## The file

```card
RESULT: card-example sha=00112233445566778899aabbccddeeff00112233
SCHEMA: v2
ID: card-example
TITLE: Cap a line without erasing its tail
KIND: fix-red
PATHS: internal/oneline/oneline.go, internal/oneline/oneline_test.go
DEPENDS-ON: card-base, card-fixtures
TIER: flash
TEST: internal/oneline TestCapZeroDropReturnsInputUnchanged
DONE-WHEN: The named test fails at the base and passes at the head.
DOORS: none
PROBES: none

Goal: Cap never erases a nonempty tail.

The brief is every byte from the first line that is not a header line to the end
of the file. It may begin with prose such as `Goal:` or `Context:`.
```

- Line 1 is the contract line: `RESULT: <id>`, with the colon, then optionally
  ` sha=<hex>` and optionally a note. `sha=`, when present, is 40 or 64 lower-case
  hexadecimal characters: the commit the work starts from. It is carried into the
  admission record as `base_commit`. The note is one clean line of at most 512
  bytes, kept and never interpreted. The colon-less form is not this profile's.
- The header starts on line 2. A header key is upper case only: a line matching
  `^[A-Z][A-Z0-9-]*: ` (or a key with nothing after the colon). Blank lines may
  occur inside the header. The first nonblank line that is not a header line ends
  it, and the brief starts on that line. So a brief may begin with `Goal: ...`,
  `Context: ...`, a URL or a heading; none of them is a header line.
- An upper-case key outside the profile refuses (`unknown-key`). A line whose key
  folds to a known key (`Kind:`, `kind:`, `KIND :`, ` KIND:`, `DEPENDS_ON:`,
  `DONEWHEN:`) refuses as `ambiguous-spelling`. A known key at column zero below
  the header, outside a fenced block, is stranded and refused. Indented text,
  quoted text (`> `) and fenced blocks (three or more backticks or tildes) declare
  nothing. A closing fence is the same character, at least as long as the opening
  one, with no info string after it.
- `ENTRY: <path>` may follow `ID`. It is an attribute, never the card's identity.

The file is refused when it is not valid UTF-8, starts with a byte-order mark,
holds a carriage return (CRLF or a bare CR) or a NUL byte, is empty, has no brief,
or is over 256 KiB. A carriage return is refused rather than trimmed: the pinned
bytes are the identity of a card, so one card is one byte sequence. A file that
holds only a contract line reports the missing keys and `no-brief` in one pass.

## Values

`none` and `-` are two words for two places, and neither is a card ID: `DEPENDS-ON`
takes card IDs or `-`; `PATHS`, `DOORS` and `PROBES` take their grammar or `none`.
`DEPENDS-ON: none` and `PATHS: -` refuse, naming the right word for that key.

| Field | Rule |
| --- | --- |
| `SCHEMA` | `v2`. `v3` is refused by name; every other value is unsupported. |
| `ID` | Nonempty ASCII letters, digits, underscore and hyphen, at most 64 bytes, and not `-` or `none`. It equals the ID on the contract line. |
| `ENTRY` | Optional. At most 128 bytes, no comma, no control character, no padding. |
| `TITLE` | One line, at most 160 bytes. |
| `DOORS` | One line of at most 200 bytes, or `none`. |
| `DONE-WHEN`, `PROBES` | One line of at most 2048 bytes (`PROBES` may be `none`). They reach the record only through the definition digest. |
| `KIND` | A name in `internal/hygiene/kinds.txt` that the completion policy classifies. |
| `PATHS` | `none`, or at most 8 globs of at most 120 bytes each, by the grammar of `hygiene.ValidatePaths`, none repeated. |
| `DEPENDS-ON` | `-`, or at most 8 card IDs, none repeated. It names card IDs, not Work paths. |
| `TIER` | A route of `internal/cardhdr`: `frontier`, `pro` or `flash`. |
| `TEST` | At most 200 bytes, by the grammar of `cardhdr.ParseTest`, with a Test function name and a package that passes the `PATHS` rule. `none <why>` is allowed only for a kind that needs no pull request. |

Every one-line value refuses a control character, a bidirectional control, a
zero-width or other format character and a line or paragraph separator.

## The completion policy

`internal/card/definition/completion.txt` classifies every kind that
`internal/hygiene/kinds.txt` declares as `pr` or `non-pr`, under a version number.
`read`, `probe`, `text`, `tone` and `report` are `non-pr`: a card of such a kind has
no code to land. Every other declared kind is `pr`: a card of that kind ends by a
landing. The classification is its own policy, not read from the `gated` or
`ungated` column of `kinds.txt`. A kind that is unknown, or declared but
unclassified, is refused, and a test fails when `kinds.txt` declares a kind the
policy does not classify. The version is tied to the content: a test holds the
SHA-256 of `completion.txt` beside the version, so a classification cannot change
without the version changing.

## The array

At most 127 files and 8 MiB in all (the table takes one of its 128 changed entries
for the card operation record). `Validate` refuses a card ID that appears twice
(naming both files), a card that depends on itself, a dependency cycle inside the
array (naming the cycle) and more than 1,024 distinct dependencies outside the
array (each is one guard-only table entry; 127 cards of 8 dependencies name at
most 1,016, so the bound holds by construction). A dependency on an ID that is not
in the array is not refused: it is reported as external, and the caller guards it
against what is already admitted. An array that draws any refusal is refused
whole, so a caller never admits a prefix.

## Pinning

The pin reads the committed blobs of the paths at one full commit and never a
working file, an index or a ref, and it never reaches the network.

- At most five git invocations, whatever the size of the array, each under a
  deadline (30 seconds unless the caller's context ends sooner).
- Git runs with the global and system configuration off, no `GIT_` variable
  inherited, lazy fetching off (`GIT_NO_LAZY_FETCH=1`) and every transport refused.
  In a partial clone a blob that was not fetched is the refusal `missing-object`,
  with the next action to fetch it, and it is still missing afterwards.
- The commit is a full lower-case object id (40 or 64 hex); abbreviations, ref
  names and objects that are not commits are refused, as is a commit the
  repository does not hold. A commit that exists is accepted whether or not any
  ref reaches it: what it names is what is pinned.
- A path is refused when it is empty, over 512 bytes, invalid UTF-8, absolute, has
  a drive letter, a backslash, an empty, `.`, `..` or `.git` segment (in any case),
  starts with a dash, holds a control character, or repeats. A path missing at the
  commit, a symlink, a directory or a submodule, or a blob over 256 KiB is refused
  by path.
- The directory is the root of a repository (or of a linked worktree, or a bare
  repository). Its `.git` directory, a subdirectory, a parent's repository, a
  directory that is not a repository and a missing directory are refused.
- The repository identity is `host[:port]/path` from the origin URL: the scheme,
  the user and credentials, a trailing slash and `.git` are dropped, the host is
  lower case and the default ports 22 and 443 are dropped. The path keeps its case:
  `Owner/Repo` and `owner/repo` are two identities. A repository with no origin, or
  an origin that gives no identity, is refused unless the caller supplies one with
  `WithIdentity`. A refusal about an origin, or about a supplied identity, names the
  rule it broke and never quotes the value, which can carry a credential.
- Git's own error text is never copied into a refusal: a known failure maps to a
  cause, any other is `git-failed` with the exit status.

## The admission record

A record holds the card ID, the definition digest (`digest`, the SHA-256 of the
file), the brief digest, the Git object id, the commit, the repository identity, the
path, the kind, the completion class and the policy version, the dependencies
(sorted), the entry, the tier, the schema, the title, the paths (sorted), the
test, the doors and the contract's `base_commit`. It holds no brief, no `DONE-WHEN`
and no `PROBES`. Its shared fields (`id`, `digest`, `object_id`, `commit`,
`repository`, `path`, `kind`, `depends_on`, `entry`, `title`) are those of the
request package's admission under the same names and grammar; a request adds the
stream row and the review policy's identity.

`EncodeAdmissions` writes each record by the card layer's one encoder
(`card.Encode`): one JSON object, keys in byte order, strings and arrays of
strings only, no whitespace between tokens, no HTML escaping, control characters
(and U+2028, U+2029, DEL) as `\u00xx` escapes, arrays sorted, an empty optional
field left out. The same record always encodes to the same bytes, and
`DigestAdmissions` is the SHA-256 of that encoding. A record is at most 6 KiB, which
fits a table field value of 64 KiB; the largest record the bounds allow is asserted
in the tests.

## Refusals

Every refusal is the card layer's one shape: operation (`parse`, `validate`, `pin`
or `admit`), file or path, line, key, cause, what was found (quoted and bounded),
the limit, and the next action. It renders on one line, and text that came from the
input is quoted and at most 48 bytes, so a newline, a NUL or a 100,000-byte key can
neither forge nor flood a line:

```
refused parse file=cards/x.md line=5 field=KIND: invalid-kind; found "nonsense"; limit KIND is one of internal/hygiene/kinds.txt: ...; next: use a kind internal/hygiene/kinds.txt declares
```

A call keeps at most 64 refusals and counts the rest.

## Adoption

`SCHEMA: v2` exists only in worker RESULT files, never in a card, so this profile
is a new card format: no card in the repository parses under it. The three swarm
cards under `cmd/nova-swarm/testdata/cards` carry dispatch keys (`LEGS`, `MODE`,
`TURNS`, `DEADLINE`, `SOURCE`, `BASE`, `SPEC`, `ROUTE`) that belong to a layer this
profile does not specify, and lack profile keys. A test runs `Parse` over every
file of the repository whose first line starts with `RESULT` and records which parse
and the causes for those that do not in `testdata/adoption.golden`; it makes
nothing parse. How the two formats meet is a decision for the owner: a converter as
a separate tool, an extension namespace whose bytes are covered by the digest but
never enter the record, or dispatch keys joining the profile when the dispatch layer
is specified.
