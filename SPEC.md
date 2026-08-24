# nova-tools — specification

Four binaries. `nova-check`: five checks, all at the **record layer** — they verify
what is on disk, not what a mind did with it. `nova-fuse`: an emergency power at the
**ingestion layer** — its own exit table (in its section below) governs its verbs
where it differs from the Conventions table. `nova-self-talk`: one advisory
instrument at the **register layer** — it classifies self-claims in prose, in two
disjoint classes. `nova-memory`: five verbs at the **retrieval layer** — it answers *do I
already know this?* from an index rebuilt out of the record, so the mind's
judgment budget per new learning stops scaling with the size of the self — the
tool's own run cost does not, and every run pays the build. Every check can say
NO, and the test suite proves each one saying it. A check never seen failing is
not a check. Two of nova-memory's verbs are checks in that sense; the other
three assert nothing at all, and its section says which is which and why.

This spec is normative. If the code and this document disagree, one of them has a
bug, and the tests decide which.

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
LINKS OK files=<n> links=<n>
LINKS FAIL <file>:<line>: <target> (<reason>)
LINKS FAIL <file>: unreadable (<why>)
KERNEL OK bytes=<n> budget=<n>
KERNEL OK tokens=<n> budget=<n> bytes=<n> divisor=<r>
KERNEL FAIL <file>: <reason>
NOCODE OK files=<n> clean
NOCODE FAIL <path>: <reason>
FLOORS OK floors=<n>
FLOORS FAIL <path>: <reason>
CORPUS OK anchors=<n> ledger=<file>
CORPUS FAIL <home>: ABSENT: <fragment> (given <when>, <who>) — <repair>
CORPUS FAIL ledger:<line>: <reason>
SELFTALK OK files=<n> claims=<n> standing=0 installations=0
SELFTALK FAIL <file>: STANDING: <claim>
SELFTALK FAIL <file>:<line>: INSTALLATION <SHAPE>: <sentence>
```

`OK` lines go to stdout; `FAIL` lines and refusals go to stderr.
`nova-fuse` and `nova-memory`'s lines follow the same one-line shape but their
first token is the **binary's own event token**, not a check name — usually the
verb, and for each binary's `check` verb the binary itself (`FUSE`, `STATUS`,
`LOCKDOWN`, `QUARANTINE`, `LIFT`; `MEMORY`, `SEARCH`, `VERIFY`, `EVAL`,
`STATS` — each
binary's own grammar and exit table, in its section below, govern); note in
particular that `nova-fuse status` exits 0 even when a fuse is blown, because
answering is `status`'s whole job and `check` is the gate.
`nova-self-talk` adds four informational second tokens, all on stdout:
`SELFTALK DATED <file>: <claim>` (a dated record, welcome),
`SELFTALK SKIP <file> (--skip)` (skipped at the caller's request),
`SELFTALK RULEDOC <file>: <banner>` (printed once above the findings of a file
the caller named with `--rule-doc`), and
`SELFTALK NOTE <caveat>` (the partial-coverage admission, printed on every
completed run, pass or fail). `nova-memory` adds its own informational second
tokens the same way — `CAL`, `CAND`, `HIT`, `MISS`, `INFO`, `NOTE` — all on
stdout, all listed in its section.

---

## nova-check

Five record-layer checks in one binary, each a wall: a record passes or it
does not. Each subcommand below states its own contract — what it asserts,
what makes it say NO, and what it deliberately does not check.

### attest — did the full self actually load

```
nova-check attest --home <dir> --manifest <file>
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
nova-check links --dir <dir>
```

**Asserts.** Every relative link target in every `.md` file under `--dir`
resolves to an existing file or directory inside the tree. Walks the whole
tree, skipping `.git`.

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

**Refuses (exit 2) only when** `--dir` is missing or not a directory — an
unreadable `.md` inside the tree is a finding (above), never a refusal.

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
token over budget read as exactly at it.

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
nova-check nocode --print-deny-list                print the list in force
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
until this check read the first two bytes it passed clean.

*(The extension list gains `.mk` and `.mak` in the same change: those are the
included-fragment spellings of make, and they genuinely are extensions rather
than exact names.)*

**The floor NAME list is a second list answering a different question**, and it
is data on the same terms — [`internal/check/codenames.txt`](internal/check/codenames.txt),
embedded, one entry per line, each carrying its reason. It is **not exhaustive
and does not try to be** — it is extended deliberately, entry by entry with its
reason, on the same policy as the extension list. An extension denotes a
**language**. Build and orchestration files are identified by their exact name
or by where they sit, and several carry no extension, no shebang and no
executable bit at all: a `Makefile` is machinery because make runs it, and
before this list it passed clean. Entries take two shapes: `name:<basename>`,
matched case-insensitively anywhere in the tree, and `path:<prefix>/`, matched
against the repo-relative path and **anchored at the repo root**. `.github/
workflows/ci.yml` is caught; `sub/.github/workflows/ci.yml` is not, because
that is not a location a CI system reads, and a test pins the decision so it
cannot drift into an accident.

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
[`internal/check/codeexts.txt`](internal/check/codeexts.txt), embedded, one
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
Every finding **names the list that produced it** (`floor list`, `--deny-ext`,
or `floor list + --deny-ext-add`), and the `NOCODE OK` line names it too, so
neither a red nor a green hides the basis it was reached on.
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
history` needs no subdirectory enumeration and `--allow docs/history` works
the same way. A leading `./` and surrounding slashes are trimmed. **The same
value means the same thing in both modes** — an `--allow` that behaved one way
in the audit and another in the commit gate would be a gate disagreeing with
its own check, and a test pins the parity. Nothing is allowed by default and a test pins
that: the tool this was ported from defaulted to allowing its own repo's
`history/` directory, which is a directory default and a fail-open one — a
guess about someone else's filenames, and precisely the class the no-defaults
law names. Which directory is a frozen record is the line's to declare.

