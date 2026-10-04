# nova-decide USE rating, nova-tools 1.1.0

Rater: Grok
Build: 4296165394da
Score: 7.5/10

## Reasons

`nova-decide help` answers what it does, how it works, and how to use it, then ten example lines and the exit table. A bare command exits 2 and names every verb. `nova-decide help ask` and `nova-decide ask -h` print the same text. The first example, run from the checkout root the banner names, exits 0: `ASK OK id=first decision=reply backend=fixed ... recorded=new`.

Two jobs, both on the fixed backend, in a scratch directory. First: a two-question schema (a choice and a noul), two states, two labels, then calibrate. `nova-decide ask --schema job/schema.json --state job/state.txt --backend fixed --answers job/answers.json --record job/rec.jsonl --op ship-1` prints `ASK OK ... verdict=yes p=0.82 recorded=new` and one `ASK ANSWER` line per question. The same line again prints `recorded=existing`. `nova-decide outcome --record job/rec.jsonl --id ship-1 --label ok --note "the note did ship"` prints `changed=true`; the same label again prints `changed=false`. A second decision labelled wrong, then `nova-decide calibrate --record job/rec.jsonl --decision ship --question clear --positive ok --negative wrong`, prints `CALIBRATE OK ... auc=1`, three `CALIBRATE BAR` lines and `CALIBRATE CATCH-ALL`. Before the second label existed, the same calibrate exited 2 and named `1 positive and 0 negative`. Second job: `nova-decide read` on a one-line card and diff prints `verdict=LAND p=0.8` as lines and, with `--json`, as the same facts. `nova-decide grade` and `nova-decide attempt` on that brief print `grade=flash` and `class=done`.

Four refusals, each exit 2. No flags on ask prints four lines, one per required flag, each saying what the flag wants and `refusing to guess`. `--nope` names the flags of ask and `did you mean --op?`, and the remedy is `nova-decide ask -h`. Verb `frobnicate` lists the verbs and says `run: nova-decide help`. `--backend nope`, `--timeout 0`, `--bars 2`, `--label "not ok"`, `--since yesterday` and `--width 0` each name the bad value and the constraint. `--json` on a refusal is one object whose `why` is those problems and whose `remedy` is the same command. `--dry-run` prints `dry_run=true` and `recorded=no` and creates no file. `nova-decide ask ... --backend jev` with no key exits 2 before any send and names `nova-secrets exec --only JEV_API_KEY -- nova-decide ...`. The same line with `--dry-run` exits 0 and writes nothing.

The jev backend was not called for real on ask, read, score, attempt, grade, gate or brief: no key and no model call. Those verbs were judged from help and `--dry-run` only.

It is not a 10. A repeated ask with no `--op` appends a new id both times, so the replay the flag describes is opt-in and a model retry would ask twice. A score whose answers file is short exits 2 with `run: make --answers answer every question of the schema`, which is not a command, and a brief that fails tells the reader to run the same line again when nothing was recorded. Every deciding verb's help says `effect: delivery: sends beyond this machine` even for `--backend fixed`, which only appended a local file. A bad `minutes` value says it is not an option and does not list the options, and the help does not either. A 10 would replay from the schema and the state when `--op` is omitted, make every remedy a command that changes the failing input, class a fixed backend as a local write, and print the legal options in the refusal.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-decide ask --schema job/schema.json --state job/state.txt --backend fixed --answers job/answers.json --record job/noop.jsonl` | The same command, run twice with no --op, prints recorded=new both times, ids ship-71ac3efddda8 and ship-a292c526eb66. Help says the same --op returns the recorded result, and it does, but the flag is optional and nothing on the success line says a repeat asks again. | When --op is absent, derive the id from the schema and the state, and print that a repeat returns the recorded decision. | S |
| 2 | `nova-decide score --card job2/card.md --diff job2/card.diff --backend fixed --answers job2/score-answers.json --record job2/rec.jsonl --op card-a@landed@abc` | Exit 2 names every missing question, then says run: make --answers answer every question of the schema. That is not a command. The same shape on a failed brief says to run the same line again, and the card line says recorded=no, so the remedy repeats the failure. | Put a real nova-decide command in the remedy, and do not tell the reader to retry a card that was not recorded. | S |
| 3 | `nova-decide help ask` | The effect line says delivery: sends beyond this machine for ask, and the same line is on read, score, attempt, grade, gate and brief. A fixed run appends a local file and a fixed dry-run creates none. An AI screening effects will treat a local replay as a send. | Say fixed writes the record and only jev sends, in the effect line of each deciding verb. | S |
| 4 | `nova-decide brief --card job2/card2.md --backend fixed --answers job2/brief-bad.json --record job2/brief.jsonl` | minutes chose "soon", not one of its options, and the line does not list the options. help brief names the options of ambiguous_step and does not name the options of minutes, so the next answers file is a guess. | Print the option names in that refusal, and name them in help brief. | S |
| 5 | `nova-decide gate --output job2/gate.txt --card job2/card.md --diff job2/card.diff --base-red TestPortInUse --backend fixed --answers job2/gate-answers.json --record job2/rec.jsonl --op g1` | The item says class=flaky p=caused:0.05,flaky:0.9,pre-existing:0.05 and the summary says route=caused. Help says an unset bar routes nothing, but the line does not say the bars were unset, so the two words look like a contradiction. | Print the bars on the GATE OK line, including unset. | S |

## Good, keep

A missing flag, an unknown flag and an unknown verb each name the problem, the missing flags arrive in one run, and the unknown flag names the real flags and the nearest (`nova-decide ask` with no flags; `nova-decide ask --nope`; `nova-decide frobnicate`).

`--dry-run` writes nothing and jev `--dry-run` needs no key; `--json` is the same result as the lines, including a refusal's why and remedy.

`--op` replay prints `recorded=existing`, a repeated label prints `changed=false`, a conflicting label is exit 1 and names both labels, and calibrate refuses a one-sided record before it prints `auc`, `BAR` and `CATCH-ALL`.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| no earlier rating | CHANGED | the first rating of this tool |
