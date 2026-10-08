# Dogfood: nova-tokens — 2026-10-06, grok

Read cold as a stranger: only `nova-tokens -h`, `nova-tokens help`, every verb's
`-h`, and the page under `docs/` (`docs/SPEC-TOKENS.md`). Built from the branch
`sprint/mechanical-2026-10-02` at `abb9bfecc729` (`v0.0.0-20261007153756-abb9bfecc729`)
and used over a scratch tree on a bench: every verb at least once with its real
flags, the refusals too. `fold`, `report` (local), `sum`, `check`, `sources`,
`session` and `profiles` ran against fixtures I wrote from the help and the spec
(a Claude Code transcript, a bus lane, a swarm pool, a Google export, a hand-built
OpenCode database); `ledger` and `report --redis` reached the connection and were
refused by it (no server was started); the two Redis verbs have no dry-run that
dials nothing, so their store modes are graded on that refusal and their help.

## Findings

1. `nova-tokens fold --out ./out2 --day 2026-09-11 --repos ./repos.tsv --bus ./busdir`
   (a lane with two reports for one day, `ada-aaaaaaaaaaaa.md` and `ada-bbbbbbbbbbbb.md`)

       TOKENS FOLD at=2026-10-07T15:47:24Z build=v0.0.0-20261007153756-abb9bfecc729 out=./out2 sources=1 days=2026-09-11 repos=./repos.tsv
       TOKENS SOURCE label=bus:ada kind=bus path=busdir/from-ada reports=- day_basis=utc files=2 unreadable=0 messages=- dup=- noid=- nousage=- unparsed=0 comments=0 redated=0 superseded=0 rows=0
       TOKENS DAY date=2026-09-11 rows=0 models=0 repos=0 turns=- unknown=0.0% other=0.0% rough=0 dashes=0 nonutc=0 sources= written=false

   and on stderr the remedy:

       TOKENS CONFLICT label=bus:ada day=2026-09-11 notes=ada-aaaaaaaaaaaa.md,ada-bbbbbbbbbbbb.md: competing reports; send a correction whose subject carries supersedes=ada-aaaaaaaaaaaa.md,ada-bbbbbbbbbbbb.md

   Typing that remedy verbatim,
   `Subject: tokens 2026-09-11 at=... build=abc supersedes=ada-aaaaaaaaaaaa.md,ada-bbbbbbbbbbbb.md`,
   is refused:

       TOKENS UNPARSED label=bus:ada note=ada-cccccccccccc.md line=1: the predecessor ada-bbbbbbbbbbbb.md is not <sender>-<12 hex>; send a correction whose subject carries supersedes=<id>

   Expected: the correction the tool itself printed clears the conflict and its
   rows fold (the tip's value replaces the replaced note's). Instead no spelling
   works: with `.md` the shape check refuses it, and without it
   (`supersedes=ada-aaaaaaaaaaaa`) the same note answers `no such note in this lane for this day: ada-aaaaaaaaaaaa`. Every id the CONFLICT line names carries
   `.md`, so the one mechanism the spec gives for joining two tips into one
   (rule 6/rule 20) is a refusal with no remedy, and a lane-day with two reports
   can never be folded. Grade: URGENT.

2. `nova-tokens sources --repos ./repos.tsv --all --provider g=./export.csv`

       SOURCES REFUSED: --provider g=: it wants <kind>:<label>=<path>, the kind one of google, openai, xai and the label the friend whose export it is, so that two friends' exports from one provider are two sources; run: nova-tokens help

   Expected: the shape the page names. `docs/SPEC-TOKENS.md`'s `sources` block
   (line 413) says `[--provider <label>=<file>]...`, the older spelling the
   `fold` block (line 401) corrects to `--provider <kind>:<label>=<file>`; the
   kinds `google, openai, xai` and the export's columns are named only in the
   refusal, never in the help or the spec. The refusal is a good breadcrumb, so
   the cost is one turn. Grade: NEXT.

