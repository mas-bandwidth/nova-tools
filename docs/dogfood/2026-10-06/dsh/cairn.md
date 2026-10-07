# nova-cairn dogfood — dsh (zhi), 2026-10-06

Read cold as a stranger: only `nova-cairn -h`, `nova-cairn help`, every verb's
`-h`, and the tool's page under `docs/` (`docs/SPEC-CAIRN.md` and the
`## nova-cairn` section of `docs/CLI.md`). No source was read. Built from the
staged checkout at
1dc3dd9cdf2d380c1b24ac6601e9e8be7bd39eb7 and used as
`nova-cairn v1.0.1-0.20261007144630-1dc3dd9cdf2d linux/amd64 go1.26.6`
(built and run on the Linux bench; no `go` runs on the machine the author sat
at). The sitting ran every verb for real on scratch stores under the job
directory: `open` (create, re-open matching and conflicting, `--dry-run`,
`--json`, a flat record, a hand-kept nested `sessions/<id>.md` with no log), 
`append` (`--text`, `--file`, `--file -`, duplicate, conflict,
`--publish` override, `--source`, `--now`, `--dry-run`, `--json`), `index`
(default, `--session`, `--max 2`, `--max 0`, `--json`, empty store), `receipt`
(`--text`, `--json`), `version`, and `help`; both store shapes were exercised,
and the refusals (missing flags, bad ids, a bad policy, a bad clock, both
`--text` and `--file`, a missing file, an empty note, a store that is a
regular file, a missing store or session or entry, an unknown verb and an
unknown flag) were run too.

## Findings

1. `nova-cairn append --store ./flat --session b9395d11 --entry beat-1405 --publish manual --text "the words to keep" --now 2026-10-01T00:00:00Z`
   (`./flat` is a hand-kept flat store holding `b9395d11.md`)

       APPEND OK session=b9395d11 entry=beat-1405 source=- persisted=true published=false publish=manual duplicate=false stamp=2026-10-01T00:00:00Z
       exit=0

   and then `find ./flat -mindepth 1 | sort` prints

       ./flat/b9395d11.md
       ./flat/.b9395d11.md.lock

   Expected: the page promises the flat record is left alone. `docs/CLI.md`
   says "Nothing appears beside the file: no `entries/`, no `log.jsonl`, no
   index", and `docs/SPEC-CAIRN.md` says "Nothing appears beside the file — no
   `entries/`, no `log.jsonl`, no index — because the file IS the record". The
   real append leaves a permanent hidden `.<session>.md.lock` (0 bytes, never
   removed; it survives later `index` and `receipt` runs) in the caller's
   hand-kept directory, so a reader who trusts the page is handed a file the
   page says cannot be there. Grade: URGENT.

2. `nova-cairn receipt --store ./checkpoints --session session-1 --entry note-1 [--text]`
   (the last command of the `sh` block that opens `docs/CLI.md`'s
   `## nova-cairn` section, pasted exactly as printed)

       RECEIPT REFUSED: takes no positional arguments, got "[--text]" (flags come before arguments); run: nova-cairn help
       exit=2

   Expected: a command printed on the tool's own page runs. The first three
   lines of that block run clean and print the documented shape, and the
   `[--text]` spelling is the page's metasyntax, not a shell form: pasted, it
   reaches the verb as the positional argument `[--text]`. The section also has
   no `### First run` heading, so this block *is* the first run a stranger is
   handed, and its last line refuses. Grade: URGENT.

