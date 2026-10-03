# nova-decide USE rating, nova-tools 1.1.0

Rater: deepseek-v4-pro
Build: 4296165394da
Score: 9/10

## Reasons

The first run succeeds on the first try, with no network and no key, exactly as
the banner promises: `nova-decide ask --schema ... --backend fixed --answers
... --record ./decisions.jsonl --op first` prints `ASK OK` with one `ASK ANSWER`
line per question and appends a plain JSON-lines record. Nothing real was
touched; every command below ran in an empty scratch directory.

One real job end to end, then a second: the decide-then-train loop is the tool's
purpose, and it works. I ran `read` over a card and its diff, attached two
outcomes (`ok` and `wrong`), and `calibrate --decision read --question defect
--positive wrong --negative ok` printed `auc=1` and a `CATCH-ALL at=0.91` bar
over the record I made. A second, different job: `score` over a landed diff
printed sixteen `SCORE ANSWER` lines and its `top=` class, and `findings`
clustered the record without a model call.

The four refusals all behave. A missing required flag names every missing flag
at once, each with what it wants, in one run; an unknown flag lists the flag's
own flags; an unknown verb lists every verb; a bad `--backend` value names
`jev or fixed`. Each one prints the next command to run. `--json` and
`--dry-run` are what the help says and are actable: the JSON of a refusal
carries the full `why` array and the remedy, and `--dry-run` writes nothing
while reporting `recorded=no` or `recorded=existing`.

The model-asking verbs were judged from help and `--dry-run` only, never a key:
`--backend jev` with no key refuses with the exact remedy
(`nova-secrets exec --only JEV_API_KEY -- nova-decide ...`), and `--dry-run`
plans the ask (`questions=5 state_bytes=...`) without a key. Op idempotency is
real: the same `--op` over the same inputs returns `recorded=existing`, and the
same `--op` over a different state refuses naming both.

Where I had to guess: `score`'s `top=` line names the highest class even on a
clean diff (`top=record_made_claim p=0.08`), so it reads like a finding when
the p is tiny; the `gate` line shows `class=flaky ... route=caused` with no bar
set, and only the spec explains why the most-likely class is not the route; a
missing-required-flag refusal remedies to `nova-decide help` rather than the
verb's `-h` that the unknown-flag refusal correctly prints.

What a 10 would need: those three output wrinkles fixed, and the `fixed`
backend's "answers whatever the state" behaviour surfaced beside the flag so a
first-timer never assumes the answers depend on the state.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-decide score --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --backend fixed --answers ./cmd/nova-decide/testdata/score-answers.json --record ./decisions.jsonl` | the `SCORE OK top=record_made_claim p=0.08` line names a defect class on a clean diff, so an AI consumer reads a finding that is 8 percent | print `top=` only when the class meets a floor, or add `top is the highest among the classes, not a verdict` | S |
| 2 | `nova-decide gate --output ./cmd/nova-decide/testdata/gate-output.txt --card ./cmd/nova-decide/testdata/card.md --diff ./cmd/nova-decide/testdata/card.diff --backend fixed --answers ./cmd/nova-decide/testdata/gate-answers.json --record ./decisions.jsonl` | the line shows `class=flaky ... route=caused` with no bar set, so a reader must consult the spec to see why the most-likely class is not the route | print `bars=unset` (or `no bar set, recorded only`) on the gate line when both bars are empty | S |
| 3 | `nova-decide read --card ./cmd/nova-decide/testdata/card.md` | a missing required flag remedies to `nova-decide help` (the whole banner) while the unknown-flag refusal remedies to `nova-decide read -h` | make required-flag refusals remedy to `nova-decide <verb> -h`, the next command a fixer actually runs | S |

## Good, keep

The refusal grammar: every problem named at once, each with what it wants, and
a next command a cold reader can paste. The `--op` idempotency and the `fixed`
backend, which make every verb runnable with no key and no network so a reader
can try the real loop before adopting it.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| no earlier rating | CHANGED | the first rating of this tool |
