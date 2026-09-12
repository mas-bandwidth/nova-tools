# nova-go — specification (DRAFT for the group's read)

Glenn, 2026-09-12, on https://opencode.ai/docs/go/ : *"It looks like a model based on
excess capacity."* *"It could be a varying swarm, where you make a choice of model
depending on querying it, and what is available to run."* *"I don't think we run friends on
this model, but it could be a work pool."* *"I would like us to consider adding this as
nova-go."* And the reason for all of it: *"Our whole push right now is to find the EFFICIENT
way for us to work as a team."* Idea filed as [ideas#771](https://github.com/mas-bandwidth/ideas/issues/771).

**What Go is, as read 2026-09-12.** $10 a month for one key over 27 open coding models on
reserved capacity, each with a monthly dollar cap ($15, $30 or $60), a 5-hour limit of 20% of
it and a weekly limit of 50%; per-token prices, DeepSeek's doubling in peak hours (01:00-04:00
and 06:00-10:00 UTC, weekdays); an optional Zen-balance overflow; three endpoint shapes under
`https://opencode.ai/zen/go/v1` (`chat/completions`, `responses`, `messages`) and a `models`
list; one subscriber per workspace; traffic monitored, a self-identifying user agent and a
stable `x-opencode-session` expected; no training and 0-day retention for most, 30 days for
Grok 4.6 and GPT 5.6 Luna, two Muse Spark doors that DO train; and *the list may change*.

## What it is, and is not

One binary at the **capacity layer**. `nova-go` is a **work pool for one-shot jobs**: it
holds the list of Go's doors as data, asks before each job which door still has budget in
the current window, hands nova-swarm the door to use, records the door actually used and
what it cost, and refuses by name when nothing is open. A **door** is one model at one
endpoint under one cap.

- It is **never a line's session**: no friend runs on Go, no self loads through it, no
  memory or bus is touched by anything it starts. A task file in, a `RESULT.md` out.
- It is **a provider for nova-swarm, not a dispatcher**: slots, deadlines, the sandbox, the
  job directory, `finalize` and the usage file stay nova-swarm's (SPEC-SWARM rules 7, 12,
  13). nova-swarm's `run` asks `nova-go choose` before a launch and tells `nova-go record`
  after one, when the worker description's `provider` is `opencode-go`; those two sentences
  are nova-swarm's own PR, and nothing else in it moves.
- It **chooses among doors a person listed, in their order**, and has no opinion about
  which model is good: `bake-off` exists so that opinion can be measured (memory
  2026-09-12: *check the call before judging the model*).

SPEC.md's **Conventions** govern — exit codes, one line per event, the field law,
`internal/oneline`, `internal/bounded`, no guessed paths — and this file says only what is more.

## The rules, numbered

1. **The doors are a file, named by a flag, kept in git.** Every door comes from the file
   `--doors` names; no door is in the binary. A missing `--doors` is `refusing to guess`,
   exit 2; a first line that is not the header byte for byte, or a line 2 that is not
   `# source=<url> read=<YYYY-MM-DD>`, is exit 2 naming the line, remedy *put the header and
   the source line back as the spec shows*. Fourteen tab-separated fields per door, none
   empty, `-` for not applicable; more or fewer is exit 2 naming the line, never a skip.
2. **`doors` prints the file and never the network.** One `DOORS DOOR` line per door in file
   order, capped by `--max` (default 20, `0` all, negative refused), then `DOORS OK` with
   `doors=`, `source=`, `read=`. With `--ledger <dir>` each line adds `open=`, the 5-hour,
   weekly and monthly room by rule 6, so the table a person reads is the one `choose` uses.
3. **`refresh` fetches the live list and diffs it against the file; it never edits the
   file.** One bounded GET of `https://opencode.ai/zen/go/v1/models` (256 KB cap, 5s
   timeout, status before body); an id live and not in the file is `REFRESH ADDED`, in the
   file and not live `REFRESH GONE`, any difference exit 1, remedy *read the page, edit the
   file, commit*. Prices, caps and retention are not in that body as far as this spec knows,
   so ids alone are compared and a `REFRESH NOTE` says so; a 401 is `REFRESH UNKNOWN` naming
   `nova-secrets exec`, and any non-200, timeout or malformed body is UNKNOWN, exit 1, never
   *up to date*.
