# nova-go — specification (DRAFT for the group's read, revision 2)

Glenn, 2026-09-12, on https://opencode.ai/docs/go/ : *"It could be a varying swarm, where you
make a choice of model depending on querying it, and what is available to run."* *"I don't
think we run friends on this model, but it could be a work pool."* *"Our whole push right now
is to find the EFFICIENT way for us to work as a team."* Idea: [ideas#771](https://github.com/mas-bandwidth/ideas/issues/771).

**What Go is, as read 2026-09-12, that the fixture cannot carry.** $10 a month for one key,
one subscriber per workspace; traffic monitored, a self-identifying user agent and a stable
`x-opencode-session` expected; an optional Zen-balance overflow; three endpoint shapes under
one base URL and a `models` list; *the list may change*. Every number is in the doors file,
one source and one date for the whole file. The page names no zero-day agreement and no date
for one, so `zdr_until` is `-` until a line quotes the page's sentence (open item 8).

## What it is, and is not

One binary at the **capacity layer**. `nova-go` is a **work pool for one-shot jobs**: it
holds the list of Go's doors as data, asks before each job which door still has budget in
the current window, hands nova-swarm the door to use, records the door actually used and
what it cost, and refuses by name when nothing is open. A **door** is one model at one
endpoint under one cap.

- It is **never a line's session**: no friend runs on Go, no self loads through it, no
  memory or bus is touched by anything it starts. A task file in, a `RESULT.md` out; rule 21.
- It is **a provider for nova-swarm, not a dispatcher**: slots, deadlines, the sandbox, the
  job directory, `finalize` and the usage file stay nova-swarm's (SPEC-SWARM rules 7, 12,
  13). What nova-swarm must gain to call it is under **Owed in SPEC-SWARM**, item by item,
  and that amendment is a PR of its own that lands before nova-go is built.

SPEC.md's **Conventions** govern — exit codes, one line per event, the field law,
`internal/oneline`, `internal/bounded`, no guessed paths — and this file says only what is more.

## The rules, numbered

1. **The doors are a file, named by a flag, kept in git.** Every door comes from the file
   `--doors` names; no door and no URL is in the binary. A missing `--doors` is `refusing to
   guess`, exit 2; a first line that is not the header byte for byte, or a line 2 that is not
   `# source=<url> base=<url> read=<YYYY-MM-DD>`, is exit 2 naming the line, remedy *put the
   header and the source line back as the spec shows*. Fourteen tab-separated fields per
   door, none empty, `-` for not applicable; more or fewer is exit 2 naming the line, never a skip.
2. **`doors` prints the file and never the network.** One `DOORS DOOR` line per door in file
   order, capped by `--max` (default 20, `0` all, negative refused), then `DOORS OK` with
   `doors=`, `source=`, `base=`, `read=`. With `--ledger <dir> --worker <file>` (both, or
   neither) each line adds `open=`, the three rooms by rule 6 under that description's
   `budget_source`, so the table a person reads is the one `choose` uses.
3. **`refresh` fetches the live list and diffs it against the file; it never edits the
   file.** One bounded GET of `<base>/models` (256 KB cap, 5s timeout, status before body); an
   id live and not in the file is `REFRESH ADDED`, in the file and not live `REFRESH GONE`, a
   field the body carries under a name the file holds and disagrees on `REFRESH CHANGED id=
   field=`; any difference exit 1, remedy *read the page, edit the file, commit*; a `REFRESH
   NOTE` names the fields compared. A 401 is `REFRESH UNKNOWN` naming `nova-secrets exec`; any
   non-200, timeout or malformed body is UNKNOWN, exit 1, never *up to date*. When `read=` is
   more than 30 days behind the tool's clock, `DOORS OK` and `CHOOSE OK` are preceded by
   `<TOKEN> NOTE read=<date> age=<n>d stale (run nova-go refresh, read the page, commit)`.
4. **A job names a shape, and the worker description names the doors for it.** `--shape` is
   one of `read`, `decision`, `mechanical`, `bake-off` (open item 1); `go.prefer.<shape>`
   lists door ids in preference order, each present in the doors file or the description is
   refused at load naming it. A shape with no list is refused naming the shape, remedy *add
   go.prefer.<shape> to the description*.
