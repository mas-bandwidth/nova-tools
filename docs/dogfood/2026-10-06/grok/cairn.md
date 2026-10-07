# Dogfood: nova-cairn — 2026-10-06, grok

Read cold as a stranger: only `nova-cairn -h`, `nova-cairn help`, every verb's
`-h`, and the page under `docs/` (`docs/SPEC-CAIRN.md` and the `## nova-cairn`
section of `docs/CLI.md`). Built from the checkout at e8f70f600 as
`nova-cairn v1.0.1-0.20261007032034-e8f70f600ebf linux/amd64 go1.26.6`, and used
for real on the Linux bench over scratch stores under the job directory: every
verb at least once with its real flags, the nested and the flat store shapes,
`--text` and `--file -`, `--now`, `--dry-run`, `--json`, `--max`, and the
refusals. `S` below is the store named on that run. No code was changed.

## Findings

1. `nova-cairn append --store "$S" --session flat1 --entry third --text "third words" --now 2026-10-03T00:00:00Z`
   (`$S` holds the hand-kept flat record `flat1.md`)

       APPEND OK session=flat1 entry=third source=- persisted=true published=false publish=unknown duplicate=false stamp=2026-10-03T00:00:00Z
       exit=0

   and then `find "$S" -mindepth 1 | sort` prints

       $S/flat1.md
       $S/.flat1.md.lock

   Expected: the page promises the flat record is left alone. `docs/CLI.md`
   says "Nothing appears beside the file: no `entries/`, no `log.jsonl`, no
   index", and `docs/SPEC-CAIRN.md` says "Nothing appears beside the file — no
   `entries/`, no `log.jsonl`, no index — because the file IS the record". A
   real append instead leaves a permanent hidden `.<session>.md.lock` (0 bytes,
   never removed) in the caller's hand-kept directory, so a stranger who trusts
   the page is surprised by a file the page says cannot be there. Grade:
   URGENT.