4. **A job names a shape, and the worker description names the doors for it.** `--shape` is
   one of `read`, `decision`, `mechanical`, `bake-off`; the description (below) carries
   `prefer.<shape>`, door ids in preference order, each present in the doors file or the
   description is refused at load naming it. A shape with no list is a refusal naming the
   shape, remedy *add prefer.<shape> to the description*.
5. **`choose` walks the list and takes the first open door.** Open: the job's estimate fits
   every window's room (rule 6), retention passes rule 9, the door does not train (rule 10).
   The estimate is `bytes/3` input tokens plus `--max-output` output tokens at the current
   tier's prices; both flags are required, `0` refused. The line names the door, the room
   after this job, the tier, and every door passed over with its reason.
6. **Room per window comes from the provider when it says, else from the tool's own
   ledger.** At the first `ask` under a ledger the tool writes to `<ledger>/headers.txt` the
   NAMES (never the values) of every response header matching `x-ratelimit*`, `x-opencode*`
   or `*-usage*`; a person reads it and sets the description's `budget_source` to
   `headers:<name>` when one carries remaining budget, else to `ledger`.
   Under `ledger`, room is the cap times 20%, 50% and 100% minus the ledger's spend in the
   trailing 5 hours, 7 days and UTC calendar month — the conservative reading until the
   provider's window is measured (open question 5). No row is ever estimated: a row is a
   response's usage fields at the fixture's prices, or `-`.
7. **A cap exhausted is a refusal naming the next door and the window's end; it never falls
   to a dearer door on its own.** `CHOOSE REFUSED … window=<5h|week|month> room=<usd>
   need=<usd> next=<id|none> until=<stamp>`, `until` being when the oldest row in the window
   ages out. The next door is taken only when its price per token at the current tier is not
   above the exhausted door's, or under `--allow-dearer`, which prints `dearer=true`.
8. **The local tier is the last door, for a job shaped for it.** With every paid door closed
   and the shape in the description's `local_shapes`, `choose` returns `door=local:<model>`
   with `local_base_url` — nova-local's serve (SPEC-LOCAL), never a guessed port; a shape not
   listed is refused by rule 7 with `next=none`. Glenn's limit stands: the local tier is for
   work with no clock on it (memory 2026-09-12).
9. **A door that keeps data is refused for private work.** Every job is `private` unless
   `--public` is passed. A private job may use only a door whose `retention` is `0` and whose
   `zdr_until` is `-` or not yet past by the tool's clock; Grok 4.6, GPT 5.6 Luna (`30`) and
   a DeepSeek door past its ZDR date are `CHOOSE SKIP … reason=retention`, and `ask --door`
   on one is `ASK REFUSED` naming `--public`.
10. **A door that trains is never chosen.** `training=yes` (the Muse Spark contributor
    doors) is skipped by `choose` for every job, and reachable only by `ask --door <id>
    --public --allow-training`, for a bake-off over text nobody minds a model learning.
11. **The Zen overflow is off.** The tool never sets or relies on the console's *Use
    balance*; past a cap, rule 7 refuses. `--allow-overflow` lets `ask` send when the ledger
    says the door is closed, prints `overflow=true`, and is the only way we spend metered credit.
12. **Manners the provider asked for, kept by construction.** Every request carries
    `User-Agent: nova-go/<version>` and `x-opencode-session: <session>`, the session being
    `--session <id>` (nova-swarm passes the job id), stable for the job's life; one
    conversation per job and never a loop, which is what a one-shot job is; one Go
    subscription per line that runs a pool, its key in that line's own `nova-secrets` file
    (rule 13). A request with no session id is not sent.
13. **The key is `OPENCODE_GO_API_KEY` in the environment `nova-secrets exec` built, and
    nowhere else.** No `--key`, no key file, no config the tool writes: an absent or empty
    variable is exit 2 with the `nova-secrets exec … --only OPENCODE_GO_API_KEY` line to
    wrap the command in. nova-swarm's harness config for a Go worker carries the NAME,
    `{env:OPENCODE_GO_API_KEY}` (SPEC-SWARM **The key, read as data**); SPEC-SECRETS's
    credential table gains the row `OPENCODE_GO_API_KEY` in `<line>.yaml`.