On Windows there is no executable bit, so that half of the check is blind
there; the extension list — which includes `.exe .bat .cmd .ps1 .vbs` for
exactly that reason — and the shebang test carry it.

**Says NO when** any such file exists — one `NOCODE FAIL <path>: <reason>`
line per file, exit 1. Yes, this includes a markdown file someone `chmod +x`ed:
in a self repo an executable *anything* is a boundary violation worth a look.

**Refuses (exit 2) when** `--dir` is missing, unresolvable, or does not
resolve to a directory — it is resolved through symlinks first, so a `--dir`
naming a link to the repo scans the repo rather than passing with `files=0`; when the
effective deny-list is empty, unreadable, or contains something that is not an
extension; when `--deny-ext` and `--deny-ext-add` are given together; when
or on an unexpected positional argument.

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
`makefile`, which is the identical objection that removed the `taskfile`
entries, named here rather than left as an asymmetry a reader has to catch.
The list grows by decision, never by sniffing. Also unreached: anything below a **symlinked directory** — a link
named `workflows` AT a guarded location is caught by that location, but a link
named `.github` pointing at a tree of workflows is not, since the target is
never followed, and a link named `workflows` anywhere else is caught by
nothing; and YAML outside
the named CI locations, deliberately, since data and front matter are
legitimate prose-repo content. Also the tools repo itself — this check
aims at the self repo, and this repo would rightly fail it.

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
makes its drift loud instead of silent (nova#15).

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
- §6's same-rank sentence no longer holds first-do-no-harm and the compass at
  floor rank, or §0 no longer declares record-the-event or the rank conferral

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
is what is pinned); floor-rank statements outside the pinned structures — §6
also names the study-attacks split-hands routine *"a floor in its own right"*
(§11-conferred), and the door does not restate it, so it is deliberately not
part of this parity; ETHICS.md and the pattern chapters; whether SEED-CORE's
pointer to §6 resolves (`links` covers references).

---

### corpus — the material a line has chosen never to lose silently

