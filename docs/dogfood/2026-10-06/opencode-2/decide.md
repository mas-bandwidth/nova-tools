# nova-decide dogfood — opencode-2, 2026-10-06

Read cold on a Linux bench from the staged checkout at
`7acb90e18a764f0e728cd5ed701196a34405a824`, with the binary built inside that
checkout (`go build -o $JOB/bin/nova-decide ./cmd/nova-decide`, never the
installed binary): `nova-decide -h`, `nova-decide help`, every `<verb> -h`, and
the `nova-decide` pages in `docs/CLI.md` and `docs/SPEC-NOVA-DECIDE.md`, nothing
else. It printed `nova-decide v1.0.1-0.20261007211151-7acb90e18a76 linux/amd64 go1.26.6`. Every verb (`ask`, `read`, `score`, `attempt`, `grade`, `gate`,
`brief`, `outcome`, `calibrate`, `import`, `score-grades`, `findings`,
`version`, `help`) was then run with its real flags in a scratch directory of
copied fixtures against the fixed backend, the refusals, the dry runs and the
boundary calls included; no backend reached the network and no live record was
touched.

## Findings

1. `nova-decide findings --record record.jsonl --since 2026-10-01 --bar 0.5 --max 10`
   Printed:
   ```
   FINDINGS REFUSED: unknown flag --max; the flags of findings are --bar, --heavy, --json, --read-shadow, --real, --record, --shadow, --since; run: nova-decide findings -h
   (one line printed)
   ```
   I expected the top-level help's sentence, "A verb that lists takes `--max <n>`
   (default 20, 0 lists all) and says `MORE` for the rest", to hold for this
   listing verb, so `findings` would bound its `FINDING` lines and print a
   `MORE`; instead the flag is refused and the promise is false for it.
   Grade: URGENT (help that lies)

2. `nova-decide outcome --record decisions.jsonl --id card-1 --label wrong --dry-run`
   Printed:
   ```
   OUTCOME OK id=card-1 label=wrong recorded=no dry_run=true
   (one line printed)
   ```
   I expected the dry run to print the plan the real run takes, as
   `docs/SPEC-NOVA-DECIDE.md` section 7 says: the same command without
   `--dry-run` prints `OUTCOME FAILED id=card-1: decision card-1 is labelled ok already ..., not wrong; an outcome is attached once` and exits 1. The dry run
   instead prints `OK` and exits 0, so a caller that plans with it is told the
   outcome will be recorded when the real run refuses it.
   Grade: URGENT (wrong result)

3. `nova-decide gate --output build.txt --card card.md --backend fixed --answers gate-answers.json --record decisions.jsonl --op buildfail-real --dry-run`
   Printed:
   ```
   GATE OK op=buildfail-real decision=gate backend=fixed failures=1 recorded=existing dry_run=true
   GATE FAILURE key=example/tools/internal/x id=buildfail-real/example/tools/internal/x state_bytes=423 class=- recorded=unasked
   ```
   I expected the summary's `recorded=existing` to mean the record holds the
   decision, as `docs/CLI.md` says (a build failure is `recorded=unasked`): with
   `build.txt` holding the single line `FAIL<TAB>example/tools/internal/x [build failed]`, the real run of the same op prints `GATE OK ... failures=1 route=caused` and `GATE FAILURE ... id=- ... recorded=unasked` and appends
   nothing (the record holds no line for the op). The dry run reports a recorded
   decision that does not exist, beside its own `recorded=unasked` line.
   Grade: URGENT (wrong result)

4. `nova-decide import --record import3.jsonl --verdicts "nomatch/*.md"`
   Printed:
   ```
   IMPORT OK verdict_new=0 verdict_existing=0 judgment_new=0 judgment_existing=0 report_new=0 report_existing=0 unanswered=0
   (one line printed)
   ```
   I expected a source glob that matches no file to be refused, or at least to
   print `matched=0`, because `IMPORT OK` with every count 0 is the same line a
   successful no-op prints; a mistyped path reads as a clean import.
   Grade: NEXT (unclear result)

5. `nova-decide read --card card.md --diff /dev/null --backend fixed --answers read-answers.json --record decisions.jsonl --op emptydiff`
   Printed:
   ```
   READ OK id=emptydiff decision=read backend=fixed verdict=LAND p=0.92 tokens_in=0 tokens_out=0 recorded=new
   READ ANSWER question=defect type=noul value=no p=yes:0.04
   READ ANSWER question=does_task type=noul value=yes p=yes:0.96
   ```
   I expected `read` to refuse a `--diff` with no bytes, because the state it
   describes is "the worker's unified diff" and a zero-byte file is not one; an
   empty diff was instead recorded as a normal read, and `score` over
   `/dev/null` records too.
   Grade: NEXT (a missing check)

6. `nova-decide ask --schema badschema2.json --state state.txt --backend fixed --answers answers.json --record decisions.jsonl --op bad4`
   Printed:
   ```
   ASK REFUSED: the schema "": it has no name; a decision is named so its record can be calibrated; question a has no instructions; noul b carries criteria; a noul is one statement, yes or no; question c has type "frob"; it wants choice or noul; run: nova-decide help
   (one line printed)
   ```
   I expected every schema problem in the one refusal, as
   `docs/SPEC-NOVA-DECIDE.md` section 2 says `ParseSchema` names them: with
   `badschema2.json` holding a nameless schema, question `a` a choice with an
   empty `instructions` and a single option, noul `b` with `criteria`, and
   question `c` of an unknown type, the one-option problem on `a` is not named
   (a schema whose only fault is that one option does print `choice a names 1 options; it wants at least two in criteria`), so the reader fixes `a` and
   meets a second refusal for the same question.
   Grade: NEXT (unclear help)