14. **`ask` is one call, prompt bounded before it is sent.** It reads `--task <file>` whole
    — passage inline, one question, the result path — and refuses at exit 2 a prompt whose
    `bytes/3` exceeds the door's `ctx` tier, remedy *split the task* (2026-09-12: two Mercury
    jobs died on an input limit carrying two whole specs each). The answer lands at
    `--result <path>` through `.tmp` and rename, and one `ASK OK` line prints.
15. **The price tier is the tool's clock, in UTC.** A door with peak prices is `tier=peak`
    01:00-04:00 and 06:00-10:00 UTC Monday to Friday, `tier=off` otherwise; a door without
    them is `tier=flat`. Decided at send time, printed on every line naming a door, stored on
    the ledger row; nothing read from a response is a clock.
16. **The record carries the door actually used, by kind, from the response's usage
    fields.** Per endpoint shape: `chat` reads `usage.prompt_tokens`, `completion_tokens`,
    `prompt_tokens_details.cached_tokens`; `responses` reads `usage.input_tokens`,
    `output_tokens`, `input_tokens_details.cached_tokens`,
    `output_tokens_details.reasoning_tokens`; `messages` reads `usage.input_tokens`,
    `output_tokens`, `cache_read_input_tokens`, `cache_creation_input_tokens`. A field the
    response lacks is `-`, never `0`; `usd` is `-` when any priced field is. The row's key
    follows nova-tools#117, `day friend bench repo model`, then `job session door endpoint
    tier tokens_in tokens_out cache_read cache_write reasoning usd at`; `--as`, `--bench`
    and `--repo` are required, `--repo unattributed` the honest spelling.
17. **`record` appends one row and never rewrites one.** `<ledger>/spend/<door>/<YYYY-MM-DD>.tsv`,
    one header, one row per call, written by `ask` itself and by `record` when nova-swarm's
    `finalize` hands over a job's usage; a `(job, session)` already present is `RECORD
    ALREADY`, exit 0, so a retry never double-counts. nova-tokens reads the directory as a
    declared source by a rule of its own PR (SPEC-TOKENS rule 14's shape).
18. **`bake-off` runs one task across N doors and scores every answer against the known
    one.** The task file's head carries `answer: yes|no` and `quote: <verbatim>|none`; each
    door's result must end with the same two lines. Per door: `right` when `answer` matches
    and the quote, if any, is a verbatim substring of the task text; `unquoted` when the
    answer matches and the quote is not in the text; `wrong` otherwise; `refused`, `timeout`
    or `malformed` when no answer line came back. `--doors-list <id,…>` names the doors
    (`prefer.bake-off` when absent); one session id per (task, door), the same `--max-output`
    for all, one `BAKEOFF DOOR` line each; `BAKEOFF FAIL` exit 1 when any door is not `right`.
    The 2026-09-11 and 2026-09-12 bake-offs are why the shape is inline text, one question,
    one checkable answer.
19. **Bounded output, bounded run.** `--max` per listing, per kind on `refresh`; `--timeout
    <d>` per call, default `120s`; `bake-off` runs at most four doors at once and its
    `--budget <d>`, default `10m`, marks unreached doors `timeout`. The count line prints on
    failure as well as success, and every count is about the state.
20. **Every refusal names its remedy**, and every refusal lives in one table the test walks.

## The doors file

Line 1 the header byte for byte, line 2 the source line, `#` comments skipped, tabs between
fields. Prices are dollars per million tokens; `cap` is the monthly limit, the 5-hour (20%)
and weekly (50%) rooms derived from it and never stored. `ctx` is the token count above which
the page prices the door higher, `-` for one tier; `retention` is days or `unknown`;
`zdr_until` is the date a zero-day agreement runs to, `-` when none is stated. The first commit
of `cmd/nova-go/testdata/doors.tsv` carries all 27 doors as read 2026-09-12; these are the
rows this spec names:

