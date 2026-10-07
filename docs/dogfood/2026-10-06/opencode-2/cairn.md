# Dogfood: nova-cairn — 2026-10-06, opencode-2

One friend, one tool, cold. I read only `nova-cairn -h`, `nova-cairn help`,
every verb's `-h`, and the page under docs/ (`docs/SPEC-CAIRN.md` and the
`## nova-cairn` section of `docs/CLI.md`), then used every verb at least once
with its real flags against nested and flat scratch stores on the Linux bench.
Built from this checkout at abb9bfecc as
`nova-cairn v1.0.1-0.20261007153756-abb9bfecc729 linux/amd64 go1.26.6`. Used
for real: `open`, `append`, `index`, `receipt`, `version` and `help`, the nested
and the flat store shapes, `--text` and `--file -`, `--source`, `--publish`,
`--now`, `--dry-run`, `--json`, `--max`, retries, conflicts, bad ids and the
refusals. No code was changed; a finding is recorded here, never fixed.

## Findings

1. `nova-cairn append --store ./cairns --session b9395d11 --entry beat-1405 --text "the words to keep"`
   (`cairns/b9395d11.md` is a hand-kept flat record, copied from
   `internal/cairn/testdata/bench-b9395d11.md`)

       APPEND OK session=b9395d11 entry=beat-1405 source=- persisted=true published=false publish=unknown duplicate=false stamp=2026-10-07T15:52:33Z
       exit=0

   and then `find ./cairns -mindepth 1 | sort` prints

       ./cairns/.b9395d11.md.lock
       ./cairns/b9395d11.md

   Expected: the page promises the flat record is left alone beside its own
   bytes. `docs/CLI.md` says "Nothing appears beside the file: no `entries/`, no
   `log.jsonl`, no index", and `docs/SPEC-CAIRN.md` says the same. The append
   instead leaves a permanent hidden `.<session>.md.lock` beside the hand-kept
   prose; `append`, `index` and `receipt` never remove it, so a caller who
   trusts the page meets a sidecar the page says cannot be there. Grade: URGENT.

2. `nova-cairn open --store ./cairns --session b9395d11 --publish never --source ptr1`
   (`cairns/b9395d11.md` is the hand-kept flat record)

       OPEN OK session=b9395d11 store=./cairns source=- publish=never stamp=2026-10-07T15:52:52.885208563Z
       exit=0

   and a second `nova-cairn open --store ./cairns --session b9395d11 --publish immediate`
   prints

       OPEN OK session=b9395d11 store=./cairns source=- publish=immediate stamp=2026-10-07T15:52:52.89060399Z
       exit=0

   while `nova-cairn index --store ./cairns` prints

       INDEX OK sessions=1 entries=0
       INDEX SESSION session=b9395d11 publish=unknown opened=- entries=0

   Expected: the flat format stores no source or policy, and the page says so —
   a flat record prints `publish=unknown`, the same reason a flat `append` with
   no `--publish` says `publish=unknown`. `open` instead echoes whatever the
   caller typed, so two opens name two different policies and both answer OK,
   and the very next read of the same file names a third value; `--source ptr1`
   is dropped without a word, and the success line prints `source=-`. Grade:
   URGENT.

3. `nova-cairn receipt --store ./checkpoints --session session-1 --entry note-1 [--text]`
   (the fourth line of the `sh` block that opens `docs/CLI.md`'s `## nova-cairn`
   section, pasted exactly as printed)

       RECEIPT REFUSED: takes no positional arguments, got "[--text]" (flags come before arguments); run: nova-cairn help
       exit=2

   Expected: a command printed on the tool's own page runs. The first three
   lines of the block run clean, and the `[--text]` spelling is not a shell form
   — it reaches the verb as the positional argument `[--text]`. The section also
   carries no `### First run` heading, so this block *is* the first run a
   stranger is handed, and its last line refuses. Grade: NEXT.

4. `nova-cairn help --json`

       CAIRN REFUSED: unknown verb "--json"; the verbs are open, append, index, receipt, version; run: nova-cairn help
       exit=2

   Expected: the banner says "Every verb takes `--json`", and the usage list
   stands `nova-cairn help [<verb>]` among the verbs, so `help --json` should
   answer the help as one JSON object the way `version --json` answers the
   version. `nova-cairn help open --json` exits 0 and ignores the flag, so the
   three spellings disagree about whether the help door takes `--json`. Grade:
   NEXT.

5. `nova-cairn NOTE:`

       CAIRN REFUSED: unknown verb "NOTE:"; the verbs are open, append, index, receipt, version; run: nova-cairn help
       exit=2

   Expected: the `nova-cairn NOTE: --publish is a recorded word ...` line sits
   between the `open` line and the `append` line of the usage block, where every
   other line is a command a reader can paste. It is a note about `--publish`,
   not a verb; as printed it reads as a sixth command, and only the refusal
   names it for what it is. Grade: NEXT.