5. **`choose` walks the list and takes the first open door.** Open means all of: the estimate
   fits every window's room (rule 6, reservations counted); `retention` passes rule 9; the
   door does not train (rule 10); the input estimate fits the door's `ctx` tier (else `SKIP
   reason=ctx`); and the door is not dearer than one already passed over for a cap window in
   this walk (else `SKIP reason=dearer`, lifted by `--allow-dearer`). Dearer: a higher `in`
   price at the current tier, or equal `in` and higher `out`. The estimate is `bytes/3` input
   tokens plus `--max-output` output tokens at the tier's prices, both flags required, `0`
   refused; it schedules, and the provider's own limit is the bound (rule 14). `CHOOSE OK`
   names the door, the room after this job, the tier, and every door passed over with its reason.
6. **Room per window comes from the provider when it says, else from the tool's own
   ledger, and a job in flight is spend.** `--ledger <dir>` is created if absent; an empty
   ledger is zero spend, so the first `choose` of a life sees full room. Under `budget_source:
   ledger`, room is the cap times 20%, 50%, 100% minus the window's spend in the trailing 5
   hours, 7 days and UTC calendar month. Spend is the ledger's rows plus the reservations in
   `<ledger>/inflight/`: `choose` writes `<ledger>/inflight/<session>` holding `door est
   deadline stamp`, counted in every window until `record` or `ask` for that session writes
   the row and removes it; one past its deadline stays counted (unknown is not zero) and
   `CHOOSE NOTE inflight-stale=<n> (run nova-go record for each, or remove the file)` says so.
   No row is estimated: a row is a response's usage at the fixture's prices, or `-`; a `-`
   row counts as its reservation's `est`, else closes the door (unpriced spend is not free).
   Under `budget_source: headers`, `go.headers: {"5h": <name>, "week": <name>, "month":
   <name>}` names one header per window, a window without one falling to the ledger; every
   `ask` writes the NAMES of response headers matching `x-ratelimit*`, `x-opencode*` or
   `*-usage*` to `<ledger>/headers.txt` (never a value) and, for each named header, `window
   value stamp` to `<ledger>/remaining/<door>.tsv`, which `choose` reads, a stamp older than
   its window being absent. Which header carries remaining budget is measured (open item 5).
7. **A cap exhausted is the tool saying NO, naming the next door and the window's end; it
   never falls to a dearer door on its own.** A walk ending with no open door and at least
   one cap-closed is `CHOOSE FAIL shape=<s> door=<first cap-closed id> window=<5h|week|month>
   room=<usd> need=<usd> next=<first dearer-skipped id|none> until=<stamp> (wait until
   <stamp>, or --allow-dearer for <next>)`, exit 1, stdout; `until` is when the oldest row or
   reservation in a trailing window ages out, the first of next month UTC for `month`. Under
   `--allow-dearer` a dearer door is open and `CHOOSE OK` prints `dearer=true`. A walk with
   nothing cap-closed (all skipped for retention, training, ctx) is `CHOOSE REFUSED`, exit 2.
8. **`choose` never returns the local tier.** Every swarm job has a deadline (SPEC-SWARM
   rule 7); Glenn's limit is that the local tier is for work with no clock (memory
   2026-09-12); the two do not meet in `choose`. A description carrying `local`,
   `local_base_url` or `local_shapes` is refused at load naming the field, remedy *the local
   tier is nova-local's; open item 6*. Every paid door closed is rule 7's `FAIL`, and a
   person decides the local run.
9. **A door that keeps data is refused for private work.** Every job is `private` unless
   `--public` is passed. A private job may use only a door whose `retention` is `0` (not
   `unknown`) and whose `zdr_until` is `-` or not yet past by the tool's clock; a door that
   fails is `CHOOSE SKIP … reason=retention`, `ask --door` on one is `ASK REFUSED` naming
   `--public`, and `bake-off` scores it `refused` with nothing sent.
10. **A door that trains is never chosen.** `training=yes` is skipped by `choose` for every
    job and scored `refused` by `bake-off` (no flag lifts it); it is reachable only by `ask
    --door <id> --public --allow-training`, for text nobody minds a model learning.
11. **The Zen overflow is off.** The tool never sets or relies on the console's *Use
    balance*; past a cap, rule 7 refuses. `--allow-overflow` lets `ask` send when the ledger
    says the door is closed, prints `overflow=true`, and is the only way we spend metered credit.
12. **Manners the provider asked for, kept by construction.** Every request carries
    `User-Agent: nova-go/<version>` and `x-opencode-session: <session>`, the session being
    `--session <id>`, stable for the job's life; one `ask` is one request and no second turn,
    which is what a one-shot job is. A request with no session id is not sent.
13. **The key is `OPENCODE_GO_API_KEY` in the environment, and nowhere else.** No `--key`,
    no key file, no config the tool writes: an absent or empty variable is exit 2 with the
    `nova-secrets exec … --only OPENCODE_GO_API_KEY` line to wrap the command in. Under
    nova-swarm that environment is the CHILD's, built by `run` reading the description's
    `key_file` as data (SPEC-SWARM **The key, read as data**; its lesson 18 unchanged); the
    harness config carries the NAME, `{env:OPENCODE_GO_API_KEY}`.
14. **`ask` is one call, prompt bounded before it is sent.** It reads `--task <file>` whole
    — passage inline, one question, the result path — and refuses at exit 2 a prompt whose
    `bytes/3` exceeds the door's `ctx` tier, remedy *split the task*. `bytes/3` estimates: a
    provider refusal for size is `ASK FAIL door=<id> reason=ctx`, exit 1, never a retry on a
    bigger door by itself. The answer lands at `--result <path>` through `.tmp` and rename,
    and one `ASK OK` line prints.
15. **The price tier is the tool's clock, in UTC.** A door with peak prices is `tier=peak`
    01:00-04:00 and 06:00-10:00 UTC Monday to Friday, `tier=off` otherwise; a door without
    them is `tier=flat`. Decided at send time, printed on every line naming a door, stored on
    the ledger row; nothing read from a response is a clock.
16. **The record carries the door actually used, tokens by kind, `-` never `0`.** `ask`
    reads its response by shape: `chat` `usage.prompt_tokens`, `completion_tokens`,
    `prompt_tokens_details.cached_tokens`; `responses` `usage.input_tokens`, `output_tokens`,
    `input_tokens_details.cached_tokens`, `output_tokens_details.reasoning_tokens`;
    `messages` `usage.input_tokens`, `output_tokens`, `cache_read_input_tokens`,
    `cache_creation_input_tokens`. `record --usage <file>` reads SPEC-SWARM rule 12's
    sixteen-column usage file — `job`, `model`, `tokens_in`, `tokens_out`, `cache_write`,
    `cache_read`, `reasoning` — prices them at the fixture, and refuses another header by
    name; the per-shape mapping is `ask`'s alone. A field the source lacks is `-`; `usd` is
    `-` when any priced field is. The row's key is SPEC-TOKENS rule 15's, `day model repo`,
    then `friend bench job session door shape tier tokens_in tokens_out cache_read
    cache_write reasoning usd at`; `friend` and `bench` are columns, not key, until PR #124's
    retained-record contract says otherwise. `--as`, `--bench` and `--repo` are required,
    `--repo unattributed` the honest spelling.
17. **`record` appends one row and never rewrites one.** `<ledger>/spend/<door>/<YYYY-MM-DD>.tsv`,
    one header, one row per call, written by `ask` itself and by `record` when nova-swarm's
    `finalize` hands over a job's usage. A `(job, session)` present with the same token
    fields is `RECORD ALREADY`, exit 0, so a retry never double-counts; the same key with
    different fields is `RECORD CONFLICT`, exit 1, nothing written. nova-tokens reads the
    directory as a declared source by a rule of its own PR (SPEC-TOKENS rule 14's shape).
18. **`bake-off` runs one task across N doors and scores every answer against the known
    one.** The task file's head carries `answer: yes|no` and `quote: <verbatim>|none`; each
    door's result must end with the same two lines. Per door: `right` when `answer` matches
    and the quote, if any, is a verbatim substring of the task text; `unquoted` when the
    answer matches and the quote is not in the text; `wrong` otherwise; `refused` (rules 9,
    10), `timeout` or `malformed` when no answer line came back. `--doors-list <id,…>` names
    the doors (`go.prefer.bake-off` when absent); one session id per (task, door), no two
    alike, the same `--max-output` for all, one `BAKEOFF DOOR` line each; `BAKEOFF FAIL`
    exit 1 when any door is not `right`.
19. **Bounded output, bounded run.** `--max` per listing, per kind on `refresh`; `--timeout
    <d>` per call, default `120s`; `bake-off` runs at most four doors at once and its
    `--budget <d>`, default `10m`, marks unreached doors `timeout`. The count line prints on
    failure as well as success, and every count is about the state.
20. **Every refusal names its remedy**, and every refusal lives in one table:
    `cmd/nova-go/refusals.go`, one entry per refusal this file names — token, reason, remedy
    — and no refusal string anywhere else; every `exit 2` above is one entry.
21. **No session is ever held, and no self is ever loaded.** `serve`, `subscribe`, `key` and
    `--watch` are unknown, exit 2. `ask` opens only the files its flags name and sends one
    request. `choose` and `bake-off` refuse at load, naming the field, a description whose
    `harness` is a line's own harness (`claude`, `codex`; open item 7) or whose `worker_dir`
    holds a self (`CLAUDE.md`, `AGENTS.md`, `MEMORY.md`, `.claude/`), remedy *an empty
    worker_dir; a friend's session is not a job*.

## The doors file

Line 1 the header byte for byte, line 2 the source line, `#` comments skipped, tabs between
fields. Prices are dollars per million tokens; `cap` is the monthly limit, the 5-hour (20%)
and weekly (50%) rooms derived and never stored. `shape` is the path under `base=`: `chat`
`/chat/completions`, `responses` `/responses`, `messages` `/messages`. `ctx` is the token
count above which the page prices the door higher, `-` for one tier; `retention` is days or
`unknown`; `zdr_until` is the date a zero-day agreement runs to, `-` when none is stated. The
first commit of `cmd/nova-go/testdata/doors.tsv` carries all 27 doors as read 2026-09-12;
these are the rows this spec names:

