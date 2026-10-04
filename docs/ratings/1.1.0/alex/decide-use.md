# nova-decide USE rating, nova-tools 1.1.0

Rater: abliterated-model-large-v2
Build: 2c2e4d40128
Score: 9/10

## Reasons

Judged from the help and the fixed backend alone, every command in a scratch directory, nothing real touched: no key, no network, no store.

The first run: `nova-decide help` answers the three questions (what it does, how it works naming the schema, the backend and the record, and a first run that needs no key), and the first example line ran green on the first try from a checkout root, exit 0, `ASK OK id=first ... recorded=new`.

Two real jobs, end to end, over files I made. Job one, a decision loop: my own two-question schema and three report states; the ask planned (`--dry-run` said questions=2 state_bytes=101 recorded=no), recorded, replayed under the same `--op` with nothing asked again, its outcome attached (`changed=true`), attached again (`changed=false`), relabelled (exit 1 naming both labels and the time), and `calibrate` over my record printed AUC 1, the bars and the catch-all 0.8, which is the right answer for positives 0.9 and 0.8 against a negative of 0.1, checked by hand. Job two, scoring landed work: my own card and diff; `score --dry-run` planned sixteen questions; `score` named `top=cut_citation p=0.82`; `outcome` attached the class as the label; `findings` clustered it (`FINDING class=cut_citation count=1 cards=mycard`), and `--bar` moved the cluster. `brief` read a directory of two cards of mine, one `BRIEF CARD` line each in id order, and `--max 1` bounded the listing with a `MORE` line naming the remedy. `ask --state -` read its state from stdin.

Four refusals provoked, each naming the problem, what it wants and the next command: a missing `--card` ("it wants the card the worker was given, a file; refusing to guess; run: nova-decide help"); an unknown `--verbose` (the flags of ask listed; run: nova-decide ask -h); an unknown verb `calibrat` ("did you mean calibrate?" plus every verb); bad values (`--backend oracle` wants jev or fixed; `--bars 0.5,0.5` sums to at most 1, so one failure could meet both, raise either). A run with three problems answered all three, one `REFUSED` line each with its own remedy, one exit 2: every problem at once.

`--json` and `--dry-run`, everywhere the help offers them: `ask --json` printed the same value as one object (the facts and items of the line form); a refusal under `--json` printed the object with its why and remedy, exit 2; `read --dry-run` and `gate --dry-run` planned with the jev backend and no key (gate naming each failure's id, state size and recorded=no, writing nothing); `outcome --dry-run` and `brief --dry-run` asked and wrote nothing. Each is what the help says and could be acted on without guessing.

Where I had to guess: the labels of a record not just written (finding 1), whether `--label` takes only the four words its help lists (finding 2), and what a first run is without a checkout (finding 3). Verbs not tried: every ask through the jev backend (read, score, attempt, grade, gate, brief with a key and a live model) — no key here; each is judged from its help and `--dry-run`, which name what is sent, the timeout and the failure exit. The record's verbs were tried over records I made.

A 10 needs the record readable back by a verb, so the calibration the tool exists for starts from the tool and not from reading its file by hand.

## Findings
| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | `nova-decide calibrate --record triage.jsonl --decision triage --question urgent --positive broke --negative ok` | the refusal names the counts (0 positive and 1 negative) but not the labels the record holds, and no verb lists a record's decisions, ids and labels, so over a record not just written the labels are learned by reading the JSON-lines file by hand: the one state the tool writes has no verb to read it back | a list verb over the record: one line per decision, its id, decision, schema hash and outcome label, bounded by --max | M |
| 2 | `nova-decide outcome --record triage.jsonl --id r1 --label cut_citation --note the-review-found-rule-4` | the help's --label says "one word (ok, wrong, pass, fail)", which reads as an enum, while any one word is taken (the class label was), so a first-time user hesitates over four | say in the flag's text "any one word you will calibrate by, such as ok or wrong" | S |
| 3 | `nova-decide help` | the example block and the reference's first run need a checkout root (the banner says so), so a binary-only reader has no runnable example, and the section never says why no quickstart verb exists, the family's answer to a first run needing nothing invented | say in the reference's First run why there is no quickstart verb (the caller's own schema and state are the first run), so the binary-only reader knows the examples need a checkout | S |

## Good, keep

The refusal grammar in use: every problem of one run answered at once, each line saying what the input wants and the next command to run; a cold reader fixes the call once.

The op-id replay: the same op asks nothing and returns the recorded decision, and every dry run plans without a key and writes nothing, so a long run resumes and a first probe is free.

The calibration output is small enough to check by hand (the AUC, the bars, the catch-all), and `findings` clusters the same record it was computed from: the tool trusts the record it keeps, and shows its math.

## Compared with earlier ratings
| earlier | now | evidence |
|---|---|---|
| no earlier rating | CHANGED | the first rating of this tool |