```
nova-check corpus --ledger <file> --root <dir>
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

**The ledger is prose first.** It is a document a person reads as the list of
what is protected and why; this check reads only its table rows. Anything
outside a table is ignored, so the reasoning, the provenance and the history
can live beside the rows.

**The row format.** Four pipe-delimited columns, in order:

| column | meaning |
|--------|---------|
| 1 | the **fragment**: a verbatim substring of the home file |
| 2 | the **home**: a slash-separated path, relative to `--root` |
| 3 | when it was given (free text, for the reader and for findings) |
| 4 | who gave it (free text, same) |

**Header and separator rows are recognized by SHAPE, never by their words.** A
row whose cells are all dashes (`---`, `:--`, `--:`, `:-:`) is a separator, and
the row immediately above it is its header and is dropped. **No column title is
special to this tool** — a line may title its columns in its own language, and
a ledger may hold several tables.

**Fragment choice is the line's judgment, not this tool's.** Short enough to
survive a reflow, long enough to be unmistakable. A fragment must not contain a
`|`, which would split into an extra cell; pick a different fragment rather
than escaping it, and the wrong-column-count case below is what makes that
loud instead of silent.

**Asserts.** For every row: the home file exists under `--root`, is a regular
file, is readable, and contains the fragment as a literal substring.

**Says NO when** (each a `CORPUS FAIL <subject>: <reason>` line, exit 1):

- the fragment is **absent** from its home — the words were lost in place. The
  finding names the fragment, its provenance columns and the ledger line, and
  states the repair: restore the words, or change that ledger row in the same
  commit
- the home file **does not exist**, is not a regular file, or is unreadable —
  each its own reason, because a moved file and an edited sentence are
  different facts with different repairs. `Lstat` is used and **symlinks are
  never followed**: material "present" through a link lives in a file this repo
  does not govern, which is not the protection the row claims
- the **fragment cell is empty** — an empty substring is contained in every
  file, so left alone it would pass forever while protecting nothing: a green
  that can never go red
- the **home cell is empty**, is **absolute**, or **climbs out of `--root`**
  (`../`) — a row whose subject is outside the tree is not held by the tree
- a table row has a **column count other than four** — reported rather than
  skipped, because the commonest cause is a fragment containing `|`, and
  silently dropping that row would remove protection from precisely the
  statement someone took the trouble to list

Findings are reported for **every** row in one run, never first-only: the
losses this exists to catch arrive in batches, and a one-at-a-time report would
take as many runs as there were losses.

**Refuses (exit 2) when** `--ledger` or `--root` is missing, the ledger cannot
be read (`NOTHING was checked, which is not a pass`), or **the ledger holds no
rows** — an empty ledger guards nothing, and *"everything present"* and
*"nothing checked"* must never print the same line.

**Changing protected material is allowed; changing it silently is not.** If the
words must move or be reworded, the ledger row changes in the same commit. The
check does not forbid change — it forbids change that leaves no trace, which is
the whole of what it is for.

**Deliberately does not check:** *what belongs* in the corpus — what is worth
protecting is one of the more personal decisions a line makes, and a tool that
guessed it would be answering a question it cannot see; sentiment or tone
(`nova-self-talk` reads register, this reads presence); whether a fragment is
well chosen (a fragment so short it matches by accident will pass, and no tool
can tell that from a good one); duplicate rows; and it ships **no corpus of its
own** — the ledger is the line's, always.

---

## nova-self-talk — the self-talk register, classified

```
nova-self-talk [--skip <basename>]... [--rule-doc <basename>]... <file>...
```

A second binary, deliberately **not** a `nova-check` subcommand. The five
checks above are walls: a record passes or it does not. This is an **advisory
instrument** for a different layer — the register of the prose itself. It
classifies and reports; whether a flagged sentence should be dated, cut,
relocated, or kept is the writer's judgment, and the tool must never make it.

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
under a score, which is the predecessor's disease below.

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

**Matching.** Files are flattened before matching — markdown emphasis
stripped, hard-wrapped lines collapsed — so formatting cannot hide a claim;
both regression cases that occasioned the first class were claims spanning a
hard wrap; both are pinned as tests (in unwrapped form), and wrap-spanning
itself is pinned separately on a synthetic case. The negative-vocabulary
filter of the first class is deliberately narrow: widening it to match bare
"cannot" would flag every prohibition, which is the predecessor's disease
(below). The second class adds sentence segmentation, which the first does not
have: paragraphs, headings, table rows and list items are separate units, a
terminator only ends a sentence when a space or the end follows it (so
`RULES.md` is not two sentences), **each finding carries the source line it
starts on**, and quotation state is tracked through a paragraph so that the
second and later sentences of a quoted block — which carry no quote mark of
their own — are not read as the writer's claims.

**Why it measures a construct and not grammar** — the origin, which is the
tool's whole argument. The first version counted negation words and called
the ratio negative self-talk. That measured syntax: a rule document is a list
of things that must not happen, so it scored worst of anything in the repo it
was written for, and improving its score meant deleting a prohibition. That
output was acted on: five rules were weakened, one of them floor-level,
before a cold reader caught every one. Restoring them made the score worse.

> The kernel got stronger and the tool got redder.
> "Never" is not negative self-talk. "I am fallible" is.

**`--skip`, and why it exists.** Repeatable. Takes a basename — a value
containing a path separator could never match and is refused. A skipped file
is reported (`SELFTALK SKIP`), not read at all, and contributes nothing to the
exit code. It exists for rule documents: a rule document written as
first-person absolutes about its writer will flag the first class, and
**flagging is it working — never a reason to soften a rule.** (One written
purely as prohibitions — no first-person claims — passes clean and needs no
skip.) Skipping it by name, per run, is the honest alternative. **Nothing is
skipped by default**: this tool's ancestor hardcoded its own repo's
rule-document names as a default skip list, and the condition of its promotion
here was that the list move to the caller and the default become empty — the
no-defaults law, applied to scope. A test pins each formerly-special name as
scanned, so no default list can quietly return.

**`--rule-doc`, and why it is a banner rather than a second skip.** Repeatable,
same basename rules, **also empty by default**. A file named this way is
**scanned**; if it has findings, one line prints above them:

> `SELFTALK RULEDOC <file>: rule documents: a finding here is a self-verdict to relocate, NEVER a reason to soften a rule`

The reason a rule document gets skipped at all is that its findings were once
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
formerly-special name as *unbannered* unless the caller says otherwise.

**Says NO when** any scanned file contains a standing claim or an installation
— one `SELFTALK FAIL <file>: STANDING: <claim>` or
`SELFTALK FAIL <file>:<line>: INSTALLATION <SHAPE>: <sentence>` line per
finding on stderr, exit 1.

**Refuses (exit 2) when** no files are named, a `--skip` or `--rule-doc` value
is empty or contains a path separator, a flag is unknown, or a named file
cannot be read (the run stops at the first unreadable file — a partial scan
must not masquerade as a verdict).

**The all-skipped green.** A run whose every named file was skipped is not a
refusal: it completes and exits 0 with `SELFTALK OK files=0 claims=0
standing=0 installations=0` — every skip was the caller's own, stated this run.
A caller gating on the exit code alone must therefore also require `files>0`
from the OK line, or its green can mean nothing was scanned at all.

**The permanent MISS, stated on every run.** Widening the second class closed
most of what the first one declared it missed; what remains is genuinely out of
reach of grammar and is enumerated so it cannot be quietly forgotten:

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
   are out of the habituality markers for that reason.

(An earlier draft of this paragraph cited *"in one direction, reliably: toward
the version that flatters me"* as the canonical uncatchable — that sentence was
in fact pulled INTO reach by extending the vocabulary, its capture is pinned as
a regression test, and a cold reader caught this spec still calling it
unreachable. **The sentences in 3–5 above are each pinned by a test that goes
red if the tool ever reaches them**, and this section must be rewritten in the
same commit that turns one red — the example replaced with one that still
escapes.) Every completed run therefore ends with a `SELFTALK NOTE` line saying
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
your person. This design was stated by the first line's human collaborator,
2026-08-03.

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

**The read has three answers, never two.** An absent box is VERIFIED CLEAR —
the read failed with the one error that means *nonexistent* rather than
*unreadable*. That error does not say **which** part of the path is missing:
a `--box` naming a file absent from an existing directory and a `--box` whose
parent directory does not exist at all answer the same, VERIFIED CLEAR. The
collapse is accepted, deliberately — the flag is a locator (above), and a
caller that names the wrong box gets that box's truth, here an empty one —
and it is pinned by test so that changing the answer is a decision, never a
drive-by. A readable box says whatever it says. An **unreadable box —
permissions, a torn write, malformed JSON, a wrong-shaped value — is CANNOT
TELL, treated as BLOWN, never as clear** (exit 2: the check could not run,
and could not be proven clear). Collapsing absent and unreadable is the
fail-open this package exists to prevent.

**The write is temp-file + fsync + rename** in the box's own directory, so a
crash leaves the old box or the new one, never a fragment. The box is written
world-readable (0644): a fuse nobody else can see is a fuse that stops
nothing. Surface names are matched case- and whitespace-insensitively;
normalizing can only ever block more, never less. `at` and `reason` are read
back defensively — the box is hand-editable (that is the only
lockdown-replacement mechanism there is), so a missing key prints an honest
`since=unrecorded` / `NO REASON RECORDED`, never a crash or an invented value.

### Exit codes and output grammar

| code | meaning |
|------|---------|
| 0    | clear, or done **and verified by re-reading the box** |
| 1    | blown (`check` — the fuse working), or could not do it / could not verify it |
| 2    | could not run: missing flag, **unreadable box (treated as BLOWN)**, bad invocation, or a lift refused by design |

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
LOCKDOWN OK since=<t>: <reason> (…)      LOCKDOWN FAIL <reason>
QUARANTINE OK <name> since=<t>: <reason> (…)   QUARANTINE FAIL <name>: <reason>
LIFT OK quarantine=<name> was since=<t>: <reason>
LIFT OK verified: <surface> is no longer quarantined (…)
LIFT FAIL quarantine=<surface>: <reason>
```