```
id	shape	cap	in	out	cache_read	cache_write	peak_in	peak_out	peak_cache_read	ctx	retention	zdr_until	training
# source=https://opencode.ai/docs/go/ base=https://opencode.ai/zen/go/v1 read=2026-09-12
deepseek-v4-flash	chat	30	0.15	0.60	0.003	-	0.30	1.20	0.006	-	0	-	no
deepseek-v4.1-flash	chat	15	0.15	0.60	0.003	-	0.30	1.20	0.006	-	0	-	no
deepseek-v4-pro	chat	15	0.66	1.98	0.022	-	1.32	3.96	0.044	-	0	-	no
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

The JSON nova-swarm takes (`--worker <file>`, strict decode, an unknown field a refusal), as
it reads after the amendment below: every field nova-swarm requires today is present,
nova-go's own live under one block, `go`, and `model` and `base_url` hold the placeholders
`run` fills per job from `CHOOSE OK`.

```json
{ "name": "rowan-go", "provider": "opencode-go", "model": "{model}", "base_url": "{base_url}",
  "env_var": "OPENCODE_GO_API_KEY", "key_file": "/Users/rowan/.config/opencode-go/env",
  "usage": "sqlite", "harness": "opencode", "harness_args": ["run", "--format", "json"],
  "worker_dir": "/Users/rowan/go-pool/worker", "deadline": 1800,
  "go": { "budget_source": "ledger",
          "prefer": { "read": ["deepseek-v4-flash", "qwen3.8-flash", "glm-5.3-flash"],
                      "decision": ["kimi-k2.7-code", "deepseek-v4-pro"],
                      "mechanical": ["glm-5.3-flash", "mimo-v2.5"],
                      "bake-off": ["deepseek-v4-flash", "qwen3.8-flash", "glm-5.3-flash", "kimi-k2.7-code", "minimax-m3"] } } }