3. `nova-tokens report --swarm ./nopool`

       REPORT REFUSED: invalid value for --swarm (--swarm wants <label>=<path>, got "./nopool"): it wants labeled swarm pool directory; repeatable; run: nova-tokens report -h

   Expected: `report` to accept only the flags its usage line names. Its
   `-h` usage lists `--claude`, `--opencode`, `--provider`, `--supersedes`,
   `--note`, `--scratch`, `--timeout` and `--dry-run`, but the flag list below it
   also carries `--bus` and `--swarm`, and the verb does read a `--bus` lane
   (`report --who ada --day 2026-09-11 --repos ./repos.tsv --bus ./busdir` folds
   it), so the usage line understates the verb. Grade: NEXT.

4. `nova-tokens version --json`

       VERSION REFUSED: takes no flags and no arguments, got 1; run: nova-tokens help

   Expected: `nova-tokens version -h` prints `usage: nova-tokens version [flags]`,
   and the banner says "Every verb but version takes `--json`"; a verb whose
   usage advertises `[flags]` should either take one or say in the same line that
   it takes none. The refusal costs one turn. Grade: NEXT.

5. `nova-tokens fold --out ./freshfold --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts`

       TOKENS REFUSED: --out does not exist: ./freshfold; it wants the directory the day files are written to; run: mkdir -p ./freshfold

   Expected: the same answer from `session`, which writes day files the same way.
   Instead `nova-tokens session --claude-session ./session.jsonl --out ./freshsess`
   answers

       SESSION turns=1 input=812 cache_write=1200 cache_read=90000 output=40 weighted=11512 avg_context=92012
       TOKENS DAY day=2026-09-11 written=true rows=1 retained=0 model=claude-fable-5-1 weighted=11512

   exit 0, and creates `./freshsess/2026-09-11.tsv` and `./freshsess/fold.lock`.
   Two verbs that write the same day files treat a missing `--out` in opposite
   ways; a caller who learned the refusal from `fold` gets a directory `session`
   made on their behalf. Grade: NEXT.

6. `nova-tokens session --claude-session ./session.jsonl --out ./sessout`

       SESSION turns=1 input=812 cache_write=1200 cache_read=90000 output=40 weighted=11512 avg_context=92012
       TOKENS DAY day=2026-09-11 written=true rows=1 retained=0 model=claude-fable-5-1 weighted=11512

   Expected: a `TOKENS DAY` line in the shape the output grammar and `fold`
   print (`rows= models= repos= turns= unknown= other= rough= dashes= nonutc= sources= written=`), or a different event name. `session`'s `TOKENS DAY`
   shares nothing but the token and the date with the documented one, so a
   scanner that reads the documented grammar cannot parse it. Grade: NEXT.

7. `nova-tokens profiles --swarm-root ./pool` (a pool with
   `pool/usage/job1.tsv` holding `model=deepseek-v3`)

       PROFILES OK models=0 cards=0 overshoot=0

   Expected: one `PROFILES MODEL` line for `deepseek-v3`, or a refusal naming
   what a swarm root must contain. The same answer comes from an empty directory
   and from a directory holding `profiles/`; the verb's page says only "a
   measurement over a swarm root", its help says "the directory the swarm batches
   live under", and neither gives a layout a stranger can build, so the verb
   cannot be used as documented. Grade: NEXT.

8. `nova-tokens sources --repos ./repos.tsv --all --opencode o=./oc.db --scratch ./ocscratch`
   (an SQLite file that is not an OpenCode database)

       SOURCES SOURCE label=opencode:o kind=opencode path=./oc.db reports=input,output,cache_write,cache_read,reasoning day_basis=utc files=1 unreadable=1 messages=0 dup=0 noid=0 nousage=- unparsed=- comments=- redated=- superseded=- rows=0
       SOURCES OK sources=1 files=1 messages=0 unreadable=1 unparsed=0 rows=0 unattributed=-

   and on stderr

       SOURCES UNREADABLE label=opencode:o path=./oc.db: sqlite3: exit status 1: Parse error in 4th command line argument: no such column: data\x0a  created/1000, 'unixepoch') AS stamp, json_extract(data, '$.providerID') AS pro\x0a                                      error here ---^

   Expected: the reason in one line, as every other source gives it — "this is
   not an OpenCode database" or "no column `data`". Instead the raw sqlite3 parse
   error, its escaped newlines (`\x0a`) and a fragment of the query are the whole
   reason. Grade: NEXT.

