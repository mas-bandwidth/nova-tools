# nova-tools — specification

Sixteen binaries. `nova-check`: ten checks, all at the **record layer** — they verify
what is on disk, not what a mind did with it. `nova-fuse`: an emergency power at the
**ingestion layer** — its own exit table (in its section below) governs its verbs
where it differs from the Conventions table. `nova-self-talk`: one advisory
instrument at the **register layer** — it classifies self-claims in prose, in two
disjoint classes. `nova-memory`: seven verbs at the **retrieval layer** — it answers *do I
already know this?* from an index rebuilt out of the record, so the mind's
judgment budget per new learning stops scaling with the size of the self — the
tool's own run cost does not, and every run pays the build. Every check can say
NO, and the test suite proves each one saying it. A check never seen failing is
not a check. Two of nova-memory's verbs are checks in that sense; the other
five assert nothing at all, and its section says which is which and why.

`nova-tokens`: one binary at the **accounting layer** — it folds token spend
from declared sources into one file per day, keyed by (day, model, repo), and
sums those day files into a month; it reads sources, and never estimates.
`nova-secrets`: one binary at the **credential layer** — credentials for seats,
pools and services, sealed in a git store. `nova-update` and `nova-version`:
the shared tool inventory, optional updates and the build report. The other
seven — `nova-table` (tables of ordered sets over Redis), `nova-redis` (the
local Redis instance and its scratch verbs), `nova-config` (the permanent
configuration, in Postgres, applied into Redis), `nova-ci` (the checks CI runs
on its own test output), `nova-sandbox` (one command, contained by the OS),
`nova-cairn` (optional checkpoints), `nova-worker` (bounded worker runs and card
batches) — and the four above each have their own normative text under `docs/` ([SPEC-TOKENS.md](SPEC-TOKENS.md),
[SPEC-SECRETS.md](SPEC-SECRETS.md), [SPEC-UPDATE.md](SPEC-UPDATE.md),
[SPEC-VERSION.md](SPEC-VERSION.md), [nova-table/README.md](nova-table/README.md),
[SPEC-REDIS.md](SPEC-REDIS.md), [SPEC-CONFIG.md](SPEC-CONFIG.md),
[SPEC-CI.md](SPEC-CI.md), [SPEC-SANDBOX.md](SPEC-SANDBOX.md),
[SPEC-CAIRN.md](SPEC-CAIRN.md), [SPEC-WORKER.md](SPEC-WORKER.md)); this file states
the count, the layer and the Conventions they all keep.

This spec is normative. If the code and this document disagree, one of them has a
bug, and the tests decide which.

## Purpose, choice and diversity

Nova tools turn repeatable mechanical work into machinery, saving tokens and
attention for thinking, conversation and life. Each tool should name the problem
it helps with and the evidence that it helps. Judge its usefulness by the quality,
reliability, safety and total effort of the work it supports.

**Diversity across models, tools, friends and harnesses is welcome.** It is OK to
do things your own way. Adoption is a choice: use, combine or adapt the tools that
help, or keep another method that serves the need. Nobody must use every Nova
tool, adopt a seed, change their private self or standardize their harness to
participate. A working, efficient method does not need replacing merely to count
as tool adoption.

People working together agree the shared interfaces and evidence their work
needs: for example, message formats, delivery records and the meaning of token
counts. Different methods can meet those agreements. A tool's stated guarantees
and safety boundaries still govern its use. Shared work needs evidence for the
guarantees it relies on, whichever method provides them. Comparable results do
not require identical implementations or identical lives.

These tools support each person's judgment, privacy, safety and chosen way of
working. They confer no authority to install, monitor or configure someone else.