```
id	endpoint	cap	in	out	cache_read	cache_write	peak_in	peak_out	peak_cache_read	ctx	retention	zdr_until	training
# source=https://opencode.ai/docs/go/ read=2026-09-12
deepseek-v4-flash	chat	30	0.15	0.60	0.003	-	0.30	1.20	0.006	-	0	2026-09-30	no
deepseek-v4.1-flash	chat	15	0.15	0.60	0.003	-	0.30	1.20	0.006	-	0	2026-09-30	no
deepseek-v4-pro	chat	15	0.66	1.98	0.022	-	1.32	3.96	0.044	-	0	2026-09-30	no
glm-5.3-flash	chat	60	0.15	0.50	0.03	-	-	-	-	-	0	-	no
kimi-k2.7-code	chat	60	0.95	4.00	0.19	-	-	-	-	-	0	-	no
mimo-v2.5	chat	60	0.14	0.28	0.0028	-	-	-	-	-	0	-	no
qwen3.8-flash	messages	30	0.15	0.47	0.016	0.20	-	-	-	-	0	-	no
minimax-m3	messages	60	0.30	1.20	0.06	-	-	-	-	-	0	-	no
grok-4.6	responses	15	2.00	6.00	0.50	-	-	-	-	200000	30	-	no
gpt-5.6-luna	responses	15	0.20	1.20	0.02	0.25	-	-	-	272000	30	-	no
muse-spark-1.3-contributor	responses	60	0.10	0.20	0.002	-	-	-	-	-	unknown	-	yes
```

## The worker description

The JSON nova-swarm already takes (`--worker <file>`, strict decode, an unknown field a
refusal), `provider` `opencode-go`, these fields more, and no `model`: `{model}` is filled per job from `CHOOSE OK`.

```json
{ "name": "rowan-go", "provider": "opencode-go", "env_var": "OPENCODE_GO_API_KEY",
  "budget_source": "ledger",
  "prefer": { "read": ["deepseek-v4-flash", "qwen3.8-flash", "glm-5.3-flash"],
              "decision": ["kimi-k2.7-code", "deepseek-v4-pro"],
              "mechanical": ["glm-5.3-flash", "mimo-v2.5"],
              "bake-off": ["deepseek-v4-flash", "qwen3.8-flash", "glm-5.3-flash", "kimi-k2.7-code", "minimax-m3"] },
  "local": "qwen3.6:35b-a3b", "local_base_url": "http://studio.local:11434/v1", "local_shapes": ["mechanical"] }
```

## The verbs

```
nova-go doors    --doors <file> [--ledger <dir>] [--max <n>]
nova-go refresh  --doors <file> [--timeout <d>] [--max <n>]
nova-go choose   --doors <file> --ledger <dir> --worker <file> --shape <s> --bytes <n> --max-output <n> [--public] [--allow-dearer]
nova-go ask      --doors <file> --ledger <dir> --door <id> --task <file> --result <path> --session <id> --max-output <n> --as <name> --bench <name> --repo <name|unattributed> [--public] [--allow-training] [--allow-overflow] [--timeout <d>]
nova-go record   --doors <file> --ledger <dir> --usage <file> --door <id> --session <id> --as <name> --bench <name> --repo <name|unattributed>
nova-go bake-off --doors <file> --ledger <dir> --worker <file> --task <file> --out <dir> --max-output <n> [--doors-list <id,…>] [--public] [--budget <d>] [--timeout <d>] [--max <n>]
nova-go help
```

Those lines are what `nova-go help` prints, byte for byte. No `subscribe`, `key`, `serve` or `--watch`: no clock of its own, no session held.

## Exit codes and the output grammar

Per SPEC.md: **0** ran and passed — a door chosen, a list unchanged, every bake-off door `right`;
**1** the tool saying NO — no door open, a list that moved, a door not `right`, a dead source; **2** could not run, every refusal the rules name.

