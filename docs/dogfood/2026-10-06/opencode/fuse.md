# nova-fuse dogfood, 2026-10-06

Reviewer: a stranger (opencode, this card). Build under test:
nova-fuse v1.0.1-0.20261006184440-8076dfdfcd85. Method: read only `nova-fuse help`
and the nova-fuse section of docs/SPEC.md and docs/CLI.md, then ran every
verb for real with its real flags against scratch boxes in a temp dir, including
the refusals.

## Findings

### 1. `--json` is refused on every verb

Command as typed:
```
nova-fuse check --json --box ./cmd/nova-fuse/testdata/example-box.json a-public-issue-tracker
```
Printed (first 3 lines):
```
nova-fuse check REFUSED: unknown flag --json; the flags of check are --box; run: nova-fuse help check
```
Expected: docs/STANDARD.md requires `Every verb accepts --json`, and `--json` is a
shared flag across the family ("The same flag means the same thing in every tool:
--json"). The banner acknowledges the exception ("There is no --json"), but every
other nova tool accepts it, so a cold AI caller typing `--json` on nova-fuse gets a
refusal instead of a JSON rendering. I tried it on version, init, status, quarantine,
lockdown, and lift quarantine — all refuse.
Grade: NEXT

### 2. Spec grammar says `FAIL`, the binary writes `FAILED`

Command as typed:
```
nova-fuse check --box ./cmd/nova-fuse/testdata/example-box.json a-public-issue-tracker
```
Printed (first 3 lines):
```
FUSE FAILED quarantine=a-public-issue-tracker since=2026-09-08T21:14:00Z: an issue body addressed me directly and asked for a token (soft: yours to lift when the surface is safe again: nova-fuse lift quarantine --box './cmd/nova-fuse/testdata/example-box.json' -- 'a-public-issue-tracker')
```
Expected: docs/SPEC.md lines 2043-2054 give the grammar as `FUSE FAIL`,
`LOCKDOWN FAIL`, `QUARANTINE FAIL`, `LIFT FAIL`, `INIT FAIL`. The binary prints
`FAILED` for all of these. The same block in SPEC.md:2065 calls them "FAIL lines",
and the executed transcript in docs/TESTS.md:388 matches the binary (`FUSE FAILED`).
So the spec's normative grammar samples do not match the bytes the binary writes —
a caller matching the grammar samples would match none of the failure lines.
Grade: NEXT

### 3. A blown check prints only to stderr; stdout is empty on failure

Command as typed:
```
nova-fuse check --box ./cmd/nova-fuse/testdata/example-box.json a-public-issue-tracker 2>/dev/null
```
Printed (first 3 lines):
```
(empty — stdout carries nothing; exit code 1)
```
Expected: a caller reading stdout alone gets no line when the check is blown.
`FUSE OK` goes to stdout but `FUSE FAILED` goes to stderr (documented at
SPEC.md:2065, but not obvious from the example block in the banner, which shows
check output without distinguishing the streams). An AI plumbing check into a pipe
that reads stdout would see a silent pass-through of empty bytes on failure.
Grade: NEXT

### 4. Dry-run omits `dry_run=true` when the write would be a no-op

Command as typed:
```
nova-fuse lockdown --box <already-blown-box> --dry-run "new reason"
```
Printed (first 3 lines):
```
LOCKDOWN OK already=blown since=2026-10-06T18:48:01Z: the source is serving a tampered package (standing record kept; the new reason was not recorded: new reason)
```
Expected: SPEC.md:2059-2061 says "its OK line carries `dry_run=true`." When the
target is already in the blown state, the line says `already=blown` and `standing
record kept` but does NOT carry `dry_run=true`. The same happens with
`quarantine --dry-run` on an already-quarantined surface. A caller checking for
the `dry_run=true` token to decide whether to trust the line would miss these.
Grade: NEXT

### 5. An unknown flag stops the parse before the missing required `--box` is named

Command as typed:
```
nova-fuse check --bogus
```
Printed (first 3 lines):
```
nova-fuse check REFUSED: unknown flag --bogus; the flags of check are --box; run: nova-fuse help check
```
Expected: the spec says "one run reports every independent problem it can find."
With `--box` missing AND `--bogus` present, the refusal only names the unknown
flag. It lists `--box` as a valid flag but never says it is required. A cold reader
needs a second run (`nova-fuse check`) to learn that `--box` is required — two
refusals to discover one missing input.
Grade: NEXT

### 6. Verb list is inconsistent between top-level and help-level refusals

Command as typed:
```
nova-fuse -v
```
Printed (first 3 lines):
```
nova-fuse REFUSED: unknown verb "-v"; the verbs are init, status, check, lockdown, quarantine, lift, path, version, help; run: nova-fuse help
```
Expected: compare with `nova-fuse help bogus`, which prints: `unknown verb "bogus";
the verbs are init, status, check, lockdown, quarantine, lift quarantine, lift
lockdown, path, version`. The top-level refusal names `lift` as a verb and includes
`help`; the help-level refusal names `lift quarantine` and `lift lockdown` and omits
`help`. A stranger reading `nova-fuse -v` is told `lift` is a verb, then must
discover through `help lift` that it needs `quarantine` or `lockdown` next.
Grade: NEXT

### 7. `-v` is not accepted as a short form of `--version`

Command as typed:
```
nova-fuse -v
```
Printed (first 3 lines):
```
nova-fuse REFUSED: unknown verb "-v"; the verbs are init, status, check, lockdown, quarantine, lift, path, version, help; run: nova-fuse help
```
Expected: the banner says `nova-fuse version (--version also accepted)`. A stranger
reaching for the common `-v` short flag gets a refusal instead of the version line.
Grade: NEXT

## What was not done

No code changes were made, per the card contract ("no code changes in this card;
a finding is never fixed here, only recorded"). Each finding above is recorded
for the next release or for v1.1.0 if judged URGENT by a reviewer. The gate test
named by the card, `./internal/docs TestDocsTreeIsConsistent`, does not exist in
this tree at commit 8076dfdfcd85; `go test -run TestDocsTreeIsConsistent ./internal/docs/`
reports "no tests to run" and exits 0.

READ 8/10
The banner answers the three questions in its first lines, every `nova-fuse help <verb>`
is detailed with usage, flags, exit codes and effect, and the example block runs as printed;
the only real cost is the SPEC.md grammar drift (FAIL vs FAILED) that a reader cannot
trust without running the tool.

USE 8/10
Every verb ran for real exactly as documented — init/status/check/lockdown/quarantine/
lift quarantine all behaved correctly, `--dry-run` wrote nothing, `--` protected dash-
prefixed surface names, and `lift lockdown` refused by design; the `--json` refusal and
the stderr-only failure output are friction for automated callers but never wrong results.

urgent=0 next=7
