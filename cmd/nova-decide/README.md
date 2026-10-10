# nova-decide

## What it is

nova-decide: typed decisions with probabilities, recorded so each one can be calibrated against its outcome

## Why use it

Let a cheap model make a typed call, and learn how far to trust it.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-decide@latest
nova-decide version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-decide).

Fixture: `cmd/nova-decide/testdata/`: a schema and a state, a card and its
diff, a child's RESULT.md, a red gate's go test output, a card to add
(`greet.md`), the fixed backend's answers for each decision (ask, read, score,
attempt, grade, gate, brief), and a record of eight labelled read decisions and
five score decisions of landed diffs. Every line below uses the fixed backend,
so it needs no network and no key; `cmd/nova-decide/firstrun_test.go` runs each
`$` line from a checkout root in one sitting, with `./decisions.jsonl` a file in
the test's own directory. The ids come from `--op`, so every line reads the same
twice.

```text
$ nova-decide ask --schema ./cmd/nova-decide/testdata/schema.json --state ./cmd/nova-decide/testdata/state.txt --backend fixed --answers ./cmd/nova-decide/testdata/answers.json --record ./decisions.jsonl --op first
ASK OK id=first decision=reply backend=fixed tokens_in=0 tokens_out=0 recorded=new
ASK ANSWER question=asks_something type=noul value=yes p=yes:0.94
ASK ANSWER question=kind type=choice value=request p=question:0.08,report:0.05,request:0.87

$ nova-decide read --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --backend fixed --answers ./cmd/nova-decide/testdata/read-answers.json --record ./decisions.jsonl --op card-1
READ OK id=card-1 decision=read backend=fixed verdict=LAND p=0.92 tokens_in=0 tokens_out=0 recorded=new
READ ANSWER question=defect type=noul value=no p=yes:0.04
READ ANSWER question=does_task type=noul value=yes p=yes:0.96
READ ANSWER question=inside_paths type=noul value=yes p=yes:0.99
READ ANSWER question=lines_changed type=noul value=yes p=yes:0.97
READ ANSWER question=verdict type=choice value=LAND p=BOUNCE:0.05,LAND:0.92,UNSURE:0.03

$ nova-decide score --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --backend fixed --answers ./cmd/nova-decide/testdata/score-answers.json --record ./decisions.jsonl --op card-1@landed@0123456789ab
SCORE OK id=card-1@landed@0123456789ab decision=score backend=fixed top=record_made_claim p=0.08 tokens_in=0 tokens_out=0 recorded=new
SCORE ANSWER question=asserted_data_cut type=noul value=no p=yes:0.05
SCORE ANSWER question=comment_contradicts_code type=noul value=no p=yes:0.05
SCORE ANSWER question=cut_citation type=noul value=no p=yes:0.07
SCORE ANSWER question=defect type=noul value=no p=yes:0.06
SCORE ANSWER question=does_task type=noul value=yes p=yes:0.95
SCORE ANSWER question=fenced_block_edit type=noul value=no p=yes:0.03
SCORE ANSWER question=inside_paths type=noul value=yes p=yes:0.99
SCORE ANSWER question=invented_reason type=noul value=no p=yes:0.06
SCORE ANSWER question=ledger_ceiling type=noul value=no p=yes:0.03
SCORE ANSWER question=lines_changed type=noul value=yes p=yes:0.96
SCORE ANSWER question=load_bearing_word_cut type=noul value=no p=yes:0.04
SCORE ANSWER question=record_made_claim type=noul value=no p=yes:0.08
SCORE ANSWER question=renamed_file_assumed type=noul value=no p=yes:0.02
SCORE ANSWER question=stranded_fragment type=noul value=no p=yes:0.04
SCORE ANSWER question=test_weakened type=noul value=no p=yes:0.02
SCORE ANSWER question=verdict type=choice value=LAND p=BOUNCE:0.05,LAND:0.92,UNSURE:0.03

$ nova-decide attempt --brief ./cmd/nova-decide/testdata/card.md --result ./cmd/nova-decide/testdata/result.md --reason "verdict not-done: tests red in pkg/decide" --backend fixed --answers ./cmd/nova-decide/testdata/attempt-answers.json --record ./decisions.jsonl --op c1@1
ATTEMPT OK id=c1@1 decision=attempt backend=fixed class=needs-pro p=0.78 tokens_in=0 tokens_out=0 recorded=new
ATTEMPT ANSWER question=class type=choice value=needs-pro p=done:0.04,needs-pro:0.78,no-result:0.08,nothing-to-do:0.02,provider-failure:0.02,wrong-scope:0.06

$ nova-decide grade --brief ./cmd/nova-decide/testdata/card.md --backend fixed --answers ./cmd/nova-decide/testdata/grade-answers.json --record ./decisions.jsonl --op c1@grade
GRADE OK id=c1@grade decision=grade backend=fixed grade=flash p=0.71 tokens_in=0 tokens_out=0 recorded=new
GRADE ANSWER question=grade type=choice value=flash p=flash:0.71,pro:0.08,script:0.21

$ nova-decide gate --output ./cmd/nova-decide/testdata/gate-output.txt --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --base-red TestPortInUse --backend fixed --answers ./cmd/nova-decide/testdata/gate-answers.json --record ./decisions.jsonl --op c1@1@gate
GATE OK op=c1@1@gate decision=gate backend=fixed failures=2 route=caused
GATE FAILURE key=example/tools/internal/serve.TestPortInUse id=c1@1@gate/example/tools/internal/serve.TestPortInUse class=flaky p=caused:0.06,flaky:0.86,pre-existing:0.08 route=caused recorded=new
GATE FAILURE key=example/tools/internal/greet.TestGreetNamesTheReader id=c1@1@gate/example/tools/internal/greet.TestGreetNamesTheReader class=flaky p=caused:0.06,flaky:0.86,pre-existing:0.08 route=caused recorded=new

$ nova-decide brief --card ./cmd/nova-decide/testdata/greet.md --backend fixed --answers ./cmd/nova-decide/testdata/brief-answers.json --record ./decisions.jsonl
BRIEF OK decision=brief backend=fixed cards=1 asked=1 existing=0 failed=0
BRIEF CARD id=greet op=greet@brief-825042ac p_converges=0.72 minutes=under-10 failed=- uncalibrated=true recorded=new

$ nova-decide outcome --record ./decisions.jsonl --id card-1 --label ok --note "the review found nothing"
OUTCOME OK id=card-1 decision=read label=ok changed=true

$ nova-decide calibrate --record ./cmd/nova-decide/testdata/record.jsonl --decision read --question defect --positive wrong --negative ok
CALIBRATE OK decision=read schema=505bd379c3753631 question=defect option=yes positives=3 negatives=5 skipped=0 auc=0.933
CALIBRATE BAR at=0.5 caught=2 of=3 bounced=1 of_negatives=5
CALIBRATE BAR at=0.7 caught=2 of=3 bounced=0 of_negatives=5
CALIBRATE BAR at=0.9 caught=0 of=3 bounced=0 of_negatives=5
CALIBRATE CATCH-ALL at=0.45 caught=3 of=3 bounced=1 of_negatives=5

$ nova-decide findings --record ./cmd/nova-decide/testdata/record.jsonl --since 2026-10-01
FINDINGS OK scored=5 classes=4 bar=0.5 since=2026-10-01T00:00:00Z
FINDINGS FINDING class=stranded_fragment count=2 cards=s1-1,s1-2
FINDINGS FINDING class=cut_citation count=2 cards=s1-1,s1-3
FINDINGS FINDING class=invented_reason count=1 cards=s1-2
FINDINGS FINDING class=unnamed count=1 cards=s1-4
```

## Verbs

The [nova-decide section of the command reference](../../docs/CLI.md#nova-decide) documents every verb's flags, effect and exit codes.

- `ask`
- `read`
- `score`
- `attempt`
- `grade`
- `gate`
- `brief`
- `outcome`
- `calibrate`
- `import`
- `score-grades`
- `findings`
- `version`
- `help`

## Spec

The contract is [docs/SPEC-NOVA-DECIDE.md](../../docs/SPEC-NOVA-DECIDE.md).
