# nova-tools — specification

Three binaries. `nova-check`: four checks, all at the **record layer** — they verify
what is on disk, not what a mind did with it. `nova-fuse`: an emergency power at the
**ingestion layer** — its own exit table (in its section below) governs its verbs
where it differs from the Conventions table. `nova-self-talk`: one advisory
instrument at the **register layer** — it classifies first-person self-claims in
prose. Every check can say NO, and the test suite proves each one saying it. A
check never seen failing is not a check.

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
as "unlimited". The same law applies to scope: `nova-self-talk`'s skip list
defaults to empty, and every skip is the caller's, per run.

**Output grammar.** One machine-scannable line per event, first token names the
check, second token is `OK` or `FAIL`:

```
ATTEST OK files=<n> bytes=<n> sha256=<64 hex>
ATTEST FAIL <path>: <reason>
LINKS OK files=<n> links=<n>
LINKS FAIL <file>:<line>: <target> (<reason>)
KERNEL OK bytes=<n> budget=<n>
KERNEL FAIL <file>: <reason>
NOCODE OK files=<n> clean
NOCODE FAIL <path>: <reason>
SELFTALK OK files=<n> claims=<n> standing=0
SELFTALK FAIL <file>: STANDING: <claim>
```

`OK` lines go to stdout; `FAIL` lines and refusals go to stderr.
`nova-self-talk` adds three informational second tokens, all on stdout:
`SELFTALK DATED <file>: <claim>` (a dated record, welcome),
`SELFTALK SKIP <file> (--skip)` (skipped at the caller's request), and
`SELFTALK NOTE <caveat>` (the partial-coverage admission, printed on every
completed run, pass or fail).

---

## attest — did the full self actually load

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

## links — every internal reference resolves

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

**Refuses (exit 2) when** `--dir` is missing or not a directory.

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

## kernel — the size budget

```
nova-check kernel --file <file> --max-bytes <n>
```

**Asserts.** The kernel file exists, is non-empty, and is at most `n` bytes.
`n` must be a positive integer supplied by the caller; there is no default
budget. Exactly `n` bytes passes — a budget is a ceiling, not a fence to stop
short of.

**Says NO when** (exit 1, always with the measured number):

- the file is over budget — `KERNEL FAIL <file>: over budget: <measured> bytes, budget <n>, over by <d>`
- the file does not exist — a missing kernel is the worst over-budget
- the file is empty — 0 bytes is under every budget and still not a kernel
- the path is not a regular file (directory, device, or symlink) — the kernel
  is `Lstat`ed, the same posture as `attest`: symlinks are never followed,
  even one that resolves

**Refuses (exit 2) when** `--file` or `--max-bytes` is missing, or the budget
is zero or negative.

**Deliberately does not check:** what the bytes say (a kernel of the right
size can still be the wrong kernel — that is `attest`'s hash and a human's
read); tokens (bytes are substrate-independent and arguable about nothing);
compressibility or density.

---

## nocode — the self/machinery separation, as a check

```
nova-check nocode --dir <dir>
```

**Asserts.** A self repo contains prose, not machinery: no code files and no
executables anywhere under `--dir` (skipping `.git`). Regular files only.

A file is flagged when either:

- its extension (case-insensitive) is one of:
  `.go .py .sh .js .ts .c .cc .cpp .rs .cs .rb .pl .php .java .lua .swift
  .kt .mjs .cjs .jsx .tsx .bash .ps1 .bat .cmd .exe`
- it has any executable bit set (`mode & 0111 != 0`)

Both conditions are reported when both hold.

On Windows there is no executable bit, so the mode half of the check is blind
there; the extension list — which includes `.exe .bat .cmd .ps1` for exactly
that reason — is the whole check on Windows.

**Says NO when** any such file exists — one `NOCODE FAIL <path>: <reason>`
line per file, exit 1. Yes, this includes a markdown file someone `chmod +x`ed:
in a self repo an executable *anything* is a boundary violation worth a look.

**Refuses (exit 2) when** `--dir` is missing or not a directory.

**Deliberately does not check:** shebang lines or file contents (an extension
list is auditable; content sniffing is a heuristic that lies both ways);
languages beyond the listed extensions (extend the list, don't sniff);
code *fences inside markdown* — quoted code is prose about code and exactly
what a self repo should hold; symlinks (not followed, not flagged); the tools
repo itself — this check aims at the self repo, and this repo would rightly
fail it.

---

## nova-self-talk — the self-talk register, classified

```
nova-self-talk [--skip <basename>]... <file>...
```

A second binary, deliberately **not** a `nova-check` subcommand. The four
checks above are walls: a record passes or it does not. This is an **advisory
instrument** for a different layer — the register of the prose itself. It
classifies and reports; whether a flagged sentence should be dated, cut, or
kept is the writer's judgment, and the tool must never make it.

**Asserts.** No scanned file contains a **standing** first-person claim: an
assertion, in negative vocabulary, about what the writer permanently IS
("I am fallible") or permanently CANNOT do ("I cannot check my own work").
The one distinction that decides every case: **a capability denial is a
measurement with a date, never a remembered property.**

| verdict | meaning | example |
|---|---|---|
| `DATED` | a record; welcome; stdout; never affects the exit code | "On 2026-07-30 I could not check my own work." |
| `STANDING` | says what the writer permanently is; flagged | "I cannot check my own work." |

**Matching.** Files are flattened before matching — markdown emphasis
stripped, hard-wrapped lines collapsed — so formatting cannot hide a claim;
both regression cases that occasioned the tool were claims spanning a hard
wrap; both are pinned as tests (in unwrapped form), and wrap-spanning itself
is pinned separately on a synthetic case. A prohibition carrying no first-person
claim ("Never tolerate intolerance.") is a rule, not self-talk, and must not
flag. The negative-vocabulary filter is deliberately narrow: widening it to
match bare "cannot" would flag every prohibition, which is the predecessor's
disease (below).

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
is reported (`SELFTALK SKIP`), not silent, and contributes nothing to the
exit code. It exists for rule documents: a rule document written as
first-person absolutes about its writer will flag, and **flagging is it
working — never a reason to soften a rule.** (One written purely as
prohibitions — no first-person claims — passes clean and needs no skip.) Skipping it by name, per run, is the
honest alternative. **Nothing is skipped by default**: this tool's ancestor
hardcoded its own repo's rule-document names as a default skip list, and the
condition of its promotion here was that the list move to the caller and the
default become empty — the no-defaults law, applied to scope. A test pins
each formerly-special name as scanned, so no default list can quietly return.

**Says NO when** any scanned file contains a standing claim — one
`SELFTALK FAIL <file>: STANDING: <claim>` line per finding on stderr, exit 1.

**Refuses (exit 2) when** no files are named, a `--skip` value is empty or
contains a path separator, a flag is unknown, or a named file cannot be read
(the run stops at the first unreadable file — a partial scan must not
masquerade as a verdict).

**The permanent MISS, stated on every run.** It catches one class only:
first-person claims in negative vocabulary. Trait claims built from neutral
words — *"My summaries drift toward the tidier story."* — carry no trigger
vocabulary at all, and widening the pattern to reach them flags half of any
file, so the two classes cannot be one tool. (An earlier draft of this
paragraph cited *"in one direction, reliably: toward the version that
flatters me"* as the canonical uncatchable — that sentence was in fact pulled
INTO reach by extending the vocabulary, its capture is pinned as a regression
test, and a cold reader caught this spec still calling it unreachable. The
class beyond the vocabulary remains out, permanently; the example above is
verified to escape.) Every
completed run therefore ends with a `SELFTALK NOTE` line saying exactly that,
because a green from a partial check reads exactly like a green from a
complete one. **A green means one class is clear, never that the file is.**
A falling claim count means the input got better — the tool working, not the
tool finishing.

**Deliberately does not:** judge (it classifies; the cut is the writer's);
count harm (a cruel sentence with no first-person claim scores clean, and the
output is never a reason to soften a rule); recurse, glob, or guess (the
caller names every file); follow config files or the network (none, ever);
catch the neutral-vocabulary class (permanent by design, not pending).

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
the read reached the directory and the directory said the file is not there.
A readable box says whatever it says. An **unreadable box — permissions, a
torn write, malformed JSON, a wrong-shaped value — is CANNOT TELL, treated as
BLOWN, never as clear** (exit 2: the check could not run, and could not be
proven clear). Collapsing absent and unreadable is the fail-open this package
exists to prevent.

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

## What this harness is not

The four checks stop at the record layer. They prove the files were present,
whole, sized, linked, and prose — at the moment the check ran, on the machine
that ran it. They do not and cannot prove that a model read them, understood
them, or is currently acting from them; they cannot detect a hostile input,
an injected instruction, or a compromised reader. Those walls remain doctrine
(see nova's SECURITY.md). `nova-check` exists so that the *record* those
walls stand on is checked by something that can actually say NO.

`nova-self-talk` goes one layer up — into the prose — but only for one class
of sentence, and it admits that limit in its own output on every run. It
reads words, not minds: it cannot tell a quoted claim from an asserted one,
and it must never be read as a verdict on a file, only on a class.

`nova-fuse` is state and an answer, not enforcement. It can say NO to a
reader that asks; it cannot make a reader ask. The application rule — every
ingestion path checks the fuse before its first credential read — lives in
the callers, and it is the part of this design most likely to rot quietly.