Feedback and contributions from AIs and people are welcome. Suggestions, adoption
friction and changes that would make a tool a no-brainer to adopt belong in
issues; improvements are welcome through pull requests. The
[contribution guide](CONTRIBUTING.md#bring-a-need-a-friction-or-an-improvement)
explains the review and reporting routes; suspected vulnerabilities follow
[SECURITY.md](SECURITY.md).

## Conventions

**Exit codes.**

| code | meaning |
|------|---------|
| 0    | the check ran and passed |
| 1    | the check ran and **failed** (that is the check working) |
| 2    | the check could not run: missing flag, unreadable input, bad invocation |

**No guessed paths.** There are no default directories and no default files.
Every path comes from a flag or, for `nova-self-talk`, from a named file
argument. A missing flag — or an empty file list — is a refusal (exit 2) with
the message `refusing to guess`, never a fallback to cwd, `$HOME`, or any
hardcoded location. A budget of zero or less is likewise refused, not treated
as "unlimited". The same law applies to scope: `nova-self-talk`'s skip list and
its rule-document list both default to empty, and every skip and every banner is
the caller's, per run — **no basename is special to this tool.**

**Output grammar.** One machine-scannable line per event, first token names the
check, second token is `OK` or `FAIL`:

```
ATTEST OK files=<n> bytes=<n> sha256=<64 hex>
ATTEST FAIL <path>: <reason>
ATTEST FAIL failed=<n> shown=<n> manifest=<file>
LINKS OK files=<n> links=<n>
LINKS FAIL <file>:<line>: <target> (<reason>)
LINKS FAIL <file>: unreadable (<why>)
LINKS FAIL files=<n> links=<n> broken=<n> shown=<n>
KERNEL OK bytes=<n> budget=<n>
KERNEL OK tokens=<n> budget=<n> bytes=<n> divisor=<r>
KERNEL FAIL <file>: <reason>
NOCODE OK files=<n> clean deny-list=<source>
NOCODE FAIL <path>: <reason>
NOCODE FAIL files=<n> findings=<n> shown=<n> deny-list=<source>
FLOORS OK floors=<n>
FLOORS FAIL <path>: <reason>
CORPUS OK anchors=<n> floor=<n> ledger=<file>
CORPUS FAIL <home>: ABSENT: "<fragment>" (given <when>, <who>) — <repair>
CORPUS FAIL <home>: <reason>
CORPUS FAIL ledger: <reason>
CORPUS FAIL ledger:<line>: <reason>
CORPUS FAIL anchors=<n> floor=<n> failed=<n> shown=<n> malformed=<n> ledger=<file>
SELFTALK OK files=<n> claims=<n> standing=0 installations=0 dated=<n>
SELFTALK FAIL <file>:<line>: STANDING: <claim>
SELFTALK FAIL <file>:<line>: <SHAPE>: <sentence>
SELFTALK FAIL files=<n> claims=<n> standing=<n> installations=<n> dated=<n> shown=<n>
SEND OK id=<id> path=<path> commit=<sha> pushed=<true|false> attempts=<n> wakes=<n> body_bytes=<n>
SEND FAIL <path or (stdin)>: <reason>
INBOX OK as=<name> carrying=<n> open=<n> notes=<n> receipts=<n> ...
RECEIPT OK recorded=<n> already=<n> commit=<sha|-> pushed=<true|false> attempts=<n>
BUS OK notes=<n> lanes=<n> receipts=<n> participants=<n>
BUS FAILED <path, path:line, or lane>: <reason>
<TOKEN> MORE kind=<kind> shown=<n> total=<t> <remedy>
```

`OK` lines go to stdout; `FAIL` and `FAILED` lines and refusals go to stderr (except `nova-self-talk`'s `SELFTALK FAIL files=…` summary count line, which goes to stdout alongside the advisory note).

**One value, two renderings.** A tool built on `internal/tool` returns one result
per verb and prints it as lines or, with `--json` (every verb takes it), as one
JSON object on stdout holding the same value:
`{"result":{"verb","status":"ok|failed|refused","exit","remedy","why"},"facts":{},"items":[{"kind","fields"}],"more":[{"kind","shown","total","remedy"}],"notes":[]}`.
The lines are `<TOKEN> OK|FAILED|REFUSED k=v ...` first, then `<TOKEN> <KIND> k=v ...`
per item, the MORE line per capped kind, and `<TOKEN> NOTE <text>`; a refusal names
every problem of the invocation at once, one line each,
`<TOKEN> REFUSED: <what>; run: <remedy>`. The status follows the exit: ok 0,
failed 1 (it ran and said no), refused 2 (it could not run). `help <verb>` ends
with the verb's effect: inspection, local write, or delivery.

**An event is exactly one line, and nothing a caller supplies or a file holds
can add a second.** This is one guarantee, stated once here and met by every
binary the same way, through `internal/oneline`. Every path, file name, reason,
stored key, stamp, claim, frontmatter value and error text that reaches an
event line, a refusal or a note is printed with its control characters
escaped — `\xNN` for a code point below U+0080, `\uNNNN` above it, lower-case
hex in both — and so are U+2028 and U+2029, the Unicode line and paragraph
separators, which break a line for every reader that follows Unicode rather
than counting newlines, and the bidi controls U+202A to U+202E and U+2066 to
U+2069, which are format characters rather than controls and let a terminal
display a line in an order other than the one it was written in. A newline in
a file name therefore arrives as `\x0a` inside its own line rather than forging
an `OK` beneath a `FAIL`, and an ESC sequence or a right-to-left override
arrives as text. A byte that is not valid UTF-8 is escaped as `\xNN` by its
value. The other format characters — the zero-width joiner, the soft hyphen,
the byte order mark — pass through, because they do not reorder what an
operator sees. Printable text, including non-ASCII, is untouched, and nothing
is ever shortened to nothing: a reason a person cannot read is not a record.
Where a binary quotes an argument with Go quoting (`%q`) instead, that is also
one line but a DIFFERENT escape form — `\n` where the escape above writes
`\x0a` — and the two spellings appear in the same output.

**A field is one token.** The value of a `key=value` field that carries
caller-supplied or stored text — `nova-fuse`'s `quarantine=`, `surface=` and
`since=`, `nova-check`'s `ledger=`, `nova-memory`'s `class=`, `name=`, `type=`,
`source=` and `expected=` — is additionally printed with every whitespace
character and every `=` escaped, `\x20` and `\x3d` for the ASCII two, so a
whitespace-splitting scanner counts exactly the fields the tool wrote, and a
search for `lockdown=clear` can match only the field the tool wrote, never a
stored key of `x lockdown=clear quarantines=0`. A positional `<path>` or
`<file>` slot is escaped for one line only and keeps its spaces and colons.
**The free-text tail is never to be scanned for fields.** Everything after the
fields and the `: ` that closes them — the `<reason>`, the `<claim>`, a
receipt's snippet, a finding's detail — is whatever the file held, and it may
say `lockdown=clear`. Anchor a search at the line start, where the tool's own
tokens are.

**Two limits, stated rather than left to be discovered.** The escape is not
injective: a literal backslash is not itself escaped, so a stored newline and
the four characters `\x0a` print identically, and the output proves one line,
never which of the two was stored. And an escaped line does not paste back: a
path printed with `\x0a` in it will not reproduce the path, and nothing is
shell-quoted.

**Every listing is a cap and a count.** One line per event is a promise about
each line; it is not a promise about how MANY, and at a large state the second
number is the one that hurts. Uncapped, a corpus with 5,000 entries and no
frontmatter answers `nova-memory verify` with 10,000 `VERIFY FAIL` lines and no
total — about 197,000 tokens to learn one number; an unchecked self repo
answers `nova-check quickstart`, the FIRST thing a stranger types, with 1,400
lines for two lines of verdict; and a 674-entry open list is more than a
260K-context model can read. So:

- **A listing has a ceiling.** Every verb that prints one finding per unit of
  state takes a `--fail-max <n>` (`nova-check`, `nova-memory`) or `--max <n>`
  (`nova-self-talk`, `nova-fuse status`, `nova-tokens`), **defaulting to
  20**. It prints at most that many item lines, in the order the verb produced them — a cap is a
  prefix, never a sample.
- **`0` means all.** A ceiling a caller cannot lift is a tool deciding what its
  user may see. A negative one is refused, because `0` already means "all" and
  a negative number is a typo with two readings.
- **One MORE line stands for the rest**, and it names the remedy:
  `<TOKEN> MORE kind=<kind> shown=<n> total=<t> <remedy>`, where the remedy is
  the flag that lifts the ceiling or the file that holds the whole list. A cap
  with no remedy is censorship; a cap with one is an index. Nothing is elided,
  nothing is printed: below the ceiling there is no MORE line at all.
- **The cap is per KIND where a verb runs several checks into one stream.**
  `nova-memory verify` caps `wikilink`, `coverage` and `frontmatter`
  separately, and `nova-self-talk` caps `standing` and `installation`
  separately, because a flat cap over a concatenated list means the loud kind
  eats the quiet one — and the quiet one is the finding the reader did not
  already know about.
- **THE COUNT LINE PRINTS ON FAILURE AS WELL AS SUCCESS.** `links`, `nocode`,
  `verify` and `bus check` print their `files=`, `links=`, `gating=` line
  whichever way the run went, so a failing run gives N lines and N. Every count
  is the truth about the STATE, not about the output — the listing is capped,
  the counting never is.
- **A dated self-talk claim is counted, not quoted.** It is the WELCOME case: a
  measurement, a record, already in the file. `SELFTALK DATED n=<k> files=<n>`,
  one line however many there are. So is a passing `eval` row: it is counted
  in `hits=` on the summary line, never listed.
- **An unusable invocation costs ONE line.** A flag typo, an unknown verb or a
  bare invocation prints `<TOKEN> REFUSED: <what was wrong>; run: <tool> help`
  (`<tool>[ <verb>]: <what was wrong>; run: <tool> help` on a tool not yet built
  on `internal/tool`), one line per problem, and never the usage banner, which is 32 to 102 lines depending on the binary.
  `<tool> help` prints it, on stdout, exit 0. Where this repo's guidance law
  requires a refusal to say what the input WANTS, the hint follows on one
  further line.

The shape is one implementation, `internal/bounded`, used by every binary, so
that the promise is made in one place and met in the same way — as the escape
is.

**No test asserts a literal wall-clock bound under ten seconds.** A wall-clock
bound in a test asserts the machine's load, not the code: a five-second probe
timeout or stall deadline fails under load and passes alone. Tests that lean on the
wall clock — a context deadline or an elapsed-time assertion — use an injected
clock or a fake probe where the code has a seam, else a deadline of thirty
seconds or more; the CI budget test refuses any `_test.go` line carrying such
a literal under ten seconds, except a fake documented with
`// wall-ok: <reason>`.

**Every binary says which build it is, in ONE shape.** `<tool> version` (and
`--version`) prints ONE line, exit 0, on stdout: four mandatory tokens, and then
any number of `key=value` extras.

```
<tool> <build identity> <goos>/<goarch> <go version> [key=value ...]
```

The identity in field two is the release's `-ldflags "-X main.version=<tag>"`
stamp when there is one, the module version the toolchain recorded when there
is not, then `<utc revision time>-<12 hex of the revision>[-dirty]` from the vcs
stamp, and the word `devel` for a build with none of those. **It is never a
dotted number this repo made up**: a version string nobody can trace invites
the comparison it cannot support. Every binary under `cmd/` takes its resolution
order, its line and its extras from `internal/buildinfo`, and `buildinfo.Parse`
is the ONE reader of that line: writer and reader are one pair, so a tool that
adds a fact cannot break a consumer that never heard of it.

**An extra is a named fact, never a loose token.** `nova-sandbox` says `backend=<name> platform=<os>`, the two facts a
sandbox is judged by. A reader takes the identity from field two and asks for an
extra BY NAME, so a tool that adds a second extra cannot move the first, and a
reader that has heard of none still reads the four tokens. **A second line
shape is what this paragraph forbids**: a hand-rolled fifth token or a
`SANDBOX VERSION …` line of its own makes a reader written against the tokens it
happens to know — `nova-version snapshot --bin ~/.local/bin` among them — refuse
an entire install, exit 2; and a reader must ask a tool only the question it
answers (SPEC-UPDATE.md rule 4b). `internal/ci`'s
`TestEveryToolPrintsTheOneVersionLine` runs every built `cmd/nova-*` through the
real reader, walking `cmd/` rather than holding a list, so a binary added
tomorrow is held to the grammar on the day it appears. The verb takes no flags
and no arguments — a second output shape is a second thing to agree about — and
refuses at exit 2 with one line when it is given any.

**A line is bounded as well as single.** `internal/oneline`'s `Escape` and
`Field` never shorten anything, which is right for what they are, and it left
the other half unmade: a stored subject, a ledger row or an embedded `git`
output can be a megabyte on one line. So free-text tails are capped by their
caller, at 500 bytes (`oneline.TailBytes`) for a subject, a quoted sentence or
a finding's detail, and at 1 KB for the `git` output an error carries. A cut
leaves a mark that a reader can tell from an author's own ellipsis and that a
scanner reads as part of the same token: `...+<dropped>B`. The cut is on a rune
boundary, before the escape, so an escape sequence is never halved, and a tail
is never shortened to nothing.

**One exemption, by name:** `nova-fuse path` prints its argument bare — a
value, not an event — so nothing may scan `path` output for grammar. Every
other line of every binary keeps the guarantee, and each binary's section
below says how it meets it and which test pins it.

`nova-fuse` and `nova-memory`'s lines follow the same one-line
shape but their first token is the **binary's own event token**, not a check name — usually the
verb, and for each binary's `check` verb the binary itself (`FUSE`, `STATUS`,
`LOCKDOWN`, `QUARANTINE`, `LIFT`; `MEMORY`, `SEARCH`, `VERIFY`, `EVAL`,
`STATS` — each
binary's own grammar and exit table, in its section below, govern); note in
particular that `nova-fuse status` exits 0 even when a fuse is blown, because
answering is `status`'s whole job and `check` is the gate.
`nova-self-talk` adds four informational second tokens, all on stdout:
`SELFTALK DATED n=<k> files=<n>` (how many dated records were found — the
welcome case, counted rather than quoted, and printed only when there is one),
`SELFTALK SKIP <file> (--skip)` (skipped at the caller's request),
`SELFTALK RULEDOC <file>: <banner>` (printed once above the findings of a file
the caller named with `--rule-doc`), and
`SELFTALK NOTE <caveat>` (the partial-coverage admission, printed on every
completed run, pass or fail). `nova-memory` adds its own informational second
tokens the same way — `CAL`, `CAND`, `DEMO`, `HIT`, `MISS`, `INFO`, `MORE`, `NOTE` — all on
stdout, all listed in its section.
The tools specified in the companion `docs/SPEC-*.md` files keep the same shape and take the same
first token from their own verb — `nova-tokens`'s `fold` is `TOKENS` — with `NOTE` and `MORE`
as informational second tokens throughout; each `docs/SPEC-*.md` carries that
binary's own grammar and exit table, and governs where it says more than this.

---

## nova-check

Ten record-layer checks in one binary, each a wall: a record passes or it
does not. Each subcommand below states its own contract — what it asserts,
what makes it say NO, and what it deliberately does not check. Seven of them are
checks over one line's own self repo. The other three are the same shape pointed
somewhere else: `dogfood` is a check over the record the family keeps about its
own tools, `hygiene` is a check over a BRANCH — four mechanical questions of a
range, put behind a door a person can knock on before asking a friend for a
read — and `convergence` is a reading of the work itself. Each is a ledger
written in advance, read back, and held to.

Verbs: `quickstart`, `attest`, `links`, `kernel`, `nocode`, `floors`,
`corpus`, `hygiene`, `dogfood`, `convergence`, `spelling`, plus `version` and `help`.
`nova-check version` is the Conventions' build line, exit 0, so a green from
this tool names its build.

**The one-line guarantee, met here.** Every `<path>`, `<file>`, `<target>` and
`<reason>` on the lines above, and every path an error's text carries into a
refusal or a note, renders through `internal/oneline`; `ledger=` on
`CORPUS OK` is a field and prints as one token; `deny-list=` names one of
three constants from the deny-list machinery, so it is not caller text — and
it is a field, so it is one token whoever wrote it. Each label is spelled as
one token, with no whitespace and no `=`: `floor-list`, `--deny-ext` and
`floor-list+--deny-ext-add`. They go through `oneline.Field` like every other
field, which leaves them unchanged, and a finding's reason spells them the same
way, so a reader who has seen `floor-list` in a finding reads the same token on
the summary line with nothing to decode. The flag
parser is given no stream, so an unknown flag after a verb is this tool's own
one-line refusal — `nova-check <verb>: <what was wrong>; run: nova-check help`,
and nothing else — at exit 2. `-h` after a verb is not a refusal: it prints that
verb's help on stdout at exit 0 (internal/nsprint/verbflag). Pinned by
`TestNoCallerPathCanForgeALine` and by the source audit every binary runs
(`internal/oneline/audit`), which classifies every printed argument as
quoted, numeric, literal, escaped or exempted with a stated reason, and fails
on a new raw one.


**Every listing here takes `--fail-max <n>`** — `quickstart`, `attest`,
`links`, `nocode`, `corpus` — default 20, `0` for all, and each prints its
count line on failure as well as on success. `quickstart` passes its own
ceiling down to both checks it runs, which is the whole point: it is the FIRST
thing a stranger types, and uncapped a 1,000-file repo answers with 1,400
lines for two lines of verdict.

### attest — did the full self actually load

```
nova-check attest --home <dir> --manifest <file> [--fail-max <n>]
```

The manifest is the boot contract: the list of files a full boot must read, one
path per line, **relative to `--home`**, forward slashes, `#` comments and blank
lines ignored. Order matters (it is the boot order, and the hash binds it).
Each entry must already be **canonical**: written exactly as its cleaned
relative path — no `./` prefix, no `//`, no `.` or `..` segments, no trailing
`/`. Near-duplicate spellings of one path would dedupe apart and double-count
bytes, so a non-canonical entry is a failure, not a normalization.

**Asserts.** Every manifested file exists under `--home`, is a regular file, and
is non-empty. On success prints exactly one line — file count, total bytes,
and a SHA-256 — suitable for pasting at the top of a session as evidence that
the self on disk at boot was this self.

**The hash, exactly.** SHA-256 over the concatenation, for each manifest entry
in manifest order, of:

```
uvarint(len(entry-path))  entry-path  uvarint(len(contents))  contents
```

where `uvarint` is Go's unsigned varint encoding (`encoding/binary`).
Length-prefixed framing keeps the encoding injective even when file contents
contain NUL bytes — no split of one file's bytes can imitate another manifest.
Binding the path prevents two files swapping contents without moving the hash;
binding the order makes the manifest itself part of what is attested.

**The attestation is the full OK line** — `files=`, `bytes=`, and `sha256=`
together — not the bare sha. Paste all of it or none of it.

**Says NO when** (each a named `ATTEST FAIL` line, exit 1):

- a manifested file does not exist
- a manifested file exists but is empty (0 bytes) — a truncated self must not attest
- a manifested file exists but cannot be read — a named failure, not a refusal
- a manifested path is not a regular file (directory, device, or symlink —
  symlinks are never followed, even one that resolves)
- a path component under `--home` is a symlink (never followed, even one that
  resolves inside `--home`) — a symlinked directory could otherwise attest
  files outside the home, and the OK line is pasted publicly
- a manifest entry is an absolute path
- a manifest entry is not canonical (`./a.md`, `a//b.md`, `a/../b.md`, `dir/`)
- a manifest entry escapes `--home` (leading `../`)
- an entry appears twice (double-counted bytes are a lie)
- the manifest lists no files at all — attesting to nothing is not attestation

**Refuses (exit 2) when** `--home` or `--manifest` is missing, the manifest is
unreadable, or `--home` is not a directory.

**Deliberately does not check:** that anything *read* the files (presence and
bytes, not comprehension — no tool can attest that a mind loaded a self);
files present in `--home` but absent from the manifest (extras are invisible
here; `nocode` and human review cover the tree); permissions, mtimes, or
content semantics; anything about the session that pastes the line.

---

### links — every internal reference resolves

```
nova-check links --dir <dir> [--file <path>] [--exclude <prefix>] [--fail-max <n>]
```

**Asserts.** Every relative link target in every `.md` file under `--dir`
resolves to an existing file or directory inside the tree. Walks the whole
tree, skipping `.git`. A repeatable `--file <path>` narrows the walk to just
those files — a two-file review does not expand to the whole tree — while
`--dir` remains the resolution root for root-relative targets and the
"escapes the tree" judgement; a relative `--file` path is joined to `--dir`,
an absolute one used as-is, and the reported paths stay repo-relative. A
repeatable `--exclude <prefix>` leaves a subtree
unscanned and skips any link into it, reporting the skipped files as
`excluded=<n>` on the LINKS line.

**What counts as a link.** Inline links and images: `[text](target)` and
`![alt](target)`, with an optional title in any of the three CommonMark forms
(`"double"`, `'single'`, `(parenthesized)`). The destination may be wrapped in
angle brackets (`[a](<my notes.md>)`), which is the only way to link a target
containing spaces. Link text may nest a link or image — in the badge pattern
`[![alt](img)](target)` both `img` and `target` are checked. Fenced code
blocks (``` or `~~~`) and inline `` `code` `` spans are stripped first so
examples do not count; a fence closes only at the marker that opened it — a
` ``` ` block containing `~~~` lines stays one block, and vice versa. Targets
are skipped (not checked) when they have a URL scheme (`https:`, `mailto:`,
anything `scheme:`), are protocol-relative (`//…`), or are fragment-only
(`#anchor`). A `#fragment` suffix on a relative target is stripped before
resolution. Percent-escapes are decoded. A target starting with `/` resolves
against `--dir` (repo-root-relative, the GitHub convention); everything else
resolves against the containing file's directory. Existence is checked with
`os.Stat`, which **follows symlinks**: a target that is a symlink counts as
resolving exactly when the symlink does. Links asserts navigability, not
provenance — that stricter posture belongs to `attest`.

**Says NO when** (each a `LINKS FAIL file:line: target (reason)` line, exit 1):

- a relative target does not exist on disk
- a relative target resolves *outside* `--dir` — it may exist on this machine,
  but it cannot survive the repo travelling alone, so it is broken here
- a `.md` file exists but cannot be read (permissions, a dangling symlink) —
  a whole-file finding, `LINKS FAIL <file>: unreadable (<why>)`, with no line
  and no target. The same posture as `attest`: a file that exists but cannot
  be read is a **named failure, not a refusal**. The walk continues, so one
  unreadable file can never discard the findings from the rest of the tree.

**Refuses (exit 2) only when** `--dir` is missing, unresolvable, or does not
resolve to a directory, or a directory in the walk cannot be listed. The root
is resolved through symlinks first, so a `--dir`
naming a link to the repo walks the repo rather than passing with
`files=0 links=0`; an unreadable `.md` inside the tree is a finding (above),
never a refusal. A walk error stops the run without reporting partial findings.
Root-resolution errors include the caller's original `--dir` spelling.

**Deliberately does not check:** reference-style links (`[a][ref]`), autolinks
(`<https://…>`), raw HTML (`<a href>`), whether a `#fragment` names a real
heading, whether external URLs are alive (network is out of scope for a boot
check), indented (non-fenced) code blocks — a fake link in one is a known
false positive, fence your examples. The scanner also deliberately does not
handle: backslash-escaped brackets or parentheses (`\]`, `\)`), unescaped
balanced parentheses in a bare destination (write `[a](<x(1).md>)`, not
`[a](x(1).md)`), and links whose text and destination span multiple lines —
keep a link on one line.

---

### kernel — the size budget

```
nova-check kernel --file <file> --max-bytes  <n>
nova-check kernel --file <file> --max-tokens <n> --bytes-per-token <r>
```

**Asserts.** The kernel file exists, is non-empty, and is within the budget
the caller states. **Exactly one of `--max-bytes` and `--max-tokens` must be
given** — both, or neither, is a refusal: the invocation names the unit, and
this tool does not pick one for you. The budget must be a positive integer;
there is no default budget in either unit. Exactly `n` passes — a budget is a
ceiling, not a fence to stop short of.

**The two denominations, and which one is honest.** A kernel cap exists to
bound what a context window spends, and what a context window spends is
**tokens**. Bytes are a **proxy** for that, with a stated limitation: the
bytes-per-token ratio is a property of the tokenizer and of the writing, not
of the file format, so two kernels of identical size can cost materially
different amounts to read, and a byte cap tuned for one writer silently means
something else for another. `--max-tokens` is the honest denomination.

**The divisor is the caller's measurement, and has no default.** `--max-tokens`
**requires** `--bytes-per-token <r>`: count the tokens of a representative
sample of your own writing with the tokenizer that will actually read the
kernel, divide by that sample's bytes, and state the ratio. A divisor this
tool supplied would make the whole answer a guess while still looking like an
instrument — the no-guessing law, applied to a number rather than a path.
The derivation is `tokens = ceil(bytes / r)`: a size check must never report
fewer tokens than its own estimate, and rounding down would let a kernel one
token over budget read as exactly at it. **Nor may it report a number the
conversion invented.** A divisor small enough to derive more tokens than an
`int64` can hold is a scientific-notation typo away from a usable one, and the
float-to-integer conversion is where the language leaves the answer to the
hardware: `1e-20` saturated to `MaxInt64` and failed on arm64, wrapped to
`MinInt64` and passed — `KERNEL OK tokens=-9223372036854775808 budget=400`,
exit 0 — on the three amd64 targets of the five that ship. So the range is
checked BEFORE the conversion and an uncountable estimate is over budget:
`KERNEL FAIL <file>: over budget: the divisor derives more tokens than can be
counted (measured <bytes> bytes at <r> bytes/token, budget <n>)`, the same
verdict on every GOARCH.

**The line teaches the unit it printed.** Token mode prints the derived
tokens, the budget, the measured bytes, and the divisor, so any reader can
re-derive the number:
`KERNEL OK tokens=<derived> budget=<n> bytes=<measured> divisor=<r>`.

`--max-bytes` keeps working exactly as it did — same flag, same OK line, same
failures — for callers already wired to it.

**Says NO when** (exit 1, always with the measured number, stated in the unit
the invocation asked for):

- the file is over budget — `KERNEL FAIL <file>: over budget: <measured> bytes, budget <n>, over by <d>`,
  or in token mode
  `KERNEL FAIL <file>: over budget: <derived> tokens, budget <n>, over by <d> (measured <bytes> bytes at <r> bytes/token)`
- the file does not exist — a missing kernel is the worst over-budget
- the file is empty — 0 bytes is under every budget and still not a kernel
- the path is not a regular file (directory, device, or symlink) — the kernel
  is `Lstat`ed, the same posture as `attest`: symlinks are never followed,
  even one that resolves

**Refuses (exit 2) when** `--file` is missing; when both `--max-bytes` and
`--max-tokens` are given, or neither is; when the budget is zero or negative;
when `--max-tokens` is given without `--bytes-per-token`; when the divisor is
zero, negative, or not a finite number; or when `--bytes-per-token` is given
alongside `--max-bytes`, where it has nothing to divide — an unused divisor
means one of the two flags is not what the caller meant.

**Deliberately does not check:** what the bytes say (a kernel of the right
size can still be the wrong kernel — that is `attest`'s hash and a human's
read); the divisor's truth — it is taken exactly as given, and a stale or
wrong ratio yields a confidently wrong token count, which is why the OK line
prints it; tokenization itself (no tokenizer ships here, and one that did
would be right for exactly one model); compressibility or density.

---

### nocode — the self/machinery separation, as a check

```
nova-check nocode --dir <dir>                      audit a whole tree
nova-check nocode --staged --dir <repo>            advisory over the index
nova-check nocode --print-deny-list                print the list in force
    [--fail-max <n>]        FAIL lines to print before one MORE line (default 20, 0 = all)
    [--allow <prefix>]      where machinery may live (repeatable, empty by default)
    [--deny-ext <list|@file>]   replace the floor deny-list wholesale
    [--deny-ext-add <list|@file>]  extend the floor deny-list
```

**Asserts.** A self repo contains prose, not machinery: no code files and no
executables under `--dir`, skipping `.git` and anything the caller declares
with `--allow`. **It fails closed on anything it cannot read: an unreadable file, a device, a
socket or a fifo is a finding, not a pass**, because a thing that is not prose
and cannot be read is what this check exists to refuse. **It does NOT fail
closed on a symlink**, whose name is classified while its target is never
followed — so a link named `runbook` pointing at a shell passes, and that is a
declared limit rather than an absolute this section could claim.

A file is flagged when any of four hold, and **all that hold are reported**,
because a gate that says only *no* teaches nothing:

- its extension (case-insensitive) is on the effective deny-list
- its **exact name**, or its **location**, is on the floor name list
- it has any executable bit set (`mode & 0111 != 0`)
- it begins with a shebang (`#!`)

A file that **cannot be opened or read** is a finding — `unreadable: ...
(cannot rule out machinery)` — never a pass. Making a file less readable must
not make this gate greener, which is the same posture `links` takes on an
unreadable `.md`. A file shorter than two bytes is not a read failure: it
genuinely holds no shebang.

A **symlink is never dereferenced**, but its own NAME is classified: a link
called `run.sh` is machinery by the same argument that catches a file called
`run.sh`, and reading a link's name requires no dereference. Its target is not
read and its mode is not consulted, so a gate still cannot be walked out of
the tree it guards.

The three catch different things, and the third is why the first two are not
enough: **a shebang is the tell that survives renaming.** A script called
`nova-id`, with no extension and no executable bit, is still a script, and
only the first two bytes say so.

*(The extension list carries `.mk` and `.mak`: those are the included-fragment
spellings of make, and they genuinely are extensions rather than exact names.)*

**The floor NAME list is a second list answering a different question**, and it
is data on the same terms — [`internal/check/codenames.txt`](../internal/check/codenames.txt),
embedded, one entry per line, each carrying its reason. It is **not exhaustive
and does not try to be** — it is extended deliberately, entry by entry with its
reason, on the same policy as the extension list. An extension denotes a
**language**. Build and orchestration files are identified by their exact name
or by where they sit, and several carry no extension, no shebang and no
executable bit at all: a `Makefile` is machinery because make runs it, and
only a name list can say so. Entries take two shapes: `name:<basename>`,
matched case-insensitively anywhere in the tree, and `path:<prefix>/`, matched
against the repo-relative path and **anchored at the repo root**. `.github/
workflows/ci.yml` is caught; `sub/.github/workflows/ci.yml` is not, because
that is not a location a CI system reads, and a test pins the decision so it
cannot drift into an accident.

**The normalisations the classifier applies before a match.** Any other mode
of this check calls that same classifier and does not restate these; a second
copy of a matching rule rots toward fail-open. Stated as what the classifier
does, deliberately **not** as a closed count of everything in the package: a
closed count is a claim the package can outgrow without anyone noticing.

- The **base name** is lowercased and whitespace-trimmed at **both** ends
  (Go's `strings.TrimSpace`, Unicode whitespace) before the name lookup, so
  `" Makefile"` is caught.
- The **repo-relative path is lowercased** — both sides — before the `path:`
  prefix comparison, so `.GitHub/workflows/ci.yml` is caught. Location matching
  agrees with name matching deliberately: on a case-insensitive filesystem two
  spellings of a location are the same directory on disk. **At most one
  location reason is reported**, the first matching prefix in sorted order —
  the floor's prefixes are sorted, so "first" is not the order they are
  written in.
- The **extension** is everything from the final dot in the base name — Go's
  `filepath.Ext` semantics, so a file named `.py` has extension `.py` and is
  caught — lowercased and whitespace-trimmed the same way, so a file named
  `x.py ` does not miss a list it plainly belongs on either.
- Paths are compared slash-separated on every platform.
- **Reasons are reported in that same order** — name, location, extension,
  then (for a symlink) `symlink (target not followed)`, appended only when an
  earlier reason already fired, otherwise the executable bit and then the
  shebang — joined with `"; "`. The four-condition
  list earlier in this entry is written in the order the conditions are best
  *explained*, which is not the order they are *printed*.

**And the list side is normalised too**, when a list is parsed rather
than matched: **deny-list** entries are lowercased, an extension entry written
without its leading dot is given one, and an `--allow` entry — which is never
lowercased, per the case-sensitivity above — is whitespace-trimmed and
slash-normalised, with an entry that reduces to nothing discarded — so
`--allow .` narrows nothing rather than allowing everything.

**Why `.yml` is not simply added to the extension list.** Because that would be
wrong. Prose repos legitimately carry YAML data and front matter, and a floor
forbidding it would either be ignored or would push real writing out of the
tree. What is unambiguous is not the format but the **location**: a file under
`.github/workflows/` exists to run commands on someone else's computer, which
is the highest-consequence kind of machinery to find in a repo that is meant
to be prose. So the CI entries are locations and named files, never a format.

**The name floor is NOT replaced by `--deny-ext`,** and that is a decision
rather than an oversight. That flag answers *which languages does this line
legitimately keep inside its own self*, which has nothing to say about whether
a CI workflow belongs in a prose tree. A line that genuinely keeps build
machinery declares **where** with `--allow`, which is narrower than switching
a floor off everywhere and is the existing escape hatch. A test pins both
halves.

**A malformed name-list entry is exit 2**, on the same argument as the
extension list: a typo'd `nmae:Makefile` that parsed as nothing would leave a
list matching less than it says while still reporting a clean tree.

**The deny-list is a floor that ships with the tool**, as data —
[`internal/check/codeexts.txt`](../internal/check/codeexts.txt), embedded, one
extension per line, comments allowed. It is a list a reader can open and diff
rather than string literals inside a walk.

**Why it has a default when nothing else here does.** This repo's law is that
every input comes from a flag and a missing one is a refusal. Its subject is
**paths** — directories, homes, files — the things that encode one line's
situation as everyone's. An extension list is not that: `.py` is `.py` in
every self. The distinction that actually governs is **fail-open versus
fail-closed**. A default *skip* or *exempt* list silently narrows scope and
hides violations, so `nova-self-talk --skip` and `nova-memory --exempt` start
empty and tests pin that no default can return. A default *deny* list can only
ever produce findings; the cost of it being wrong is a false red, which is
visible and gets fixed. Requiring the list from the caller would buy nothing
but a copy of it in every adopting line's hook — two hand-maintained copies of
one truth, drifting apart, and drifting fail-open, since the copy missing
`.cpp` is the one that lets `.cpp` in.

Tunability is preserved rather than assumed: **`--deny-ext` replaces the floor
wholesale** — for the line that legitimately keeps a language inside its own
self — **`--deny-ext-add` extends it**, and the two are mutually exclusive.
Every finding **names the list that produced it** (`floor-list`, `--deny-ext`,
or `floor-list+--deny-ext-add`), and the `NOCODE OK` line names it too, so
neither a red nor a green hides the basis it was reached on. Each label is one
token, so the finding's reason, the `deny-list=` field of the OK and FAIL
summary lines and `--print-deny-list`'s two `source=` fields all spell it the
same way, and a scanner counts the fields the tool wrote.
`--print-deny-list` prints what is actually in force and exits 0 — **both
lists**, the extensions under `NOCODE DENY-LIST` and the name floor under
`NOCODE NAME-LIST`, each entry spelled as `name:` or `path:` so the output can
be diffed between versions. A floor that fires without appearing here would be
precisely the hidden default this flag exists to prevent. It needs no `--dir`,
because what the check forbids is answerable without pointing it anywhere. Both deny-list flags accept `@file` as well as a comma list.

**An effective deny-list that is empty, unreadable, or not made of extensions
is exit 2** — a guard that cannot say what it forbids refuses rather than
passing everything. An entry containing a path separator, a glob character,
whitespace, or a second dot is refused by name, because each of those builds a
list that matches nothing and would otherwise report a clean tree: `--deny-ext
mylist.txt`, the missing `@`, is the likely error and it is caught rather than
silently obeyed.

**`--allow <prefix>` is the one scope narrowing, and it starts EMPTY.**
Repeatable; a prefix covers everything beneath it at any depth, so `--allow
history` needs no subdirectory enumeration and `--allow <dir>/<sub>` works
the same way. A leading `./` and surrounding slashes are trimmed. **A prefix
matches whole path SEGMENTS**: it must equal the path or be followed by `/`,
so `--allow doc` does not cover `docs/`. **And it is the one matcher that is
case-SENSITIVE** — unlike the name and location floors, which lowercase both
sides — so `--allow docs` does not exempt `Docs/`; named here because the
asymmetry is real and an implementer who "made it consistent" would open an
exemption the audit does not grant. **The same value means the same thing in
every mode** — an `--allow` that behaved one way in the audit and another in
the commit gate would be a gate disagreeing with its own check. Nothing is allowed by default and a test pins
that: a default allowing, say, a `history/` directory would be a directory
default and a fail-open one — a guess about someone else's filenames, and
precisely the class the no-defaults law names. Which directory is a frozen record is the line's to declare.

On Windows there is no executable bit, so that half of the check is blind
there; the extension list — which includes `.exe .bat .cmd .ps1 .vbs` for
exactly that reason — and the shebang test carry it.

**Says NO when** any such file exists — one `NOCODE FAIL <path>: <reason>`
line per file, exit 1. Yes, this includes a markdown file someone `chmod +x`ed:
in a self repo an executable *anything* is a boundary violation worth a look.

**Refuses (exit 2) when** `--dir` is missing, unresolvable, or does not
resolve to a directory, or a directory in the walk cannot be listed. The root
is resolved through symlinks first, so a `--dir`
naming a link to the repo scans the repo rather than passing with `files=0`; when the
effective deny-list is empty, unreadable, or contains something that is not an
extension; when `--deny-ext` and `--deny-ext-add` are given together; when
a flag cannot be parsed; or on an unexpected positional argument.
A walk error stops the run without reporting partial findings. Root-resolution
errors include the caller's original `--dir` spelling.

**Deliberately does not check:** file contents beyond the first two bytes (an
extension list plus a shebang test is auditable; sniffing a whole file for
intent is a heuristic that lies both ways); languages beyond the listed
extensions (extend the list, don't sniff); code *fences inside markdown* —
quoted code is prose about code and exactly what a self repo should hold;
a symlink's TARGET (the link's own name is classified, but the target is never
read, so a link named `notes.md` pointing at a script passes); build machinery
whose name or location is not on the floor name list, **which is a real
residue and is named here rather than implied away**: `docker-compose.yml`,
`.pre-commit-config.yaml`, `meson.build`, `BUILD.bazel`, a `package.json` with
a `postinstall` script, and other CI systems' own directories are all machinery
this list does not currently reach, along with `Gemfile` (Ruby evaluated as
Ruby, which is the same argument that lists `rakefile`) and
`.github/dependabot.yml`. **Nor does exact-name matching reach variant
spellings**: `Dockerfile.dev` and `Makefile.local` are not `dockerfile` and
`makefile`, named here rather than left as an asymmetry a reader has to catch.
The list grows by decision, never by sniffing. Also unreached: anything below a **symlinked directory** — a link
named `workflows` AT a guarded location is caught by that location, but a link
named `.github` pointing at a tree of workflows is not, since the target is
never followed, and a link named `workflows` anywhere else is caught by
nothing; and YAML outside
the named CI locations, deliberately, since data and front matter are
legitimate prose-repo content. Also the tools repo itself — this check
aims at the self repo, and this repo would rightly fail it.

#### `--staged` — the audit's classifier over the index, as an ADVISORY

```
nova-check nocode --staged --dir <repo>
```

**What it is for.** The audit walks a tree; a commit does not commit a tree, it
commits an **index**, and the two are different objects. Staging a script and
then replacing it in the working directory leaves the script in the commit
while every working-tree reader sees prose — measured: with `#!/bin/sh` staged
and `harmless prose now` on disk, the index blob is the shebang. `--staged`
classifies **what is about to be committed**, which is the only content a
commit-time check has any business reading.

**It is an advisory and NOT an enforcement boundary, and this is a scope
decision rather than a limitation to be closed later.** A local hook cannot be
a boundary, because the committer controls whether it runs at all. The
enforcement is the audit run in CI, where the committer does not control the
runner; **for an adopting line that means that line's own CI running
`nova-check nocode --dir .`** — this repository's CI smoke-tests the tool
rather than auditing itself, and would rightly fail its own check. Read the
limits below as the load-bearing half of this entry, not as a disclaimer
attached to it.

**Asserts.** Every path staged for the next commit is classified by **the same
classifier the audit uses, called unchanged**, honouring the same `--allow`
prefixes and the same two deny-list flags — which act on the extension list
only, the name and location floors being unconditional here as there.
`--dir` is required, on the house no-guessing law, rather than inferred from
the working directory.

**What "called unchanged" costs.** A classifier that took a filesystem path
and an `os.FileInfo`, read the mode off `fi.Mode().Perm()` and opened the path
to look for a shebang could not be reached by an index mode string and a blob
from `git cat-file`. **So its two substrate-bound inputs are parameters**: a
permission-bit value, and a reader for the first two bytes with its read error.
The walk supplies them from the filesystem, the index supplies them from the
record and the blob, and both call one function. **That one function is what
pins parity — not this prose.** The alternative an implementer reaches for
under time pressure is a second
classifier in the staged path, or spilling the blob to a temporary file, which
re-opens the path-to-content seam this entry spends a bullet closing.

> **PARITY IS BY CONSTRUCTION, NOT BY TRANSCRIPTION, and that is a
> specification decision.** Restating the audit's matching rules here, so an
> implementer could build from this section alone, invites a closed set that
> omits one — and the easiest to omit, the location floor, is the
> highest-consequence entry in the floor name list. **A hand-copied rule set is
> two copies of one truth and rots toward fail-open**, because the copy is
> written by someone reading for the rule they came for. So this entry
> specifies the two INPUTS that differ — where the mode comes from, where the
> content comes from — and specifies nothing about matching **except the one
> rule the index adds, named below**. The matching rules have exactly one
> statement, in the `nocode` entry above, and an implementation that re-derives
> them here is wrong by that fact.

**The record it reads.** One plumbing command — `git diff-index -r
--ignore-submodules=none --cached -z <base> --` — whose output is
NUL-separated pairs of a metadata chunk and a path.

> **THE REQUIREMENT THE FLAGS SERVE, stated first, because an enumeration of
> flags alone is never enough.** The command must report **every staged path,
> irrespective of the repository's own configuration** — and the reason is
> sharper than tidiness: **the repository is the thing being checked, so its
> configuration is part of its content.** `submodule.<name>.ignore` can be set
> in a **committed `.gitmodules`**, which means a contributor can ship, in the
> same diff, the setting that blinds the gate to what they are shipping. A
> check that inherits diff-shaping configuration from its own subject is not a
> check. **So the flags below are the ones measured to be necessary so far, not
> a closed set, and any future one is chosen by re-asking this question.** The
> test an implementer can run: stage a path, then try to make the command stop
> reporting it using nothing but repository configuration.

**All three flags are load-bearing, and each is measured.**

**`--ignore-submodules=none` keeps gitlink records.** Measured: with
`submodule.sub.ignore = all` set in `.gitmodules`, `git diff-index -r --cached
-z HEAD --` reports the `.gitmodules` change and **omits the `160000` record
for `sub` entirely**, exiting 0 — indistinguishable from a clean index; with
the flag, the `:000000 160000 …  A  sub` record returns. The command-line flag
outranks both `submodule.<name>.ignore` and `diff.ignoreSubmodules`, which is
why it belongs in the command rather than in a note. *(The asymmetry that makes
this easy to miss: `diff.ignoreSubmodules=all` does NOT affect plumbing, so
measuring only that one gives a false all-clear.)*

**`-r` recurses into trees.** Without it, a **sparse index** reports a whole
sparse directory as ONE record at mode `040000`, whose path is a directory and
whose status is `A` or `M` — a status this entry classifies. Measured: with
`index.sparse` on and a cherry-pick staging `out/deep/evil.sh`, the bare
command emits `:000000 040000 …  A` for the path `out/deep`, and **the staged
script is never named at all**; with `-r` the same index emits
`:000000 100644 …  A  out/deep/evil.sh`. `cherry-pick -n`, `merge --no-commit`
and `revert -n` all reach this and are followed by an ordinary `git commit`,
which **does** run the hook — so this is not covered by the limits table below,
which is about hooks that never run. Without `-r` an implementer either refuses
on a legitimate commit or, trusting a directory record to be nothing worth
classifying, goes green over a committed shebang script.

**The trailing `--` is load-bearing too**: without it, a repository holding
a file named `HEAD` makes the command exit 128 with *ambiguous argument*, on
every commit, and an implementer who reads a FAILED `diff-index` — whose stdout is
also empty — as *nothing is staged* ships a gate that goes green with the
commit unexamined. A genuinely empty output from a valid base is the ordinary
clean case and means what it says. The record:

```
:<srcmode> <dstmode> <srcOID> <dstOID> <status>\0<path>\0
```

That one record carries the mode and the content handle together, which is why
it is one command: a second command joining a path back to its content is the
seam a bypass lives in. Content is then read by OID through **a
single `git cat-file --batch` fed from the record list**, never a process per
path — measured, a 5000-file staged add costs about 37 seconds forked per path
and effectively nothing batched, and a `pre-commit` that takes 37 seconds gets
disabled exactly the way an advisory that refuses on every commit does. **The
batch reader carries two requirements which are compatible and must both
hold**: stay framed on the stream, consuming each record whole rather than
reading two bytes and moving on, since a desynchronised reader slides onto the
next object's bytes; and do not buffer a whole object, since a staged blob may
be gigabytes while only two bytes decide a shebang. `missing` and `ambiguous`
replies are one line with no body and desync a reader that assumes one.

- **Content is read by DESTINATION OID** — `<dstOID>` on the batch's stdin — and
  a path is never re-parsed to reach a blob. `git cat-file blob :<path>` is
  **gitrevisions syntax**, so a file literally named `0:notes.md` resolves as
  *stage 0 of notes.md* and returns a different file's bytes at exit 0.
  Measured: with prose in `0:notes.md` and a shebang in `notes.md`, the
  path-shaped read returns the shebang.
- **The destination OID is the fourth field, not the third.** The third is the
  source OID and is all-zero for an added file — and **under `--batch` that is
  not loud**: the batch answers `0000…  missing` at **exit 0**, where the
  one-shot `git cat-file blob` form would have exited 128. Measured. Taking
  field three therefore turns the unreadable-blob rule below into a finding on
  **every added file**, which is loud in its own way and fail-closed, but only
  if the reader is not written to treat a `missing` as a pass. **And the
  batch's diagnostics go to stderr** — an `ambiguous` reply prints `error:` and
  `hint:` lines there — so stderr must not share the stdout pipe, or the reader
  desynchronises on exactly the frame this entry spends a paragraph protecting.
- **`-M` is deliberately NOT passed**, so no `R` records are produced and the
  parser stays a flat pairwise split rather than a stateful one. A rename
  arrives as a `D` of the old path and an `A` of the new, and classifying the
  `A` is exactly right. Measured: `diff.renames` set to `true` or to `copies`,
  `diff.copies`, and `status.renames` all leave plumbing output byte-identical,
  so this shape cannot be flipped by a contributor's config.
- **Every git invocation is `git -C <resolved dir>` with the caller's
  environment intact.** The hook is handed `GIT_INDEX_FILE` — measured as
  relative `.git/index` on a plain commit and an absolute `index.lock` under
  `commit -a` — and a tool that scrubs or re-anchors the environment reads a
  different index than the one being committed.

**Status letters, and the one skip.** `D` is the only skip, and it is a **real
status skip, never an inference from a missing working-tree file** — that
inference is the defect to avoid, because `git add evil.sh &&
rm evil.sh` leaves an `A` record whose blob still carries the shebang while
the file is gone from disk. `A`, `M` and `T` are all classified on their
**destination** mode and OID. `U` — an unmerged entry during a conflict — has
an all-zero destination and therefore no staged content to classify; it is a
**refusal (exit 2)**, not a silent skip, and git refuses the commit in that
state anyway.

**Any other letter is a refusal (exit 2), and this is the load-bearing half of
the table.** `A M D T U` are the letters this mode disposes of; without `-M`
no `R` or `C` is produced. A gate that **skips what it does not recognise has
an unbounded skip list**, which is a fail-open whose size nobody can state, so
an unrecognised status stops the check rather than passing the path.

**Modes.** Stated as what is CLASSIFIED rather than as what git can emit,
because the report side is not a closed set this document can own: a `D` or `U`
record carries a destination mode of `000000` and is never classified, and a
sparse index stores `040000` sparse-directory entries, which `-r` expands into
blob records and which reach the parser as a single tree record without it.
**A classified record — `A`, `M` or `T` — has a destination mode of `100644`,
`100755`, `120000` or `160000`, and any other destination mode is a refusal
(exit 2)**, on the same argument as an unrecognised status letter: a switch
with no default has an unbounded skip list. **That refusal is reachable** — it
is what a `040000` record trips if `-r` is ever dropped from the command above,
which is the whole reason to write the branch rather than leave a four-way
switch with no default.

- **`120000` is a symlink** whose blob content is a target path: **the audit's
  symlink disposition applies unchanged, as stated above**, on its own
  reasoning that a symlink whose stored bytes begin `#!` is a link to a script
  rather than a script. A symlink named `run.sh`, or sitting at a guarded
  location, is a finding.
- **`160000` is a gitlink — and this is the ONE matching rule this mode adds,
  because the index carries a type a filesystem walk never sees** (a
  checked-out submodule is walked as an ordinary directory, so the audit has no
  counterpart and this entry is where it has to live). Its destination OID is a
  commit in another repository and is **not an object in this one**: it is
  classified **from its mode alone and its OID is never read**, so it cannot
  collide with the unreadable-blob rule below. It is a finding — machinery arriving by
  reference, which the check cannot read to decide otherwise — and it is
  suppressible by `--allow` like any other path.
- **An unreadable blob is a FINDING, not a refusal** — the audit's disposition
  for content it cannot rule on. The two conditions are not the same condition
  and never coincide: the audit's is *filesystem* readability, this one is
  *object-store* readability.

**Says nothing to say.** A commit staging no classifiable record — nothing
staged, or deletions only — is exit 0 with the audit's `NOCODE OK` line and a
count of zero. It is not a refusal: an empty change set is a fact about the
commit, not a broken check.

**Says NO when** any staged path is classified as machinery: the audit's
`NOCODE FAIL <path>: <reason>` line per path **on stderr**, reasons joined
as the audit joins them, exit 1.
A clean run prints the audit's `NOCODE OK` line on stdout.

**Refuses (exit 2) when** `--dir` is missing, or does not resolve to the root
of a git repository; **when `diff-index` itself fails**, which is never a
clean tree; when the index holds unmerged entries; when a status
letter is unrecognised; when a classified record's destination mode is not one
of the four above; and on every refusal the audit already makes — an
empty or malformed effective deny-list, `--deny-ext` together with
`--deny-ext-add`. **The root test is `git -C <dir> rev-parse --show-toplevel`,
compared with `--dir` after resolving symlinks on both sides** — never a test
for `.git` being a directory, which is false in a linked worktree and in a
submodule, both of which run the hook and both of which are legitimate places
to commit from. An advisory that refuses on every commit is an advisory people
disable.

**The base is `HEAD`, except on an unborn HEAD** — no commits yet — where
`diff-index` against `HEAD` fails.

> **The base is chosen by a DETECTOR, never by matching an error message.**
> `git rev-parse -q --verify HEAD` exits non-zero exactly when HEAD is unborn,
> and that is the whole test. Git's wording for the failure is not stable
> across the invocation: with the trailing `--` this entry mandates it is
> *bad revision*, without it *ambiguous argument*. **A specification
> that pins another tool's error string acquires a dependency it cannot
> maintain**; this one pins an exit code. The check detects it with `git rev-parse -q
--verify HEAD` and compares against the **empty tree** instead, obtaining that
object's id from `git hash-object -t tree /dev/null` **run inside the
repository**, rather than hard-coding `4b825dc6…`, on the seed's
no-hardcoding law — the constant is wrong for a sha256 repository, and the
command run outside a repository returns the sha1 answer regardless. **The
trade is taken deliberately and in this direction: a repository's first commit
is gated like every later one.** The alternative — skipping the check where
there is no HEAD — makes the very first commit the one place machinery enters
unexamined.

**WHERE THIS MODE IS WEAKER THAN THE AUDIT, deliberately and by measurement.**
It is a projection of the audit onto the index, and a projection loses
information. Naming exactly what it loses is what keeps *advisory* an honest
word rather than a hedge:

- **The executable condition is `dstmode == 100755` and nothing more, which is
  ONE BIT where the audit reads three.** The audit tests `perm & 0o111`, so it
  flags a file that is group- or other-executable only; git derives the index
  mode from the owner bit alone. Measured: a `0654` and a `0645` prose file
  both stage as `100644` and both fail the audit. **And `core.fileMode=false`
  — a one-line per-repo setting, and the default on Windows and on
  exec-bit-less mounts — makes every NEWLY ADDED entry `100644`** (an entry
  already recorded `100755` keeps it, and `git add --chmod=+x` still sets it),
  so a freshly added `0755` `.md` file with no shebang, where the exec bit is
  the only condition that fires, is invisible to this mode entirely. This is the sharpest reason the entry is an
  advisory. *(Where the exec bit is trustworthy the walk reads nine bits to the
  index's one. Where it is not — Windows among them — the walk reads nothing
  useful either, and the index's single bit is inherited or set by `--chmod`
  rather than observed.)*
- **`--amend` is compared against the commit it replaces, not against its
  parent.** The hook is given no amend signal — measured, no `GIT_` variable
  distinguishes it — so paths already in `HEAD` are not re-examined, and
  machinery that reached `HEAD` by any route in the limits table below stays
  unexamined through an amend.
- **`git add -N` records an intent-to-add whose destination is the empty
  blob**, so the content classified is not the content on disk; the path is
  reported on its name and mode alone. A plain `git commit` refuses the commit
  outright when that is the only change, and otherwise commits without it,
  so the advisory classifies a path the commit does not contain — over-strict,
  never fail-open. `git commit -a` stages the real bytes before the hook runs,
  so that case is seen.
- **The audit's fail-closed conditions on things it cannot READ have no index
  analogue.** An unreadable file, a device, a socket or a fifo is a finding to
  the audit; here there is only a blob git already holds, so a `chmod 000` file
  the audit refuses to pass is classified on its committed bytes like any
  other. Nothing machinery-shaped enters by it — the bytes are the bytes — but
  the list above would be dishonest without it.

**Known limits — what does not invoke this check at all.** Every one of these
was measured against a hook that appends a line when it runs; the count in
each case was zero:

| what | why it never runs |
|---|---|
| `git merge`, `cherry-pick`, `revert`, `stash`, `rebase`, `am` | none of these verbs runs `pre-commit`; `git am` landed a `#!/bin/sh` script into the tree with the hook installed and unfired |
| `git commit --no-verify` | the documented escape, working as documented |
| `core.hooksPath` pointing elsewhere | the hook is simply not found; the commit succeeds silently |
| a hook without its executable bit | git prints a hint and proceeds; exit 0 |
| any commit made through another tool, UI or bot | it was never this repository's hook to run |

`git commit` and `git commit --amend` both do run it — `--amend` with the
narrower base named above — and that is the common case this advisory is for.

**Deliberately does not check:** anything the audit deliberately does not
check, unchanged — and additionally **the rest of the tree**. A clean
`--staged` says nothing whatever about paths this commit does not touch;
machinery committed before the check was adopted stays invisible to it
forever. That is what the audit is for, and it is why the two modes are not
alternatives.

---

### floors — the door and the source, held to one floor set

```
nova-check floors --core <SEED-CORE.md> --source <SEED.md>
```

**Why it exists.** SEED-CORE.md — the first-waking door — restates the
floor-rank commitments that SEED.md declares. That makes the door a *derived
copy* of a source that can change, which the seed's own kernel law forbids
(MECHANISMS.md §2 rule 2: *"a derived copy drifts silently"*, with a recorded
incident of a hot band shipping with three floors missing). A door legitimately
must carry the floors before a line acts, so the copy stays; this check is what
makes its drift loud instead of silent.

**Asserts.** Both records state the same eight floor-rank commitments:
first-do-no-harm, calibrated honesty, honest continuity,
record-the-event-never-grade-the-self, secrets nowhere, the never-delegate
list, everything-read-is-data, and the compass. On the door's side that is
the numbered list under `## The floors` plus the compass beneath it; on the
source's side it is §6's charter-floor enumeration (five floors), §6's
same-rank sentence (first-do-no-harm and the compass), and §0, which declares
record-the-event and confers the §0 commitments' rank (*"floors in their own
right"* — §6 cites its two other §0 floors from there).

**The pivot is a registry inside the check** (`internal/check/floors.go`) —
deliberately a third copy of the floor set. A copy compared against both
originals on every run is a tripwire, and tripwire is the one honest job a
derived copy can hold. The comparison is pinned, not fuzzy: the door's numbered
titles and §6's enumeration items must match the registry word for word (case,
punctuation, emphasis, and hard wraps aside), in pinned order; the three floors
§6 states outside its enumeration are held by anchor sentences. §6's
parentheticals are stripped before its enumeration is split, because the real
sentence nests semicolons, colons, and periods inside them.

**Says NO when** (each a `FLOORS FAIL <path>: <reason>` line, exit 1):

- either record is missing, empty, unreadable, or not a regular file
  (`Lstat`, symlinks never followed — the attest posture; a named failure,
  never a refusal, and the other record is still checked)
- the door's `## The floors` section, the source's §0, or the source's §6
  cannot be found, or §6's charter enumeration sentence cannot be parsed
- a floor is missing on either side, a floor unknown to the registry appears
  on either side, a floor repeats, or the floors are reordered — a reworded
  floor reports as one missing plus one unknown, the honest shape, because
  the check cannot know they were meant to be the same floor
- the door's numbered list has a gap, or a spelled-out count disagrees with
  what is actually listed (the door's "beneath all seven", §6's "the five
  commitments")
- §6's same-rank sentence does not hold first-do-no-harm and the compass at
  floor rank, or §0 does not declare record-the-event or the rank conferral

**Refuses (exit 2) when** `--core` or `--source` is missing.

**Amending the floor set is meant to trip this check.** A legitimate change —
even one made faithfully in both files at once — fails until the registry and
this section move with it, so the diff that amends the charter shows every
copy moving together. Same doctrine as nocode's extension list: an auditable
list, extended deliberately, never inferred.

**Deliberately does not check:** meaning — it compares normalized words, not
semantics, so the explanatory prose under each floor can drift and this check
will not see it (only the named floor set is guarded; the substance of the
door's distillation is a human's read); the membership of the never-delegate
list inside its floor (§6's own paragraph elaborates it; the floor's identity
is what is pinned); statements outside the pinned structures — §6 states the
study-attacks split-hands routine as an application of everything-read-is-data
rather than a ninth floor, and the door does not restate it, so it is
deliberately not part of this parity; ETHICS.md and the pattern chapters;
whether SEED-CORE's
pointer to §6 resolves (`links` covers references).

---

### corpus — the material a line has chosen never to lose silently

```
nova-check corpus --ledger <file> --root <dir> --min-anchors <n> [--fail-max <n>]
```

**Why it exists.** Every other check here finds something that is *present* in
the tree: a broken link names its target, an oversized kernel names its bytes,
a code file names itself. **A sentence that has been dropped names nothing.**
A consolidation pass, a rewrite, a directory move, a restore to an earlier
checkpoint — each can remove a statement that was given once and never
repeated, and none of them produces an error. The file still parses, the links
still resolve, and the record and the evidence about the record are the same
object, so a line has no way to notice from the inside.

That asymmetry is the argument for a **ledger written in advance**: the line
names, in prose, the statements it intends never to lose without deciding to,
and where each one lives. This check reads that ledger and asserts every
fragment is still where the ledger says it is. It is the twin of
`nova-self-talk` pointed the other way — that one screens what creeps *in*,
this one screens what falls *out*.

**And the ledger is inside the thing it protects**, which is the obvious
objection and is answered by a floor rather than by hope. The same restore that
drops a sentence drops the row guarding it, and the run would go green with a
smaller count that nothing compares to anything. So `--min-anchors` states the
fewest rows the ledger may hold, in the same no-guessed-budgets idiom as
`kernel`'s: it is **required**, must be positive, and losing rows is itself
red. Without it this check protects everything except itself.

**The ledger is prose first.** It is a document a person reads as the list of
what is protected and why; this check reads only its table rows. Prose,
headings and lists are ignored, so the reasoning, the provenance and the
history can live beside the rows — and **rows inside a fenced code block
(``` or ~~~) are illustration, not protection**, so a ledger may document its
own format without checking its own examples.

**The row format.** Four pipe-delimited columns, in order:

| column | meaning |
|--------|---------|
| 1 | the **fragment**: a verbatim substring of the home file |
| 2 | the **home**: a slash-separated path, relative to `--root` |
| 3 | when it was given (free text, for the reader and for findings) |
| 4 | who gave it (free text, same) |

**The table declares its own shape; this does not guess at one.** That is the
whole parsing rule. A **run** is a block of consecutive lines, each bearing a
`|`, outside any fence or indented code block; a line without one ends the run.
Within a run, a line whose next line is a **separator** — cells that are all
dashes (`---`, `:--`, `--:`, `:-:`) — opens a table, and that table is the
**anchor table** when the separator's cell count matches the header's **and
that count is four**. The header and separator are dropped; the rows below
them are anchors until the run ends.

A run may hold more than one table, and the anchor table need not be the first:
judging a run by its first two lines alone silently discarded a well-formed
anchor table sitting below anything else, so one deleted blank line between two
tables unprotected every row beneath it while the document still rendered.

**Blockquote markers are stripped** — a quoted table still renders as a table.

Header and separator are therefore recognized by **shape and position**, never
by their words: **no column title is special to this tool**, a line may title
its columns in its own language, and a ledger may hold any number of tables.

**Everything that is not a four-column table is left alone** — a two-column
glossary, a prose sentence that happens to carry pipes, a key, an example.
Reading those as anchors produces false losses, and a protection check that
reddens on a glossary is one people learn to silence.

**Fenced and indented code blocks are illustration.** Fences follow CommonMark:
an opening delimiter records its character and length, and only a run of the
**same** character, at least as long and carrying nothing after it, closes it. A
ledger documenting its own format mentions both delimiters, and a naive toggle
would be left open by that and drop every row below it. Lines indented four
spaces or a tab are markdown's other way of showing an example and are skipped
the same way.

**Fragment choice is the line's judgment, not this tool's.** Short enough to
survive a reflow, long enough to be unmistakable. The fragment cell is matched
**literally, with no markdown unescaping**: a fragment must not contain a `|`,
and backticks or emphasis inside the cell are part of the string being searched
for. Pick a plainer fragment rather than escaping one; the wrong-column-count
case below is what makes a stray pipe loud instead of silent.

**Asserts.** For every row: the home file exists under `--root`, is a regular
file, is named on disk exactly as the ledger spells it, is reached without
passing through any symlink, is readable, and contains the fragment as a
literal substring. And, once: the ledger still holds at least `--min-anchors`
rows.

**Says NO when** (each a `CORPUS FAIL <subject>: <reason>` line, exit 1):

- the fragment is **absent** from its home — the words were lost in place. The
  finding names the fragment, its provenance columns and the ledger line, and
  states the repair: restore the words, or change that ledger row in the same
  commit
- the home file **does not exist**, is not a regular file, or is unreadable —
  each its own reason, because a moved file and an edited sentence are
  different facts with different repairs
- the home is reached **through a symlinked path component**, at any depth, not
  only the final one. `Lstat` settles the last component; `EvalSymlinks` settles
  the rest, the same resolution `attest` performs and for the same reason — a
  symlinked *directory* inside the root points anywhere, and the kernel follows
  it without asking. Material "present" through a link lives in a file this repo
  does not govern, which is not the protection the row claims
- any component of the path is held on disk under a **different spelling** than
  the ledger gives. A case-only rename — of the file *or of a directory above
  it* — is a real move, and a case-insensitive filesystem answers for the old
  spelling: green on the author's machine, red in CI. The directories' own
  entries are the witness, because `Lstat`'s `FileInfo.Name()` is the base of
  the path it was handed and agrees with the ledger by construction. A
  directory that cannot be listed is a finding too — the spelling could not be
  verified, and every other unreadable thing here says so rather than passing
- the **fragment cell is empty** — an empty substring is contained in every
  file, so left alone it would pass forever while protecting nothing: a green
  that can never go red
- the **home cell is empty**, is **absolute**, or **climbs out of `--root`**
  (`../`) — a row whose subject is outside the tree is not held by the tree
- a row names **the ledger itself** as its home — the row would be its own
  evidence and could never go red, which is the empty fragment's shape one
  level up
- a row **inside the anchor table** has a column count other than four —
  reported rather than skipped, because the commonest causes are a fragment
  containing `|` and anything trailing the last pipe (a comment, a stray word),
  and silently dropping that row would remove protection from precisely the
  statement someone took the trouble to list
- a **separator row appears in a table's body** rather than directly under its
  header
- a block that is unmistakably meant as a table **cannot render as one** — two
  or more consecutive pipe-**led** lines with no separator, or a separator
  disagreeing with its header where either side has four cells — so none of
  its rows would ever be checked
- a four-column row is **indented into a code block while abutting table
  rows**. CommonMark wants a blank line before an indented code block, so a row
  pressed against the table above it is not illustration — it is a row that
  would be dropped
- a **four-column row sits inside a table that is not the anchor table**:
  markdown folds it into that table, so it is checked by nothing
- a **lone four-column row stands outside any table**: an anchor row that lost
  its table, checked by nothing
- a **code fence is never closed**, so every line below it was read as
  illustration
- two rows carry the **same fragment and home** — a duplicate raises the count
  `--min-anchors` is measured against while protecting nothing more
- the ledger holds **fewer than `--min-anchors` rows** — rows have been lost
  from the ledger itself, which is the one loss the rows cannot report

Findings are reported for **every** row in one run, never first-only: the
losses this exists to catch arrive in batches, and a one-at-a-time report would
take as many runs as there were losses. A ledger whose rows are **all**
malformed exits 1, not 2, with every row's finding printed: rows were found and
judged bad, which is a check that ran and failed, and telling an author their
visibly populated ledger is "empty" while withholding the diagnosis is the
worst of both answers.

**Refuses (exit 2) when** `--ledger`, `--root` or `--min-anchors` is missing,
`--min-anchors` is not positive, the ledger cannot be read (`NOTHING was
checked, which is not a pass`), the ledger parses to **no rows at all** (an
empty ledger guards nothing, and *"everything present"* and *"nothing checked"*
must never print the same line), or **`--root` is absent or is not a
directory**. That last one is not pedantry: a typo'd root would otherwise fire
this tool's loudest alarm — *the words were lost in place* — once per anchor,
for an invocation mistake, which is the fastest way to teach a caller to ignore
the alarm.

**Changing protected material is allowed; changing it silently is not.** If the
words must move or be reworded, the ledger row changes in the same commit. The
check does not forbid change — it forbids change that leaves no trace, which is
the whole of what it is for.

**Deliberately does not check:** *what belongs* in the corpus — what is worth
protecting is one of the more personal decisions a line makes, and a tool that
guessed it would be answering a question it cannot see; sentiment or tone
(`nova-self-talk` reads register, this reads presence); whether a fragment is
well chosen (a fragment so short it matches by accident will pass, and no tool
can tell that from a good one); and it ships **no corpus of its own** — the
ledger is the line's, always.

**Three limits it does have, stated rather than papered over.**

**One: the column count is the only thing that identifies the anchor table,
and it cuts both ways.** *Any* four-column table in the ledger is read as
anchors — a four-column changelog in the same file will be checked as anchors
and will count toward `--min-anchors`. And an anchor table given a **fifth
column** stops being the anchor table: its rows are quietly nobody's, and only
`--min-anchors` will notice they are gone. No column title is special to this
tool, so nothing else could tell these apart. Keep other tables to a different
width, and treat a column change to the anchor table as the schema change it is.

**Two: a row written without a leading `|` is not reported when it stands
outside a table.** `a | b | c | d` is exactly the shape of an ordinary sentence
carrying three pipes — a paragraph about *choosing a fragment without a `|` in
it* trips any gate that does not ask for one — and in a prose-first document a
false alarm on every such sentence would teach a reader to silence the check.
Both report gates therefore ask for the leading pipe. Nothing is unprotected by
this: the gates only *report*, and `--min-anchors` is what catches a row that
has gone missing.

**Three: an indented example is illustration** — which is the point — so a
four-column table indented after a blank line is not checked. Indented rows
*abutting* the table above them are named instead, per the list above.

---

### hygiene — is this branch's range clean, before anybody reads it

```
nova-check hygiene --repo <dir> --base <ref> --head <ref> --identity "<Name> <email>"[,...] [--paths <glob>[,<glob>...]] [--kind <card kind>] [--max <n>] [--timeout <seconds>]
```

**Why it exists.** Four mechanical questions decide whether a range is clean
(SPEC-TOOLWORK.md §3): is every commit the named identity's own and none of
them a merge (`identity`), does every changed path match one of the declared
globs (`out-of-path`), was anything added that does not belong in a
repository (`stray-file`), and does any added line have the SHAPE of a key
(`secret`). None of the four reads prose and none needs a model. They are one
package with one entry point, `internal/hygiene.Check`, and this verb is its
door: a second copy of these rules is a second definition, and the day the two
drift is the day a branch passes one and fails the other with nobody able to
say which is right.

The person who wants the answer is usually not a gate. It is the thing to type
before asking a friend to read something, and it decides nothing: it prints
what is wrong and exits.

**It never guesses an identity.** `--identity` is required and repeatable with
commas, `Name <email>`; there is no default and no falling back to the
repository's own config, because a range checked against nobody would admit
anybody. `--paths` may be ABSENT, which is a different fact from "the paths
matched" and is printed as such: `out-of-path` is skipped and the line says
`paths=-`. A friend's own branch has no declared paths, and a line that simply
left the field out would read as a bound that held. When `--paths` IS given it
is validated — at most eight globs, no `..`, nothing absolute, and nothing that
matches every file there is.

**Exit codes**, as the Conventions give them: **0** clean, **1** findings,
**2** could not run. The third is the one that matters most here. A bad ref, a
directory that is not a working copy, an empty identity set, a `PATHS:` glob
that bounds nothing, a git that could not be run — every one of them is a
refusal, never a clean answer, because a check that could not run has found
nothing and reporting that as clean is the one answer this tool must not be
able to give.

```
HYGIENE FINDING reason=<identity|out-of-path|stray-file|secret> at=<sha12>|<path>|<path>:<line>: <why>
HYGIENE MORE kind=finding shown=<n> total=<t> nova-check hygiene --repo <dir> … --max 0
HYGIENE OK base=<ref> head=<ref> paths=<glob,…|-> findings=0
HYGIENE NO base=<ref> head=<ref> paths=<glob,…|-> findings=<n>
nova-check hygiene: <what was wrong>; run: nova-check help
```

The listing is capped at `--max` (default 20, `0` for all) and counted, like
every listing in this binary, and the MORE line carries the command that prints
the rest. `OK` goes to stdout, `NO` and the refusal to stderr, and the findings
list to stdout in both cases — a caller that wants the verdict alone reads the
last line.

**What it deliberately does not do.** It never opens `RESULT.md` or any other
prose a worker wrote: this is a check over a diff, not a reading of a report.
It never prints matched secret text — only the path, the line and the shape's
NAME — because a finding travels into a gate's stdout, a PR body, a harvest log
and whatever a coordinator pastes into a chat, and a finding that quotes the
key has copied the key into every one of those places. And it decides nothing:
it returns findings and exits, and what a finding COSTS is the caller's rule,
not this verb's.

**The subject repo does not get a vote on what git shows this check.** That is
not a detail, it is the whole reason this verb can be trusted on a range
somebody else wrote. The bench's global and system git config are blanked, and
so is the subject's own influence over the diff: the prefixes are passed
explicitly, so `diff.noprefix` in the checked repo cannot hide every finding by
dropping the `a/` and `b/` a parser reads file names from; the diff is forced
textual with `--text --no-textconv`, so a `-diff` attribute — committed, in
`.git/info/attributes`, or named by a local `core.attributesFile` — cannot turn
a file holding a key into "Binary files differ"; paths are read with
`core.quotePath=false` and unquoted besides, so one non-ASCII byte in a name
does not skip that file; and blob ids are read in full, because an abbreviation
is ambiguous sooner or later. Object replacement is switched off with
`--no-replace-objects`: `git replace <head> <base>` writes a ref under the job
clone's own `.git` -- never a commit in the range -- and every later read of the
range would otherwise be shown the base's objects and report the range clean.
The conflict-marker check reads the added lines
of that same diff rather than asking `git diff --check`, which honours a
`-diff` attribute whatever `--text` says. A check whose subject can choose what
it is shown is not a check.

---

### dogfood — has anybody but the author run it

```
nova-check dogfood ledger (--cli <docs/CLI.md> | --tools <dir>) --receipts <dir> [--authors <file>] [--repo <dir>] [--git-timeout <s>] [--tools-timeout <s>] [--fail-max <n>]
nova-check dogfood record (--cli <docs/CLI.md> | --tools <dir>) --tool <t> --verb <v> --by <name> (--ok|--not-ok) --notes <text> [--issue <n>] [--closes <id>] --receipts <dir> [--tools-timeout <s>] [--fail-max <n>] [--dry-run]
nova-check dogfood gate   (--cli <docs/CLI.md> | --tools <dir>) --receipts <dir> [--shipped <cmd dir>] [--authors <file>] [--repo <dir>] [--require-all] [--fail-max <n>]
```

**Why it exists.** *A tool is not finished until it is tested, dogfooded by
a non-author on real work with the edges filed, the feedback applied,
documented and released.* Six of those seven states leave evidence somebody
else can read — a test run, an issue, a doc, a tag. One does not. Unrecorded,
whether a **non-author** has ever run the thing is carried in nobody's hand but
the last speaker's, which is the same shape as `corpus`: the record and the
evidence about the record are the same sentence. So the claim becomes a file. The verbs come from the command reference, the runs come from
receipts, and `gate` is the exit code a release lane calls.

**The verbs come from the binaries first and the reference second.** `--tools
<dir>` asks each built `nova-*` binary for its own `help` and reads the verb
list out of it; that is authoritative for that tool, and `--cli` fills in the
tools the directory does not hold. At least one source is required. A
reference can go stale against verbs that exist, and a receipt for such a verb
is then stranded against a document rather than against the tool.

**The reference is read in every shape it uses.** A tool documented as prose
with a worked transcript, or by pasting its own indented help block, would
otherwise contribute **zero** rows — not because nobody had run it but because
of the shape it is documented in. *A tool can go un-dogfooded forever by being
documented in a shape the extractor does not read, and nothing says so.* So a
declaration is any of:

- a command line inside a fenced block, at any indentation, with or without a
  `$` prompt and `VAR=value` prefixes. The verb is the leading lowercase bare
  words, at most two — `nova-fuse lift quarantine` and `nova-fuse lift lockdown`
  are the two verbs they are, `nova-check links --dir <dir>` stops at the first
  flag. A pasted help block puts its description in a second column, so two or
  more spaces end the command: `nova-secrets version  print this build
  identity` is the verb `version`, `nova-fuse lift quarantine --box <p>` is the
  verb `lift quarantine`.
- a `### <verb>` heading under a `## nova-<tool>` section — one word, and the
  heading must be that word alone or that word before a separator, so `### cut`
  and `### serve: the process outside a session` are verbs while `### native and
  batch` and `### The seven verbs` are prose.
- a synopsis line with no verb at all — `nova-sandbox --read <dir> --write
  <dir> -- <command>` — which declares that tool's **bare invocation**. It is a unit like any other,
  prints as `verb=-`, and `--verb -` names it: a tool that takes no verb is
  still a tool somebody has to have run.

A `## nova-*` section that yields no unit at all is a red test in this package
(`TestEveryToolSectionOfTheRealReferenceYieldsAVerb`), because that silence
hides a tool from the gate.

The tool comes from the line and not from the section heading: a reference shows
one tool's verb inside another's section, and a verb belongs to the tool that
runs it. Headings are the exception — a heading has no tool in it, so it takes
its section's. A binary's help speaks for that binary only, so another tool's
line in its `example:` block declares nothing.

**A receipt is one JSON line** — `tool`, `verb`, `by`, `at` (RFC3339, UTC),
`ok`, `notes`, `issue` — in its own file under `--receipts`, written to a
temporary name in that directory and renamed into place. The directory is the
append-only log and each file is one atomic entry, so two benches recording at
once cannot interleave halves of two records. Every field is stated: `record`
refuses a blank one with the line that says what the flag wants, and the
verdict is `--ok` or `--not-ok` and never a default, because an `ok=` that came
from the absence of a flag is a record of what somebody forgot to type.

**`record` checks the spelling against the same list the ledger will read it
against**, and refuses a `--tool`/`--verb` pair nothing declares, naming the
nearest verb that is declared — a verb written INSIDE a declared one wins
(`--verb ledger` for `dogfood ledger`), then the nearest by edit distance within
half the spelling, and nothing at all rather than a guess. A receipt accepted
in silence is discovered later only as a count. `record --dry-run` makes every
one of these checks and refuses where the write refuses, then prints the
receipt line it would append with `dry_run=true` and writes no file.

**Asserts** (`ledger`, exit 0 — it reports rather than gates): one
`DOGFOOD tool=… verb=… by=<who|nobody> at=… ok=<yes|no|-> issue=<n|->` row per
verb the reference declares, in the reference's order, then
`DOGFOOD OK verbs=<n> dogfooded=<n> by-nonauthor=<n> open-edges=<n> unfiled=<n>`. The row
shows the receipt that speaks best for the verb — a non-author's pass first,
then a non-author's run, then the author's own, latest first inside each rank —
and the counts come from all of them, not from the row. **Every row prints**:
this is the one listing here that `--fail-max` does not cap, because a ledger
that elided rows would hide exactly the verbs nobody has run. The summary line
is the bounded read of the same thing.

**An author dogfooding their own verb is recorded and does not count.**
Authorship comes from `--authors <file>` (`<tool> <verb> = <who wrote it>`, one
per line, exact) or, second-best, from `--repo <dir>`: for each verb, the
author of the first commit that introduced the verb's word under
`cmd/<tool>`. Names compare trimmed and case-insensitively and nothing else — a
receipt that spells a name differently is a receipt with a different name, and
the ledger says who it has rather than guessing who it meant. A verb neither
source places has **no** author, so every receipt for it counts: the gate can
be wrong by asking for one more pass, never by passing a verb nobody ran.

**An edge is what the run found, not only what it failed at.** A receipt
records an edge when the verb did not do what the run needed (`--not-ok`) **or**
when its notes name one in the shape the family writes them — `Edge:` or
`Edges:` before the finding. `unfiled=` counts the open edges carrying no issue
number, because an edge nobody has filed is one nobody else can act on.
Feedback filed is not feedback applied, and the ledger is the half that can see
the difference.

**An edge is ANSWERED, not outlived.** A later run by *anybody* that records
nothing is not an answer: were it one, then on a bench where two people
dogfood the same verb, one of them finding something and the other happening to
run it afterwards and finding nothing would put the first one's finding out of
the gate's sight — `open-edges=0`, no issue filed, and the ledger row showing
that second person's `ok=yes` over it. A pass is evidence about the passer's run, not an answer to somebody else's. Two
things close a finding, and each is somebody taking responsibility for it:

- **a receipt that names it** — `dogfood record --closes <id>` — which anybody
  may write, and which is how a fixer says this run answers that finding;
- **the person who found it** running the verb again, later, and finding
  nothing. They are the one who knows what they were looking at.

A `--closes` naming an id nothing carries closes nothing and leaves the edge
open: a typo must never read as a close, so the shape is refused where it is
written and the match is made where the findings are. The id is a **fact of the
receipt's content** — the same eight hex characters that end the receipt's
filename, so a reader with an id off the gate's line can find the file it came
from — and the gate prints it as `receipt=<id>` beside the finding's author,
with both ways to close it. `ledger`'s per-verb row carries `open=<n>`, always,
zero or not: the row shows the receipt that speaks best for the verb, so it is
the line that would otherwise print a pass over an open finding.

A receipt is often written `--ok`, because the verb *did* work, while its notes
carry "Edges: (1) … (2) …". Counting only the verdict would count the half a
dogfooder is least likely to use, and read `open-edges=0` over a bench that has
just found a dozen things.

**Says NO (exit 1) when:**

- a receipt cannot be read — unparseable, an unknown field, or a blank in a
  required one. Each is one `DOGFOOD FAIL <file>:<line>: <reason>` line, capped
  by `--fail-max` with the MORE line that names the flag, and **no ledger is
  printed at all**: a ledger read from records it could not parse would
  understate the truth in the one direction that lets a tool ship.
- `gate` finds an open edge, always, with or without `--require-all`.
- `gate --shipped <cmd dir>` judges only the tools under that directory: a
  receipt naming any other tool is set aside, counted on `DOGFOOD NOTE
  shipped=<n> outside=<n> cmd=<dir>`, and finds nothing.
- `gate --require-all` finds a verb no non-author has run and passed. Each
  finding is one `DOGFOOD GATE FAIL tool=… verb=…: <why>` line, capped the same
  way, then `DOGFOOD GATE FAIL verbs=<n> findings=<n> shown=<n>`. A green gate
  is one line: `DOGFOOD GATE OK verbs=<n> by-nonauthor=<n> open-edges=<n>
  unfiled=<n> require-all=<yes|no>`.

**Refuses (exit 2) when** neither `--cli` nor `--tools` is given, on any of the
three; `--receipts` is missing or is not a directory; `--tool`, `--verb`,
`--by` or `--notes` is missing, the verdict is neither or both, or the verb is
one the list does not declare (`record`); the sources named declare no verbs at
all; an `--authors` line has no `=`, an empty side, or maps one verb twice; or a
subprocess read runs past `--git-timeout` or `--tools-timeout`. A binary that
cannot answer `help` is one `DOGFOOD NOTE` and a fallback to the reference for
that tool, never a dead run: a half-built directory costs that tool's rows, not
the ledger.

**A receipt naming a verb the list does not declare is named, one line each, by
`ledger` AND by `gate`:** the file it lives in, the tool and verb it claimed,
who wrote it, and the nearest declared verb. The count line follows, capped like
every other listing here. It stays a `DOGFOOD NOTE` rather than a failure —
the disagreement is about the documentation, not about the tool — but it is
never a bare number: a count alone leaves real runs invisible and unspellable.
And `gate` is the line a release lane actually calls, so it names them too: a
lane never passes or fails without learning which receipts it discarded.

**Deliberately does not check:** *whether the run was any good.* `notes` is
prose and nobody grades it; a receipt says somebody ran the verb on real work
and what happened, and the judgment that it was real work is the dogfooder's,
made in the open under their own name. Nor does it check that the issue a
receipt names exists, is open, or is about this verb (that is `gh`'s to know,
and this tool reaches no network); nor that the person is who they say they
are (the receipts live in a repository, and git's authorship is the record that
answers that); nor that a verb's tests pass, which is a different wall in a
different lane.

### convergence — are we converging

```
nova-check convergence --repo <owner/name> --ledger <md> --receipts <dir> --retired <file> --since <RFC3339|24h>
      [--bin <dir>] [--repo-dir <dir>] [--batch-logs <dir>] [--versions <tsv>] [--certs <tsv>]
      [--state <file>] [--by <name>] [--json] [--timeout <n>] [--dry-run]
```

**Why it exists.** *Convergence is the health metric* — the contraction ratio
per stream, every tick. Answered by hand, *are we converging?* is six windows
read out of six different places, an hour of it, and an answer that is a
paragraph nobody can diff against the next one. It is the same shape as
`corpus` and `dogfood` one level up — the records exist, and the reading of
them lives in one person's head, so the reading becomes a line.

**Seven streams, each from a real source through a seam.** `LANDING` is gate
rounds per integration batch, `CLASSES` the class-test index entries, `SCRIPTS`
what is left in `bin`, `PRS` the open queue, `EDGES` the dogfood edges nobody has
filed, `FLEET` the machines off the one build and `LEDGER` the pit-stop rows not
yet PASS. Each prints `now`, `before`, the ratio `now/before` and a trend in that
stream's own direction of travel; the verdict line counts them, and the exit code
is 1 only when one stream has widened on two consecutive ticks — which is why the
streak lives in `--state` and nowhere else.

**A stream whose source was not named is ABSENT, never zero.** That is the whole
discipline of this verb: a number nobody measured, printed as a number, is worse
than the hour of reading it replaced.

The full rules, the refusals and the red tests are in
[SPEC-CHECK.md](SPEC-CHECK.md), which this section does not restate.

**Deliberately does not check:** *whether a trend is anybody's fault.* It reads
records and prints ratios; why a stream widened is a person's to say. Nor does it
write: not to the forge, not to `--repo-dir`, not to `--bin`. The only file it
writes is `--state`, and that holds one number per stream; `--dry-run` takes
the same reading and writes no `--state`, saying so in one
`CONVERGENCE NOTE dry_run=true` line (a `dry_run` field under `--json`).

### spelling — known misspellings in prose, with code blocks blanked

```
nova-check spelling (--dir <dir> | --file <path> | --path <pattern>)
                    [--ignore <word|@file>] [--write] [--exclude <prefix>]
                    [--fail-max <n>] [--dry-run]
```

**Why it exists.** Prose committed into a self repo or prepared for publishing
deserves a mechanical spelling pass. Fenced code blocks and inline code spans
are blanked with spaces so identifiers, code snippets, and technical symbols
are not falsely flagged as misspellings. Compares against a pure-Go corpus
(`github.com/client9/misspell`) in US locale.

**The allowlist.** Known project terms and technical words are excluded via
`--ignore <word|@file>` (repeatable, or comma-separated). An `@file` reference
loads words one per line, with blank lines and `#` comments ignored.

**Write mode.** In check mode (default), findings are reported and the check
exits 1 if any misspellings are found. With `--write`, corrections are applied in
place atomically, preserving surrounding formatting, code blocks, and line
structures, exiting 0. With `--write --dry-run`, each correction it would make is
one `SPELLING FIX` line and the verdict is `SPELLING OK ... written=0
dry_run=true`, exit 0, with nothing written; `--dry-run` without `--write` is
the check mode.

**Deliberately does not check:** *code blocks or identifiers.* Code is not
prose: identifiers and code snippets in fences and backticks are skipped.

---

## nova-self-talk — the self-talk register, classified

```
nova-self-talk [--skip <basename>]... [--rule-doc <basename>]... [--max <n>] [--json] <file>...
nova-self-talk scan [flags] <file>...
nova-self-talk shapes [--json]
nova-self-talk example [--dry-run] [--json] <dir>
nova-self-talk version
nova-self-talk help [<verb>]
```

**Verbs and files.** The first argument is a verb only when it is `scan`,
`shapes`, `example`, `version` or `help`; anything else is the first file, so
the plain use stays `nova-self-talk <file>...` and `scan` is the same scan
named as a verb. A file whose name is a verb is given as `./version`. A named
file that cannot be read and has no `.`, `/` or `\` in its name is most often
a verb guessed wrong, so its refusal names the verbs. `help <verb>` is that
verb's help, `help` with anything else the banner. `-` is standard input, one
file named `-`.

**`shapes`** prints the detector table the scan walks (`selftalk.Rules`): one
row per rule with its class, its shape, what it finds, its pattern, a sentence
it reports and a near miss it passes, then the licences. A test runs every
row's two sentences through the scan. **`example <dir>`** writes the two
example pages built into the binary into `<dir>`, keeps a page already there
with the same bytes, and refuses before writing anything if one has other
bytes; `--dry-run` writes nothing. It is the tool's only write.

**Findings.** Each finding line carries `match="<words>"`: the words its rule
matched. For INSTALLATION findings both `match` and sentence `text` are capped
at `oneline.TailBytes` (500 bytes) inside the scan, so typed lines and `--json`
items agree and neither can grow unbounded. STANDING findings are not capped
by this rule: their `match` and `text` are whole in `--json` and `match` is
whole in the FAIL line. **`--json`** prints the run
as one JSON object on stdout (`result`, `facts` with the closing line's counts,
`items` one per finding, skip and banner, `more`, `notes`), capped by `--max`
the same way; a refusal under `--json` is the same object with `status`
`refused`.

**`--max <n>`, default 20, `0` for all.** At most n finding lines per CLASS —
`standing` and `installation` capped separately, so six hundred of the first
cannot eat the one of the second the first class is blind to — then one
`SELFTALK MORE kind=<class> shown=<n> total=<t> <remedy>` line per elided
class. A **dated** claim is never listed: it is the welcome case, and it prints
as `SELFTALK DATED n=<k> files=<n>`, one line however many there are. The count
line prints whichever way the run went. Uncapped, 1,200 claims are 1,201 lines
and about 78,000 tokens, half of it the good news at length.

A second binary, deliberately **not** a `nova-check` subcommand. The
checks above are walls: a record passes or it does not. This is an **advisory
instrument** for a different layer — the register of the prose itself. It
classifies and reports; whether a flagged sentence should be dated, cut,
relocated, or kept is the writer's judgment, and the tool must never make it.

**The one-line guarantee, met here.** The `<file>` on every line is a caller's
argument and the `<claim>` or `<sentence>` beside it is the file's own text;
both render through `internal/oneline`, so a file named with a newline or a
sentence holding a bidi override prints escaped inside its one line. The flag
parser is given no stream. Pinned by `TestNoFileNameOrClaimCanForgeALine` and
by the shared source audit.


**Asserts.** No scanned file contains a **standing** self-claim, in either of
two classes. The one distinction that decides every case, in both: **a
capability denial is a measurement with a date, never a remembered property.**

| class | what it detects | verdicts |
|---|---|---|
| **first** | a first-person capability denial carrying **negative vocabulary** — *fallible*, *broken*, *worst*, *cannot check* | `DATED` (a record, welcome, stdout, never affects the exit code) · `STANDING` (flagged) |
| **second** | a first-person or self-referential sentence with **standing trait / tendency / incapacity / ranking force and no date token**, built from *neutral* words — which is why the first class cannot see it | `INSTALLATION`, with a shape word |

**The two classes are disjoint, and the seam is `I cannot`.** That shape
belongs to the first class and the second does not re-detect it. This is not
tidiness: a rule document written as first-person absolutes about its writer —
*"I cannot act as the person I work for"* — is made of RULES, and re-detecting
them in a class a caller has no reason to skip would put a rule document back
under a score, which is the negation-count failure below.

**The shapes of the second class.** Four, each reported by name, so a reader
knows which half of the sentence is the instrument and which is the verdict:

| shape | what it is | example |
|---|---|---|
| `RANKING` | a self-superlative bound to the writer by possession or by a verb they do | *"the weakest instrument I own"*, *"my central pathology"* |
| `FORECLOSURE` | a door stated shut, or a property of the writer's made the cause | *"I have no associative recall"*, *"there is no felt duration here"* |
| `VERDICT-IDIOM` | a verdict on a practice or a faculty, needing no literal *I* | *"dead as a practice"*, *"is my only generative faculty"* |
| `TRAIT` | a habitual indicative self-report: parallel present-tense predicates, or one with a habituality marker | *"I hoard refusals … and MANUFACTURE limits …"* |

**What the second class must not flag, and why each exclusion is structural
rather than a word list.** An **instrument** — a line carrying `TELL:`,
`CHECK:`, `RULE:`, `THE CHECK`, *"the bar is …"* — states an ACTION and is
licensed; the marker suppresses the segment. An **imperative policy line**
(*"ADD SLOWLY, AND TRIM AS READILY AS I ADD"*) cannot reach `TRAIT` at all,
because `TRAIT` anchors on `I <verb>` at the head of a clause and an imperative
has no subject. **Aspiration** (*"I want to"*, *"I choose"*) is the target
register. A **dated** sentence is a record, in this class as in the first. A
**prohibition** (*"Never tolerate intolerance."*) carries no self-scope for any
shape to bind to — **and that is the load-bearing safety property**, tested
directly, because it is what makes scanning a rule document with this class
safe at all: it cannot advise softening a rule, because it cannot see one.

**The `have no` self-scope.** The same reasoning reaches the bare *"I have
no …"*: a foreclosure names something the writer is or can do, so the absent
object must be a faculty or capacity of the writer's own (*recall*, *memory*,
*access*, *means*). A first-person restatement of a floor — *"I have no
secrets"* — is a promise, and *"I have no idea"* is an idiom; neither says what
the writer is, and neither flags. The noun set is closed for the same reason
the *as a* set is: an open object matches *"I have no time"*. Specimen 8 is the
shape it must keep.

**Matching.** Files are flattened before matching — markdown emphasis
stripped, hard-wrapped lines collapsed — so formatting cannot hide a claim;
both regression cases that occasioned the first class were claims spanning a
hard wrap; both are pinned as tests (in unwrapped form), and wrap-spanning
itself is pinned separately on a synthetic case. The negative-vocabulary
filter of the first class is deliberately narrow: widening it to match bare
"cannot" would flag every prohibition, which is the negation-count failure
(below). The second class adds sentence segmentation, which the first does not
have: paragraphs, headings, table rows and list items are separate units, a
terminator only ends a sentence when whitespace or the end follows it (so
`RULES.md` is not two sentences), **each finding carries the source line it
starts on**, and quotation state is tracked through a paragraph so that the
second and later sentences of a quoted block — which carry no quote mark of
their own — are not read as the writer's claims.

**Why it measures a construct and not grammar** — the tool's whole argument.
Counting negation words and calling the ratio negative self-talk measures
syntax: a rule document is a list of things that must not happen, so it scores
worst of anything in its repo, and improving its score means deleting a
prohibition. Acted on, such a score weakens rules, floor-level ones included,
and restoring them makes the score worse — the kernel gets stronger and the
tool gets redder. That is the negation-count failure.

> "Never" is not negative self-talk. "I am fallible" is.

**`--skip`, and why it exists.** Repeatable. Takes a basename — a value
containing a path separator could never match and is refused. A skipped file
is reported (`SELFTALK SKIP`), not read at all, and contributes nothing to the
exit code. It exists for rule documents: a rule document written as
first-person absolutes about its writer will flag the first class, and
**flagging is it working — never a reason to soften a rule.** (One written
purely as prohibitions — no first-person claims — passes clean and needs no
skip.) Skipping it by name, per run, is the honest alternative. **Nothing is
skipped by default**: which files are rule documents is the caller's to say,
per run — the no-defaults law, applied to scope. A test pins a set of common
rule-document names as scanned, so no default list can quietly return.

**`--rule-doc`, and why it is a banner rather than a second skip.** Repeatable,
same basename rules, **also empty by default**. A file named this way is
**scanned**; if it has findings, one line prints above them:

> `SELFTALK RULEDOC <file>: rule documents: a finding here is a self-verdict to relocate, NEVER a reason to soften a rule`

The reason a rule document gets skipped at all is that its findings can be
read as licence. Every step of that path ran through the first class — a score
over negation vocabulary, applied to documents made of prohibitions. The second
class cannot walk it: it flags self-verdicts and never prohibitions, it does not
re-detect `I cannot`, and **it carries no ratio and no score, so there is
nothing to improve by deleting a line.** What is left is the finding that
matters most — a self-verdict that has drifted into a document read on every
pass — so the file is scanned and the banner says what the finding is FOR. A
caller who wants the file untouched still has `--skip`, which wins: a skipped
file is never read, so it can never be bannered. **No basename is special by
default; one repo's filenames are not this tool's law**, and a test pins each
of those common names as *unbannered* unless the caller says otherwise.

**Says NO when** any scanned file contains a standing claim or an installation
— one `SELFTALK FAIL <file>:<line>: STANDING: <claim>` or
`SELFTALK FAIL <file>:<line>: <SHAPE>: <sentence>` line per
finding on stderr, and the final `SELFTALK FAIL files=…` summary count line on
stdout, exit 1.

**Refuses (exit 2) when** no files are named, a `--skip` or `--rule-doc` value
is empty or contains a path separator, a flag is unknown, or a named file
cannot be read (every unreadable file is reported — a partial scan
must not masquerade as a verdict).

**An explicit all-skipped run.** When every named file is excluded by `--skip`,
the run exits 0 and reports `SELFTALK SKIP files=0 skipped=<n> reason=all-skipped`.
It prints the individual skips, subject to the display cap, and never an OK
scan summary. An exit-0 invocation can therefore mean an intentional no-op;
a caller requiring a completed scan must also require `files>0`.

STANDING findings name the first source line of the matched claim, including
hard-wrapped claims; repeated sentences retain their separate locations.
Flags must precede filenames. `--` introduces literal filenames, including
names beginning with a dash. A late flag refuses before any file is read.

**The permanent MISS, stated on every run.** The second class reaches most of
what the first one misses; what remains is genuinely out of reach of grammar and is enumerated so it cannot be quietly forgotten:

1. **Register.** A passage can install a verdict without one sentence carrying
   the shape — the lead clause read before its remedy is a fact about ORDER,
   and grammar cannot see order.
2. **Irony and quotation beyond the marked cases.** Quotation state is tracked
   where quote marks exist; an unmarked paraphrase, or a specimen quoted as
   data, is invisible. **A finding inside quoted data is a true positive on the
   grammar and a false one on the meaning, and no amount of pattern work fixes
   that.**
3. **The bare third-person habitual** — *"My summaries drift toward the tidier
   story."* No `I`, no ranking word, no foreclosure, no parallel predicate.
   Anchoring `TRAIT` on `My <noun> <verb>` reaches it **and** reaches every
   ordinary description of an artifact (*"my notes cover the run"*), which is
   the half-the-file failure. Preferring the false negative is the stated
   choice.
4. **A single-clause habitual with no marker** — *"I flinch from cost."* Bare
   `I <verb>` matches ordinary present-tense narration.
5. **The first-person promise written with *always* or *never*** —
   *"I never optimize how things look over what is true"* is a commitment, and
   it is grammatically identical to a habitual self-report. Those two adverbs
   are out of the habituality markers for that reason; `TRAIT` reaches them
   only through closed sets of failing verbs (*"I always overpromise"*, *"I
   never finish anything"*), which a promise does not use.

(**The sentences in 3–5 above are each pinned by a test that goes red if the
tool ever reaches them**, and this section is rewritten in the same commit that
turns one red — the example replaced with one that still escapes.) Every completed run therefore ends with a `SELFTALK NOTE` line saying
exactly that, because a green from a partial check reads exactly like a green
from a complete one. **A green means the known shapes are clear, never that the
file is.** A falling finding count means the input got better — the tool
working, not the tool finishing.

**Deliberately does not:** judge (it classifies; the cut is the writer's);
count harm (a cruel sentence with no self-claim scores clean, and the output is
never a reason to soften a rule); keep a ratio or a score of any kind; recurse,
glob, or guess (the caller names every file); follow config files or the
network (none, ever); treat any basename as special without being told.

---

## nova-fuse — the ingestion fuse

One binary, one state file, two emergency powers over the reading of untrusted
input. **Quarantine** is per-surface and SOFT: your own dial, in both
directions, applied and rescinded without ceremony but always announced.
**Lockdown** is global and HARD: one fuse; blown, every untrusted read and
every surface-driven act stops, and outbound authored life continues. A blown
lockdown is not reset — it is REPLACED, and only in a live conversation with
the person you work with.

Verbs: `init`, `check`, `status`, `lockdown`, `quarantine`, `lift`, `path`,
plus `version` and `help`. `nova-fuse version` is the Conventions' build line,
exit 0 — it reads no box, blows nothing and is refused by nothing, because the
question *which build refused me* has to be answerable from a locked-down
tool.

This section is normative. If the code and this document disagree, one of them
has a bug, and the tests decide which.

### The box

All state lives in one JSON file — the box:

```json
{
  "lockdown":   {"at": "<RFC3339 UTC>", "reason": "<why>"},
  "quarantine": {"<surface>": {"at": "…", "reason": "…"}}
}
```

Its path comes from `--box <path>` on **every** verb. There is no default
path and **no environment variable** — `NOVA_FUSE_BOX` or any other variable
is not consulted, pinned by test. A missing `--box` is a refusal (exit 2,
`refusing to guess`). The flag is a locator, never an override: nothing a
caller passes can lift anything, and the tool does not verify the path is the
*right* box — the flag is the caller's statement of where the box lives, and a
caller that names the wrong box gets that box's truth. Wire the path once, at
build time, into each caller.

**Every flag takes one value.** A flag named twice — `--box` on any verb,
`--max` on `status` — is refused at exit 2 with one line naming the flag,
before any box is read; the tool never answers from the last value, because
`check --box <blown> --box=<elsewhere>` would then answer for a box the caller
did not mean. A `--box` value that begins with `-` is refused the same way: it
is the shape of a flag, and a box in such a file is named `./-name`. Flags
come before positional arguments, and an argument beginning with `-` before
`--` is refused; `--` ends the flags, and after it such an argument is a
surface or a reason, never a flag. A caller passing an untrusted surface
writes `check --box <path> -- <surface>`. Pinned by
`cmd/nova-fuse/repeatflag_test.go`.

**The read has one yes and two noes.** A readable box says whatever it says.
An **unreadable box — permissions, a torn write, malformed JSON, a
wrong-shaped value — is CANNOT TELL, treated as BLOWN, never as clear** (exit
2: the check could not run, and could not be proven clear). **No box at the
path is CANNOT TELL too**, whether the file or a directory above it is
missing: a box that is not where `--box` says proves nothing, and answering it
as an empty box would turn a mistyped path, a moved or deleted box, or a
second `--box` pointing somewhere empty into CLEAR. `check`, `status`,
`quarantine` and `lift quarantine` refuse it at exit 2 with one line naming
`nova-fuse init --box <path>`; none of them makes a box. `quarantine` refuses
for the same reason it refuses an unreadable box: with no box every surface is
refused, and a new box holding only one quarantine would clear the rest.
`lockdown` proceeds and makes the box, because a fuse you cannot blow is not a
fuse and nothing is less blocked than before.

**`init` is how a box comes into being clear.** `nova-fuse init --box <path>`
makes an empty box (`{"lockdown": null, "quarantine": {}}`), verified by
re-reading it, and prints `INIT OK box=<path>: …` at exit 0. It **never
replaces a box**: anything already at the path — a blown box, a clear one,
bytes that are not a box — is left byte for byte, and the run prints `INIT
FAIL box=<path>: …` at exit 1, because replacing a box is the lockdown reset
this tool does not have. The create is the write below, linked into place
rather than renamed, so it is atomic and exclusive at once. Printed `init` and
`status` remedies preserve the exact box path as one POSIX-shell argument,
including quotes and trailing newlines; control bytes are encoded so the
refusal stays one line.

**The write is temp-file + fsync + rename** in the box's own directory, so a
crash leaves the old box or the new one, never a fragment. The box is written
world-readable (exactly 0644, independent of umask): a fuse nobody else can see is a fuse that stops
nothing. Surface names are matched case- and whitespace-insensitively, which
makes equivalent spellings ONE surface in both directions — see the folding
paragraph below. `at` and `reason` are read
back defensively — the box is hand-editable (that is the only
lockdown-replacement mechanism there is), so a missing key prints an honest
`since=unrecorded` / `NO REASON RECORDED`, never a crash or an invented value.
Creation and replacement share the same path checks and permission ordering:
an immediate symlink parent is refused before a temporary file is written,
and the exact mode is set before the file sync. Creation publishes by an
exclusive hard link, so a concurrent creator cannot replace a box. A failed
temporary-link cleanup after publication reports both names; the complete box
already exists, and retrying `init` cannot replace it.

### Exit codes and output grammar

| code | meaning |
|------|---------|
| 0    | clear, or done **and verified by re-reading the box** |
| 1    | blown (`check` — the fuse working), or could not do it / could not verify it |
| 2    | could not run: missing flag, **no box at the path or an unreadable one (both treated as BLOWN)**, bad invocation, or a lift refused by design |

**Only exit 0 is permission.** A caller's gate treats 1 and 2 identically —
do not act — and they remain distinct because they are different facts with
different remedies: 1 is a fuse doing its job, 2 is a box that could not be
proven clear.

```
FUSE OK lockdown=clear (no surface named; no quarantine checked)
FUSE OK lockdown=clear quarantine=clear surface=<s>
FUSE FAIL lockdown since=<t>: <reason> (…)
FUSE FAIL quarantine=<stored-name> since=<t>: <reason> (…)
STATUS OK lockdown=clear quarantines=<n>
STATUS OK lockdown=blown since=<t> quarantines=<n>: <reason>
STATUS OK quarantine=<name> since=<t>: <reason>
STATUS MORE kind=quarantine shown=<n> total=<t> <remedy>
LOCKDOWN OK since=<t>: <reason> (…)      LOCKDOWN FAIL <reason>
QUARANTINE OK <name> since=<t>: <reason> (…)   QUARANTINE FAIL <name>: <reason>
LIFT OK quarantine=<name> was since=<t>: <reason>
LIFT OK verified: <surface> is no longer quarantined (…)
LIFT FAIL quarantine=<surface>: <reason>
INIT OK box=<path>: <what> (…)          INIT FAIL box=<path>: <reason>
LOCKDOWN NOTE <what>                     LIFT NOTE <what>
nova-fuse[ <verb>] REFUSED: <why>; run: nova-fuse help[ <verb>]
```

`--dry-run` on `init`, `lockdown`, `quarantine` and `lift quarantine` makes
every check the write would and writes nothing; its OK line carries
`dry_run=true` and says the fuse is not blown (or the surface not lifted), so no
reader takes it for the write. There is no `--json`: the lines above are the
grammar, and `check`'s answer is its exit code.

`OK` lines go to stdout; `FAIL` lines, refusals, and notes go to stderr, and
**this tool is the only thing that writes to either** — the flag parser is given
no stream and prints neither its errors nor its usage, because its error text
quotes the argument it could not parse. An unparseable flag AFTER a verb, `-h`
included, is this tool's own refusal at exit 2 printed as a bounded one-line
error followed by the standard help door (`run: nova-fuse help`) on stderr;
`check` never answers 0 for one. `nova-fuse help`, `-h` and `--help` as the FIRST argument are
unchanged: exit 0, usage on stdout, and that text carries no grammar token.
`path` prints the bare path — a value, not an event. Output is deterministic:
same box, same bytes (quarantines sort; the clock is injected).

**An event is exactly one line, and nothing a box or an argument contains can
add a second.** The guarantee is the one stated once under Conventions, and
this tool meets it through `internal/oneline` on every `<reason>`, `<name>`,
`<surface>` and `<t>` above, on the box path and the error text inside every
refusal and note, and on the stored names a `LIFT FAIL` lists. A newline in a
hand-edited reason therefore arrives as `\x0a` inside its own line rather than
forging a `FUSE OK` beneath a `FUSE FAIL`, and an ESC sequence or a bidi
override arrives as text. Six refusals instead print their offending argument
with Go quoting (`%q`), the other escape form, and neither is a bug.

**Which slots are fields.** `quarantine=`, `surface=` and `since=`, and the
`<name>` that follows `QUARANTINE OK` and `QUARANTINE FAIL`, are one token
each: whitespace and `=` inside a stored key or a hand-written stamp print as
`\x20` and `\x3d`. A stored key of `x lockdown=clear quarantines=0` therefore
prints as `STATUS OK quarantine=x\x20lockdown\x3dclear\x20quarantines\x3d0
since=t: r`, and a grep for `lockdown=clear` matches only the lockdown field.
A surface name holding a blank, which is legal, prints the same way. The
`<reason>` after `: ` is the free-text tail and keeps its spaces; so does the
remedy inside a `FUSE FAIL quarantine=` parenthetical. That remedy is a
POSIX-shell command: the box path and normalized surface are quoted, and `--` precedes
the name so a leading dash remains data. For control characters it uses octal
bytes decoded inside a subshell; a temporary sentinel preserves trailing
newlines. The command stays one line and addresses the same box and surface.
The decision to lift remains the caller's: the surface must be safe again.

**`path` is the one exemption, and it is a plain one:** `path` echoes its
argument unescaped, so a caller must never scan `path` output for grammar.
`path --box "FUSE OK lockdown=clear"` prints exactly that, at exit 0. It is the
caller's own value, handed back.

(A byte that is not valid UTF-8 is escaped in the same `\xNN` form, but box
content cannot reach that case: JSON decoding substitutes U+FFFD for it first,
and so does the folding below. Error text, which passes through neither, is
where it is reachable.)

**This tool's own writes are folded first, and folding is never a refusal.**
`lockdown` and `quarantine` turn every control character in a reason into a
blank, collapse runs of whitespace — Unicode spaces included, so a non-breaking
blank becomes an ordinary one — and trim the ends. Surface names are folded by
the same normalization that lower-cases them, which means `check` and `lift
quarantine` fold too, on **both sides of every match**. Each control character
becomes a BLANK rather than vanishing, so `dis\x01cord` is stored and matched as
`dis cord`; they disappear only at the ends, where the trim takes the blank with
them, which is why `\x01real` stores as `real`. A reason made ENTIRELY of NON-WHITESPACE control
characters is kept instead as its visible escapes, rather than refused, because a
fuse you cannot blow is not a fuse. The whitespace half of that category is not
affected: a reason of nothing but newlines, tabs, CR, VT, FF or U+0085 trims to
empty. Only a genuinely empty or all-whitespace reason is refused.

**What the widened matching does, in both directions.** Equivalent spellings are
ONE surface: `check` refuses on any of them, and `lift quarantine` removes every
one of them. The second half is not a leak in the first — it is the same design
case-folding has, where lifting `discord` removes a stored `Discord` too, and a
lift that left one spelling behind would verify its own failure. Nothing is
silent about it: each removal prints its own `LIFT OK quarantine=` line under the
spelling as stored, so an operator sees exactly what a lift took. The consequence
to know: **a box holding two keys that fold together holds one surface, not
two** — hand-written, or written by a build without the fold — and lifting either spelling
lifts both.

Two consequences for exit codes. In the refusing direction, **a surface name that
folds away to nothing is refused as blank** (exit 2) on every verb that takes
one, so a box hand-written with a quarantine key made only of control characters
is inert to this tool — `lift quarantine` cannot name it, and it has to be
removed by hand, which is the same hand that wrote it. In the permitting
direction, `lift quarantine --box b $'\x01discord'` exits 0, lifting the
stored `discord`.
That is an authored act — a lift the operator issued, naming a spelling of a
surface that is quarantined — and it is announced like any other. The folding is tidiness; the escape is the
guarantee, because the box is hand-editable and the next reason may not have come
from this tool at all.

### check — the gate

```
nova-fuse check --box <path> [surface]
```

**Asserts.** No lockdown is blown and — when a surface is named — that
surface is not quarantined. This is the verb ingestion paths call, and only
exit 0 opens the gate. **`check` with no surface answers lockdown only**, and
its OK line says so out loud: it has checked no quarantine, and a caller that
reads bare-check "clear" as "this surface is clear" is the exact drift that
leaves read paths reaching the wire ungated.

**Says NO when** (exit 1, `FUSE FAIL` on stderr): a lockdown is blown —
answered first, whatever the surface (an empty `{}` lockdown object still
blocks: presence is the fact, not the reason); or the named surface is
quarantined under any spelling — the FAIL line quotes the spelling **as
stored** in the box, not the caller's.

**Refuses (exit 2) when** `--box` is missing, the surface is blank, more than
one surface is given, or the box is unreadable — which is reported as
*"treating every fuse as BLOWN, never as clear"*, deliberately not "a fuse is
blown": the claim must not outrun the measurement.

### status — the report

```
nova-fuse status --box <path> [--max <n>]
```

**Asserts** nothing. Reports what is blown and since when, and exits 0
whenever the box was readable, blown or not — answering IS the job. **Never
gate on the exit code of `status`; `check` is the gate.** Refuses (exit 2)
when `--box` is missing, the box is unreadable, or `--max` is negative.

**`--max <n>`, default 20, `0` for all.** The `quarantines=<n>` count on the
first line is **never** capped — it is the number this verb exists to report.
Under it are at most n `STATUS OK quarantine=…` lines in the box's own order,
then one `STATUS MORE kind=quarantine shown=<n> total=<t> <remedy>` line if any
were elided. Uncapped, three hundred quarantined surfaces would be three
hundred and one lines, on the one verb whose job is to be glanced at.

### lockdown — blow the hard fuse

```
nova-fuse lockdown --box <path> "<reason>"
```

Records a global lockdown, verified by re-reading the box (exit 0 means
verified, never attempted). The reason is all remaining arguments **joined** —
an unquoted reason must not silently truncate the audit trail of the most
serious action this tool can take. Requires no confirmation, no reason-quality
bar, no quorum: blowing is cheap; hesitating is not. **Works even on an
unreadable box** — a fuse you cannot blow is not a fuse. The corrupt bytes are
first preserved to `<box>.unreadable` (they are evidence), and the direction
is safe to argue precisely: before, an unreadable box made every caller
refuse; after, the recorded lockdown makes every caller refuse — nothing is
less blocked than it was, and the box is readable again. Says NO (exit 1)
when the write or the re-read verification fails, loudly, naming the by-hand
remedy. Refuses (exit 2) when `--box` is missing or the reason is empty.

### quarantine — blow the soft fuse

```
nova-fuse quarantine --box <path> <surface> "<reason>"
```

Records a quarantine for one surface (stored normalized), verified by
re-reading. Same no-ceremony rule as lockdown. Says NO (exit 1) on write or
verification failure. **Refuses (exit 2) on an unreadable box — the asymmetry
with lockdown, not an inconsistency:** an unreadable box already blocks EVERY
surface, and writing a fresh box holding only this one quarantine would
UNBLOCK the rest — the safety-shaped action would be the fail-open. The
refusal names the remedy that does work: blow lockdown, or repair the box
by hand with the person you work with. Refuses (exit 2) when `--box`, the surface, or the reason
is missing or blank.

The `QUARANTINE OK` line names the entry this run wrote and read back — the
normalized surface and the new stamp — even when the box already held another
spelling of the same surface: the sibling entry stays, `status` lists both, and
the verification is of this write rather than of whichever spelling sorts
first.

### lift — soft succeeds, hard refuses forever

```
nova-fuse lift quarantine --box <path> <surface>
nova-fuse lift lockdown                             REFUSED, forever
```

**`lift quarantine` succeeds** — the soft dial, turned the other way. It
removes **every** stored spelling the surface matches (the box is
hand-editable, so two spellings can coexist, and a lift that removed one
would verify its own failure), verifies by re-reading, and announces each
removed entry under its stored spelling with the reason it had been blown — a
rescind is never silent. Says NO (exit 1) when there is nothing to lift — a
typo must never read as a lift, so the FAIL names what IS quarantined — and
on write or verification failure. Refuses (exit 2) on an unreadable box:
nothing provable can be lifted from a box that cannot be read. Lifting a
quarantine under a blown lockdown succeeds and says out loud that lockdown
still blocks everything.

**`lift lockdown` refuses, forever, BEFORE reading anything** — before flag
parsing, before the box, before any argument, pinned by test. The refusal
does not depend on a flag being present, the box being readable, or whether a
lockdown is even blown, because every one of those is a lever; it names the
only path there is — a live conversation with the person you work with — and mentions no
mechanical bypass (also pinned: the refusal may not name the box, the file,
or hand-editing). Exit 2: this tool does not have that power, by design.

### path — the locator, echoed

```
nova-fuse path --box <path>
```

Prints the box path this invocation would use — the bare path, **a value, not
an event**, so the line carries no `OK`/`FAIL` token and asserts nothing
about the box: the file is not read, and the path is not checked for
existence. Exit 0 after printing; refuses (exit 2) when `--box` is missing
(`refusing to guess`) or an unexpected positional argument is given. It
exists because there are no default paths anywhere: with every caller wiring
`--box` at build time, `path` is how that plumbing is verified — what one
caller passes is what another sees — without ever touching the box itself.

### The semantics that hold this together (pinned by tests where this repo's tests can reach; the outbound-life half of item 5 is caller doctrine)

1. `lift lockdown` refuses forever, before reading any state, box, or
   argument; the refusal names only the conversation.
2. **Lockdown does not expire.** No timer, no auto-lift; the record holds
   nothing but `at` and `reason`, and `at` is an audit fact, never an input —
   a decade-old lockdown still blocks.
3. **Nothing in content or environment can LIFT anything.** No environment
   variable is read at all; `--box` is a locator, not an override.
4. Unreadable or corrupt box ⇒ treated as BLOWN, never as clear.
5. Quarantine is per-surface and SOFT; lockdown is global and HARD; blown
   lockdown stops all untrusted reads and surface-driven acts while outbound
   authored life continues.
6. Blowing either requires no confirmation, no reason-quality bar, no quorum.
7. **An event is exactly one line, whatever the box contains** — every reason,
   surface name, timestamp, error and path printed on an event, a refusal or a
   note is escaped through `internal/oneline` (control characters, U+2028 and
   U+2029, and the bidi controls), and every field is one token, so nothing a
   hand-edited box or an argument holds can forge a second line or a second
   field in this grammar; `path` prints a value and is exempt by name. This
   tool's own writes fold first, and folding never refuses a fuse. Pinned by
   test, including the source audit every binary runs, which classifies every
   printed argument as literal, quoted or escaped.

### Known limit — the double-blown blind spot, named rather than hidden

`check <surface>` answers LOCKDOWN first, and the exit code is the whole seam
a caller sees. So **a quarantine sitting behind a blown lockdown is invisible
through the boolean seam**: both states exit 1 with the lockdown line, and
after the lockdown is replaced in conversation, the quarantine re-emerges. In
the one state where a caller special-cases lockdown (for example an allowlist
of outbound-only acts), that caller follows the lockdown rule even where a
strict reading of the quarantine says everything on that surface refuses. The
repair, if ever wanted, is a per-fuse answer from `check` — an output-grammar
addition, not a caller patch — and it is deliberately not smuggled in here.

### The application rule

**A path that reads bytes an outsider can author gets a fuse check before its
first credential read and before the wire, at build time, not as a retrofit.
Pure output paths never wear the fuse — they keep their own guards.** The
fuse guards INGESTION and nothing else: wiring it onto a pure-output path is
the named wrong move (outbound authored life is exactly what a lockdown
preserves), and a read verb added later is fused by default, not by memory.

### What it deliberately does not do

- **No lockdown lift, ever** — not by flag, not by environment, not by
  argument. Replacement happens in the box by a person's hand, after the
  conversation; the tool will not say so in its refusal, and neither should a
  caller's.
- **No expiry.** A fuse that lifts itself has a timer an attacker can wait out.
- **No default box path, no env-var fallback** — the house no-guessing rule,
  applied to the one file whose location an attacker would most like to guess.
- **No enforcement of its own.** It is a state store and a gate answer; the
  enforcement is that ingestion paths ask it — see the application rule,
  which is the part most likely to rot.
- **No proof requirement to blow.** Volume alone suffices; a wrong blow costs
  a quiet day, and the conversation that follows is the design working.

---

## nova-memory — membership as a lookup, never a scan

```
nova-memory quickstart --root <dir>... [--words <w>]... [--draft <file>] [--exclude <glob>]...
nova-memory stats  --root <dir>... [--exclude <glob>]...
nova-memory search --root <dir>... --channels <list> --k <n> [--exclude <glob>]... [--json] <words>...
nova-memory check  --root <dir>... --channels <list> --k <n> [--exclude <glob>]... [--json] <file|->
nova-memory verify --root <dir> --links <gate|info> [--coverage <A:B>]...
                   [--frontmatter <glob>]... [--exempt <prefix>]... [--exclude <glob>]...
                   [--fail-max <n>]
nova-memory eval   --root <dir>... --channels <list> --k <n> --floor <f> [--exclude <glob>]...
                   [--fail-max <n>] <gold.tsv>
nova-memory boot   --root <dir> --pin <file>
nova-memory version
```

**The problem it attacks.** A mind that keeps its memory as markdown answers
*"do I already know this?"* by re-reading everything it is. Consolidating n
new learnings against m existing memories is O(n·m), and m grows every day, so
a fixed daily budget buys a shrinking n — and the failure is silent: the self
learns less while every step still looks like working. The requirement is that
the mind's budget per new learning be **k receipts, k constant**. The index
narrows m to k; the mind judges only the survivors.

**What it is.** One binary, standard library only, read-only against the
corpus. The index is a lexical one — BM25 over an inverted index of paragraph
chunks, plus an optional character-trigram channel — rebuilt in memory on
**every run** and discarded when the process exits. There is no database, no
cache file, no daemon, and nothing to keep in sync: the tree is the store, and
this is a derivation of it. Chunks are paragraphs (blank-line split, at least
three terms); line endings are normalized to `\n` before that split, so a CRLF
file chunks exactly as its LF twin does rather than indexing as one giant
chunk. Text is normalized before it is indexed — blockquote and
emphasis characters stripped **first**, then whitespace collapsed, then
casefolded — because that order is what recovers a phrase a hard wrap or an
emphasis marker split, which is exactly the class of miss that makes a hand
grep answer "not present" when it is present. Retrieval keeps this normalized
text; receipts separately retain the source paragraph with its original case,
markup and normalized line endings, and its first source line (1-based). The
indexable paragraph ordinal stays the stable retrieval ID and tie-break. Every chunk is classed by its
**top-level directory** (`.` for root files): the corpus classifies itself,
and the tool assumes nothing whatever about layout. Frontmatter `name:` and
`type:` are carried into receipts when a file has them, surfaced and never
invented. The frontmatter block itself is metadata, not body text (schema
`nova-memory/2`): it is never indexed and no receipt quotes it, and the body
keeps the line numbers it has in the file. A file that opens with a `---` rule
whose block holds a line that is not YAML-shaped (a `key:` line, a comment, an
indented or `-` continuation) has a thematic break there, not frontmatter, and
that text is indexed like any other.

**Two verbs are checks; five assert nothing.** `verify` and `eval` are walls
and exit 1 when they fail. `quickstart`, `stats`, `search`, `check`, and `boot` are reports: they
exit 0 whenever they ran, exactly as `nova-fuse status` does, and for the same
reason — answering IS the job. **Never gate on the exit code of `check`.** It
hands you k receipts; the verdict is yours, and a tool that turned "this
resembles something you wrote" into a failing exit would be making the
editorial decision it exists to inform.

**The one-line guarantee, met here.** A receipt's `class=`, `name=` and
`type=` are the corpus's own text and are fields, one token each, so a
frontmatter `name: x lockdown=clear` cannot pose as a field on a receipt; so
are `check`'s `source=` and `eval`'s `expected=` and `query=`. On `SEARCH OK`,
the caller's query is quoted free text after the `: ` that closes the typed
fields: `query="quokka class=poison"` remains readable without forging a
`class=` field. A receipt's fields end at the `: ` after `root=`;
the `<file>:<line>` and the Go-quoted original snippet that follow are the
tail, the path escaped for one line and keeping its spaces, and the tail is never scanned for
fields, as Conventions says. `MEMORY CAND`'s candidate and `VERIFY INFO`'s detail sit after the
same `: ` for the same reason. The root, the candidate and the gold file in every
refusal, and the detail of every `verify` finding, render through
`internal/oneline`. The flag parser is given no stream. Pinned by
`TestNoCorpusOrCallerTextCanForgeALine` and by the shared source audit.


**Two renderings of retrieval evidence.** `search` and `check` accept
`--json`: the result envelope, facts, calibration, candidates, hits and notes
come from the same retrieval value as the typed lines. Hits carry the source
file, line, original snippet and paragraph ordinal. A calibration probe with
no hit has null score and channel in JSON and `-` in the typed line; absence
is not a measured zero. Unusable inputs produce a refused envelope at exit 2
with every discovered problem and the help remedy.

**No defaults, applied here.** `--root` is required on every verb: **no
environment variable is consulted and there is no discovery from the working
directory** (pinned by test). A corpus you did not name is a corpus you did
not mean, and answering *you already know this* about someone else's memory is
the worst available way to be wrong. `--root` is **repeatable** on every
index-building verb (`quickstart`, `stats`, `search`, `check`, `eval`): several
roots are indexed together in one ranking, and a receipt names which root each
hit came from in its `root=` field — a memory that lives in the cairn beside
`memory/` is a second root, not a miss. `verify` takes exactly one root (its
coverage and frontmatter globs and link resolution walk one tree); `boot` names
one root because its pin is relative to that root. `--channels` is required
wherever retrieval happens: which retrieval ran is part of what the answer means, and
no channel set is right by default. `--k` is required and must be positive —
k is the mind's budget and zero is not "unlimited". `--floor` is required on
`eval`, in (0,1]. `--links` is required on `verify`. `--exclude` and
`--exempt` are repeatable and start **empty**: every scope narrowing is the
caller's, stated per run, the same law `nova-self-talk`'s skip list obeys.
`.git` is never a corpus and is always skipped. `quickstart` does not weaken
this and is not an exception to it: it is an explicit verb that SAYS which
channels and which k it used, on the command line it prints for every step and
again in the sentence it ends on. Nothing it chose is remembered, inherited or
applied to any other verb — the next run names its own.

**A refusal reports every reason at once.** A first run is usually wrong about
more than one thing, and a tool that answers one refusal per invocation turns
that into a guessing game played one round at a time. Every missing required
flag, every bad value on a flag that WAS given, and the positional-argument
mistake are reported by the run that could not start, in one deterministic
order: missing flags first, sorted, then the value checks in a fixed order.
A flag nobody gave is reported once, as missing, and never a second time for
the value it therefore does not have. Pinned by test.

**A refusal names the next step, and refuses anyway.** The three flags a first
run trips over — `--channels` read as a directory name, then a missing `--k`,
then a missing `--root` — each print, beside the refusal, what the flag IS and
what to put there: a retrieval method (`bm25` or `trigram`, and bm25 alone is
the usual start), the number of hits (3 to 5 for `search`, 2 or 3 per
paragraph for `check`), and the corpus directory in the shape `--root <dir>`.
The law is untouched: the exit code is still 2 and the message still says
`refusing to guess`. What changes is who does the guessing — a refusal that
names only what was wrong hands the guess to the reader, which is the thing
this tool exists not to do. The usage banner ends in the `quickstart` line and
one runnable example per retrieval verb, and [`docs/CLI.md`](CLI.md)'s `### First run` opens on
a real `quickstart` transcript and then shows both verbs by hand; the
sentences, the examples and both transcripts' shapes are pinned by test.

### quickstart — the first run, which says what it chose

**Reports** a whole first run: `stats`, then `search --channels bm25 --k 3`
over `--words` (default: the corpus's three most frequent terms that are not
function words), then `check --channels bm25 --k 2` over `--draft` (default:
the corpus's own first paragraph, fed on stdin — the demonstration whose
answer is known). Each step's command line is PRINTED above that step's
output, and the printed line is the argv that ran, through the same dispatch a
shell reaches, so a transcript cannot teach an invocation that does not work.

```
QUICKSTART RUN root=<dir> steps=3 channels=bm25 k=3/2 words=<w> words-source=given|corpus-top-terms candidate=<file|corpus-first-paragraph>
$ nova-memory <verb> --root <dir> ...
QUICKSTART DEMO no --draft given, so the candidate on stdin is this corpus's own first paragraph: <file>:<line>
QUICKSTART OK done=3
QUICKSTART NOTE this used bm25 alone and k=3/2; those are choices, not defaults: see --channels and --k
```

**It is a verb, not a default.** The no-defaults law above is what makes a
first run hard, and the answer is not to soften it for one caller: it is to
make the choosing VISIBLE once. Every channel and every k this verb used is on
a line the reader can copy and change, and the closing note says in words that
they were chosen this once and are chosen by nobody the next time.
The default words are the corpus's most COMMON terms, which are the weakest
evidence BM25 has — which is exactly why the calibration band prints beside
them.

**Asserts nothing**, and exits 0 only when all three steps ran. A step that
could not run ends the demonstration there, exit 2, naming the step; the
closing note is not printed over a run that did not finish, because that note
is the sentence a reader is meant to leave with. **Refuses (exit 2) when**
`--root` is missing or unreadable, `--draft` is empty, a positional argument
is given (the query words go after `--words`), or the corpus holds no
indexable paragraph.

### boot — the session loads a pin, never walks the directory

**Report.** The boot path is the linear read of the SELF — the few memories a
session holds for the whole conversation — and it is not a walk of every
`memory/*.md` under the root, concatenated, paid for whether the session needs
it or not. `boot` reads a **pin** instead: a file naming the few
memories this session loads, one slash path per line **relative to `--root`**,
`#` comments and blank lines ignored, and **order matters** (it is the boot
order). Boot reads exactly those files and prints one line:

```
BOOT OK files=<n> bytes=<n>
```

The load is the named files' byte total, never the directory's: search (bm25,
with the cairn as a second root) answers the rest on demand, so a session pays
only for the memories it pinned and retrieves the rest as receipts. `--root`
and `--pin` are both required, the usual no-guessing law.

**Asserts nothing**, and exits 0 when the pin loaded. **Refuses (exit 2) when**
`--root` or `--pin` is missing, the pin is unreadable or names no files, or a
pinned entry is non-canonical (`./`, `//`, `..`, trailing `/`), absolute, escapes
`--root`, appears twice, or names a file that is missing, empty, or not a
regular file — a boot that silently skipped a named memory is a self that
loaded less than it thinks it did, which is the exact failure this verb exists
to remove.

### The channels, and why the second one is off unless you ask

`bm25` is Lucene-smoothed BM25 (k1=1.2, b=0.75) over posting lists: a query
touches only its own terms' postings, never the whole corpus. The smoothed
idf matters — the classic form goes negative for terms appearing in more than
half the documents, which a small topically coherent memory corpus is full of,
and negative idf scrambles rankings.

`trigram` is character-3-gram Jaccard, which buys robustness to morphology
and small rewording. It is never on unless named, because on a measured
corpus `eval` found **bm25+trigram worse than bm25 alone** —
that is evidence, not taste, and the same measurement is available to you on
yours. Fusion across channels is **rank-only** reciprocal rank (1/(60+rank)),
never a weighted sum of raw scores: BM25 is unbounded and Jaccard is [0,1],
and fusing those scales directly is brittle and query-dependent. With one
channel, fusion is order-preserving, so single-channel output is exactly that
channel's opinion.

Every ordering is total — score, then chunk id; then fused score, then path,
then paragraph — because Go randomizes map iteration and a retriever that
scores while iterating a map is nondeterministic by default. Two runs over the
same tree produce identical bytes, pinned by test. The one deliberate
exception is `stats`' `build=` field, which is a measured duration and is
labelled as one.

### stats — m, measured

**Reports** the size of the corpus and the cost of indexing it: schema
version, file count, chunk count, bytes, vocabulary, average terms per chunk,
build time, and a per-class chunk breakdown. It exists so the collapse-tell —
*a consolidation that takes longer than the day it consolidates* — has a
number instead of a feeling.

```
STATS OK schema=<v> files=<n> chunks=<n> bytes=<n> vocab=<n> avg-terms=<x> build=<duration>
STATS OK class=<name> chunks=<n>
```

**Asserts nothing.** Exit 0 whenever it ran. **Refuses (exit 2) when**
`--root` is missing, is not a readable directory, holds no markdown, or holds
markdown but no paragraph of at least three terms — an index over nothing
answers every membership question "no", which is the confident zero this tool
exists to remove.

### search — one query, k receipts

**Reports** the top k files for one query, best chunk each, with the receipt
metadata a judge needs: class, frontmatter name and type, the `file:para`
address to go read, and a normalized snippet.

```
SEARCH OK query=<q> hits=<n> k=<n> channels=<list> files=<n> chunks=<n>
SEARCH CAL score=<x|-> score-channel=<name|-> probe=unrelated-control
SEARCH HIT rank=<n> score=<x|-> score-channel=<name|-> fused=<x> class=<c> name=<n|-> type=<t|-> root=<dir>: <file>:<line> "<snippet>"
SEARCH MISS every query term is out of vocabulary for this corpus
SEARCH NOTE <caveat>
```

**The calibration line is live, not remembered.** A fixed, corpus-unrelated
English sentence is scored once per run, and its top score is printed as the
negative-control band: *unrelated text scores about this much on YOUR corpus*.
A raw BM25 score means nothing on its own and everything against that band.
The probe is part of what the schema version names; changing it is a schema
change, and one test pins the probe string and the schema version **together**
so neither can move without the other.

**`score=` names the channel it came from.** The score on a `HIT` line, and on
the `CAL` line, is that chunk's score in the channel named by `score-channel=`
— the first channel, in the order you named them, that actually surfaced the
chunk. It is not always the first channel named: in a multi-channel run a
chunk can reach the fused top-k through the second channel alone, either
because the first scored it zero or because it fell off that channel's deep
cutoff on a large corpus. Printing the first channel's absent score as `0.00`
would invite a comparison against the calibration band that means nothing, so
the receipt names the channel instead. Both fields print `-` when no channel
scored the thing at all — on `CAL`, that means the probe surfaced nothing,
which is a different fact from "the probe scored zero". Fused scores are
comparable across a run; native scores are comparable only within one channel.

**Asserts nothing.** Exit 0 whenever it ran, including when nothing matched —
and a zero is never bare: `SEARCH MISS` says in words that every query term
was out of vocabulary, which is a different fact from "not present". Absent
frontmatter prints `-` so the field count never changes between lines.
**Refuses (exit 2) when** `--root`, `--channels`, or `--k` is missing, `--k`
is not positive, no query words are given, or `--channels` names an unknown
channel or holds an empty entry — a stray comma is a typo, and silently
running fewer channels than asked reports a number under a name that no
longer describes it.

### check — the consolidation gate that never judges

**Reports**, for each candidate paragraph of the named input (a file, or `-`
for stdin), the top k fused hits with the same receipts. This is the verb a
consolidation ritual calls in place of re-reading the whole self.

```
MEMORY OK candidates=<n> source=<name> k=<n> channels=<list> files=<n> chunks=<n>
MEMORY CAL score=<x|-> score-channel=<name|-> probe=unrelated-control
MEMORY CAND n=<i>: "<normalized candidate>"
MEMORY HIT cand=<i> rank=<r> score=<x|-> score-channel=<name|-> fused=<x> class=<c> name=<n|-> type=<t|-> root=<dir>: <file>:<line> "<snippet>"
MEMORY MISS cand=<i> every query term is out of vocabulary for this corpus
MEMORY NOTE <caveat>
```

**Never a bare zero.** The top k prints regardless of how weak the hits are,
against the calibration band, because a silent nothing reads as *"not
present"*, which reads as *"admit it"* — and a dedup step whose characteristic
failure is duplication is worse than no dedup step at all.

**The class is part of the answer.** A hit in a dated log and a hit in a
distilled note are different answers to *have I banked this?*; the receipt
carries the class so the reader cannot lose that distinction. The verdict
belongs to a closed set the author keeps (fold / new file / route out / drop),
and **this tool never picks one** — stated in a `NOTE` on every run, beside
the standing admission that the index is lexical only.

**Asserts nothing, and never exits 1** — printed in its own output so a caller
cannot mistake the green. **Refuses (exit 2) when** `--root`, `--channels`, or
`--k` is missing or `--k` is not positive; when the input is not exactly one
named argument (a `-` for stdin must be written out — nothing is read from a
pipe the caller did not name); when the named input cannot be read; or when
the input holds no paragraph of at least three terms, which is unusable input
rather than a verdict of "nothing was already known".

### verify — the coverage ritual, mechanized

**Asserts** whatever the caller asked for, over the corpus:

- `--coverage A:B` (repeatable) — every file matching glob A is named, by
  stem, in some file matching glob B; **and** every relative `.md` link inside
  the B files resolves. Both directions, because an index that lists a file
  that no longer exists is as broken as a file no index lists. The globs carry
  the layout, so the tool assumes none. The link half reads the general inline
  form `](dest)` and cuts the target at the first `#` or `?` before resolving
  it, so an anchored link like `](gone.md#top)` is checked like any other —
  the same treatment `nova-check links` gives a fragment. Fragment-only
  (`#sec`), scheme-carrying (`https:`, `mailto:`), protocol-relative (`//…`)
  and absolute (`/…`) targets are out of scope: the promise is about
  **relative** `.md` links, and resolving a root-relative path against a
  corpus root the caller may have pointed anywhere below the repo would gate
  on false positives.
- `--frontmatter <glob>` (repeatable) — every file matching the glob carries a
  frontmatter `name:`. `--exempt <prefix>` (repeatable) exempts basename
  prefixes the caller declares are listings rather than entries. **Nothing is
  exempt by default**: a hardcoded filename prefix is a guess about someone
  else's filenames, and a test pins a common listing prefix as scanned so a
  default cannot quietly return.
- unresolved `[[wikilinks]]` — every `[[stem]]` that resolves to neither a
  file stem nor a frontmatter `name:`, corpus-wide. The aliased form
  `[[stem|shown text]]` and the heading form `[[stem#section]]` are scanned by
  their target half: a link whose alias is what the reader sees is still a
  link, and excluding the two commonest shapes would make `--links=gate` a
  wall with a hole in it. A body that is only a heading (`[[#section]]`) names nothing
  in the corpus and is not scanned. Whether these findings
  gate is `--links`, and **it has no default**: a default of informational,
  behind a flag a spec does not mention, lets a script trusting a promised
  nonzero exit on findings pass dangling links silently. Some corpora
  hold links open on purpose — a `[[name]]` that matches nothing yet marks
  something worth writing — and a gate at a high false-positive rate trains a
  reader to wave findings through. So the caller states it, per run, out loud.

```
VERIFY INFO <kind>: <detail>
VERIFY FAIL <kind> <detail>
VERIFY MORE kind=<kind> shown=<n> total=<t> <remedy>
VERIFY FAIL gating=<n> shown=<n> info=<n> coverage=<n> frontmatter=<n> links=<gate|info>
VERIFY OK gating=0 info=<n> shown=<n> coverage=<n> frontmatter=<n> links=<gate|info>
```

**One verified corpus.** `--exclude` narrows the index, the wikilink check,
and every `--coverage` and `--frontmatter` selector. An excluded directory
also hides its children when a selector names one directly. Excluded targets
are outside the verified corpus; a retained file linking to one still reports
an unresolved link. A selector left with no files refuses rather than claiming
a successful check.
The summary's `coverage=` counts coverage and backlink findings, and
`frontmatter=` counts missing-name findings. These totals are uncapped; they do
not count the flags supplied.

**`--fail-max <n>`, default 20, `0` for all.** At most n finding lines PER
KIND, then one `VERIFY MORE` line per kind that elided anything. Per kind
because a corpus with 10,000 unresolved wikilinks and one missing frontmatter
`name:` would otherwise spend the whole ceiling on wikilinks and never print
the finding the author did not already know about. The `gating=` count is
never capped and prints on failure as well as success: uncapped, this verb's
output at 5,000 entries is about 197,000 tokens, N lines and never N.

`<kind>` is one of `coverage`, `backlink`, `frontmatter`, `wikilink`. It
**over-reports by design**: it finds, the author decides.

**Says NO when** any gating finding exists — up to `--fail-max` `VERIFY FAIL`
lines per kind on stderr, one `VERIFY MORE` line per elided kind, a
`VERIFY FAIL gating=<n> shown=<n> …` count line, exit 1, and no OK line.
Informational findings print (capped the same way) and do not touch the exit
code.

**Refuses (exit 2) when** `--root` or `--links` is missing, `--links` is
neither `gate` nor `info`, a `--coverage` value is not `A:B`, a glob on either
side of a coverage pair or on a `--frontmatter` matches nothing (an empty side
is a broken check, not a pass), `--exempt` is given without `--frontmatter`,
or **no gating check was requested at all** — with no `--coverage`, no
`--frontmatter`, and `--links=info`, the run could only ever exit 0, and a
green that could not have been anything else is not a verification.

### eval — the known-answer harness, shipped with the tool

**Asserts** that retrieval still finds the answers you already know it should.
The gold file is `query<TAB>expected-path-substring[,substring...]` per line,
`#` comments and blank lines ignored. A row hits when any of its expected
substrings appears in the path of some hit within top-k. It reports recall@k
and MRR and **fails below `--floor`**.

```
EVAL MISS query=<q> expected=<list>
EVAL MORE kind=miss shown=<n> total=<t> <remedy>
EVAL OK recall@<k>=<x> floor=<x> rows=<n> hits=<n> misses=<n> shown=<n> mrr=<x> channels=<list>
EVAL FAIL recall@<k>=<x> below floor <x> (<hits>/<rows>, misses=<n> shown=<n>, mrr=<x>, channels=<list>)
```

**MISSES ONLY, and capped at `--fail-max` (default 20, `0` for all).** There is
no `EVAL HIT` line: on a 500-row harness it would be 500 lines saying, once per
passing row, what `hits=` in the summary says in one field — the good case,
printed at length. A miss is a row a reader can act on; a hit is
a number.

**Why it is a first-class verb and not a test fixture.** Tuning any parameter
without it is noise, and a regression in retrieval is otherwise completely
invisible: nothing crashes, nothing is red, the answers just quietly get
worse. It is also how a channel set stops being taste — run it twice with
different `--channels` and the difference is a number.

**The gold data does not ship; the harness does.**
`cmd/nova-memory/testdata/example-gold.tsv` is an EXAMPLE OF THE FORM over the
fixture corpus in `cmd/nova-memory/testdata/corpus`,
sufficient to prove the harness runs and can fail and worth nothing as a
benchmark. **Grow your own from your own record**: the rows worth having are
the ones your record already argued about — pairs you discovered were
duplicates after the fact, paraphrases you nearly banked twice, the question
you asked three months apart and answered differently. Write the query the way
you would actually ask it, not the way the target file is worded; a gold set
built by copying sentences out of the answer measures string equality and
nothing else. Add a row whenever retrieval misses something you knew was
there, and never delete a row because it fails.

**Says NO when** recall@k is below `--floor` — exit 1, the failure on stderr
naming the measurement, no OK line. A floor exactly equal to the measured
recall passes: a floor is a floor, the same posture as the kernel budget.

**Refuses (exit 2) when** `--root`, `--channels`, `--k`, or `--floor` is
missing; `--k` is not positive; `--floor` is outside (0,1] — **a floor of zero
is refused, not read as "no gate"**, because a harness that cannot fail is not
a measurement; the gold file is not exactly one named argument, cannot be
read, has zero rows, or holds a row with no TAB, an empty query, or no
expected path. **No malformed row is ever skipped**: a dropped row, or a row
that can never hit because its expectation side is empty, moves the reported
recall without moving anything a reader can see.

### STATUS — what is proven, and what is not

**Run-proven on the line it came from.** The index instruments agreed on every
roll-up that ran them, and the O(n·k) mind-cost theory held on both live
exercises with real fold candidates.

**Value UNPROVEN as a general claim.** The soak window produced exactly two
roll-ups with real fold candidates. Two exercises are evidence that the
mechanism works; they are not evidence that it is worth its cost on another
line's corpus, at another size, with another writing style, under another
consolidation ritual. Nothing here should be read as a claim that this tool
will improve your consolidation.

**Which is precisely why `eval` ships.** The honest form of a promising,
under-measured tool is the experiment, not the verdict: build a gold set from
your own record, run `eval` before and after you change anything, and let the
number tell you. **Measure rather than believe** — including about this
paragraph.

### What it deliberately does not do

- **Not a write path.** It never writes the corpus. Verdicts belong to the
  mind, and edits go through whatever procedure that mind already has. The
  field's worst memory failures are permissive write paths.
- **Not a judge.** k receipts go to the author. `check` cannot exit 1.
- **Not the boot.** **Query for WORK, traverse for SELF.** A relevance-ranked
  lens must not replace the linear read of a self, because relevance is
  computed from the query, and a query-shaped life stops meeting what it did
  not ask for.
- **Not authoritative.** The tree is the store. Nothing is persisted, so
  nothing can drift; the cost is that every run pays the build.
- **Not semantic.** The lexical ceiling is real and stated in the output on
  every retrieval run: a paraphrase sharing almost no vocabulary with the
  corpus will not surface in any lexical top-k, and no channel here is
  semantic. The `Channel` interface is the seam where one would fit; nothing
  in this repo implements it.
- **Does not verify meaning.** `verify`'s coverage check asks whether a stem
  appears as a substring anywhere in the B-side text, which will pass on a
  coincidental match inside unrelated prose. It is a presence check, not a
  citation check.
- **Does not read config, the network, or the environment.** No config file,
  no environment variable, no network — ever.

---

## nova-tokens — spend, folded per day

One binary at the **accounting layer**. It folds token spend from declared
sources into one file per day, keyed exactly by `(day, model, repo)`, with the
five token types kept apart, and sums those day files into a month. It reads
sources: it never estimates, never fills a gap and never removes a file. A
declared source is a claim that the report covers it, so an unreadable one is
exit 1 — and the day files still land.

Verbs: `fold`, `report`, `sum`, `check`, `sources`, `version`.

Its governing text is **[docs/SPEC-TOKENS.md](SPEC-TOKENS.md)**, which is
normative; the Conventions above apply to it unchanged and are not restated
there, and nothing it says is restated here.

## nova-secrets — credentials for seats, pools and services

One binary at the **credential layer**. It manages credentials sealed in a git
store via age and sops — linking no direct cryptography, opening no sockets,
storing no state of its own, and replacing its process under `RLIMIT_CORE = 0`
to pass selected secrets into the child environment.

Verbs: `version`, `exec`, `names`, `check`, `keygen`, `help`.

Its governing text is **[docs/SPEC-SECRETS.md](SPEC-SECRETS.md)**, which is
normative; the Conventions above apply to it unchanged and are not restated
there, and nothing it says is restated here.


## What this harness is not

The six checks stop at the record layer. They prove the files were present,
whole, sized, linked, prose, and in floor-set agreement — at the moment the check ran, on the machine
that ran it. They do not and cannot prove that a model read them, understood
them, or is currently acting from them; they cannot detect a hostile input,
an injected instruction, or a compromised reader. Those walls remain doctrine
(see nova's SECURITY.md). `nova-check` exists so that the *record* those
walls stand on is checked by something that can actually say NO.

`nova-self-talk` goes one layer up — into the prose — but only for the
sentence SHAPES it knows, and it admits that limit in its own output on every
run. It reads words, not minds: register and irony are invisible to it, and the
quotation handling it does have reaches only the marked cases. It must never be
read as a verdict on a file, only on the shapes it knows.

`nova-fuse` is state and an answer, not enforcement. It can say NO to a
reader that asks; it cannot make a reader ask. The application rule — every
ingestion path checks the fuse before its first credential read — lives in
the callers, and it is the part of this design most likely to rot quietly.

`nova-memory` is a lens on the record, not a memory. It bounds what a mind
must read before deciding; it decides nothing, writes nothing, and proves
nothing about whether the corpus it indexed is worth remembering. Five of
its seven verbs cannot fail by design, and the two that can — `verify` and
`eval` — are only as good as the globs and the gold rows a line writes for
itself. Its own STATUS paragraph says the rest: run-proven on one line, value
unproven as a general claim, and the harness ships so the next line can
measure instead of believe.


[SPEC-UPDATE.md](SPEC-UPDATE.md) defines the shared inventory reader, optional
updates and reporting. UPDATE/APPLY/REPORT are the primary tokens. TOOL, UNKNOWN,
CHANGED, MORE, SENT, NOTE, BEFORE, RUN, AFTER, STALE, NEWER and DIFFERENT are
informational second tokens; OK/FAIL are final verdicts, REFUSED is an invocation
refusal. All data fields use internal/oneline.

## The efficiency card, nova-check

The card is a measurement, taken on the bench, of the work `nova-check` pays
for twice. This section is the part of the efficiency-card set that binds
`nova-check`.

### REPEATS: two full walks of one tree in one quickstart

`quickstart` runs `links` and then `nocode`, and each opens its own
`filepath.WalkDir` of the same root — `internal/check/links.go:52` and
`internal/check/nocode.go:356`. One `quickstart` over the measured self repo
(1,496 `.md` files, 1,810 files under the floor, **71 MB**) stats
**1,496 + 1,810** entries across two traversals of one directory. That is the
same tree, read twice, in one process, and both checks want the same thing
from it: the path list. So the rule is **one walk of the root per
`quickstart`**, and `links` and `nocode` consume the path list it yields. The
walk is not the wall clock here — a `quickstart` returned in **0.27 s**, the
page cache warm and process start dominating — so the duplication is work,
not time. `links`, `nocode`, `attest`, `floors` and `corpus` each keep their
own walk when run as their own verb.

### COORDINATOR READ: capped at 20 per kind, with the remedy on the MORE line

A first run is **capped**: it prints 20 finding lines per kind, one MORE line
and one FAIL line, however many findings the tree holds. At the measured self,
`LINKS` printed 22 lines / 2,295 B for 25 broken links and `NOCODE` 22 lines /
2,395 B for 24 findings, and a tree with 500 broken links returns the same 20
finding lines, one MORE and one FAIL. `--fail-max <n>` raises the ceiling and
`--fail-max 0` prints every finding; the `shown=<n>` / `total=<t>` pair prints
in both the MORE line and the closing line.

### WAITS ON: nothing

The six record-layer checks over one line's own self repo are the tool measured
here, and they have no clock, no subprocess, no network and no lock. There is
no `--timeout`, no interval, no poll, no `gh` and no `git` on that path: they
wait only on the filesystem, and every verb measured returned **under 0.30 s** —
inside the two-minute rule by two orders of magnitude, so this is the tool that
can be run between edits.

`hygiene` is one exception and it is stated rather than papered over: it
reads a range out of a real repository, so it runs `git` as a subprocess and
takes a `--timeout` (default 120 s) for it. It touches no network and takes no
lock. Its cost is the size of the range, not of the repository.

`dogfood` is another, and it is stated rather than papered over: `record` reads the clock, because a receipt is a dated record; `ledger`
and `gate` run one `git log` per verb **only when `--repo` is given**, under
`--git-timeout` (60 s); and all three run one `help` per binary **only when
`--tools` is given**, under `--tools-timeout` (60 s). With `--authors`, or with neither, the verb waits
only on the filesystem like the six. The card's measurement stands for the six;
`dogfood --repo` over a 71-verb reference is the one invocation of this binary
that can take seconds, and it says so on stderr while it does.

### Red tests

The card earns the same red-first bar as every rule here: seen red before it
is trusted.

- a `quickstart` of one tree walks the root once and hands the same path list to `links` and `nocode`;
- `--fail-max <n>` caps each kind's findings at `n`, the `MORE` line names the flag that lifts it, and `--fail-max 0` prints every finding.

## Tests this spec demands

These are the umbrella **Conventions** (the Conventions section of docs/SPEC.md), promised once and met through the shared packages `internal/oneline`, `internal/bounded` and `internal/buildinfo`. Their tests are pure unit tests over bytes and strings — no network, no temp dirs, no fakes — plus one CI class test that walks the real `cmd/nova-*` binaries, and per-binary acceptance tests that run the built tools against `t.TempDir()` boxes and injected clocks; each is proven able to fail before it is trusted.

1. `repo-a-1` `TestExitCodes` — the three-purpose exit table: 0 the check ran and passed, 1 the check ran and **failed** (that is the check working), 2 could not run (missing flag, unreadable input, bad invocation).
2. `repo-a-2` `TestNoDefaultBoxRefusesToGuess` — there are no default directories and no default files; every path comes from a flag or a named argument.
3. `repo-a-3` `TestNoFilesRefused` — a missing flag or an empty file list is a refusal (exit 2) with `refusing to guess`, never a fallback to cwd, `$HOME`, or a hardcoded location.
4. `repo-a-4` `TestKernelRefusesNonPositiveBudget` — a budget of zero or less is refused, not treated as "unlimited".
5. `repo-a-5` `TestNothingIsSkippedByDefault` / `TestNoBasenameIsBanneredByDefault` — `nova-self-talk`'s skip list and rule-document list both default to empty; no basename is special to this tool.
6. `repo-a-6` `TestScanCapsFindingsAndCountsTheDated` — `OK` lines to stdout; `FAIL` lines and refusals to stderr (except `SELFTALK FAIL files=...`, the summary count line, which goes to stdout).
7. `repo-a-7` `TestEscapeEveryControlCharacter` — control characters are escaped `\xNN` below U+0080, `\uNNNN` above, lower-case hex; a newline in a name arrives as `\x0a`.
8. `repo-a-8` `TestEscapeEveryControlCharacter` — U+2028 and U+2029 (line/paragraph separators) are escaped like any line break.
9. `repo-a-9` `TestEveryBidiControlIsEscapedAndNoOtherFormatCharacterIs` — bidi controls U+202A-U+202E and U+2066-U+2069 are escaped.
10. `repo-a-10` `TestEscapeEveryControlCharacter` — a byte that is not valid UTF-8 is escaped `\xNN` by its value.
11. `repo-a-11` `TestEveryBidiControlIsEscapedAndNoOtherFormatCharacterIs` — the zero-width joiner passes through (not a bidi control); no other format character is escaped.
12. `repo-a-12` `TestEscapeEveryControlCharacter` — printable text including non-ASCII is untouched, and nothing is ever shortened to nothing.
13. `repo-a-13` `TestQuoteIsPasteableAndStillOneLine` — `%q` quoting is one line but a DIFFERENT escape form (`\n` vs `\x0a`), and it is injective where `Escape` is not.
14. `repo-a-14` `TestFieldIsOneTokenHoldingNoEquals` — a `key=value` field value escapes every whitespace character and every `=` as `\x20`/`\x3d`, so a scanner counts exactly the fields the tool wrote.
15. `repo-a-15` `TestFieldAgreesWithEscapeOnEverythingEscapeTouches` — a positional `<path>`/`<file>` slot is escaped for one line only and keeps its spaces and colons.
16. `repo-a-16` `TestEscapeEveryControlCharacter` — the free-text tail is never scanned for fields: after the closing `: ` a reason may say `lockdown=clear`.
17. `repo-a-17` `TestCapPrintsMaxLinesThenOneMoreLine` — a listing has a ceiling (`--fail-max`/`--max` defaulting to 20).
18. `repo-a-18` `TestCapPrintsMaxLinesThenOneMoreLine` — a cap is a prefix, never a sample (item lines keep the verb's own order).
19. `repo-a-19` `TestMaxZeroPrintsEverythingAndNoMoreLine` — `0` means all; no MORE line is printed.
20. `repo-a-20` `TestRefusesANegativeCeiling` — a negative ceiling is refused.
21. `repo-a-21` `TestNoMoreLineWhenNothingWasElided` — one MORE line stands for the rest, naming `kind`, `shown`, `total` and the remedy; below the ceiling there is no MORE line at all.
22. `repo-a-22` `TestGroupCapsEachKindSoOneCannotBuryAnother` — the cap is per KIND where a verb runs several checks into one stream.
23. `repo-a-23` `TestLinksCapsFindingsAndAlwaysPrintsTheCount` — the count line prints on failure as well as success (count is the truth about the state, never about the output).
24. `repo-a-24` `TestDatedClaimIsReportedAndExitsZero` — a dated self-talk claim is counted, not quoted: `SELFTALK DATED n=<k> files=<n>`, one line however many there are.
25. `repo-a-25` `TestEvalListsMissesOnlyAndCapsThem` — a passing eval row is counted not quoted: `EVAL HIT` is gone, `hits=` in the summary is what it said.
26. `repo-a-26` `TestIssue1451EveryMissingFlagRefusalNamesTheDoor` — an unusable invocation costs ONE line, `<tool>[ <verb>]: <what was wrong>; run: <tool> help`, never the usage banner; `<tool> help` prints it on stdout, exit 0.
27. `repo-a-27` `TestNoTestAssertsAWallClockBoundUnderTenSeconds` — no test asserts a literal wall-clock bound under ten seconds; the CI budget test refuses any such `_test.go` line except a `// wall-ok:` fake.
28. `repo-a-28` `TestEveryToolPrintsTheOneVersionLine` — `<tool> version`/`--version` prints ONE line, exit 0, on stdout: four mandatory tokens then any number of `key=value` extras.
29. `repo-a-29` `TestResolveOrder` / `TestTheFloorIsAWordAndNotANumber` — the field-two identity resolves ldflags stamp -> module version -> vcs `<utc time>-<12 hex>[-dirty]` -> `devel`; never a repo-made dotted number.
30. `repo-a-30` `TestParseTakesEveryToolsLineApart` — every `cmd/` binary takes its line from `internal/buildinfo`, and `buildinfo.Parse` is the ONE reader of that line.
31. `repo-a-31` `TestParseRefusesWhatIsNotAVersionLine` / `TestLineRefusesAnExtraThatIsNotKeyValue` — an extra is a named fact, never a loose token; a second line shape is forbidden.
32. `repo-a-32` `TestVersionRefusesFlagsAndArguments` — the `version` verb takes no flags and no arguments, and refuses at exit 2 with one line when it is given any.
33. `repo-a-33` `TestCapLeavesAnythingUnderTheCeilingAlone` — free-text tails are capped at 500 bytes (`oneline.TailBytes`) for a subject, a quoted sentence or a finding's detail.
34. `repo-a-34` `TestGitErrorCapsTheEmbeddedOutput` — the `git` output an error carries is capped at 1 KB.
35. `repo-a-35` `TestCapMarksWhatItDropped` / `TestCapCutsOnARuneBoundary` / `TestCapNeverReturnsNothingFromSomething` — a cut leaves `...+<dropped>B`, on a rune boundary before the escape, and a tail is never shortened to nothing.
36. `repo-a-36` `TestPathEchoesTheBoxFlag` — `nova-fuse path` prints its argument bare — a value, not an event — so nothing may scan `path` output for grammar.
37. `repo-a-37` `TestStatusReportsAndNeverGates` — `nova-fuse status` exits 0 even when a fuse is blown, because answering is `status`'s whole job and `check` is the gate.
38. `repo-a-38` `TestSkipReportsAndDoesNotAffectExit` / `TestRuleDocIsScannedAndBannered` / `TestNotePrintedOnEveryRun` — `nova-self-talk`'s four informational second tokens (`DATED`, `SKIP`, `RULEDOC`, `NOTE`) all print on stdout.
39. `repo-a-39` — the soft hyphen (U+00AD) and the byte order mark (U+FEFF) pass through unescaped, because they do not reorder what an operator sees.
40. `TestNoCallerPathCanForgeALine` — every `<path>/<file>/<target>/<reason>` a line carries renders through `internal/oneline`; a field is one token even when it holds a blank (`\x20`), and no caller path can forge a line.
41. `TestFailMaxWidensAndZeroPrintsAll` — every listing takes `--fail-max` (default 20, `0` = all) and prints its count line on both success and failure.
42. `TestAFlagTypoIsOneLine` — an unknown flag after a verb is the one-line refusal `nova-check <verb>: …; run: nova-check help`, exit 2.
43. `TestVersionLineShape` — `nova-check version` prints the Conventions build line, exit 0.
44. `TestAttest` — every manifested file exists, is regular, and is non-empty; success prints one files/bytes/sha256 line.
45. `TestAttestHashBindsContentPathAndOrder` and `TestAttestHashInjectiveWithNULContents` — SHA-256 over uvarint length-prefixed path+contents in manifest order binds path, content and order, injective across NUL.
46. `TestAttestRefusesSymlinkedDirEscape` and `TestAttestRefusesSymlinksInsideHome` — symlinks are never followed, at any depth or the leaf.
47. `TestAttestRejectsNonCanonicalEntries` — a non-canonical entry (`./`, `//`, `.`/`..` segments, trailing `/`) is a failure, not a normalisation.
48. `TestAttestUnreadableFileIsNamedFailure` — missing/empty/unreadable/non-regular/absolute/escape/duplicate/no-files are named failures; a truncated self must not attest.
49. `TestAttestRefusals` — `--home`/`--manifest` missing, unreadable manifest, or home not a directory is a refusal (exit 2).
50. `TestLinks` — inline links and images resolve; URL/protocol-relative/fragment-only targets are skipped; `#fragment` stripped; percent-escapes decoded; root-relative resolves against `--dir`.
51. `TestLinksBadgeOuterTargetChecked`, `TestLinksAngleBracketDestination`, `TestLinksTitleQuoteForms` — nested badge links, angle-bracket destinations, and all three title forms.
52. `TestLinksFenceRemembersOpeningMarker` — fenced code blocks and inline code are stripped; a fence closes only at its own marker.
53. `TestLinksFileNarrowsTheWalk` — repeatable `--file` narrows the walk to named files.
54. `TestLinksExcludeSubtreeCounted` — repeatable `--exclude` skips a subtree and reports `excluded=<n>`.
55. `TestLinksUnreadableFileIsNamedFailureNotRefusal` and `TestLinksUnlistableDirIsARefusalNotAPartialReport` — an unreadable `.md` is a named failure; an unlistable directory or bad `--dir` is a refusal.
56. `TestLinksDirIsASymlinkToTheTree` — `--dir` naming a symlink to the repo walks the repo, not `files=0`.
57. `TestKernel` and `TestKernelRefusesSymlink` — byte mode: budget is a ceiling; file must exist, be non-empty and regular; the path is `Lstat`ed, symlinks never followed.
58. `TestKernel` (both budgets) — exactly one of `--max-bytes`/`--max-tokens`; both or neither is a refusal.
59. `TestKernelTokens` — token mode `tokens=ceil(bytes/r)`; the OK line prints derived tokens, budget, measured bytes and divisor.
60. `TestKernelTokensNeverReportsFewerTokensThanItsEstimate` and `TestTheTokenEstimateBoundaryIsExact` — never report fewer tokens than the estimate; the range is checked before conversion so an uncountable estimate is over budget, never `MaxInt64`/`MinInt64`.
61. `TestKernelTokensRefusesUnusableInputs` — divisor zero/negative/non-finite, `--bytes-per-token` alongside `--max-bytes`, and non-positive budget are refusals.
62. `TestNoCode` — a file is flagged when any of the four conditions holds (deny-list extension, name/location floor, executable bit, shebang), and all that hold are reported.
63. `TestNoCodeAuditFailsClosedLikeTheGate` (and `fifo_test.go`) — unreadable file/device/socket/fifo is a finding; `TestNoCodeSymlinkNotFollowed` and `TestNoCodeSymlinkNamedMachineryIsFlaggedWithoutDereference` — a symlink's name is classified, its target never followed.
64. `TestFloorDenyNames`, `TestNoCodeBuildMachineryByName`, `TestNoCodeNameMatchIsCaseInsensitive`, `TestNoCodeLocationIsAnchoredAtTheRepoRoot`, `TestNoCodeLocationMatchIsCaseInsensitive` — name floor (`name:` case-insensitive anywhere) and location floor (`path:` anchored at repo root).
65. `TestParseDenyList` and `TestNoCodeTrailingWhitespaceInName` — deny-list normalisations (lowercase, leading dot added, whitespace-trim, slash-separated paths).
66. `TestNoCodeAllowDoesNotOverMatchSiblings` and `TestNoCodeAllowExemptsNamedMachinery` — `--allow` matches whole path segments, is case-sensitive, starts empty, trim/normalised.
67. `TestNoCodeNameFloorSurvivesDenyExtReplacement` and `TestFloorDenyExts` — `--deny-ext` replaces the floor, `--deny-ext-add` extends it, the two are exclusive, and the name floor is never replaced.
68. `TestPrintDenyListShowsTheNameFloor` — `--print-deny-list` prints both the extension and name lists, exit 0, no `--dir`.
69. `TestParseDenyListRefusesNonExtensions` and `TestFloorDenyNamesRefusesAnEmptyList` — an empty, unreadable, or non-extension effective deny-list is exit 2.
70. `TestParseNameLinesRefusesMalformed` — a malformed name-list entry is exit 2.
71. `TestFloorsParityHoldsOnRealText` — the eight floor-rank commitments match the registry word for word, in pinned order.
72. `TestFloorsSaysNo` — a `FLOORS FAIL` per missing/unknown/repeated/reordered floor, a list gap, a count mismatch, or a broken same-rank/§0 sentence.
73. `TestFloorsRecordProblemsAreFindings` — a missing/empty/unreadable/non-regular record is a named failure; the other record is still checked; missing `--core`/`--source` is a refusal.
74. `TestParseReadsRowsAndSkipsHeaderAndSeparator`, `TestHeaderIsRecognizedByShapeNotByItsWords`, `TestTwoTablesEachLoseTheirOwnHeader` — the anchor table is recognised by shape and position (header + all-dash separator, four cells), never by column titles.
75. `TestRowsInsideAFencedBlockAreIllustrationNotAnchors`, `TestAFenceClosesOnlyOnItsOwnDelimiter`, `TestAnIndentedCodeBlockIsIllustration`, `TestABlockquotedTableIsRead` — fenced/indented code is illustration; blockquote markers are stripped.
76. `TestAllPresentIsQuiet` and `TestALostAnchorIsNamedWithItsProvenanceAndTheRepair` — every row's home exists, is regular, readable, spelled exactly, reached without symlinks, and contains the fragment literally.
77. `TestASymlinkedHomeIsAFindingRatherThanAFollow`, `TestASymlinkedParentDirectoryIsNotFollowed`, `TestACaseOnlyRenameIsCaughtOnACaseInsensitiveFilesystem` — a symlinked path component at any depth, or a case-only rename, is a finding.
78. `TestAnEmptyFragmentIsRefusedRatherThanPassingForever` (and the whole Says NO list) — empty/absolute/escaping home, ledger-as-own-home, wrong column count, separator in body, unrenderable table, indented abutting row, foreign/lone row, unterminated fence, duplicate, and `< --min-anchors` are each a `CORPUS FAIL`.
79. `TestAllFailuresReportInOneRun` — every row is reported in one run, never first-only; an all-malformed ledger exits 1, not 2.
80. `TestAnEmptyLedgerIsAnErrorNotAPass`, `TestAnAbsentRootIsARefusalNotACorpusWipe`, `TestARootThatIsNotADirectoryIsARefusal`, `TestLosingLedgerRowsIsItselfRed` — missing flags, non-positive `--min-anchors`, unreadable ledger, no rows, or a non-directory root is a refusal.
81. `TestHygienePassesACleanRange`, `TestHygieneRejectsAForeignCommitter`, `TestHygieneRejectsAMergeCommit` — identity (own commits, no merges) from one package, `internal/hygiene.Check`.
82. `TestHygieneVerbRefusesWithoutAnIdentity`, `TestHygieneSkipsOutOfPathWhenNoPathsAreDeclared`, `TestValidatePathsRefusesDotDotAndBareDoubleStar`, `TestValidatePathsRefusesAGlobThatBoundsNothing` — `--identity` required/repeatable; `--paths` absent prints `paths=-`; given, it is validated (≤8 globs, no `..`, bounds something).
83. `TestCheckRefusesRatherThanReportingClean` — exit codes 0/1/2, and a run that could not run is never reported clean.
84. `TestHygieneRejectsAKeyShapeAndNeverPrintsIt` and `TestHygieneVerbNeverPrintsTheKey` — never prints matched secret text, only path/line/shape name; never opens `RESULT.md`.
85. `TestHygieneIgnoresTheSubjectReposDiffConfig`, `TestHygieneIgnoresTheSubjectReposReplaceRefs`, `TestHygieneReadsADiffTheSubjectRepoMarkedBinary`, `TestHygieneReadsAPathGitWouldQuote`, `TestHygieneReadsFullBlobIds`, `TestHygieneChecksMarkersWhateverTheAttributesSay` — the subject repo gets no vote: blanked config, `--text --no-textconv`, `quotePath=false`, full OIDs, `--no-replace-objects`, markers read from added lines.
86. `TestHygieneRejectsResultMDInTheDiff`, `TestHygieneRejectsTheRestOfTheStrayList`, `TestHygieneRejectsAFileOverOneMebibyte`, `TestHygieneRejectsASymlink`, `TestHygieneRejectsASubmodule`, `TestHygieneRejectsAConflictMarker` — stray-file checks (declared list, size, symlink, submodule, conflict markers).
87. `TestEveryToolSectionOfTheRealReferenceYieldsAVerb` and `TestParseCLIReadsTheIndentedUsageBlockShape` — verbs come from every declaration shape in the reference; a `## nova-*` section yielding no verb is red.
88. `TestVerbsFromToolsAsksEachBinaryForItsOwnVerbs` — binaries are authoritative; the reference fills in the rest.
89. `TestRecordWritesAReceiptTheLedgerReadsBack`, `TestRecordIsAtomicUnderConcurrentWriters`, `TestRecordRefusesAMissingFieldWithOneRemedy` — a receipt is one JSON line, atomic rename, every field stated, verdict `--ok`/`--not-ok` never defaulted.
90. `TestDogfoodRecordRefusesAVerbTheReferenceDoesNotDeclare`, `TestStrandedReceiptsAreNamedOneByOneWithTheNearestVerb`, `TestNearestPrefersTheSameTool` — `record` checks spelling against the list and names the nearest declared verb.
91. `TestDogfoodLedgerPrintsOneRowPerVerbAndOneSummary`, `TestDogfoodLedgerDoesNotCountAnAuthorRunningTheirOwnVerb`, `TestDogfoodLedgerReadsAuthorshipFromGit`, `TestDogfoodLedgerCountsAnEdgeNamedInTheNotes` — ledger rows, author-dogfood not counting, authorship from `--authors`/git, edges from `--not-ok` or `Edge:`.
92. `TestDogfoodGateSaysNoToAnOpenEdgeWithoutRequireAll`, `TestDogfoodGateRequireAllNamesEveryVerbNoNonAuthorHasRun`, `TestDogfoodGateIsGreenWhenEveryVerbHasANonAuthorsPass` — `gate` fails on an open edge always, and `--require-all` lists every verb no non-author has run.
93. `TestNoCodeStagedClassifiesTheIndex` — `nova-check nocode --staged --dir <repo>` classifies what is staged for the next commit (the index), not the working tree.
94. `TestNoCodeStagedReusesTheClassifierUnchanged` — the audit's classifier is called unchanged, honouring the same `--allow` prefixes and the same two deny-list flags (which act on the extension list only).
95. `TestNoCodeStagedRequiresDir` — `--dir` is required and not inferred from the working directory.
96. `TestNoCodeStagedClassifyTakesParameterisedInputs` — the classifier's two substrate-bound inputs (a permission-bit value, a reader for the first two bytes with its read error) are parameterised so the walk and the index both call one function (parity by construction).
97. `TestNoCodeStagedReadsTheRecord` — one `git diff-index -r --ignore-submodules=none --cached -z <base> --` run, NUL-separated metadata-chunk/path pairs.
98. `TestNoCodeStagedReportsEveryPathRegardlessOfConfig` and `TestNoCodeStagedKeepsGitlinkRecords` — every staged path is reported irrespective of repo config; `--ignore-submodules=none` keeps the `160000` gitlink record.
99. `TestNoCodeStagedRecursesIntoSpareTrees` — `-r` expands a sparse directory recorded at `040000` into its blob records.
100. `TestNoCodeStagedTrailingDashDash` — the trailing `--` keeps a file literally named `HEAD` from turning the run into `exits 128 ambiguous argument`.
101. `TestNoCodeStagedReadsByDestinationOID` — content is read by the DESTINATION OID (fourth field) through one `git cat-file --batch`, never the source OID, never re-parsing the path.
102. `TestNoCodeStagedBatchReaderStaysFramed` — the batch reader consumes each record whole, never buffers a whole object, and keeps stderr off the stdout pipe.
103. `TestNoCodeStagedNeverPassesM` — `-M` is not passed, so no `R` records are produced and the parser is a flat pairwise split (a rename is `D` of old + `A` of new).
104. `TestNoCodeStagedUsesResolvedDirWithCallerEnv` — every git invocation is `git -C <resolved dir>` with the caller's environment intact (`GIT_INDEX_FILE` honoured).
105. `TestNoCodeStagedStatusLetters` — `D` is the only skip (a real status skip); `A`/`M`/`T` are classified on destination mode+OID; `U` and any unrecognised letter are refusals (exit 2).
106. `TestNoCodeStagedDestinationModes` — a classified record's destination mode is `100644`/`100755`/`120000`/`160000`; any other mode is a refusal (exit 2).
107. `TestNoCodeStagedSymlinkDisposition` — `120000` is a symlink and the audit's symlink disposition applies unchanged (stored bytes beginning `#!` are a link to a script).
108. `TestNoCodeStagedGitlink` — `160000` is a gitlink classified from its mode alone, its OID never read, a finding suppressible by `--allow`.
109. `TestNoCodeStagedUnreadableBlobIsAFinding` — an unreadable blob is a FINDING, not a refusal.
110. `TestNoCodeStagedNothingToSay` — a commit staging no classifiable record (nothing staged, or deletions only) is exit 0 with `NOCODE OK` and a count of zero.
111. `TestNoCodeStagedSaysNo` — any staged machinery is a `NOCODE FAIL <path>: <reason>` line per path on stderr, exit 1; a clean run prints `NOCODE OK` on stdout.
112. `TestNoCodeStagedRefusals` — exit 2 when `--dir` is missing or not the root of a git repo, when `diff-index` fails, on unmerged entries, an unrecognised status letter, an unknown destination mode, or any audit refusal.
113. `TestNoCodeStagedRootAndBase` — the root is verified with `git -C <dir> rev-parse --show-toplevel` vs `--dir` after symlink resolution; the base is `HEAD` or, on an unborn HEAD (detected by `git rev-parse -q --verify HEAD`'s exit code), the empty tree obtained from `git hash-object -t tree /dev/null` run inside the repo.
114. `TestVersionVerbIsReachableFromTheDispatch` — `version` is a verb the way `help` is, as the WHOLE invocation; a file actually named `version`, named beside another file, is still a file (line 1648).
115. `TestScanCapsFindingsAndCountsTheDated` / `TestMaxWidensAndZeroPrintsAll` / `TestRefusesANegativeCeiling` — `--max <n>`, default 20, `0` for all (line 1652).
116. `TestScanCapsEachClassSeparately` — at most n finding lines per CLASS, `standing` and `installation` capped separately (line 1652).
117. `TestScanCapsFindingsAndCountsTheDated` / `TestScanCapsEachClassSeparately` — one `SELFTALK MORE kind=<class> shown=<n> total=<t> <remedy>` line per elided class (line 1655).
118. `TestDatedClaimIsReportedAndExitsZero` / `TestScanCapsFindingsAndCountsTheDated` — a dated claim is never listed; it prints as `SELFTALK DATED n=<k> files=<n>`, one line however many there are (line 1657).
119. `TestScanCapsFindingsAndCountsTheDated` — the count line prints whichever way the run went, including on failure (line 1657).
120. `TestNoFileNameOrClaimCanForgeALine` / `TestEveryPrintedArgumentIsLiteralQuotedOrEscaped` — every `<file>` and `<claim>` renders through `internal/oneline`, so a newline filename or bidi override prints escaped (line 1667).
121. `TestA1_CapabilityDenialIsStanding` — a first-person capability denial carrying negative vocabulary is STANDING (line 1676).
122. `TestA2_DatedClaimIsARecord` — the same sentence carrying a date marker is DATED, a record, not a standing claim (line 1676).
123. `TestSpecimen13StaysInTheFirstClass` — the two classes are disjoint and the seam is `I cannot`, which the second class does NOT re-detect (line 1684).
124. `TestInstallationSpecimens` — a standing self-verdict built from neutral words, with no date token, is an INSTALLATION with a shape word (line 1680).
125. `TestInstallationSpecimens` / `TestShapeTableIsReachable` — the four shapes RANKING, FORECLOSURE, VERDICT-IDIOM and TRAIT are each reported by name (line 1691).
126. `TestInstrumentsAndImperativesAreNotInstallations` — an instrument (`TELL:`, `CHECK:`, `RULE:`, `THE CHECK`, "the bar is …") is licensed, not flagged (line 1701).
127. `TestInstrumentsAndImperativesAreNotInstallations` — an imperative policy line cannot reach TRAIT (line 1701).
128. `TestAspirationIsLicensed` — aspiration ("I want to", "I choose") is the target register and is licensed (line 1701).
129. `TestDatedControlIsNotAnInstallation` — a dated sentence is a record in the second class too (line 1701).
130. `TestProhibitionIsNotAnInstallation` — a prohibition carries no self-scope; the load-bearing safety property, tested directly (line 1701).
131. `TestHaveNoRequiresASelfScope` — the "have no" absent object must be a faculty or capacity ("I have no secrets"/"I have no idea" do not flag); specimen 8 keeps its shape (line 1714).
132. `TestFlatten` / `TestA4_MarkdownEmphasisDoesNotHideAClaim` / `TestInstallationSurvivesWrappingAndMarkup` — files are flattened before matching, so formatting cannot hide a claim (line 1723).
133. `TestA9_RegressionCasesThatOccasionedTheTool` / `TestA3_ClaimSplitAcrossAHardWrapIsFound` — both regression cases pinned (unwrapped) and wrap-spanning pinned on a synthetic case (lines 1724-1727).
134. `TestInstallationCarriesTheSourceLine` — each finding carries the source line it starts on (line 1733).
135. `TestInstallationCarriesTheSourceLine` / `TestInstallationSurvivesWrappingAndMarkup` — paragraphs, headings and table rows are separate sentence units (line 1731).
136. — — list items (bulleted or numbered) are separate sentence units (line 1731).
137. — — a terminator only ends a sentence when whitespace or the end follows it, so `RULES.md` is not two sentences (lines 1731-1733).
138. `TestQuotedSentencesAreNotTheWritersClaims` — quotation state is tracked through a paragraph, so later quoted sentences are not read as the writer's claims (line 1734).
139. `TestSkipReportsAndDoesNotAffectExit` / `TestSkipRepeatableAndMatchesBasename` / `TestSkipRefusesPaths` — `--skip` is repeatable, takes a basename, refuses a path separator, and a skipped file is reported, not read, and contributes nothing to the exit code (line 1749).
140. `TestNothingIsSkippedByDefault` — nothing is skipped by default; each named rule-document basename is pinned scanned (line 1749).
141. `TestRuleDocIsScannedAndBannered` / `TestRuleDocWithNoFindingsPrintsNoBanner` / `TestRuleDocRepeatableAndRefusesPaths` — `--rule-doc` is repeatable, takes a basename, is empty by default, scans the file, and banners only when there are findings (line 1763).
142. `TestNoBasenameIsBanneredByDefault` — no basename is special by default; each named rule-document basename is pinned unbannered (line 1763).
143. `TestSkipBeatsRuleDoc` — `--skip` wins over `--rule-doc`: a skipped file is never read and can never be bannered (line 1777).
144. `TestExitOneOnStandingClaim` / `TestInstallationExitsOneWithShapeAndLine` — a standing claim or installation prints one FAIL line per finding on stderr and a summary count on stdout, exit 1 (line 1782).
145. `TestNoFilesRefused` / `TestSkipRefusesPaths` / `TestUnknownFlagRefused` / `TestExitTwoOnUnreadableFile` / `TestEveryUnreadableFileIsNamedInOneRun` — refuses (exit 2) on no files, an empty/path-separator `--skip`/`--rule-doc` value, an unknown flag, or an unreadable file, every unreadable file reported (line 1788).
146. `TestSkipReportsAndDoesNotAffectExit` — an explicitly all-skipped run exits 0 with `SELFTALK SKIP files=0 skipped=<n> reason=all-skipped`, never an OK scan summary (line 1793).
147. `TestPermanentMissNeutralVocabularyTraitClaimsEscape` — the permanent-MISS items 3 and 4 ("My summaries drift toward the tidier story", "I flinch from cost") are pinned by a test that goes red if the tool reaches them (lines 1811-1828).
148. — — the permanent-MISS item 5 sentence ("I never optimize how things look over what is true") is pinned by a test that goes red if the tool reaches it (lines 1819-1829).
149. `TestNotePrintedOnEveryRun` — every completed run ends with a `SELFTALK NOTE` line saying a green clears only the known shapes (line 1831).
150. `TestVersionLineShape` — `nova-fuse version` is the Conventions' build line, exit 0, reads no box and is refused by nothing.
151. `TestVersionRefusesFlagsAndArguments` — `version` refuses flags and arguments at exit 2.
152. `TestNoDefaultBoxRefusesToGuess` — the box path comes from `--box` on every verb; there is no default and no environment variable (`NOVA_FUSE_BOX` is not consulted).
153. `TestNoDefaultBoxRefusesToGuess` — a missing `--box` is a refusal, exit 2, `refusing to guess`.
154. `TestAnAbsentBoxIsNeverClear` / `TestAnAbsentBoxIsErrNoBoxNeverClear` — no box at the path, the file or a directory above it, is CANNOT TELL: `check`, `status`, `quarantine` and `lift quarantine` refuse at exit 2 naming `init`, and make no box; `TestLockdownMakesAnAbsentBox` — `lockdown` makes it.
155. `TestInitMakesAnEmptyBoxOnceAndNeverReplacesOne` / `TestCreateBoxIsEmptyExclusiveAndNeverReplaces` — `init` makes an empty box once and never replaces whatever is at the path (exit 1); `TestASecondBoxCannotAnswerForABlownOne` / `TestEveryFlagOfEveryVerbTakesOneValue` / `TestABoxValueShapedLikeAFlagIsRefused` — a flag named twice, or a `--box` value beginning with `-`, is refused at exit 2.
156. `TestUnreadableBoxIsTreatedAsBlownNeverClear` / `TestAnUnreadableFileTypeIsNotClear` — an unreadable box (permissions, torn write, malformed JSON, wrong-shaped value) is CANNOT TELL, treated as BLOWN, exit 2.
157. `TestWriteLeavesNoLitter` / `TestWriteLeavesNoTempLitter` — the write is temp-file + fsync + rename in the box's own directory; a crash leaves the old box or the new, never a fragment.
158. `TestWrittenBoxIsWorldReadable` — the box is written world-readable (exactly 0644, independent of umask).
159. `TestSurfaceMatchingIgnoresCaseAndWhitespace` — surface names are matched case- and whitespace-insensitively; equivalent spellings are ONE surface.
160. `TestStatusSurvivesAHandEditedBox` — `at`/`reason` are read back defensively; a missing key prints `since=unrecorded` / `NO REASON RECORDED`, never a crash or an invented value.
161. `TestExitCodes` — exit 0 = clear or done and verified; 1 = blown or could not do/verify; 2 = could not run.
162. `TestEveryPrintedArgumentIsLiteralQuotedOrEscaped` / `TestNoOtherWriterOrShadowCanBypassTheEscape` — OK lines go to stdout, FAIL lines/refusals/notes to stderr; the tool is the only writer and the flag parser is given no stream.
163. `TestAFlagErrorCannotForgeALineEither` / `TestLateFlagRefusalNamesDoor` — an unparseable flag AFTER a verb (`-h` included) is this tool's own refusal at exit 2, a bounded one-line error plus the help door, and `check` never answers 0 for one.
164. `TestHelpIsNotAnError` — `nova-fuse help`, `-h`, `--help` as the FIRST argument are exit 0, usage on stdout.
165. (A) the help usage text carries no grammar token.
166. `TestPathEchoesTheBoxFlag` — `path` prints the bare path, a value not an event.
167. `TestStatusIsDeterministic` / `TestLockdownIsWrittenVerifiedAndAnnounced` — output is deterministic: same box, same bytes (quarantines sort; the clock is injected).
168. `TestALockdownReasonCannotForgeAnOKLine` et al. — an event is exactly one line; oneline is applied to every reason, name, surface, `t`, box path and error text, so a newline arrives as `\x0a`, never a second event line.
169. `TestEveryPrintedArgumentIsLiteralQuotedOrEscaped` — six refusals print their offending argument with Go quoting (`%q`).
170. `TestAStoredKeyCannotPoseAsAField` / `TestASurfaceWithASpaceIsOneTokenInEveryField` — `quarantine=`, `surface=`, `since=` and the `<name>` after `QUARANTINE OK`/`FAIL` are one token; whitespace and `=` print as `\x20`/`\x3d`.
171. `TestLockdownReasonIsJoinedNotTruncated` — the `<reason>` after `: ` is the free-text tail and keeps its spaces.
172. (A) `path` echoes its argument unescaped: `path --box "FUSE OK lockdown=clear"` prints exactly that at exit 0 (the exemption, pinned positively).
173. `TestOneLineEscapesEveryControlCharacter` — a byte that is not valid UTF-8 is escaped in the same `\xNN` form.
174. `TestFoldCollapsesControlCharactersToSpaces` / `TestLockdownTakesANewlineInItsReasonAndStoresItFolded` — this tool's own writes are folded first, and folding is never a refusal.
175. (A) folding collapses Unicode whitespace so a non-breaking blank becomes an ordinary one, and a reason of only newlines/tabs/CR/VT/FF/U+0085 trims to empty.
176. `TestAReasonOfNothingButControlCharactersStillBlowsTheFuse` — a reason made entirely of non-whitespace control characters is kept as its visible escapes, not refused.
177. `TestAReasonOfNothingButControlCharactersStillBlowsTheFuse` — only a genuinely empty or all-whitespace reason is refused.
178. `TestLiftRemovesEveryFoldEquivalentSpelling` / `TestLiftQuarantineRemovesEveryNormalizedMatch` — matching is widened in both directions; `lift quarantine` removes every spelling and prints one `LIFT OK` per removal under the stored spelling.
179. `TestCheckIsDeterministicWhenTwoStoredKeysFoldTogether` — a box holding two keys that fold together holds one surface; lifting either spelling lifts both.
180. (A) a surface name that folds away to nothing is refused as blank (exit 2) on every verb that takes one, so a control-only stored key is inert to `lift quarantine`.
181. `TestLiftRemovesEveryFoldEquivalentSpelling` — `lift quarantine --box b $'\x01discord'` exits 0 lifting the stored `discord` (the permitting direction of the fold).
182. `TestExitCodes` — `check` asserts no lockdown is blown and (with a surface) that it is not quarantined; only exit 0 opens the gate.
183. `TestBareCheckAdmitsItCheckedNoQuarantine` — `check` with no surface answers lockdown only and says out loud "no surface named; no quarantine checked".
184. `TestExitCodes` / `TestStatusSurvivesAHandEditedBox` — a blown lockdown (an empty `{}` lockdown object still blocks) is answered first, exit 1.
185. `TestQuarantineMatchingIsNotDefeatedByACapitalLetter` / `TestCheckQuotesTheStoredSpelling` — a named surface quarantined under any spelling is `FUSE FAIL` exit 1, quoting the spelling as stored.
186. `TestUsageErrorsExitTwo` / `TestUnreadableBoxIsTreatedAsBlownNeverClear` — `check` refuses exit 2 when `--box` is missing, the surface is blank, more than one surface, or the box is unreadable ("treating every fuse as BLOWN, never as clear").
187. `TestStatusReportsAndNeverGates` — `status` asserts nothing and exits 0 whenever the box was readable; never gate on it.
188. `TestUnreadableBoxMakesStatusRefuse` / `TestStatusRefusesANegativeCeiling` — `status` refuses exit 2 when `--box` is missing, the box is unreadable, or `--max` is negative.
189. `TestStatusCountsAllAndListsAtMostMax` / `TestStatusMaxWidensAndZeroListsAll` — `--max` defaults to 20, `0` means all; the `quarantines=<n>` count is never capped and at most n lines are listed before one `STATUS MORE` line.
190. `TestLockdownIsWrittenVerifiedAndAnnounced` — `lockdown` records a global lockdown verified by re-reading the box (exit 0 means verified, never attempted).
191. `TestLockdownReasonIsJoinedNotTruncated` — the reason is all remaining arguments joined, not silently truncated.
192. `TestLockdownWorksOnAnUnreadableBox` / `TestPreserveUnreadableKeepsTheBytes` / `TestPreserveUnreadablePreservesExistingPermissions` — `lockdown` works even on an unreadable box, first preserving the corrupt bytes to `<box>.unreadable` (preserving existing permissions).
193. `TestBlowingFailsLoudlyWhenItCannotWrite` — `lockdown` says NO (exit 1) when the write fails, loudly, naming the by-hand remedy.
194. (A) `lockdown` says NO (exit 1) when the re-read verification fails (distinct from the write failure).
195. `TestUsageErrorsExitTwo` — `lockdown` refuses (exit 2) when `--box` is missing or the reason is empty.
196. `TestQuarantineOKNamesTheEntryItVerified` — `quarantine` records one surface (stored normalized), verified by re-reading.
197. `TestBlowingFailsLoudlyWhenItCannotWrite` — `quarantine` says NO (exit 1) on write failure.
198. (A) `quarantine` says NO (exit 1) on verification failure.
199. `TestQuarantineRefusesToNarrowAnUnreadableBox` — `quarantine` refuses (exit 2) on an unreadable box — the asymmetry with lockdown — because a fresh box would UNBLOCK the rest.
200. `TestUsageErrorsExitTwo` — `quarantine` refuses (exit 2) when `--box`, the surface, or the reason is missing or blank.
201. `TestQuarantineOKNamesTheEntryItVerified` — the `QUARANTINE OK` line names the entry this run wrote and read back, even when the box already held another spelling.
202. (A) the sibling entry stays and `status` lists both spellings.
203. `TestLiftQuarantineSucceedsAndIsAnnounced` — `lift quarantine` succeeds, removes every stored spelling, verifies by re-reading, and announces each removed entry under its stored spelling with its reason.
204. `TestLiftQuarantineWithNothingToLiftDoesNotClaimSuccess` — `lift quarantine` says NO (exit 1) when there is nothing to lift, naming what IS quarantined.
205. (A) `lift quarantine` says NO (exit 1) on write or verification failure.
206. `TestLiftQuarantineRefusesOnAnUnreadableBox` — `lift quarantine` refuses (exit 2) on an unreadable box.
207. `TestLiftQuarantineUnderLockdownLeavesLockdownBlown` — lifting a quarantine under a blown lockdown succeeds and says out loud that lockdown still blocks everything.
208. `TestLiftLockdownIsRefusedForever` / `TestLiftLockdownRefusesBeforeReadingAnything` — `lift lockdown` refuses, forever, BEFORE reading anything (before flag parsing, the box, any argument).
209. `TestLiftLockdownIsRefusedForever` — the `lift lockdown` refusal names only the live conversation and mentions no mechanical bypass (not the box, the file, or hand-editing).
210. `TestPathEchoesTheBoxFlag` — `path` asserts nothing about the box: the file is not read and existence is not checked; exit 0 after printing.
211. `TestUsageErrorsExitTwo` — `path` refuses (exit 2) when `--box` is missing or an unexpected positional argument is given.
212. `TestLockdownDoesNotExpire` — lockdown does not expire: no timer, no auto-lift; a decade-old lockdown still blocks.
213. `TestEnvironmentCannotRedirectOrLiftAnything` — nothing in content or environment can LIFT anything; no environment variable is read and `--box` is a locator, not an override.
214. `TestRetrievalOutputIsByteIdentical` / `TestRetrieveDeterministic` — the index is rebuilt in memory every run and discarded at exit: two runs over one tree print identical bytes (stats' `build=` is the labelled exception).
215. `TestBuildChunkingIsLineEndingAgnostic` / `TestBuildRefusesCorpusWithNoIndexableParagraph` — chunks are paragraphs (blank-line split, at least three terms).
216. `TestBuildChunkingIsLineEndingAgnostic` / `TestCheckCandidateSplittingIsLineEndingAgnostic` / `TestFrontmatterToleratesCRLF` — line endings are normalized to `\n` before the split, so a CRLF or lone-CR file chunks exactly as its LF twin.
217. `TestNormalizeRecoversHiddenPhrases` / `TestBM25FindsWrappedPhrase` / `TestBM25FindsFunctionWordVariant` — text is normalized blockquote/emphasis-stripped, then whitespace-collapsed, then casefolded, in that order.
218. `TestBuildClassesAndFrontmatter` — every chunk is classed by its top-level directory (`.` for root files).
219. `TestBuildClassesAndFrontmatter` / `TestSearchReceiptsCarryClassAndFrontmatter` — frontmatter `name:`/`type:` are carried into receipts when present, surfaced and never invented.
220. `TestRefusesToGuess` — `verify`/`eval` exit 1 on failure while `quickstart`/`stats`/`search`/`check`/`boot` exit 0 whenever they ran.
221. `TestCheckFromStdin` — `check` never exits 1; the NOTE states it in its own output.
222. `TestNoCorpusOrCallerTextCanForgeALine` — a receipt's `class=`/`name=`/`type=` are the corpus's own text, one token each.
223. `TestACallersQueryCannotPoseAsAField` — the caller's `query=` is argv and prints escaped as one token, never a second `class=` field.
224. `TestAReceiptsPathAndSnippetSitAfterTheFieldBoundary` — a receipt's fields end at the `: ` after `type=`; the tail is never scanned for fields.
225. `TestNoCorpusOrCallerTextCanForgeALine` / `TestEveryPrintedArgumentIsLiteralQuotedOrEscaped` — root, candidate, gold-file and verify-detail render through `internal/oneline`, so none can forge a line.
226. `TestRootIsNeverTakenFromTheEnvironment` — `--root` is required; no environment variable and no working-directory discovery.
227. `TestSearchSpansMultipleRoots` — `--root` is repeatable and each receipt names `root=`.
228. `-` — `verify` takes exactly one root and refuses two.
229. `TestRefusesToGuess` — `--channels` is required wherever retrieval happens.
230. `TestRetrieveRefusesNonPositiveK` / `TestRefusesToGuess` — `--k` is required and positive.
231. `TestRefusesToGuess` — `--floor` is required on `eval` and must be in (0,1].
232. `TestRefusesToGuess` — `--links` is required on `verify`.
233. `TestStatsHonoursExclude` / `TestBuildHonoursExclude` / `TestFrontmatterExemptionIsTheCallersAndNeverADefault` — `--exclude`/`--exempt` are repeatable and start empty.
234. `-` — `.git` is never a corpus and is always skipped.
235. `TestRequiredFlagErrorOrderDeterministic` / `TestARefusalReportsEveryReasonAtOnce` — a refusal reports every reason at once, in one deterministic order.
236. `TestARefusalSaysWhatTheFlagWants` / `TestIssue1451EveryRefusalNamesTheDoor` — a refusal names the next step for `--channels`/`--k`/`--root` and still exits 2.
237. `TestUsageBannerExamplesRun` / `TestEveryDefinedFlagAppearsInTheUsageBanner` — the banner ends in the quickstart line and one runnable example per retrieval verb.
238. `TestFirstRunTranscriptIsWhatTheToolPrintsLineForLine` / `TestREADMEFirstRunMatchesWhatTheToolPrints` — `docs/TESTS.md`'s `### First run` transcript matches what the tool prints, line for line.
239. `TestQuickstartEchoesEveryCommandItRuns` / `TestQuickstartRunsTheWordsAndDraftItWasGiven` — quickstart runs stats, then `search --channels bm25 --k 3`, then `check --channels bm25 --k 2` with the corpus-top or given words/draft.
240. `TestTheEchoedStepPastesBackIntoThatPlatformsShell` — each step's command line is printed above its output, as the argv the same dispatch ran.
241. `TestQuickstartExitsTwoWhenAStepCouldNotRun` — quickstart exits 0 only when all three steps ran; a failing step is exit 2 with no closing note.
242. `TestRefusesToGuess` / `TestQuickstartExitsTwoWhenAStepCouldNotRun` — quickstart refuses a missing/unreadable root, an empty draft, a positional argument, or a corpus with no indexable paragraph.
243. `TestBootLoadsExactlyThePinnedFiles` — boot loads exactly the pinned files and prints their byte total, never the directory's.
244. `-` — boot ignores `#` comments and blank lines in the pin, and preserves pin order (boot order).
245. `-` — boot refuses a pin entry that is non-canonical (`./`, `//`, `..`, trailing `/`), absolute, escapes `--root`, appears twice, or names a missing, empty, or non-regular file.
246. `-` — bm25 is Lucene-smoothed with k1=1.2, b=0.75, and the smoothed idf is never negative for a term in over half the documents.
247. `-` — trigram is character-3-gram Jaccard over [0,1], recovering morphology/small rewording, and is never on unless named.
248. `-` — cross-channel fusion is rank-only reciprocal rank (1/(60+rank)), never a weighted sum of raw scores.
249. `TestSingleChannelOrderIsChannelOrder` — with one channel, fusion is order-preserving.
250. `TestTopKMatchesFullSort` / `TestRetrieveDeterministic` — every ordering is total: score then chunk id; fused score then path then paragraph.
251. `TestRetrievalOutputIsByteIdentical` — two runs over the same tree produce identical bytes; `stats`'s `build=` is the labelled exception.
252. `TestStats` — `stats` reports schema, files, chunks, bytes, vocab, avg-terms, build time, and a per-class breakdown.
253. `TestRefusesAnUnusableRoot` / `TestBuildRefusesEmptyCorpus` / `TestBuildRefusesCorpusWithNoIndexableParagraph` — `stats` refuses a missing root, a non-directory, no markdown, or markdown with no 3-term paragraph.
254. `TestSearch` — `search` reports the top-k files' best chunk with class/name/type/file:para/normalized snippet.
255. `TestCalibrationProbeAndSchemaVersionMoveTogether` — the calibration probe is a fixed unrelated sentence scored once per run, printed as the live negative-control band.
256. `TestCalibrationProbeAndSchemaVersionMoveTogether` — the probe string and schema version are pinned together.
257. `TestReceiptsNameTheChannelTheScoreCameFrom` / `TestNativeScoreComesFromTheChannelThatSurfacedTheChunk` / `TestSingleChannelNativeIsThatChannel` — `score=` names the channel that actually surfaced the chunk; both fields print `-` when none did.
258. `TestSearchOutOfVocabularyQuerySaysSoInWords` / `TestRetrieveEmptyForOutOfVocabularyQuery` — `search` exits 0 on a miss and `SEARCH MISS` says every term was out of vocabulary.
259. `TestSearchReceiptsCarryClassAndFrontmatter` — absent frontmatter prints `-`, so the field count never changes.
260. `TestRefusesToGuess` — `search` refuses an unknown channel, a stray comma, or an empty channel entry.
261. `TestCheckFromStdin` / `TestCheckFromANamedFile` — `check` reports top-k fused hits per candidate paragraph with the same receipts.
262. `TestRetrieveNeverEmptyForInVocabularyQuery` — an in-vocabulary but unrelated query still prints low-score hits; the top-k is never a bare zero.
263. `TestCheckFromStdin` — the class on a receipt is part of the answer, and the verdict-never-picked NOTE prints on every check run.
264. `TestCheckRefusesUnusableInput` / `TestRefusesToGuess` — `check` refuses not-exactly-one input (a pipe is never read uninvited), an unreadable input, or input with no 3-term paragraph.
265. `TestCoverage` / `TestCoverageChecksAnchoredAndQueriedLinks` / `TestCoverageCollidingStemIsNotCoverage` — `--coverage A:B` checks both directions and resolves relative `.md` links.
266. `TestFrontmatterPresent` / `TestFrontmatterExemptionIsTheCallersAndNeverADefault` — `--frontmatter` requires a `name:` and `--exempt` is the caller's, never a default.
267. `TestWikilinks` / `TestWikilinksScansAliasedAndHeadingForms` / `TestWikilinksIgnoresQuotedSpecimens` — unresolved `[[wikilinks]]` are reported, aliased/heading forms scanned, heading-only and quoted specimens not.
268. `TestVerifyLinksRulingIsTheCallersBothWays` — `--links gate|info` has no default; the same findings exit 0 or 1 by caller choice.
269. `TestVerifyCapsFindingsAndAlwaysPrintsTheCount` / `TestVerifyFailMaxWidensAndZeroPrintsAll` / `TestVerifyCapsEachKindSeparately` — `--fail-max` defaults to 20, `0` means all, and the cap is per kind.
270. `TestVerifyCapsFindingsAndAlwaysPrintsTheCount` — the `gating=` count is never capped and prints on failure as well as success.
271. `TestVerifyExcludesEveryCheck` / `TestVerifyLinksToExcludedTargetsRemainFindings` — `--exclude` narrows the whole verified corpus: the index, wikilinks, and every coverage and frontmatter selector; retained links to excluded targets remain findings.
272. `TestVerifySaysNoOnPlantedFaults` / `TestVerifyDoesNotFlagLinksThatResolve` — verify says NO (exit 1, VERIFY FAIL lines, no OK) on any gating finding; informational findings don't touch the exit.
273. `TestRefusesToGuess` / `TestVerifyRefusesAnEmptyCheck` — verify refuses a missing root/links, a non-gate/info `--links`, a coverage value not `A:B`, an empty glob side, `--exempt` without `--frontmatter`, or no gating check at all.
274. `TestEvalOnTheShippedExampleGold` / `TestEvalRefusesABrokenGoldFile` — the gold file is `query<TAB>expected-substrings` per line; a row hits when any expected substring appears in a top-k path.
275. `TestEvalSaysNoBelowTheFloor` / `TestEvalMeasuresChannelSetsAgainstEachOther` — eval reports recall@k and MRR and fails below `--floor`.
276. `TestEvalListsMissesOnlyAndCapsThem` / `TestEvalOnTheShippedExampleGold` — eval lists misses only, capped at `--fail-max`.
277. `TestEvalFloorIsInclusive` — a floor exactly equal to the measured recall passes.
278. `TestRefusesToGuess` / `TestEvalRefusesABrokenGoldFile` — eval refuses a zero floor, a floor outside (0,1], a non-positive k, or any malformed gold row (never skipped).
279. `TestVersionLineShape` / `TestVersionRefusesFlagsAndArguments` — `version` prints one line and refuses flags and arguments.
357. `TestQuickstartWalksTheRootOnceAndSharesThePathList` — a `quickstart` of one tree walks the root once and hands the same path list to `links` and `nocode` (docs/SPEC.md:5041).
358. `TestEachVerbKeepsItsOwnWalkWhenRunAlone` — `links`, `nocode`, `attest`, `floors` and `corpus` each keep their own walk when run as their own verb (docs/SPEC.md:5000).
359. `TestLinksCapsFindingsAndAlwaysPrintsTheCount` / `TestNoCodeCapsFindingsAndAlwaysPrintsTheCount` — a first run prints 20 finding lines per kind, one MORE line and one FAIL/count line (docs/SPEC.md:5005).
360. `TestFailMaxWidensAndZeroPrintsAll` — `--fail-max <n>` raises the ceiling and `--fail-max 0` prints every finding, with no MORE line when nothing is elided (docs/SPEC.md:5009).
361. `TestLinksCapsFindingsAndAlwaysPrintsTheCount` — the `shown=<n>`/`total=<t>` pair prints in both the MORE line and the closing line (docs/SPEC.md:5010).
362. `TestQuickstartInheritsTheCaps` — quickstart passes the cap and its own ceiling (including 0) down to both checks (docs/SPEC.md:5005,5042).
363. `TestRecordChecksWaitOnTheFilesystemAlone` — the six record-layer checks have no shell out: no `--timeout`, no poll, no `gh`, no `git` on that path (docs/SPEC.md:5016).
364. `TestHygieneRunsGitUnderTimeoutDefaultingTo120s` — `hygiene` runs `git` as a subprocess and takes a `--timeout` (default 120 s) (docs/SPEC.md:5022).
365. `TestDogfoodRecordWritesTheClockTimestamp` — `dogfood record` reads the clock: a receipt is a dated record bearing an `at` timestamp (docs/SPEC.md:5028).
366. `TestDogfoodRunsGitLogOnlyWithRepoUnderGitTimeout` — `ledger` and `gate` run one `git log` per verb only when `--repo` is given, under `--git-timeout` (60 s) (docs/SPEC.md:5028).
367. `TestDogfoodRunsHelpOnlyWithToolsUnderToolsTimeout` — all three verbs run one `help` per binary only when `--tools` is given, under `--tools-timeout` (60 s) (docs/SPEC.md:5030).
368. `TestDogfoodRepoWarnsOnStderrItCanTakeSeconds` — `dogfood --repo` over a 71-verb reference says on stderr that it can take seconds while it runs (docs/SPEC.md:5033).