2. `nova-cairn receipt --store ./checkpoints --session session-1 --entry note-1 [--text]`
   (the third command of the `sh` block that opens `docs/CLI.md`'s
   `## nova-cairn` section, pasted exactly as printed)

       RECEIPT REFUSED: takes no positional arguments, got "[--text]" (flags come before arguments); run: nova-cairn help
       exit=2

   Expected: a command printed on the tool's own page runs. The first three
   lines of that block run clean, and the `[--text]` spelling is not a shell
   form — it reaches the verb as the positional argument `[--text]`. The
   section also has no `### First run` opening (the convention every other
   tool's section follows), so this block *is* the first run a stranger is
   handed, and its last line refuses. Grade: URGENT.

3. `nova-cairn open --store "$S" --session s1 --source a --publish manual`
   run twice against the same store, no `--now`

       OPEN OK session=s1 store=$S source=a publish=manual stamp=2026-10-07T03:34:25.732050799Z
       exit=0

       OPEN OK session=s1 store=$S source=a publish=manual stamp=2026-10-07T03:34:25.753775003Z
       exit=0

   Expected: the no-op its own help promises ("A re-open naming the recorded
   policy (and source, when given) changes nothing"). Both lines are
   byte-identical but for the stamp, and the second stamp is this call's clock,
   not the `Opened:` stamp the record holds, so a retry after a timeout cannot
   tell a create from an already-open record. `append` gets this right: a retry
   prints `duplicate=true` with the original stored stamp. Grade: NEXT.

4. `nova-cairn open --store "$S" --session s --publish immediate`
   (`$S` holds only the flat record `s.md`)

       OPEN OK session=s store=$S source=- publish=immediate stamp=2026-10-07T03:36:28.824493835Z
       exit=0

   and then `nova-cairn receipt --store "$S" --session s --entry e1` prints

       RECEIPT OK session=s entry=e1 stamp=2026-10-01T00:00:00Z bytes=5 source=- persisted=true published=false publish=unknown

   Expected: the flat format stores no policy, and the receipt says so
   (`publish=unknown`); the `open` line echoes the caller's `--publish` as
   though the record held it, so two readings of the same record disagree
   about what it stores. Grade: NEXT.

5. `nova-cairn append --store "$S" --session s1 --entry f3 --file "$S/nope.txt"`

       APPEND REFUSED: open $S/nope.txt: no such file or directory; run: nova-cairn help
       exit=2

   Expected: a refusal naming the flag that wants a readable file, the way the
   missing-input refusals name `--store`, `--session`, `--entry` and `--text`.
   Instead the line is the raw `os.Open` error, and `run: nova-cairn help` is
   the only next step, so a reader who mistyped `--file` is not told so.
   Grade: NEXT.

6. `nova-cairn open --store "$S/afile" --session s1 --publish never`
   (`afile` is a regular file, not a directory)

       OPEN REFUSED: cannot read the session's open record from $S/afile/log.jsonl: not a directory; run: nova-cairn help
       exit=2

   Expected: `index` on the same input refuses with `store "$S/afile" is not a
   directory`; `open` instead names an internal `log.jsonl` path the caller
   never typed and reports the syscall (`not a directory`) rather than the
   wrong input, so the one-line remedy does not name the offending store.
   Grade: NEXT.

7. `nova-cairn -h`
   (the `usage:` block, third line)

       usage:
         nova-cairn open --store <dir> --session <id> [--source <ptr>] --publish <never|manual|deferred|immediate> [--now <rfc3339-utc>] [--dry-run]
         nova-cairn NOTE: --publish is a recorded word, nothing more: never, manual, deferred and immediate are the four this tool accepts and it acts on none of them; append --publish records the entry's own word, and one that differs from the session's is recorded as given, not a conflict (exit 0).

   Expected: the `usage:` block lists the commands a reader can run. The
   `nova-cairn NOTE: ...` line is inside it, so it reads as a command; pasted,
   `nova-cairn NOTE:` is an unknown verb. The paragraph belongs after the
   block, as prose. Grade: NEXT.

8. `nova-cairn --version`

       nova-cairn v1.0.1-0.20261007032034-e8f70f600ebf linux/amd64 go1.26.6
       exit=0

   and `nova-cairn version --version`

       VERSION REFUSED: unknown flag --version; the flags of version are --json; run: nova-cairn version -h
       exit=2

   Expected: the accepted spelling is named where it is accepted. The bare
   `--version` works, but neither the top help nor `version -h` mentions it,
   and the natural `version --version` is refused, so the one spelling a
   stranger guesses by analogy is the one turned away. Grade: NEXT.

## What worked

The nested round trip held: `open` with `--source` and `--publish`, `append`
carrying the session's `--source` and `--publish`, a retry as `duplicate=true`
with the original stamp, a same-id/different-words conflict at exit 1 naming
`receipt --text`, and `index`/`receipt` naming stamp, bytes, source and the
`persisted/published/publish` split. The flat shape read and appended a
hand-kept record in place (duplicate and conflict rules over the dated
sections), `--text` stayed byte-for-byte through `--file -` and an empty file
was refused, a missing `--file` aside. The refusals mostly name every missing
input at once and end in a runnable next step; the printed `open` remedy
round-trips through a POSIX shell, including a store path with a single quote
and one with a newline. `--json` carries the same value as the line, `--max`
bounds sessions and entries separately with `MORE kind=... total=...`, and
`open --dry-run` created nothing.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	1.653s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	14.667s

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.009s [no tests to run]

The card's named test `TestDocsTreeIsConsistent` does not exist in
`./internal/docs` at this tip, so the named-test run passes with nothing to
run; the docs package's own walk is the check this report's file answers, and
both packages are green at this head.

READ 7/10 — the banner answers what the tool does, how it works, where its
state lives and a first run, every verb answers `-h` with its flags, effect
class and the one exit table, and the spec states the nested/flat, duplicate,
conflict and refusal rules plainly; the score is held down by the `usage:`
block's non-command NOTE line, an accepted `--version` no page names while
`version --version` is refused, and a `docs/CLI.md` section with no
`### First run` whose printed last line refuses.

USE 7/10 — every verb ran for real over nested and flat scratch stores, and
`--text`/`--file -`, `--now`, `--dry-run`, `--json`, `--max`/`MORE`, the
duplicate, conflict, missing-input and bad-id refusals and the shell remedy
round trip all did what the help says; a real flat append leaves an
undocumented `.s.md.lock`, a re-`open` is indistinguishable from a create, and
two error paths print raw OS or internal names instead of the input.

urgent=2 next=6