9. `nova-tokens sources --repos ./repos.tsv --all --provider google:g=./missing.csv`

       SOURCES SOURCE label=google:g kind=provider path=./missing.csv reports=- day_basis=utc files=1 unreadable=1 messages=- dup=- noid=- nousage=- unparsed=0 comments=- redated=- superseded=- rows=0
       SOURCES OK sources=1 files=1 messages=0 unreadable=1 unparsed=0 rows=0 unattributed=-

   exit 0, while the banner's exit-code paragraph puts "an unreadable source"
   under exit 1. `docs/SPEC-TOKENS.md`'s `sources` section says "Exits 0 whenever
   it ran", so the specific page is right and the banner's list is the one that
   lies for this verb: a caller who gates on the exit code of `sources` (the verb
   the banner says "only looks") reads an unreadable source as success. Grade:
   NEXT.

10. `nova-tokens report --who ada --day 2026-09-11 --repos ./repos.tsv --claude bench=./transcripts --json`

        {"result":{"verb":"report","status":"ok","exit":0},"facts":{...},"items":[...],"payload":"2026-09-11\tada\tclaude-fable-5-1\tschema\tinput\t812\n..."}

   Expected: rule 20 says `report`'s stdout is "exactly the body lines ... and
   nothing else" and that this is the one place where the OK line leaves stdout.
   With `--json` the artifact moves into a `payload` string inside an envelope,
   and the page never says so; a reader who passes `--json` to a consumer that
   reads `report`'s stdout as the body gets JSON. The code is defensible, the
   page is incomplete. Grade: NEXT.

## Gate

The card's named test does not exist at this tip, so the named run passes with
nothing to run and the packages' own walks are the real gate. Both ran on the
bench against this commit's tree:

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci
    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.0s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	14.4s

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent
    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.0s [no tests to run]

Nothing was changed in the tool: this card records findings, it fixes none.

WHAT WAS NOT DONE. `ledger` and `report --redis` never reached a live store (no
server was started on the bench, and the card forbids one); both were driven to
`dial tcp 127.0.0.1:1: connect: connection refused` and their help read. The
OpenCode source was exercised against a database I built to the schema the
tool's own refusal named, not against a real OpenCode file. `profiles` never
produced a `PROFILES MODEL` line (finding 7). No real swarm pool with
`attempt=2`, no provider export carrying a local-day basis, and no legacy
twelve-column day file written by an older build were used; the twelve-column
reader was checked with a hand-written file and summed both lanes into one row.

READ 7/10 — the banner answers what the tool does, where its state lives, every
verb's effect and the exit table, the refusal grammar names every missing input
at once with a pasteable remedy, and the dash-never-zero rule is stated where a
reader needs it; against that, the `sources` provider spelling contradicts the
page, `version` advertises flags it refuses, `report`'s usage hides `--bus` and
`--swarm`, and the banner's exit list is untrue for the one verb that cannot
fail.

USE 7/10 — every verb but the store pair ran for real against fixtures and did
what its help says (fold, the merge by source, the shrink refusal and
`--allow-shrink`, the noid failure, the duplicate-id refusal, the badline
continue, `check`'s gap/stray/notes counts, `sum`'s dashes and MORE lines,
`report`'s body and `--note`, the OpenCode copy, the unattributed listing), and
the refusals are one line with a next step; but the bus conflict remedy cannot
be followed at all, `session` and `fold` disagree about `--out`, and `profiles`
cannot be brought to answer.

urgent=1 next=9