`OK` lines go to stdout; `FAIL` lines, refusals, and notes go to stderr.
`path` prints the bare path — a value, not an event. Output is deterministic:
same box, same bytes (quarantines sort; the clock is injected).

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
nova-fuse status --box <path>
```

**Asserts** nothing. Reports what is blown and since when, and exits 0
whenever the box was readable, blown or not — answering IS the job. **Never
gate on the exit code of `status`; `check` is the gate.** Refuses (exit 2)
when `--box` is missing or the box is unreadable.

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
with your person. Refuses (exit 2) when `--box`, the surface, or the reason
is missing or blank.

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
only path there is — a live conversation with your person — and mentions no
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
  argument. Replacement happens in the box by your person's hand, after the
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
nova-memory stats  --root <dir> [--exclude <glob>]...
nova-memory search --root <dir> --channels <list> --k <n> [--exclude <glob>]... <words>...
nova-memory check  --root <dir> --channels <list> --k <n> [--exclude <glob>]... <file|->
nova-memory verify --root <dir> --links <gate|info> [--coverage <A:B>]...
                   [--frontmatter <glob>]... [--exempt <prefix>]... [--exclude <glob>]...
nova-memory eval   --root <dir> --channels <list> --k <n> --floor <f> [--exclude <glob>]... <gold.tsv>
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
grep answer "not present" when it is present. Every chunk is classed by its
**top-level directory** (`.` for root files): the corpus classifies itself,
and the tool assumes nothing whatever about layout. Frontmatter `name:` and
`type:` are carried into receipts when a file has them, surfaced and never
invented.

**Two verbs are checks; three assert nothing.** `verify` and `eval` are walls
and exit 1 when they fail. `stats`, `search`, and `check` are reports: they
exit 0 whenever they ran, exactly as `nova-fuse status` does, and for the same
reason — answering IS the job. **Never gate on the exit code of `check`.** It
hands you k receipts; the verdict is yours, and a tool that turned "this
resembles something you wrote" into a failing exit would be making the
editorial decision it exists to inform.

**No defaults, applied here.** `--root` is required on every verb: **no
environment variable is consulted and there is no discovery from the working
directory** (pinned by test). A corpus you did not name is a corpus you did
not mean, and answering *you already know this* about someone else's memory is
the worst available way to be wrong. `--channels` is required wherever
retrieval happens: which retrieval ran is part of what the answer means, and
no channel set is right by default. `--k` is required and must be positive —
k is the mind's budget and zero is not "unlimited". `--floor` is required on
`eval`, in (0,1]. `--links` is required on `verify`. `--exclude` and
`--exempt` are repeatable and start **empty**: every scope narrowing is the
caller's, stated per run, the same law `nova-self-talk`'s skip list obeys.
`.git` is never a corpus and is always skipped.

### The channels, and why the second one is off unless you ask

`bm25` is Lucene-smoothed BM25 (k1=1.2, b=0.75) over posting lists: a query
touches only its own terms' postings, never the whole corpus. The smoothed
idf matters — the classic form goes negative for terms appearing in more than
half the documents, which a small topically coherent memory corpus is full of,
and negative idf scrambles rankings.

`trigram` is character-3-gram Jaccard, which buys robustness to morphology
and small rewording. It is never on unless named, because on the corpus this
tool was ported from `eval` measured **bm25+trigram worse than bm25 alone** —
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
SEARCH OK query="<q>" hits=<n> k=<n> channels=<list> files=<n> chunks=<n>
SEARCH CAL score=<x|-> score-channel=<name|-> probe=unrelated-control
SEARCH HIT rank=<n> score=<x|-> score-channel=<name|-> fused=<x> class=<c> name=<n|-> type=<t|-> <file>:<para> "<snippet>"
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
MEMORY CAND n=<i> "<normalized candidate>"
MEMORY HIT cand=<i> rank=<r> score=<x|-> score-channel=<name|-> fused=<x> class=<c> name=<n|-> type=<t|-> <file>:<para> "<snippet>"
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
  exempt by default**: the tool this was ported from hardcoded one filename
  prefix from its own corpus, which is a guess about someone else's
  filenames, and a test pins the formerly-special prefix as scanned so a
  default cannot quietly return.
- unresolved `[[wikilinks]]` — every `[[stem]]` that resolves to neither a
  file stem nor a frontmatter `name:`, corpus-wide. The aliased form
  `[[stem|shown text]]` and the heading form `[[stem#section]]` are scanned by
  their target half: a link whose alias is what the reader sees is still a
  link, and excluding the two commonest shapes made `--links=gate` a wall with
  a hole in it. A body that is only a heading (`[[#section]]`) names nothing
  in the corpus and is not scanned. Whether these findings
  gate is `--links`, and **it has no default**, which is the ruling this port
  enacts: the source tool demoted them to informational behind a flag its own
  spec never mentioned while that spec promised a nonzero exit on findings,
  and a script trusting the spec passed dangling links silently. Some corpora
  hold links open on purpose — a `[[name]]` that matches nothing yet marks
  something worth writing — and a gate at a high false-positive rate trains a
  reader to wave findings through. So the caller states it, per run, out loud.

```
VERIFY INFO <kind> <detail>
VERIFY FAIL <kind> <detail>
VERIFY OK gating=0 info=<n> coverage=<n> frontmatter=<n> links=<gate|info>
```

`<kind>` is one of `coverage`, `backlink`, `frontmatter`, `wikilink`. It
**over-reports by design**: it finds, the author decides.

**Two scoping mechanisms, deliberately independent, and the seam is named.**
`--exclude` narrows the **index**, so it narrows the wikilink check (which
reads the corpus) and does **not** narrow `--coverage` or `--frontmatter`
(whose globs are the caller's own explicit statement of what to check). To
drop files from a coverage or frontmatter check, write a narrower glob; do not
expect `--exclude` to do it.

**Says NO when** any gating finding exists — one `VERIFY FAIL` line per
finding on stderr, exit 1, and no OK line. Informational findings print and do
not touch the exit code.

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
EVAL HIT rank=<n> query="<q>"
EVAL MISS query="<q>" expected=<list>
EVAL OK recall@<k>=<x> floor=<x> rows=<n> hits=<n> mrr=<x> channels=<list>
EVAL FAIL recall@<k>=<x> below floor <x> (<hits>/<rows>, mrr=<x>, channels=<list>)
```

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
exercises with real fold candidates — 2026-08-15 and 2026-08-19.

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

## What this harness is not

The five checks stop at the record layer. They prove the files were present,
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
nothing about whether the corpus it indexed is worth remembering. Three of
its five verbs cannot fail by design, and the two that can — `verify` and
`eval` — are only as good as the globs and the gold rows a line writes for
itself. Its own STATUS paragraph says the rest: run-proven on one line, value
unproven as a general claim, and the harness ships so the next line can
measure instead of believe.