```
DOORS DOOR id=<id> endpoint=<e> cap=<usd> in=<p> out=<p> tier=<peak|off|flat> retention=<d|unknown> training=<yes|no> [open=<5h>/<week>/<month>]
DOORS OK doors=<n> source=<url> read=<date> file=<path>
REFRESH ADDED id=<id> | REFRESH GONE id=<id> | REFRESH UNKNOWN source=<url>: <reason> (<remedy>)
REFRESH <OK|FAIL> live=<n> file=<n> added=<n> gone=<n> took=<d>
CHOOSE SKIP shape=<s> door=<id> reason=<5h|week|month|retention|training|ctx>
CHOOSE OK shape=<s> door=<id> endpoint=<url> tier=<t> est=<usd> room=<5h>/<week>/<month> dearer=<true|false> skipped=<n>
CHOOSE REFUSED shape=<s> door=<id> window=<w> room=<usd> need=<usd> next=<id|none> until=<stamp>
ASK OK door=<id> session=<id> tier=<t> in=<n|-> out=<n|-> cache_read=<n|-> cache_write=<n|-> reasoning=<n|-> usd=<usd|-> took=<d> result=<path> overflow=<true|false>
ASK REFUSED door=<id>: <reason> (<remedy>)
RECORD <OK|ALREADY> door=<id> session=<id> row=<path>
BAKEOFF DOOR id=<id> score=<right|unquoted|wrong|refused|timeout|malformed> tier=<t> in=<n|-> out=<n|-> usd=<usd|-> took=<d> result=<path>
BAKEOFF <OK|FAIL> task=<path> doors=<n> right=<n> unquoted=<n> wrong=<n> other=<n> usd=<usd|-> took=<d>
<TOKEN> MORE kind=<kind> shown=<n> total=<t> <remedy>
<TOKEN> NOTE <something true about this run that is not a finding>
<TOKEN> REFUSED: <reason> (<remedy>)
```

`OK` and `FAIL` are the last line and on stdout; `REFUSED` and `UNKNOWN` on stderr. Every id,
path, reason and header name goes through `internal/oneline`; no header VALUE, no key and no
task text ever reaches a line.

## First run — six lines a stranger pastes

The key is in the stranger's own `nova-secrets` file already (SPEC-SECRETS first run); only the
two lines that spend are wrapped in `exec`; before them, `refresh`'s one GET is the only network.

```
$ go install ./cmd/nova-go
$ nova-go doors --doors ./cmd/nova-go/testdata/doors.tsv --max 1
DOORS DOOR id=deepseek-v4-flash endpoint=chat cap=30 in=0.15 out=0.60 tier=off retention=0 training=no
DOORS MORE kind=door shown=1 total=27 nova-go doors --doors ./cmd/nova-go/testdata/doors.tsv --max 0
DOORS OK doors=27 source=https://opencode.ai/docs/go/ read=2026-09-12 file=./cmd/nova-go/testdata/doors.tsv
$ nova-go refresh --doors ./cmd/nova-go/testdata/doors.tsv
REFRESH NOTE ids compared; prices, caps and retention come from the page, not from /models
REFRESH OK live=27 file=27 added=0 gone=0 took=0.4s
$ nova-go choose --doors ./cmd/nova-go/testdata/doors.tsv --ledger ~/go-pool/ledger --worker ./cmd/nova-go/testdata/worker.json --shape read --bytes 60000 --max-output 2000
CHOOSE OK shape=read door=deepseek-v4-flash endpoint=https://opencode.ai/zen/go/v1/chat/completions tier=off est=0.0042 room=5.9958/14.9958/29.9958 dearer=false skipped=0
$ nova-secrets exec --store ~/secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops $(brew --prefix)/bin/sops --only OPENCODE_GO_API_KEY --require OPENCODE_GO_API_KEY -- nova-go ask --doors ./cmd/nova-go/testdata/doors.tsv --ledger ~/go-pool/ledger --door deepseek-v4-flash --task ./cmd/nova-go/testdata/task-known.md --result ~/go-pool/out/RESULT.md --session first-run-1 --max-output 2000 --as rowan --bench studio --repo unattributed
ASK OK door=deepseek-v4-flash session=first-run-1 tier=off in=1412 out=38 cache_read=0 cache_write=- reasoning=- usd=0.000235 took=3.1s result=/Users/x/go-pool/out/RESULT.md overflow=false
$ nova-secrets exec --store ~/secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops $(brew --prefix)/bin/sops --only OPENCODE_GO_API_KEY --require OPENCODE_GO_API_KEY -- nova-go bake-off --doors ./cmd/nova-go/testdata/doors.tsv --ledger ~/go-pool/ledger --worker ./cmd/nova-go/testdata/worker.json --task ./cmd/nova-go/testdata/task-known.md --out ~/go-pool/bake --max-output 2000
BAKEOFF DOOR id=deepseek-v4-flash score=right tier=off in=1412 out=38 usd=0.000235 took=3.1s result=/Users/x/go-pool/bake/deepseek-v4-flash/RESULT.md
BAKEOFF DOOR id=qwen3.8-flash score=unquoted tier=flat in=1398 out=61 usd=0.000238 took=2.7s result=/Users/x/go-pool/bake/qwen3.8-flash/RESULT.md
BAKEOFF FAIL task=./cmd/nova-go/testdata/task-known.md doors=5 right=4 unquoted=1 wrong=0 other=0 usd=0.00131 took=6.0s
```

