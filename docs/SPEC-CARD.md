# SPEC-CARD: the card layer

A card is a unit of work held as plain data. Its logic is a set of functions over
arrays of cards; a single card is an array of one, and no function takes one card
alone. Nothing in the layer executes or interprets a card's prose.

This file states the parts of the layer that exist. The code is
`internal/card/definition`.

## Definitions

A definition is a card as it is committed: a UTF-8 text file in Git. The package
reads a set of definition files, checks the set, pins each file to a committed
blob, and produces one canonical admission record per card. It has no store, no
network and no verbs.

### The file

```
RESULT: <id> sha=<hex> <note>
SCHEMA: v2
ID: <id>
TITLE: <one line>
KIND: <kind>
PATHS: <glob>[, <glob>...] | none
DEPENDS-ON: <id>[, <id>...] | -
TIER: frontier | pro | flash
TEST: [-tags <tags>] <package> <TestName> | none <why>
DONE-WHEN: <one line>
DOORS: <one line> | none
PROBES: <one line> | none

<the brief: every byte from here to the end of the file>
```

- Line 1 is the contract line. It is `RESULT: <id> sha=<hex>`, the form the swarm
  cards carry; the colon-less `RESULT <id> sha=<hex>` is read too. The `sha=`
  token is optional; when present it is 7 to 40 hexadecimal digits. Any text after
  it is the contract note, kept and never interpreted.
- The header starts on line 2. It is one `KEY: value` per line, the key at column
  zero. Blank lines may occur inside it. The first nonblank line that is not a
  `KEY: value` line ends the header, and the brief starts on that line. A brief
  therefore does not begin with a line of the form `word: text`; a heading is a
  safe first line.
- `ENTRY: <path>` may follow `ID`. It is an attribute, never the card's identity.
- A known key at column zero below the header, outside a fenced block, is
  stranded and refused. Indented text, quoted text (`> `) and fenced blocks
  (three or more backticks or tildes) declare nothing.

The file is refused when it is not valid UTF-8, starts with a byte-order mark,
holds a carriage return (CRLF or a bare CR) or a NUL byte, is empty, has no
brief, or is over 256 KiB. A carriage return is refused rather than trimmed: the
pinned bytes are the identity of a card, so one card is one byte sequence.

### Values

| Field | Rule |
| --- | --- |
| `SCHEMA` | `v2`. `v3` is refused by name; every other value is unsupported. |
| `ID` | Nonempty ASCII letters, digits, underscore and hyphen, at most 128 bytes, not the lone `-`. It equals the ID on the contract line. |
| `ENTRY` | Optional. At most 512 bytes, no comma, no control character, no padding. |
| `TITLE`, `DONE-WHEN`, `DOORS`, `PROBES` | One line, nonempty, at most 2048 bytes, no control character. `DOORS` and `PROBES` say `none` for none. |
| `KIND` | A name in `internal/hygiene/kinds.txt` that the completion policy classifies. |
| `PATHS` | `none`, or at most eight globs by the grammar of `hygiene.ValidatePaths`, none repeated. |
| `DEPENDS-ON` | `-`, or at most 64 card IDs, none repeated. It names card IDs, not Work paths. |
| `TIER` | A route of `internal/cardhdr`: `frontier`, `pro` or `flash`. |
| `TEST` | The grammar of `cardhdr.ParseTest`, with a Test function name and a package that passes the `PATHS` rule. `none <why>` is allowed only for a kind that completes without a pull request. |

A key repeated, a key outside the profile, and a case or spacing variant of a
known key (`Kind:`, `KIND :`, `DEPENDS_ON:`) are refused by line and key. A
required key that is missing is refused by key.

### The completion policy

`internal/card/definition/completion.txt` classifies every kind that
`internal/hygiene/kinds.txt` declares as `pr` or `non-pr`, under a version number.
`read`, `probe`, `text`, `tone` and `report` are `non-pr`: a card of such a kind
completes by a recorded outcome. Every other declared kind is `pr`: a card of that
kind completes by a verified landing. The classification is its own policy; it is
not read from the `gated` or `ungated` column of `kinds.txt`. A kind that is
unknown, or declared but unclassified, is refused, and a test fails when
`kinds.txt` declares a kind the policy does not classify.

### The array

`Parse` reads an array of files, at most 128 and 8 MiB in all, and `Validate`
checks the array. Both return no results beside a refusal: an array that draws any
refusal is refused whole, so a caller never admits a prefix.

`Validate` refuses a card ID that appears twice (naming both files), a card that
depends on itself, and a dependency cycle inside the array (naming the cycle). A
dependency on an ID that is not in the array is not refused: it is listed in the
report as external, and the caller guards it against what is already admitted.

### Pinning

`Pin` reads the committed blobs of an array of repository-relative paths at one
full commit of a Git repository, and returns for each the repository identity, the
full commit, the path, the Git object id, the SHA-256 of the bytes and the bytes.
It reads no working file, no index and no ref, and runs no fetch, no publication
and no network call.

- One `Pin` runs at most five git invocations, whatever the size of the array,
  each under a deadline (30 seconds unless the caller's context ends sooner).
- Git runs with the global and system configuration off and no `GIT_` variable
  inherited.
- The commit is a full lower-case object id; abbreviations, ref names and objects
  that are not commits (a tree, a blob, an annotated tag) are refused, as is a
  commit the repository does not hold.
- A path is refused when it is empty, over 1024 bytes, absolute, climbs out of the
  repository, is not in canonical form, or repeats. A path that is missing at the commit, a
  symlink, a directory or a submodule, or a blob over 256 KiB is refused by path.
- The directory is the root of a repository; a subdirectory of one, a directory
  that is not a repository and a missing directory are refused.
- The repository identity is the origin URL as `host/path`: the scheme, the user
  and any credentials, a trailing slash and a trailing `.git` are removed, and the
  host is lower case. A repository with no origin, or an origin that is a local
  path, is refused unless the caller supplies an identity.

### The admission record

`Admissions` joins definitions to their pins, position for position, and refuses a
definition that was not read from its pin's bytes. A record holds the card ID,
the definition digest (SHA-256 of the file), the brief digest, the Git object id,
the commit, the repository identity, the path, the kind, the completion class and
policy version, the dependencies, the entry, the tier, the schema, the title, the
paths, the test and the doors. It holds no brief, no `DONE-WHEN` and no `PROBES`.

`EncodeAdmissions` writes each record as one JSON object: keys in byte order, only
strings and arrays of strings, no whitespace between tokens, and no HTML escaping.
The same record always encodes to the same bytes, and `DigestAdmissions` is the
SHA-256 of that encoding.

### Refusals

Every refusal is a value with the operation, the file or path, the line, the key,
the cause, what was found, the other files involved, and the next action. It
renders on one line:

```
REFUSED parse file=cards/x.md line=5 key=KIND cause=invalid-kind found="..." next="..."
```