```

## Owed in SPEC-SWARM — the amendment that lands first

SPEC-LOCAL records nova-swarm's decoder as strict, eleven fields required; SPEC-SWARM says
*it does not choose a model; the worker description does, and it is required*. For
`provider: opencode-go` these six move, and nothing else:

1. **Schema.** `go` is a known block; `model` and `base_url` may hold `{model}` and
   `{base_url}`, filled per job for this provider only. Strict decode stays.
2. **Before a launch**, `run` calls `nova-go choose … --job <id> --session <id> --deadline
   <d>`; `CHOOSE OK` fills the placeholders and the harness config; on `CHOOSE FAIL` the job
   waits or takes its default action (open item 4).
3. **The RUN line.** `RUN START` and `RUN RECLAIM` gain `door=<id>`.
4. **After `finalize`** writes `<pool>/usage/<job>.tsv`, `run` calls `nova-go record --usage
   <that file> --door --job --session --as --bench --repo`; a `CONFLICT` is a `RUN NOTE`.
5. **The key** reaches nova-go as it reaches every worker: `run` reads `key_file` as data and
   sets `OPENCODE_GO_API_KEY` in the child's environment (rule 6, lesson 18 unchanged).
6. **Beside it**: SPEC-SECRETS's credential table gains `OPENCODE_GO_API_KEY` in
   `<line>.yaml`; SPEC-TOKENS gains the ledger directory as a source. Named here so a yes to
   this draft is not a quiet yes to them.

## The verbs

```
nova-go doors    --doors <file> [--ledger <dir> --worker <file>] [--max <n>]
nova-go refresh  --doors <file> [--timeout <d>] [--max <n>]
nova-go choose   --doors <file> --ledger <dir> --worker <file> --shape <s> --bytes <n> --max-output <n> --job <id> --session <id> --deadline <d> [--public] [--allow-dearer]
nova-go ask      --doors <file> --ledger <dir> --door <id> --task <file> --result <path> --job <id> --session <id> --max-output <n> --as <name> --bench <name> --repo <name|unattributed> [--public] [--allow-training] [--allow-overflow] [--timeout <d>]
nova-go record   --doors <file> --ledger <dir> --usage <file> --door <id> --job <id> --session <id> --as <name> --bench <name> --repo <name|unattributed>
nova-go bake-off --doors <file> --ledger <dir> --worker <file> --task <file> --out <dir> --max-output <n> [--doors-list <id,…>] [--public] [--budget <d>] [--timeout <d>] [--max <n>]
nova-go help
```

Those lines are what `nova-go help` prints, byte for byte.

## Exit codes and the output grammar

Per SPEC.md: **0** ran and passed — a door chosen, a list unchanged, every bake-off door `right`;
**1** the tool saying NO — no door open, a list that moved, a door not `right`, a dead source, a
conflicting record; **2** could not run, every refusal the rules name.

```
DOORS DOOR id=<id> shape=<chat|responses|messages> cap=<usd> in=<p> out=<p> tier=<peak|off|flat> retention=<d|unknown> training=<yes|no> [open=<5h>/<week>/<month>]
DOORS OK doors=<n> source=<url> base=<url> read=<date> file=<path>
REFRESH ADDED id=<id> | REFRESH GONE id=<id> | REFRESH CHANGED id=<id> field=<f> | REFRESH UNKNOWN source=<url>: <reason> (<remedy>)
REFRESH <OK|FAIL> live=<n> file=<n> added=<n> gone=<n> changed=<n> took=<d>
CHOOSE SKIP shape=<s> door=<id> reason=<5h|week|month|retention|training|ctx|dearer>
CHOOSE OK shape=<s> door=<id> endpoint=<url> tier=<t> est=<usd> room=<5h>/<week>/<month> dearer=<true|false> skipped=<n> inflight=<path>
CHOOSE FAIL shape=<s> door=<id> window=<w> room=<usd> need=<usd> next=<id|none> until=<stamp> (wait until <stamp>, or --allow-dearer for <next>)
ASK OK door=<id> session=<id> tier=<t> in=<n|-> out=<n|-> cache_read=<n|-> cache_write=<n|-> reasoning=<n|-> usd=<usd|-> took=<d> result=<path> overflow=<true|false>
ASK FAIL door=<id> reason=<ctx|status:<n>> took=<d> (<remedy>)
RECORD <OK|ALREADY|CONFLICT> door=<id> session=<id> row=<path>
BAKEOFF DOOR id=<id> score=<right|unquoted|wrong|refused|timeout|malformed> tier=<t> in=<n|-> out=<n|-> usd=<usd|-> took=<d> result=<path>
BAKEOFF <OK|FAIL> task=<path> doors=<n> right=<n> unquoted=<n> wrong=<n> other=<n> usd=<usd|-> took=<d>
<TOKEN> MORE kind=<kind> shown=<n> total=<t> <remedy>
<TOKEN> NOTE <something true about this run that is not a finding>
<TOKEN> REFUSED: <reason> (<remedy>)
```

`OK` and `FAIL` are the last line and on stdout; `REFUSED` and `UNKNOWN` on stderr, exit 2
always. Every id, path, reason and header name goes through `internal/oneline`; no header
VALUE, no key and no task text ever reaches a line.

## First run — five lines a stranger pastes

The key is in the stranger's own `nova-secrets` file already (SPEC-SECRETS first run); only
the line that spends is wrapped in `exec`; before it, `refresh`'s one GET is the only network.

```
$ go install ./cmd/nova-go
$ nova-go doors --doors ./cmd/nova-go/testdata/doors.tsv --max 1
DOORS DOOR id=deepseek-v4-flash shape=chat cap=30 in=0.15 out=0.60 tier=off retention=0 training=no
DOORS MORE kind=door shown=1 total=27 nova-go doors --doors ./cmd/nova-go/testdata/doors.tsv --max 0
DOORS OK doors=27 source=https://opencode.ai/docs/go/ base=https://opencode.ai/zen/go/v1 read=2026-09-12 file=./cmd/nova-go/testdata/doors.tsv
$ nova-go refresh --doors ./cmd/nova-go/testdata/doors.tsv
REFRESH NOTE compared=id; prices, caps and retention come from the page, not from /models
REFRESH OK live=27 file=27 added=0 gone=0 changed=0 took=0.4s
$ nova-go choose --doors ./cmd/nova-go/testdata/doors.tsv --ledger ~/go-pool/ledger --worker ./cmd/nova-go/testdata/worker.json --shape read --bytes 60000 --max-output 2000 --job first-run-1 --session first-run-1 --deadline 600
CHOOSE OK shape=read door=deepseek-v4-flash endpoint=https://opencode.ai/zen/go/v1/chat/completions tier=off est=0.0042 room=5.9958/14.9958/29.9958 dearer=false skipped=0 inflight=/Users/x/go-pool/ledger/inflight/first-run-1
$ nova-secrets exec --store ~/secrets --as rowan --key ~/.config/nova-secrets/rowan.key --sops $(brew --prefix)/bin/sops --only OPENCODE_GO_API_KEY --require OPENCODE_GO_API_KEY -- nova-go ask --doors ./cmd/nova-go/testdata/doors.tsv --ledger ~/go-pool/ledger --door deepseek-v4-flash --task ./cmd/nova-go/testdata/task-known.md --result ~/go-pool/out/RESULT.md --job first-run-1 --session first-run-1 --max-output 2000 --as rowan --bench studio --repo unattributed
ASK OK door=deepseek-v4-flash session=first-run-1 tier=off in=1412 out=38 cache_read=0 cache_write=- reasoning=- usd=0.000235 took=3.1s result=/Users/x/go-pool/out/RESULT.md overflow=false
```

Inside `go test` every endpoint is an `httptest` server answering the three shapes with
fixture usage bodies, and the key is a fixture string in the test's environment. Pasted in a
terminal, line 5 spends about a tenth of a cent and `refresh` reaches the live list.
`task-known.md` is a passage from SPEC.md, one question, answer `yes`, one rule as the quote.

## Tests this spec demands

One test per rule, named for it, each proven able to fail by a mutation first; the tripwires
live together: outside the parser's table and the docs, no `opencode.ai`, no `11434`, no
`os.Getwd`, `os.UserHomeDir`, hardcoded path, `exec.Command("sh"`, or `"-c"`; no test reads a
real key.

1. `TestTheDoorsComeFromAFile`: no `--doors` is exit 2 `refusing to guess`; a wrong header, a
   source line without `base=`, 13 and 15 fields are each exit 2 naming the line; no door id
   and no URL is a string constant in the binary (the tripwire).
2. `TestDoorsPrintsTheFileAndNeverTheNetwork`: 27 doors with `--max 2` print 2 lines, one
   MORE `total=27`, `DOORS OK`; a counting server sees zero requests; `--ledger --worker`
   adds `open=` equal to `choose`'s `room=` before its estimate; `--ledger` alone is exit 2.
3. `TestRefreshDiffsAndNeverEdits`: one id more and one less print `ADDED`, `GONE`, exit 1; a
   body whose `ctx` disagrees prints `CHANGED field=ctx`; the file's bytes are unchanged; a
   401, 500, timeout, `{}` and a 257 KB body are each `REFRESH UNKNOWN` exit 1, and `up to
   date` appears nowhere; at 31 days past `read=` `doors` and `choose` print the `stale`
   NOTE, at 30 they do not.
4. `TestAShapeNamesItsDoors`: `--shape audit` is exit 2 listing the four; no
   `go.prefer.decision` is exit 2 naming the shape; a `prefer` id absent from the doors file
   is exit 2 naming it.
5. `TestChooseTakesTheFirstOpenDoor`: door 1's 5-hour room closed chooses door 2 with `SKIP
   reason=5h`; a door whose `ctx` is below `bytes/3` is `SKIP reason=ctx`; door 1 cap-closed
   and door 2 dearer is `SKIP reason=dearer`; `--bytes 0` and `--max-output 0` are exit 2;
   the estimate equals `bytes/3*in + max_output*out` at the tier; two `choose`s in a row see
   the second's room reduced by the first's `est`.
6. `TestRoomIsMeasuredThenLedgered`: an absent ledger is created and the first `choose`
   prints full room; `ask` writes header names, never values, to `headers.txt` (a fixture
   value is in no file but `remaining/`); `go.headers` naming `x-fixture-remaining` for `5h`
   reads `remaining/<door>.tsv` for that window and the ledger for the other two, a stamp
   5 h 1 s old being absent; under `ledger` and an injected clock a row 5 h 1 s old is out of
   the 5-hour sum and in the weekly one; a reservation past its deadline is counted and
   `inflight-stale=1` prints; a `-` row with no reservation closes its door.
7. `TestExhaustionNamesTheNextDoorAndNeverPaysMoreAlone`: door 1 closed and door 2 dearer
   is `CHOOSE FAIL … next=<door 2> until=<stamp> (wait until <stamp>, or --allow-dearer for
   <door 2>)`, exit 1, stdout, `until` the oldest in-window row plus 5 h for `5h` and the
   first of next month for `month`; `--allow-dearer` gives `CHOOSE OK dearer=true`; door 2
   not dearer is chosen `dearer=false`; every door skipped for retention alone is `CHOOSE
   REFUSED`, exit 2, stderr.
8. `TestChooseNeverReturnsALocalDoor`: every paid door closed under every shape is `CHOOSE
   FAIL … next=none` and `local:` is in no line; a description with `local`, `local_base_url`
   or `local_shapes` is exit 2 naming the field.
9. `TestRetentionRefusesPrivateWork`: `grok-4.6`, `gpt-5.6-luna` and a `retention=unknown`
   door are `SKIP reason=retention` for a private job, the first two chosen under `--public`;
   a `zdr_until` behind the clock is skipped the same way; `ask --door grok-4.6` without
   `--public` is `ASK REFUSED` naming `--public`, nothing sent; `bake-off --doors-list
   grok-4.6` private scores `refused` with zero requests.
10. `TestATrainingDoorIsNeverChosen`: `muse-spark-1.3-contributor` is skipped under every
    shape and both marks; `bake-off --doors-list` naming it scores `refused` with zero
    requests under `--public` too; `ask --door` on it needs `--public --allow-training`.
11. `TestOverflowIsOff`: a closed door under `ask` is `ASK REFUSED` and zero requests;
    `--allow-overflow` sends once and prints `overflow=true`.
12. `TestMannersAreOnEveryRequest`: every request carries `User-Agent: nova-go/<version>` and
    the `--session` value in `x-opencode-session`; one `ask` is exactly one request at the
    counting server, whatever the response; no `--session` is exit 2, nothing sent.
13. `TestTheKeyComesFromTheEnvironmentOnly`: an unset or empty `OPENCODE_GO_API_KEY` is exit
    2 naming the `nova-secrets exec` line; `--key` is unknown; the fixture key appears in no
    byte of stdout, stderr, the ledger, `headers.txt`, `remaining/` or the result.
14. `TestAskBoundsThePromptFirst`: a task of `ctx*3+3` bytes against `grok-4.6` is exit 2
    naming *split the task* with zero requests; a 400 naming context length is `ASK FAIL
    reason=ctx` exit 1 after one request; the result lands via `.tmp` and rename.
15. `TestTheTierIsTheClock`: under an injected clock Tuesday 02:00Z, 03:59Z, 06:00Z, 09:59Z
    are `tier=peak`, Tuesday 04:00Z, 05:59Z, 10:00Z and Saturday 02:00Z `tier=off` for a
    DeepSeek door, `flat` for GLM at all; the tier on the ledger row equals the line's.
16. `TestTheRecordReadsUsageByShape`: fixture bodies for `chat`, `responses` and `messages`
    each map to the named fields; a body without `usage` yields `-` in every token column and
    `usd=-`; `record --usage` on a sixteen-column rule-12 file maps the seven named columns
    and prices them, a fifteen-column file is exit 2 naming the header; the row leads `day
    model repo`; missing `--as`, `--bench`, `--repo` are exit 2 named at once.
17. `TestRecordAppendsOnce`: two `record`s of one `(job, session)` with the same fields leave
    one row, the second `ALREADY`; a third with another `tokens_out` is `CONFLICT` exit 1 and
    the file unchanged; the bytes before are a prefix of the bytes after.
18. `TestBakeOffScoresAgainstTheKnownAnswer`: five fixture doors answering right, right with
    a paraphrased quote, wrong, nothing, and late score `right`, `unquoted`, `wrong`,
    `malformed`, `timeout`, `BAKEOFF FAIL` exit 1; all right is `OK`; every door received its
    own session id, no two alike, and the same `max_tokens`.
19. `TestOutputAndRunAreBounded`: 60 `ADDED` and 60 `GONE` print at most `2*max+4` lines with
    two MORE lines; a door answering after `--budget` is `timeout`; never a fifth request in
    flight; the count line prints on failure.
20. `TestEveryRefusalNamesItsRemedy`: `refusals.go` is walked, each entry ends in a
    parenthesised remedy naming a flag, a file, a command or the values allowed; every `exit
    2` sentence in this file has an entry; no refusal string in the binary is outside the table.
21. `TestNoSessionIsEverHeld`: `serve`, `subscribe`, `key` and `--watch` are exit 2 unknown;
    under a filesystem fixture `ask` opens no file but `--doors`, `--task`, `--result` and the
    ledger; `harness: claude` is exit 2 naming the field, and a `worker_dir` holding
    `CLAUDE.md` is exit 2 naming the file.

## Open items for the group — each with a default; the default stands unless a line says otherwise

Rowan carries this draft to every line; Stella takes it over on 2026-09-14 if she is back.
Answer by number, one line each; `abstain` is an answer; deadline 2026-09-14 18:00Z.

1. **The four shapes and the doors in each list.** Default: `read`, `decision`,
   `mechanical`, `bake-off`, and the description above, until a bake-off says otherwise.
2. **Whether Grok 4.6 on Go is a door Johnny wants for his point of view** ($15 a month,
   30-day retention, the `responses` shape). His decision; default: not in anyone's list.
3. **A household subscription or one per line.** One subscriber per workspace at Go, one key
   per line at nova-secrets. Default: per line; the first pool is Rowan's, key in `rowan.yaml`.
4. **What nova-swarm does on `CHOOSE FAIL`, and whether over-commitment is reserved against
   or accepted.** Default: the job waits until `until=` when inside its deadline, else its
   default action; reservations (rule 6), not a 429 discovered later.
5. **Where the 5-hour window starts.** Default: rolling over the ledger until a first run's
   `headers.txt` shows a header carrying remaining budget, one per window.
6. **Whether the local tier is ever a door for a clocked job.** Default: `choose` never
   returns it (rule 8); a `--no-clock` shape nova-swarm never passes is the only design that would.
7. **What marks a line's own harness** for rule 21. Default: `harness` in `claude`, `codex`,
   or a `worker_dir` holding `CLAUDE.md`, `AGENTS.md`, `MEMORY.md` or `.claude/`.
8. **The constants a stranger cannot check**: 30 days stale, `bytes/3`, four bake-off doors
   at once, `120s`, `10m`, 256 KB, 5 s, 20 per listing, `zdr_until` empty until the page's
   sentence is quoted. Default: as written.
9. **The order.** Default: the SPEC-SWARM amendment is its own PR, merged before
   `cmd/nova-go` is built; SPEC-SECRETS's row and SPEC-TOKENS's source rule ride with it;
   PR #124 decides the record key and this file follows.