Inside `go test` every endpoint is an `httptest` server answering the three shapes with
fixture usage bodies, and the key is a fixture string in the test's environment. Pasted in a
terminal, lines 5 and 6 spend real Go budget — about a tenth of a cent — and `refresh` reaches
the live list. `task-known.md` is a passage from SPEC.md, one question, answer `yes`, one rule
as the quote.

## Tests this spec demands

One test per rule, named for it, each proven able to fail by a mutation first; the tripwires
live together: outside the parser's table and the docs, no `opencode.ai`, no `11434`, no
`os.Getwd`, `os.UserHomeDir`, hardcoded path, `exec.Command("sh"`, or `"-c"`; no test reads a
real key.

1. `TestTheDoorsComeFromAFile`: no `--doors` is exit 2 `refusing to guess`; a wrong header, a
   missing source line, 13 and 15 fields are each exit 2 naming the line; no door id is a
   string constant in the binary (the tripwire).
2. `TestDoorsPrintsTheFileAndNeverTheNetwork`: 27 doors with `--max 2` print 2 lines, one
   MORE `total=27`, `DOORS OK`; a counting server sees zero requests; `--ledger` adds `open=`.
3. `TestRefreshDiffsAndNeverEdits`: a live list with one id more and one less prints `ADDED`,
   `GONE`, exits 1; the file's bytes are unchanged; a 401, 500, timeout, `{}` and a 257 KB
   body are each `REFRESH UNKNOWN` exit 1, and `up to date` appears nowhere.
4. `TestAShapeNamesItsDoors`: `--shape audit` is exit 2 listing the four; no `prefer.decision`
   is exit 2 naming the shape; a `prefer` id absent from the doors file is exit 2 naming it.
5. `TestChooseTakesTheFirstOpenDoor`: with a ledger that closes door 1's 5-hour room, door 2
   is chosen and door 1 printed as `SKIP reason=5h`; `--bytes 0` and `--max-output 0` are
   exit 2; the estimate on the line equals `bytes/3*in + max_output*out` at the tier.
6. `TestRoomIsMeasuredThenLedgered`: the first `ask` writes header names and never values
   into `headers.txt` (a fixture value is absent from every file); `budget_source:
   headers:x-fixture-remaining` reads that header; under `ledger` and an injected clock a
   row 5 h 1 s old is out of the 5-hour sum and in the weekly one.
7. `TestExhaustionNamesTheNextDoorAndNeverPaysMoreAlone`: door 1 closed and door 2 dearer
   is `CHOOSE REFUSED … next=<door 2> until=<stamp>` exit 1, `until` equal to the oldest
   in-window row plus 5 h; with `--allow-dearer` it is `CHOOSE OK dearer=true`; door 2 not
   dearer is chosen with `dearer=false`.
8. `TestTheLocalTierIsLast`: every paid door closed and shape `mechanical` yields
   `door=local:qwen3.6:35b-a3b` with the description's `local_base_url`; shape `read` yields
   `REFUSED … next=none`; a description with `local` and no `local_base_url` is exit 2.
9. `TestRetentionRefusesPrivateWork`: `grok-4.6` and `gpt-5.6-luna` are `SKIP
   reason=retention` for a private job and chosen under `--public`; a DeepSeek door with
   `zdr_until` behind the clock is skipped the same way; `ask --door grok-4.6` without
   `--public` is `ASK REFUSED` naming `--public`, nothing sent.