6. `nova-cairn append --store ./cairns --session s1 --entry e2 --publish never`
   (session `s1` was opened with `--publish manual`)

       APPEND FAILED session=s1 entry=e2: session "s1" holds publish=manual; --publish never names another; run: nova-cairn append --store ./cairns --session s1 --entry e2 --publish manual
       exit=1

   Expected: the printed `run:` is the next command, so pasting it should repair
   the call. Pasting it instead refuses:

       APPEND REFUSED: the words come from --text or --file; refusing to guess; run: nova-cairn help
       exit=2

   The remedy names the recorded policy but not the words, so the one-turn
   recovery the refusal promises costs a second turn. Grade: NEXT.

7. `nova-cairn append --store ./cairns --session nope --entry e1 --text hi`
   (no such session)

       APPEND REFUSED: no such session "nope" under store "./cairns"; open first: nova-cairn open --store ./cairns --session nope --publish manual; run: nova-cairn help
       exit=2

   Expected: the remedy names the door and the record, but not a policy the
   caller never chose. This caller named no `--publish` and the tool writes
   `manual` into the remedy; with `--publish never` on the append the same
   refusal prints `never`, and with `immediate` it prints `immediate`, so a
   policy is invented only when the caller names none. A reader who wanted
   local-only and pastes the remedy records `manual`. Grade: NEXT.

8. `go test -count=1 -timeout 600s ./cmd/nova-cairn` (run with `TMPDIR` and
   `GOTMPDIR` inside this job's directory, as the card's STEP 1 names, whose
   name carries `~`)

       --- FAIL: TestReopenPrintsStoredOpenedTime (0.04s)
       --- FAIL: TestAReOpenNamingAnotherPolicyOrSourceIsAConflict (0.00s)
       --- FAIL: TestEveryRefusalEndsAtTheCommandThatMovesTheUserOn (0.08s)

   (five refusal tests in all, `TestAConflictOrAMissingEntryNamesTheCommandToRunNext`
   and `TestAppendPublishThatDisagreesIsRefused` among them), while the same
   command with a `~`-free `TMPDIR` answers
   `ok  	github.com/mas-bandwidth/nova-tools/cmd/nova-cairn	0.112s`. The package's
   tests compare a refusal's `run:` remedy against the raw temp path, and the
   tool shell-quotes a store path whose bytes carry `~`; the printed
   `run: nova-cairn open --store '/…w1~15.g17/tmp/…' --session s --publish manual`
   is a valid POSIX command and runs, so the tool is right and the tests are
   brittle to a `~` in the path. Expected: the package is green under the
   `TMPDIR` the card names, or the tests accept the quoted remedy. Grade: NEXT.

What did hold: no default store and a missing flag named every time, one line
each and all at once; every bad id refused at exit 2 with the store's bytes
unchanged; the same call through `--text`, `--file` and `--file -` kept the
bytes and the retry answered `duplicate=true` with the original stamp; a
conflicting same-id append exited 1 and named the `receipt --text` that reads
it; a re-open of a nested session with the recorded policy answered
`reopened=true` with the stored stamp, and another policy or source exited 1
naming the matching `open`; the session's `--source` was inherited by an append
that named none; `--dry-run` on both `open` and `append` wrote nothing and a new
entry said `persisted=false`; `index` defaulted to 20 with one `MORE shown=20
total=25` line, `--max 3` cut to three, `--max 0` listed all 25, and a missing
store or session refused while an empty store indexed as `sessions=0 entries=0`;
`--json` carried the same facts and items as the lines; the flat shape's dated
sections were read without changing the file, the nested record won when both
shapes existed and counted once, an invalid stamp and a duplicate heading
refused, and ordinary prose was not turned into entries; 20 concurrent appends
all landed and a stale `.*.tmp*` sibling was never indexed; refusals went to
standard error and successes to standard output.

## Gate

Run on the Linux bench (linux/amd64, go1.26.6, `GOCACHE` on the bench,
`GOFLAGS=-mod=readonly`, `NOVA_TEST_NO_HOST=1`, `TMPDIR` and `GOTMPDIR` inside
the job directory). The card's STEP 4 gate,
`go test -count=1 -timeout 600s ./internal/docs ./internal/ci`, ends:

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.081s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	13.353s

The card's named test,
`go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent`,
does not exist at this tip and ends:

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.020s [no tests to run]

`go test -count=1 -timeout 600s ./internal/cairn` ends:

    ok  	github.com/mas-bandwidth/nova-tools/internal/cairn	0.196s

`go test -count=1 -timeout 600s ./cmd/nova-cairn` is the package finding 8
records: under the job directory's `~`-carrying `TMPDIR` five refusal tests are
red, and with a `~`-free `TMPDIR` it ends:

    ok  	github.com/mas-bandwidth/nova-tools/cmd/nova-cairn	0.112s

The one new file is this report; no code changed.

READ 7/10 — the banner, the four verb helps and the two store shapes were
enough to run every verb cold and to predict every refusal, but the usage block
offers `NOTE:` as a command, the page's own receipt line does not run, and the
`--json` promise the help door refuses.

USE 7/10 — nested open, append, retry, conflict, file, stdin, index, receipt,
`--max`, `--json` and `--dry-run` kept the bytes and named a next command, but a
flat hand record is the weak half: its append leaves a file the page says cannot
exist, and its open reports a policy the file does not hold.

urgent=2 next=6