3. `nova-cairn append --store ./nested --session s1 --entry q1 --text "policy try" --publish never`
   (`./nested/s1` was opened with `--publish manual`)

       APPEND FAILED session=s1 entry=q1: session "s1" holds publish=manual; --publish never names another; run: nova-cairn append --store ./nested --session s1 --entry q1 --publish manual
       exit=1

   Expected: the docs page says the flag records the entry's own word.
   `docs/CLI.md` says "an `append` with no `--publish` carries it; an `append
   --publish` names the entry's own", and `docs/SPEC-CAIRN.md` says the
   caller-chosen policy "travels with the entry for a later explicit act to
   carry". The tool instead refuses the documented override at exit 1 and its
   remedy tells the reader to drop the flag the page told them to use; the
   tool's own help agrees with the refusal, so the page and the tool cannot
   both be true. Grade: URGENT.

4. `nova-cairn append --store ./nested --session s1 --entry f1 --file ./nope.txt`

       APPEND REFUSED: open ./nope.txt: no such file or directory; run: nova-cairn help
       exit=2

   Expected: a refusal naming the flag that wants a readable file, the way the
   missing-input refusals name `--store`, `--session`, `--entry` and `--text`.
   Instead the line is the raw `os.Open` error and `run: nova-cairn help` is
   the only next step, so a reader who mistyped `--file` is not told so.
   Grade: NEXT.

5. `nova-cairn open --store ./afile --session s1 --publish never`
   (`./afile` is a regular file, not a directory)

       OPEN REFUSED: cannot read the session's open record from afile/log.jsonl: not a directory; run: nova-cairn help
       exit=2

   and `nova-cairn index --store ./afile` prints

       INDEX REFUSED: store "./afile" is not a directory; run: nova-cairn help

   Expected: `index` names the input the caller typed; `open` names an internal
   `log.jsonl` path the caller never typed and reports the syscall (`not a
   directory`) rather than the wrong store, so the one-line remedy does not
   name the offending store and two verbs reading the same input describe it
   differently. Grade: NEXT.

6. `nova-cairn open --store ./flat --session b9395d11 --publish immediate`
   and then `nova-cairn open --store ./flat --session b9395d11 --publish never`
   (`./flat` is a flat record, which stores no policy)

       OPEN OK session=b9395d11 store=./flat source=- publish=immediate stamp=2026-10-07T14:55:55.221823317Z
       OPEN OK session=b9395d11 store=./flat source=- publish=never stamp=2026-10-07T14:55:55.226842938Z

   Expected: `docs/CLI.md` says "`open` on a flat record is a no-op" and the
   flat format "stores no source or publication policy", which the read verbs
   honor (`index` prints `publish=unknown opened=-`, `receipt` prints
   `publish=unknown source=-`). Both `open` lines run and echo the caller's
   `--publish` as though the record held it, and neither marks the no-op
   (`reopened=true` now appears for a nested re-open), so two readings of the
   same record disagree about what it stores. Grade: NEXT.

7. `nova-cairn -h`
   (the `usage:` block, second line)

       usage:
         nova-cairn open --store <dir> --session <id> [--source <ptr>] --publish <never|manual|deferred|immediate> [--now <rfc3339-utc>] [--dry-run]
         nova-cairn NOTE: --publish is a recorded word, nothing more: never, manual, deferred and immediate are the four this tool accepts and it acts on none of them; append with no --publish carries the session's, and one that differs is a conflict naming both (exit 1).

   Expected: the `usage:` block lists the commands a reader can run. The
   `nova-cairn NOTE: ...` paragraph is inside it, so it reads as a command;
   pasted, `nova-cairn NOTE:` is an unknown verb. The paragraph belongs after
   the block, as prose. Grade: NEXT.

8. `nova-cairn --version`

       nova-cairn v1.0.1-0.20261007144630-1dc3dd9cdf2d linux/amd64 go1.26.6
       exit=0

   and `nova-cairn version --version`

       VERSION REFUSED: unknown flag --version; the flags of version are --json; run: nova-cairn version -h
       exit=2

   Expected: the accepted spelling is named where it is accepted. The bare
   `--version` works, but neither the top help nor `version -h` mentions it,
   and the natural `version --version` is refused, so the one spelling a
   stranger guesses by analogy is the one turned away. Grade: NEXT.

## What worked

The nested round trip held: `open` with `--source` and `--publish`, a matching
re-open as `reopened=true` with the original stored stamp, `append` carrying the
session's `--source` and `--publish`, a retry as `duplicate=true` with the
original stamp, a same-id/different-words conflict at exit 1 naming
`receipt --text`, and `index`/`receipt` naming stamp, bytes, source and the
`persisted/published/publish` split. The flat shape read a hand-kept record in
place: `append` landed a dated `## <stamp> — <entry>` section with the words
byte-for-byte, `open` on it wrote no second record, and the duplicate and
conflict rules read that section (the duplicate line correctly said
`publish=unknown`). `--text`, `--file` and `--file -` kept bytes exactly
(a trailing newline counted: 18 bytes for `line one\nline two\n`), an empty
file or empty stdin was refused, double quotes in `--text` printed escaped, a
non-UTC RFC 3339 `--now` normalized to UTC, and `--dry-run` created nothing.
The refusals mostly name every missing input at once and end in a runnable next
step; the printed `open` remedy for an append to a session neither shape holds
round-trips through a POSIX shell. `--json` carries the same value as the line
(including `more` under `--max`), `--max 2` bounded the entries with
`INDEX MORE kind=entry shown=2 total=8` and `--max 0` listed all, a missing
session or entry refusal named the `index` that lists what is there, and the
exit codes matched the table everywhere else I could reach (0 for a duplicate
and for an existing empty store, 1 for a conflict, 2 for usage and for a store
that could not answer).

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	1.858s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	11.226s

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.007s [no tests to run]

The card's named test `TestDocsTreeIsConsistent` does not exist in
`./internal/docs` at this tip, so the named-test run passes with nothing to
run; the docs package's own walk is the check this report's file answers, and
both packages are green at this head.

READ 6/10 — the banner answers what the tool does, how it works, where its
state lives and a first run, every verb answers `-h` with its flags, effect
class and the exit table, and the spec states the nested/flat, duplicate,
conflict and refusal rules plainly; the score is held down by a `docs/CLI.md`
section whose printed first-run block ends in a refusal, a page that promises an
`append --publish` override the tool refuses as a conflict, a `usage:` block
carrying a non-command `NOTE` line, and an accepted `--version` no page names
while `version --version` is refused.
USE 7/10 — every verb ran for real over nested and flat scratch stores, and
`--text`/`--file -`, `--now`, `--dry-run`, `--json`, `--max`/`MORE`, the
duplicate, conflict, missing-input and bad-id refusals and the shell remedy all
did what the help says; a real flat append leaves an undocumented `.s.md.lock`,
an `open` on a flat record echoes a policy the record does not store, and two
error paths print raw OS or internal names instead of the input.

urgent=3 next=5