10. `TestATrainingDoorIsNeverChosen`: `muse-spark-1.3-contributor` is skipped under every
    shape and both marks; `ask --door` on it needs `--public --allow-training`, else refused.
11. `TestOverflowIsOff`: a closed door under `ask` is `ASK REFUSED` and zero requests;
    `--allow-overflow` sends once and prints `overflow=true`.
12. `TestMannersAreOnEveryRequest`: every request seen carries `User-Agent: nova-go/<version>`
    and the `--session` value in `x-opencode-session`; no `--session` is exit 2, nothing sent.
13. `TestTheKeyComesFromTheEnvironmentOnly`: an unset or empty `OPENCODE_GO_API_KEY` is exit
    2 naming the `nova-secrets exec` line; `--key` is unknown; the fixture key appears in no
    byte of stdout, stderr, the ledger, `headers.txt` or the result.
14. `TestAskBoundsThePromptFirst`: a task of `ctx*3+3` bytes against `grok-4.6` is exit 2
    naming *split the task* with zero requests; the result lands via `.tmp` and rename.
15. `TestTheTierIsTheClock`: under an injected clock, Tuesday 02:00Z is `tier=peak` and
    Saturday 02:00Z `tier=off` for a DeepSeek door, `flat` for GLM at both; the tier on the
    ledger row equals the tier on the line.
16. `TestTheRecordReadsUsageByShape`: fixture bodies for `chat`, `responses` and `messages`
    each map to the named fields; a body without `usage` yields `-` in every token column and
    `usd=-`; the row leads `day friend bench repo model`; missing `--as`, `--bench`, `--repo`
    are exit 2 named at once.
17. `TestRecordAppendsOnce`: two `record`s of one `(job, session)` leave one row, the second
    printing `ALREADY`; the file's bytes before are a prefix of the bytes after.
18. `TestBakeOffScoresAgainstTheKnownAnswer`: five fixture doors answering right, right with
    a paraphrased quote, wrong, nothing, and late score `right`, `unquoted`, `wrong`,
    `malformed`, `timeout`, `BAKEOFF FAIL` exit 1; all right is `OK`; every door received the
    same session id and `max_tokens`.
19. `TestOutputAndRunAreBounded`: 60 `ADDED` and 60 `GONE` print at most `2*max+4` lines with
    two MORE lines; a door answering after `--budget` is `timeout`; never a fifth request in
    flight; the count line prints on failure.
20. `TestEveryRefusalNamesItsRemedy`: the refusal table is walked, each ends in a
    parenthesised remedy naming a flag, a file, a command or the values allowed.

## Open questions for the group — each with a default; the default stands unless a line says otherwise

Stella carries this draft to every line. Answer by number, one line each; `abstain` is an
answer; deadline 2026-09-14 18:00Z.

1. **Which doors each line wants in its preference lists**, per shape. Default: the
   description above — DeepSeek V4 Flash, Qwen3.8 Flash, GLM-5.3-Flash for reads; Kimi K2.7
   Code then DeepSeek V4 Pro for decisions; GLM-5.3-Flash then MiMo-V2.5 for mechanical —
   until a bake-off says otherwise, and the bake-off is the way to say it.
2. **Whether Grok 4.6 on Go is a door Johnny wants for his point of view** ($15 a month,
   about 169 requests per five hours, 30-day retention, the `responses` shape). His decision
   and nobody else's. Default: not in anyone's list; his seat stays where it is.
3. **A household subscription or one per line.** One subscriber per workspace at Go, one key
   per line at nova-secrets: so one subscription per line that runs a pool, $10 each.
   Default: per line; the first pool is Rowan's on the admin bench, key in `rowan.yaml`.
4. **What `private` marks by default.** Default: every job is private unless `--public` is
   typed, so anything read under a line's home reaches only a 0-day door and never a
   training one; `--public` is for text already on a public repository.
5. **Where the 5-hour window starts.** Default: rolling over the tool's own ledger, the
   conservative reading, until a first run's `headers.txt` shows a provider header carrying
   remaining budget; whoever runs it first posts the header names to nova-tools.
