# nova-tokens dogfood — codex (zhi), 2026-10-06

Read as a stranger: only `nova-tokens -h`, `nova-tokens help`,
`nova-tokens <verb> -h` and the tool's page `docs/SPEC-TOKENS.md`. Built from the
staged checkout at `1dc3dd9cdf2d380c1b24ac6601e9e8be7bd39eb7` and used as
`nova-tokens v1.0.1-0.20261007144630-1dc3dd9cdf2d linux/amd64 go1.26.6`: every
verb at least once with its real flags against a scratch store under a Linux
build bench's home (invented values only), the help's own `setup:`/`example:`
block run verbatim first, every source kind (`--claude`, `--swarm`, `--bus`,
`--provider` google/openai/xai) folded, and the refusals too. The two Redis
verbs were exercised against a loopback address with no store (dial refused), so
`report --redis`/`ledger` are graded on help, `--dry-run`, and that refusal. No
code was changed and no finding was fixed here.

## Findings

1. `nova-tokens fold --out ./outb3 --all --repos ./example/repos.tsv --bus ./bus3` (two notes in one lane for one day), then a successor note whose trailer is the id the conflict line prints, and `nova-tokens report --who ada --day 2026-09-11 --repos ./example/repos.tsv --bus ./bus3 --supersedes ada-111111111111.md`
   Printed (the conflict):
   ```
   TOKENS CONFLICT label=bus:ada day=2026-09-11 notes=ada-111111111111.md,ada-222222222222.md: competing reports; send a correction whose subject carries supersedes=ada-111111111111.md,ada-222222222222.md
   TOKENS NOTE a lane-day has competing reports (bus:ada): one note whose subject carries supersedes=<every tip, sorted> is the replacement snapshot that clears it
   TOKENS FAILED days=0 rows=0 sources=1 unreadable=0 unparsed=0 mixed=0 conflict=1 shrank=0 partial=0 quiet=0
   ```
   The printed successor with that exact trailer is refused:
   ```
   TOKENS UNPARSED label=bus:ada note=ada-333333333333.md line=2: the predecessor ada-222222222222.md is not <sender>-<12 hex>; send a correction whose subject carries supersedes=<id>
   ```
   `report --supersedes` refuses the same spelling, and the trailer without `.md` is refused as a missing target:
   ```
   REPORT REFUSED: --supersedes ada-111111111111.md: it wants a note id of the shape <sender>-<12 hex>; run: nova-tokens help
   TOKENS UNPARSED label=bus:ada note=ada-333333333333.md line=2: no such note in this lane for this day: ada-111111111111; send a correction whose subject carries supersedes=<id>
   ```
   I expected the remedy the `TOKENS CONFLICT` line prints to be a command that runs and a trailer that clears the conflict: one lane-day with two tips has a documented replacement snapshot, and `report --supersedes` is the tool that writes it. The note id the bus reader uses is the full file name (`<sender>-<12 hex>.md`, as `notes=` prints it), but the predecessor validator and `report --supersedes` both require `<sender>-<12 hex>` with no extension; each spelling is refused by the other's rule, so a conflicting lane-day cannot be reconciled by following the tool's own printed remedy.
   Grade: URGENT (a refusal with no remedy that runs; the tool's two id spellings contradict each other).

2. `nova-tokens check --out ./out6 --strict` (an `--out` of two day files 09-07 and 09-09 plus a `README.md` and a `collate.log`, no `--strict` run before it)
   Printed:
   ```
   CHECK MISSING date=2026-09-08
   CHECK STRAY out6/README.md
   CHECK STRAY out6/collate.log
   CHECK FAILED files=2 rows=4 first=2026-09-07 last=2026-09-09 bad=0 missing=1 stray=2 gap=1 notes=0
   ```
   The same run without `--strict` prints `CHECK OK … missing=0 stray=0 gap=1 notes=2`. I expected `--strict` to name the notes and the gap while leaving them notes: the tool's own help says a `*.md` or `*.log` beside the day files "is `notes=<n>` rather than a stray", and "`--strict` names those too". Instead the strict run relabels both as `CHECK STRAY` and moves them from `notes=2` to `stray=2`, so a reader is told to remove a README the non-strict line deliberately called a note.
   Grade: NEXT (a finding label and count that contradict the tool's own note/stray rule).

3. `nova-tokens sources --repos ./example/repos.tsv --all --claude a=./attr --unattributed`
   Printed:
   ```
   SOURCES SOURCE label=claude:a kind=claude path=./attr reports=input,output,cache_write,cache_read day_basis=utc files=1 unreadable=0 messages=4 dup=0 noid=0 nousage=- unparsed=- comments=- redated=- superseded=- rows=3
   SOURCES UNATTRIBUTED stem=/work/nope/y mentions=1
   SOURCES OK sources=1 files=1 messages=4 unreadable=0 unparsed=0 rows=3 unattributed=1
   ```
   I expected the field the spec's output grammar names: `docs/SPEC-TOKENS.md` documents `SOURCES UNATTRIBUTED stem=<path> tokens=<n>` and its prose says the listing is "the mentions each stem got". The binary prints `mentions=`, so a scanner or checker written from the spec's grammar finds no `tokens=`.
   Grade: NEXT (the spec's own output grammar and the binary disagree on the field name).

4. `nova-tokens session --claude-session ./session.jsonl --out ./out`
   Printed:
   ```
   SESSION turns=1 input=812 cache_write=1200 cache_read=90000 output=40 weighted=11512 avg_context=92012
   TOKENS DAY day=2026-09-11 written=true rows=2 retained=1 model=claude-fable-5-1 weighted=11512
   ```
   I expected the `TOKENS DAY` line the spec's output grammar and every other verb use: `TOKENS DAY date=<d> rows=<n> models=<n> repos=<n> turns=…`. The session form spells the day `day=`, drops `models=`/`repos=`/`turns=`, and adds `model=`, `weighted=` and `retained=`; the spec says session's lines are "in the output grammar; a scanner that reads the grammar parses them", but a scanner keyed on `TOKENS DAY date=` reads this line as a different event, and the help tells a reader nothing about the shape.
   Grade: NEXT (a written-file event that leaves the documented output grammar).

5. `nova-tokens fold --out ./out2 --all --repos ./example/repos.tsv --bus ./bus` (a `participants.json` holding a JSON array, the first shape a stranger tries)
   Printed:
   ```
   TOKENS UNREADABLE label=bus path=bus/participants.json: json: cannot unmarshal array into Go value of type struct { Participants []struct { Name string "json:\"name\""; Lane string "json:\"lane\"" } "json:\"participants\"" }
   ```
   I expected one refusal naming the shape the roster wants, the way the later valid-object error does (`participants.json names no lane; it wants {"participants":[{"name":"Ada","lane":"from-ada"}]} …`). Neither `-h` nor the docs page states the roster's JSON shape, so the first failure a stranger meets is a Go type dump.
   Grade: NEXT (an internal type in a refusal where a shape and a remedy belong).

6. `nova-tokens fold --out ./out5 --all --repos ./example/repos.tsv --bus ./bus` (a note whose `Date:` header is the RFC 3339 spelling the tool itself writes as `at=`)
   Printed:
   ```
   TOKENS UNPARSED label=bus:ada note=n1.md line=1: the Date: is not a date nova-bus writes (Mon Jan  2 15:04:05 UTC 2006): 2026-10-06T12:00:00Z
   ```
   I expected the reason to say what the header wants in prose or to show a sample (`Date: Tue Oct  6 12:00:00 UTC 2026`). The parenthetical is the Go reference layout `time.UnixDate`, and only a Go reader recognises it; the same page that documents the trailer's RFC 3339 `at=` says nothing about the `Date:` spelling, so a stranger's first note is refused with a string they cannot read.
   Grade: NEXT (an unreadable reason where a sample date is the remedy).

7. `nova-tokens fold --out ./out3 --all --repos ./example/repos.tsv --provider google:g=./bad.csv`
   Printed:
   ```
   TOKENS UNREADABLE label=google:g path=./bad.csv: the google parser does not know the column garbage in: garbage; it reads timestamp or date, cache_read_tokens, cache_write_tokens, input_tokens, model, output_tokens, reasoning_tokens, and a file of another provider's shape is declared by ITS kind
   ```
   I expected `fold -h` to tell me the export's shape before I ran it. The flag line is only `--provider <value>  kind:labeled provider export file; repeatable`, the docs page calls it "a billing export the account holder downloads (Google Cloud, xAI)", and the accepted columns (`timestamp`/`date`, `cache_*_tokens`, `input_tokens`, `model`, `output_tokens`, `reasoning_tokens`) appear for the first time in a failed run — and they are a normalised header, not the Google Cloud billing export the spec describes.
   Grade: NEXT (help that names a source but not the shape it reads; the shape is learned from an error).

8. `nova-tokens report --who ada --day 2026-09-11 --repos ./example/repos.tsv --claude bench=./transcripts`
   Printed on stderr:
   ```
   TOKENS AVG day=2026-09-11 model=claude-fable-5-1 tokens=92052 usd=- usd_per_mtok=- unpriced=92052
   TOKENS AVG-ALL day=2026-09-11 tokens=92052 usd=- usd_per_mtok=- unpriced=92052
   REPORT OK who=ada day=2026-09-11 rows=4 at=2026-10-07T14:49:40Z build=v1.0.1-0.20261007144630-1dc3dd9cdf2d subject="tokens 2026-09-11 at=2026-10-07T14:49:40Z build=v1.0.1-0.20261007144630-1dc3dd9cdf2d"
   ```
   I expected `subject=` to carry the literal subject a friend pastes into `nova-bus draft --subject`, as rule 20 says the line is printed "ready for" it. The value is wrapped in double quotes (a display quoting a reader is not told about), and a note whose `Subject:` is that quoted string is silently not a tokens note: the same `fold --bus` then prints `TOKENS OK days=0 rows=0 … quiet=0` and exits 0, so a friend who copies the field verbatim ships a note that counts nothing and no line says why.
   Grade: NEXT (a rendered id that is not the value, with a silent no-op on the paste).

9. `nova-tokens report --who ada --day 2026-09-11 --repos ./example/repos.tsv --claude a=./attr --note /nope/dir/note.md`
   Printed the three body lines on stdout and then on stderr:
   ```
   REPORT REFUSED: --note /nope/dir/note.md: atomicfile: parent directory for "/nope/dir/note.md": lstat /nope/dir: no such file or directory; run: nova-tokens help
   ```
   exit 2. I expected the missing parent to be named before the artifact is emitted, or at least for the refusal to say what to do (`mkdir -p /nope/dir`). As it stands the body reaches stdout while the command is refused, and the reason leaks the internal package and syscall (`atomicfile:`, `lstat`) rather than the sentence a person acts on; the help says `--note` writes "only on `REPORT OK`", which a reader takes to mean a refused note writes nothing anywhere.
   Grade: NEXT (a refusal carrying an internal name and no remedy, after the artifact was already printed).

10. `nova-tokens fold --out ./outm1 --all --repos ./example/repos.tsv --claude m=./multid --max 1`, and `nova-tokens fold --out ./outa --day 2026-09-11 --repos ./example/repos.tsv --claude a=./attr --claude b=./attr`
    Printed:
    ```
    TOKENS MORE kind=day shown=1 total=3 nova-tokens fold ... --max 0
    ```
    and
    ```
    TOKENS REFUSED: two declared sources fed the same 4 message ids (claude:a and claude:b); run: nova-tokens fold ... without --claude b
    ```
    I expected the next command to be one that runs, as the onboarding standard requires ("a result names the next command … so the next turn is a paste"). Both remedies carry a literal `...` in place of the flags, so neither can be pasted; `SUM MORE` has the same placeholder (`nova-tokens sum --out <dir> --month <m> --max 0`).
    Grade: NEXT (remedies a reader cannot run, in the lines that exist to give one).

11. `nova-tokens help` (the `report` usage block) against `nova-tokens report -h` (the flag list), then `nova-tokens report --who ada --day 2026-09-11 --repos ./example/repos.tsv --bus ./bus --swarm pool=./pool`
    Printed by `help`:
    ```
      nova-tokens report (local mode) --who <name> --day <YYYY-MM-DD> --repos <file>
                          mode: local note body, printed as the tokens note artifact
                          [--claude <label>=<dir>]... [--opencode <label>=<file>]... [--provider <kind>:<label>=<file>]...
    ```
    `report -h`'s flag list adds `--bus` and `--swarm`, and the run above reads both sources and prints their rows. I expected the usage block to list every source the verb reads; the spec's verb block omits `--bus`/`--swarm` for `report` too, so a stranger reading either would not know a friend can send a local report from a bus lane or a swarm pool without first folding it.
    Grade: NEXT (the usage line understates the source flags the verb accepts).

READ 6/10 — the banner, the exit table and most refusals are clear and the docs page is thorough, but three shapes a first run needs (the `--provider` export header, the bus roster JSON, the bus note `Date:` spelling) are learnable only from a failed run, and one of those failures is a Go type dump.

USE 6/10 — every verb ran cold against scratch files, the help's own example block ran as printed, the numbers were traceable to their source labels, two folds were byte-identical below the stamp, and `TOKENS MIXED`/shrink/quiet all held; held down by the one reconciliation the tool documents for competing bus notes, which cannot be performed with either id spelling the tool itself prints, and by several refusals whose remedy cannot be pasted.

NOT DONE: `ledger` and `report --redis` were not used against a live store (no Redis server was started, and none is on the bench), so the store modes are graded on their help, `--dry-run`, and the connection-refused line; `--opencode` was not run against a real OpenCode database (none on the bench), so that source is graded on its help and the `--scratch` refusals; `profiles --swarm-root` returned `PROFILES OK models=0 cards=0 overshoot=0` over a directory that is not a card root and no overshoot case was built; a real swarm pool with attempt=2 was not folded. The card's named test `TestDocsTreeIsConsistent` does not exist in `./internal/docs` at this tip (the gate below runs what is there). No code was changed.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.694s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	13.316s

Run on the build bench against this report's own new file, with the file committed
(`internal/ci`'s `TestNoTestWritesIntoTheSourceTree` refuses an untracked dogfood
file, so the commit comes first). `TestDocsTreeIsConsistent` is named by the card
but does not exist at this tip; `go test -count=1 -timeout 600s ./internal/docs
-run TestDocsTreeIsConsistent` answers `ok  github.com/mas-bandwidth/nova-tools/internal/docs  0.007s [no tests to run]`,
so the two packages are the real gate.

urgent=1 next=10