7. `nova-decide grade --brief card.md --backend fixed --answers grade-answers.json --record decisions.jsonl --seed s --op g2`
   Printed:
   ```
   GRADE OK id=g2 decision=grade backend=fixed grade=flash p=0.71 tokens_in=0 tokens_out=0 recorded=new
   GRADE ANSWER question=grade type=choice value=flash p=flash:0.71,pro:0.08,script:0.21
   ```
   I expected `--seed` (and `--held-out`) to be refused without `--examples`,
   because every help line marks them "(with --examples)" and the standard
   refuses a flag it cannot honour; instead both are silently ignored and the
   grade runs as if they were absent.
   Grade: NEXT (a silently ignored flag)

8. `nova-decide help --json`
   Printed:
   ```
   {"result":{"verb":"","status":"refused","exit":2,"remedy":"nova-decide help","why":["unknown verb \"--json\"; the verbs are ask, read, score, attempt, grade, gate, brief, outcome, calibrate, import, score-grades, findings, version"]},"facts":{}}
   (one line printed)
   ```
   I expected `--json` to work on `help`, or for the banner's "Every verb takes
   `--json`" to exempt it by name: `help` is listed in the usage, yet it reads
   `--json` as the verb name, while `help ask --json` accepts the flag and
   ignores it (exit 0, text help), so the same flag means two things.
   Grade: NEXT (unclear help)

9. `nova-decide ask --schema schema.json --state state.txt --backend fixed --answers badanswers.json --record decisions.jsonl --op bad2`
   Printed:
   ```
   ASK FAILED id=bad2 backend=fixed: the backend's answers do not fit the schema: asks_something gives yes the probability 2, outside [0, 1]; kind chose "nope", not one of its options; kind gives its choice "nope" no probability; run: make --answers answer every question of the schema
   (one line printed)
   ```
   I expected a remedy a cold reader can paste, as the other refusals give: the
   line ends `run: make --answers answer every question of the schema`, which is
   not a command (there is no such target and no file is named); the cause
   wanted `nova-decide ask -h` or the answers file's shape.
   Grade: NEXT (a refusal whose remedy is not a command)

10. `nova-decide score-grades --record decisions.jsonl --log emptyobj.json --day 2026-10-07`
   Printed:
   ```
   SCORE-GRADES OK day=2026-10-07 decisions=1 cards=1 no_log=1
   (one line printed)
   ```
   I expected a named log that is not a `nova-sprint log --json` export to be
   refused by shape: `emptyobj.json` holds `{}`, no `lines` key, while the help
   says the log is the export and the tool does refuse a non-JSON file
   (`SCORE-GRADES REFUSED: card.md: the log is not a nova-sprint log --json export: ...`). Here every grade decision is silently counted `no_log` and the
   run reports OK.
   Grade: NEXT (a malformed named input accepted)

11. `nova-decide ask --schema schema.json --state state.txt --backend fixed --answers missing.json --record decisions.jsonl --op bad3`
   Printed:
   ```
   ASK REFUSED: open missing.json: no such file or directory; run: nova-decide help
   (one line printed)
   ```
   I expected the refusal to say what the input wants (the fixed backend's
   answers, a JSON file, as `ask -h` does) and to point at `nova-decide ask -h`,
   because `run: nova-decide help` sends the reader back to the whole banner for
   a flag the verb's own help already explains.
   Grade: NEXT (unclear remedy)

## What held

`ask`, `read`, `score`, `attempt`, `grade`, `gate` and `brief` ran clean end to
end against the fixed backend: each wrote one result value, `--json` held the
same value as the lines, and `--dry-run` wrote nothing. `ask` with no flags named
every missing flag at once; an unknown flag named the verb's flags; `--timeout nonsense` named the duration shape. The op id held: the same op over the same
inputs printed `recorded=existing` and asked nothing, the same op over another
state was refused, and `outcome` attached once (`changed=false` on the same
label). `gate` refused `--bars 0.3,0.3` with the reason, routed `--bars 0.8,`
flaky, refused output with no failure, and marked an already-recorded failure
`recorded=existing` in a dry run. `calibrate` printed the AUC and one
`BAR`/`CATCH-ALL` line per bar, and refused a missing positive or negative and an
option no answer names. `findings` clustered the record's score decisions and
printed the named classes and `unnamed`; `--shadow`/`--real` printed a `SHADOW`
line and `--read-shadow`/`--heavy` printed the joined `tp`/`fp`/`fn`/`tn`.
`import` read the in-tree verdict, judgment and report fixtures (2/2/2, one
`unanswered`), a second run was all `existing`, and its dry run wrote nothing.
`score-grades` read a constructed log and printed the `GRADE` and `BUCKET` lines.
`brief` read a 25-card directory, printed one `BRIEF CARD` per card and `BRIEF MORE shown=20 total=25`, and did not descend into a subdirectory. The jev backend
refused with `reason=key_absent` and the `nova-secrets exec` remedy before any
call. `version` printed the one version line. `help <verb>`, `<verb> -h` and
`help` all exited 0.

READ 7/10 — the banner answers what it does, how it works and how to use it, and
every `-h` names the flags, the effect and the exit codes, so a stranger can run
the whole tool cold; the false `--max` promise, the `help --json` split and the
remedies that are not commands keep it off a higher score.

USE 6/10 — the fixed backend and `--dry-run` let every verb run with no store and
no key, and op-id replay, `--json` and the `MORE` line all work; a dry run that
reports the opposite of the real run, and green `OK` lines over nothing imported
or an unreadable log, are what cost it.

urgent=3 next=8
