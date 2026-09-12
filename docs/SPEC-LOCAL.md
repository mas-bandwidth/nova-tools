# nova-local — specification

**What this tool is for, in one sentence (Glenn, 2026-09-12):** *"somebody should
be able to grab nova-local and run local models."*

Everything below serves that sentence or is cut. Glenn cut it twice more the same
night — *"I want to not overengineer this. I think it should just run the model."*,
*"no deps."*, *"if we want to eval, it is another tool"* — and the draft that went
to the table with seven verbs and a trust ladder was held by two cold reads for
being the tool those sentences ruled out. This revision is three verbs.

The gate before the tool is called done is the **new-user hour**: a fresh AI line,
given `README.md` and nothing else, on a box with ollama installed and running,
pastes the six-line first run and ends with a model serving and a worker
description `nova-swarm` accepts. That is demanded test 1.

`nova-local` is one binary. It does not run inference, it does not fetch weights,
and it does not judge models. It makes a local engine usable: what is answering,
what is on disk and what it costs in memory; one model served at a context you
chose; and a worker description a swarm takes.

This spec is normative. If the code and this document disagree, one of them has a
bug, and the tests decide which. It stands beside [SPEC.md](../SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line output
grammar, the cap-and-count rule, `internal/oneline` and `internal/bounded` —
applies here unchanged and is not restated.

**Dates.** Every date in this document is **UTC**. The night Glenn's rulings were
given is 2026-09-11 evening EDT, which is 2026-09-12 UTC, and both the rulings and
the measurements below carry the UTC date.

**Source paths.** A `memory/…`, `standard/…` or `ds4_server.c` path below is
outside `nova-tools`; **Sources** at the end says where each one lives and which
are readable from this bench.

## DATA

| | |
|---|---|
| language | Go — `cmd/nova-local`, **standard library only** |
| kind | engine runner. Not an inference client, not a fetcher, not a judge, not an outbound actor |
| engines | **ollama** (many models, one daemon) · **ds4** (antirez's DwarfStar, one resident model per process). Both are adapters; see **the engines** |
| pin | **loopback only.** The base URL is `--base`, defaulting to the adapter's constant; a non-loopback host is exit 2 |
| reach | **box-level.** One engine per port started as a system service, one shared weight store, every account on the box reaching both (rule 15) |
| secrets | **none.** Opens no key file, exports no key, prints no credential. It `stat`s the key file the description names and never reads a byte of it |
| exit | `0` did it · `1` did it and said **NO** · `2` could not run |
| verbs | `status` · `serve` (with `--stop`) · `worker` — three, no others |

## The three verbs, and why these three

The sentence is *grab it and run local models*. Running a local model, end to end,
is three questions and no more: **what is here** (`status`), **serve one at a
context I chose** (`serve`), **hand it to the thing that will call it**
(`worker`). Each other verb the first draft had is named in **deliberately not in
this tool** with where the thing it did now lives.

`status` is a **report**: it asserts nothing and exits 0 whenever it ran, except
when no engine answers at all. `serve` and `worker` are **walls**: each can exit 1.

## The rules, numbered

Every rule here is normative and has one line in **tests this spec demands**.

1. **No dependencies** (Glenn, 2026-09-12: *"no deps"*). One static Go binary from
   the standard library alone: `net/http` to the engines' HTTP APIs,
   `crypto/sha256` where a digest is computed, `os/exec` for the ds4 process. No
   third-party module appears in its `go.mod` graph. **What the stranger installs
   is not this binary's dependency and the README says so**: the description
   `worker` writes is read by `nova-swarm`, which runs a harness (OpenCode) that
   `nova-swarm` itself points at the local engine, by writing the provider block
   into the slot at every start (`internal/swarm/worker.go:219-243`). That install
   is the stranger's, named in `README.md`, and it is not a Go dependency.

2. **An engine is an adapter, and adding one changes nothing else** (Glenn:
   *"the requirement that we can try the antirez stuff and others"*). An engine is
   anything with a start command, a base URL, a health path and an
   OpenAI-compatible `/v1` endpoint. An adapter declares six things: its name, its
   default base URL, its health path, how it lists models, how it serves one at a
   given context, and how it stops one. `ollama` and `ds4` are the two adapters
   here. **`llama.cpp`'s server and `mlx-lm` are named as the next two and are not
   built**; adding either must be one file and no change to a verb.

3. **Loopback only, and the port is the operator's.** Every verb takes
   `--base <url>`, defaulting to the adapter's constant
   (`http://127.0.0.1:11434/v1` for ollama, `http://127.0.0.1:8000/v1` for ds4,
   which is `ds4_server`'s own default port, `ds4_server.c:14043`). A `--base`
   whose host is not a loopback address is exit 2, naming the host. A port may
   move because a person moved it; a host may not, because a local tier that can
   be pointed at a remote endpoint is not a local tier. The default is printed on
   every `LOCAL ENGINE` line, so what was used is never a guess a reader makes.

4. **A model is named by `(engine, reference, digest)`, and the digest is printed,
   never ruled on.** Every line that names a model carries the digest the engine
   reports for it, because the bytes under a name are the only provenance this
   tool can see and a tag is not a promise (`gemma4:12b` and `gemma4-32k:latest`
   are different digests on this box today). `serve --expect-digest <sha256:…>`
   is optional; when given and different, `serve` refuses at exit 1 naming both
   digests. **There is no lockfile, no quarantine and no trust state in this
   tool**: Glenn cut them the same night the tool was asked for, and the ladder
   they served — a soak, a promotion, an audit of what production may call —
   belongs with the evaluation tool that would have the evidence (see
   **deliberately not in this tool**). The practice this line comes from
   (**Sources**) is that bench's, and a stranger who grabs this binary is not
   under it.

5. **`--num-ctx` has no default.** ollama's context default is small — **reported**
   as a silent cap at 4,096 tokens whatever the model supports
   (`standard/MODELS.md` line 100, Rowan's self repo, 2026-09-07: *"ollama
   silently caps context at 4,096 tokens … check `num_ctx` before the next
   eval"*) — so a long prompt can return a confident answer about a truncated
   input with no error. `serve` without `--num-ctx` is exit 2 and `refusing to
   guess`. `status` never asserts a default: it prints the `context_length`
   `/api/ps` reports for a loaded model and prints nothing for a model that is not
   loaded.

6. **`serve` makes the served thing, and prints the name the harness must use.**
   ollama persists per-model options only through a Modelfile, and a caller
   reaching it through OpenAI-compatible `/v1` sends no `num_ctx`, so a model
   "configured" in place reloads at the daemon's default on the next request. So
   `serve --engine ollama` **creates a derived tag** `<name>-<ctx>k` from the given
   reference with `num_ctx`, `temperature 0` and the given `seed` baked in, reads
   `/api/show` back to confirm them, and prints `serve_as=<the derived tag>`.
   `<name>` is the reference with its `:<tag>` stripped and any registry prefix
   dropped — `gemma4:12b` derives `gemma4-32k`, the name already on this box — so
   two references sharing a name (`gemma4:12b` and a future `gemma4:27b`) derive
   the same tag, and that collision is caught by the parent comparison below, not
   by the name. `<ctx>k` is `--num-ctx` divided by 1024; a `--num-ctx` that is not
   a multiple of 1024 is exit 2 naming the two nearest multiples, because a tag
   whose name rounds is a tag that lies about the context it holds. That is the
   practice already on this box: `gemma4-32k:latest` and `qwen3.6-32k:latest`
   sit beside `gemma4:12b` and `qwen3.6:35b-a3b` (`/api/tags`, 2026-09-12), and
   `/api/ps` shows `qwen3.6-32k:latest` loaded at `context_length 32768`. A
   derived tag that already exists is compared on exactly four things — the parent
   (`/api/show`'s `details`/`FROM`), `num_ctx`, `temperature` and `seed` — and used
   as it stands when all four match (`serve` is idempotent); any of the four
   different is exit 1 naming both values and the remedy (`ollama rm <tag>`). Only
   those four are compared, because `/api/show` also reports fields the parent
   passed down that `nova-local` never set — on this box `gemma4-32k:latest`
   reports `top_k 64` and `top_p 0.95`, inherited — and a tool that compared those
   would refuse its own tag. On ds4 there is no derived tag:
   the context is `--ctx` on the process and `serve_as` is the id the process
   advertises.

7. **`keep_alive` is on, and load time is measured here, once, by the thing that
   caused it.** After creating the tag, `serve` sends one warm-up load —
   `/api/generate` with an empty prompt and `keep_alive` — and times it with its
   own wall clock; that number is `load=` on `SERVE OK`. Nothing else in this tool
   reports a duration: `/api/ps` reports none, so `status` prints no `load=` and no
   `gen=`, and generate time is the caller's number (`nova-swarm`'s `RUN DONE`
   line). The reason the two are apart at all: Gemma 4 12B's first `nova-swarm`
   task took **2m48s wall clock** (nova-tools issue #75, 2026-09-12: *"Gemma 4 12B
   ran one task through it tonight, 2m48s, clean"*); **how much of it was load is
   not in that record**, which is exactly why a tool that reports one number
   cannot tell a cold start from a slow model.

8. **READY is a measurement, printed — and a refusal only against a number the
   operator gave.** `serve` reads the 1-minute load average, free memory and the
   engines already answering **before** it starts anything, and prints them on
   every `SERVE OK` and every refusal. It has **no threshold of its own**: Glenn,
   2026-09-12 (**Sources**), on saturating this machine with local models, *"Have fun!!! a lock is probably not needed"*. `--max-load
   <n>` and `--min-free <size>` are optional; given, and exceeded, the serve is
   exit 1 naming what was measured and what was asked. Not given, the numbers are
   printed and nothing is refused. The two flags stay beside a lifted lock for the
   one caller the lock was lifted for: an operator deliberately saturating the box
   who wants *this* serve to stand off rather than join in.

9. **One local worker at a time.** An engine is a queue: a second concurrent
   worker against one engine interleaves two queues and makes both slower while
   the pool reports two running. `worker` prints `workers=1`, and the operator
   passes `--workers 1`. (`nova-swarm`'s decoder is strict and has no field for
   it; a `max_workers` field is proposed as owed work below, not invented here.)

10. **The memory is read at the moment of the call, and printed — not judged.**
    `status` prints, on every model line, the weights the engine reports and, once
    on `LOCAL OK`, `mem_free`, `mem_total` and the live `wired_cap`. It prints no
    verdict: *fits* was three words over two numbers, and the two numbers are what
    a person needs. Measured 2026-09-12: with Qwen 3.6 35B (23 GB resident,
    `/api/ps`) reading on the GPU the Studio sat at 93% CPU idle, 173 GB used of
    512 and 338 GB free — mid-size models run beside normal work — and Glenn's line
    for the top end is *"the hardcore 500gb models would only work if that's all
    this studio did; maybe a few hundred or 200gb"* beside other work. A registry's
    tier bucket is not this box either. The memory is never cached between calls
    and never a total standing in for what is free.

11. **Triage and report; never decide-and-act; never the safety arbiter.** What a
    local model returns is a hint, treated as untrusted data; on uncertainty,
    timeout or error the caller escalates. No verb here merges, sends, deletes or
    rules on safety, and this tool writes exactly one file: the `--out` path
    `worker` was given. **The worker's conditions are not written here.**
    `nova-swarm` owns them (`SPEC-SWARM.md` rules 1–5, baked into `nova-swarm
    template`), and two writers of one text drift. The doctrine sentence is not
    enforceable by
    code; what is enforceable, and is tested, is the verb set and the one written
    file.

12. **Every model has a caller or it leaves.** The behaviour is two printed fields
    and one flag: every `LOCAL MODEL` line carries `weights=` and `loaded=`, and
    `serve --stop` is the act — a model resident for nobody is memory the box is
    not using for work, and an engine holding 23 GB for nothing is the visible
    form of that.

13. **Every listing is bounded.** Counts by default, a list only behind `--list`
    and capped by `--max` (default 20, `0` for all), and one `MORE` line carrying
    the remedy. A degenerate state prints one remedy line, never the state.

14. **Content on stdin, never in argv; no key, ever.** No prompt, no task text and
    no model output reaches a command line, a log or a printed line. This binary
    has no shell, never executes anything a model returned, and **never opens the
    key file** a worker description names: it `stat`s it, refuses an empty one with
    the command that writes it, and that is the whole of its contact with a secret.

15. **An engine is a box service and the weights are one store** (Glenn,
    2026-09-12: *"I would like for local models to be accessible both here in this
    admin account, and in your rowan account."* / *"This is a requirement for this
    local setup and nova-local."*). An engine is started by a **LaunchDaemon** on
    macOS — `/Library/LaunchDaemons`, loaded at boot, **not** a per-user
    `LaunchAgent` — or a **systemd system unit** on linux, bound to loopback, so
    every account on the box reaches the same daemon at `127.0.0.1:<port>` and no
    account starts one of its own. The weights live in **one store on the box**: a
    directory every model-running account can read, named by the box recipe
    (below), with `OLLAMA_MODELS` pointing the ollama service at it and ds4's `-m`
    paths under it. **Never a copy per user** — a second copy is the same weights
    bought twice on a box where one model is 434 GiB. `nova-local` starts no
    daemon and installs no unit; that is the box recipe's job.

    The store has a name, not a shape. On **darwin** it is
    **`/Users/Shared/nova-local/`**, one subdirectory per engine —
    `models/ollama`, `models/ds4` — group `staff`, readable by every account that
    runs models and written by the engine's own service account. On **linux** it
    is **`/var/lib/nova-local/`** with the same layout, owned by a `nova-local`
    service user and the same group rule. A **home directory can never be the
    store**: its mode is its owner's to change and macOS's own default for a new
    account is `700`. The only reason either account reads the other's today is
    that both homes happen to be `750 <user>:staff`, and one `chmod 700`, or one
    account outside `staff`, ends it silently — which is not a thing a requirement
    for *both accounts* can rest on.

    The **one-time move is the box recipe's**, in this order and no other: copy
    the store to the path above; switch the engine's own setting to it
    (`OLLAMA_MODELS` for ollama, the `-m` paths for ds4); **verify a model serves
    from the new path** — `nova-local status` from each account showing the same
    `store=`, then one `serve` and one task; and only then remove the old copy.
    Copy before switch and verify before delete, because the thing being moved is
    528 GB nobody wants to download twice.

    This rule is what the tool **reports and refuses against**: `status` prints the
    same engines, the same models and the same `store=` whichever account runs it, and
    notes an engine whose store is inside a user home; `serve` on an adapter that
    starts a process is exit 1 when something already listens on the port, naming
    the holder — `held_by=<user>`, read from the listening socket's owner, and
    `held_by=unknown (<why>)` when it cannot be read, never a guessed name.

## The engines

| | **ollama** | **ds4** |
|---|---|---|
| what it is | a daemon serving many installed models | antirez's DwarfStar: a checkout built by `make`, one process per resident model |
| default base | `http://127.0.0.1:11434/v1` | `http://127.0.0.1:8000/v1` (`ds4_server.c:14043`, `.port = 8000`) |
| health | `GET /api/tags` on the daemon root | `GET /v1/models` |
| a model is | a tag, `<name>:<tag>` | a **GGUF file** on disk |
| arrives by | the stranger's own `ollama pull <tag>` — **this tool fetches nothing** | the stranger's own download of the GGUF — antirez converts and publishes the frontier-local weights himself, so they are in no catalogue |
| loaded | several, as memory allows | **exactly one**, for the life of the process |
| `serve` | create `<name>-<ctx>k` with `num_ctx`, `temperature 0`, `seed`; read `/api/show` back; one warm-up load with `keep_alive` | start `ds4_server -m <gguf> --ctx <n> --host 127.0.0.1 --port <port of --base>` (`--host` `ds4_server.c:14130`, `--port` `:14132`, `-c/--ctx` `:14122`) |
| `serve --stop` | one request with `keep_alive: 0` — the model unloads; the derived tag stays on disk | end the process this tool started (`pid` from `SERVE OK`) |
| determinism | baked into the derived tag (`temperature 0`, `--seed`) | **per request**, not per process: `serve --seed` on ds4 is exit 2 naming this row, because this tool sends no inference requests |
| harness id | provider `ollama`, model `<the derived tag>` — what `serve_as=` printed. OpenCode's own `--model` is `<provider>/<model>`, so the harness argv carries `ollama/{model}`; the description's `model` stays the bare served id, which is how `nova-swarm` lists it under the provider (`worker.go:229`) | unknown, listed below |

**Measured on this bench, carried with their dates and never assumed again.**
512 GB unified memory. DeepSeek V4 Flash 2-bit on ds4: 81 GB resident, ~42
tokens/s (2026-09-03, **Sources**) — from weights that had sat on disk three weeks
waiting for an engine. PRO 2-bit: 434.5 GiB at 32K context, 13.8 tokens/s
(2026-09-06, same file), and only under
`iogpu.wired_limit_mb=480000`, which is **per boot** and was raised by hand;
`status` reads the live cap and never assumes the raised value. On ollama on this
box, `GET /api/tags` at 2026-09-12, **six tags**, as it prints them:

```
qwen3.6-32k:latest   dc1d37d39ad9   23.9 GB
gemma4-32k:latest    e4dfa7a2c19d    7.6 GB
qwen3.6:35b-a3b      07d35212591f   23.9 GB
gemma4:12b           4eb23ef187e2    7.6 GB
qwen2.5vl:7b         5ced39dfa4ba    6.0 GB
qwen2.5-coder:32b    b92d6a0bd47e   19.9 GB
```

The `-32k` pair are derived tags made by hand on 2026-09-12 UTC (`/api/tags`
`modified_at` `2026-09-11T21:02:12-04:00`) with `PARAMETER
num_ctx 32768`; rule 6 is that practice made a verb. (The four-name list in issue
#75's body is a sentence a person wrote, not a measurement, and it spells the
vision tag `qwen2.5-vl:7b`, which is not what the daemon answers with.)

**The two-store state rule 15 ends, measured 2026-09-12.** ollama runs on this box
as a **user process under `glenn`** (`/opt/homebrew/opt/ollama/bin/ollama serve`,
pid 56334) on 11434, with its weights in that one account's `~/.ollama`: 53 GB on
disk for the six tags above, which sum to 88.9 GB of tag sizes because the derived
tags share their parents' blobs. The ds4 checkout and its weights are **528 GB
under `/Users/rowan/rowan-working/ds4`**, owned by the other account and reachable
from this one only because that directory happens to be world-readable; no ds4
server is running. Two accounts, two stores — `~/.ollama` under one, 528 GB under
the other, shared only by a directory mode — is the state rule 15 ends, and
neither engine is a LaunchDaemon today.

**What could not be verified from the record, and is read off the source before
the ds4 adapter is written**, never recalled (the `ds4_server.c` line numbers in
this section are carried from the record and are not readable from this bench —
see **Sources**):

1. whether any endpoint reports load progress, so `state=loading` would be
   inferred from a live process with no successful completion yet and labelled as
   inferred;
2. whether the process has a shutdown path of its own, or is ended through its
   `com.rowan.ds4` launch agent (the plist is not readable from every bench user,
   so this is read on the box that owns it);
3. the accepted `reasoning_effort` names, parsed at `ds4_server.c:1003`, which
   this spec does not name;
4. **the harness id ds4 answers to** — the provider and model spelling an
   OpenAI-compatible client must send — which `worker` needs and cannot guess.

The port and bind-host flags were the other two unknowns and are now closed from
the source: `--host` at `ds4_server.c:14130`, `--port` at `:14132`, default port
8000 at `:14043`, which is the `--base` default in the table.

## The verbs

```
nova-local status [--engine <name>] [--base <url>] [--list] [--max <n>] [--timeout <s>]

nova-local serve  --engine <name> --model <ref> --num-ctx <n> [--base <url>]
                  [--keep-alive <d>] [--seed <n>] [--expect-digest <sha256:…>]
                  [--max-load <f>] [--min-free <size>]
nova-local serve  --stop --engine <name> --model <ref> [--base <url>]

nova-local worker --engine <name> --model <ref> --out <file> --name <text>
                  --harness <cmd> --harness-args <a,b,{model},…> --worker-dir <abs dir>
                  --key-file <file> --env-var <NAME> --usage <opencode|none>
                  --deadline <duration> [--base <url>] [--board <owner/repo#n>]
                  [--provider <id>]
```

**No guessed anything.** No default engine, no default context, no default model,
no default output path, and on `worker` no default for any field the description
carries. A missing one is exit 2 and `refusing to guess`. The one place a default
is allowed is `--base`, whose value is the adapter's own published constant and is
printed on every line that uses it, plus `--timeout` (2 s), `--max` (20) and
`--keep-alive` (`30m`), which are this tool's own bounds and are printed too —
`keep_alive=` on `SERVE OK` is what was sent, given or defaulted. `--provider
<id>` is required exactly where the adapter publishes no provider id of its own
(ds4) and is exit 2 on an adapter that publishes one (ollama), because two answers
to one question is the drift this verb exists to stop. There is no `--force` on any verb:
every refusal names a different command as its remedy. The binary is `nova-local`,
and that is its only name.

**`status`** answers: is anything answering, what is loaded, at what context, what
is on disk, and what does it cost in memory. Run with no engine answering it
prints the quickstart notes — the first-run guidance is folded into the verb a
stranger runs first rather than given a verb of its own.

```
LOCAL OK engines=2 answering=2 loaded=1 models=8 mem_used=173G mem_free=338G mem_total=512G wired_cap=480000 load1=1.8
LOCAL ENGINE ollama state=up base=http://127.0.0.1:11434/v1 loaded=1 advertised=6 store=/Users/Shared/nova-local/models/ollama
LOCAL ENGINE ds4 state=up base=http://127.0.0.1:8000/v1 advertised=2 resident=deepseek-v4-flash-q2.gguf ctx=32768
LOCAL MODEL engine=ollama model=qwen3.6-32k:latest digest=sha256:dc1d37d39ad9 weights=23.9G loaded=yes num_ctx=32768
LOCAL MODEL engine=ollama model=gemma4:12b digest=sha256:4eb23ef187e2 weights=7.6G loaded=no
LOCAL MORE kind=model shown=2 total=8 remedy="nova-local status --list --max 20"
```

It asserts nothing about a model and never says whether one *fits*: `weights=`,
`mem_free=` and `mem_total=` are on the page and the person reads them.
`advertised` is what the server answers to; `resident` is what was actually
loaded. They are different fields because ds4 once listed `deepseek-v4-flash` and
`deepseek-v4-pro` with only Flash loaded, and the pair was printed as resident
(2026-09-03, rowan-tools#60). `resident` comes from the process's own `-m`
argument; when nothing can say, it is `resident=unknown (<why>)` and never what a
table wishes were loaded. `state=up` is a **2xx on the adapter's health path
inside `--timeout`**, and nothing weaker: anything else that answered is
`state=down` carrying the status it gave, so another program on port 8000 is
`state=down status=404`, never `up`. An engine that does not answer inside
`--timeout` is `state=timeout` at exit 0; no engine answering at all is exit 1
with one remedy line.

**What `status` deliberately does not check:** whether a model is any good,
whether its weights are what its publisher shipped, whether anything may call it,
and whether the box *should* run it. Three of those are somebody's judgment and
the fourth is the evaluation tool's.

**`serve`** puts exactly one model in a state a caller can use, at a context you
chose, and prints the name that caller must use. It measures the box first and
prints what it measured (rule 8). On ollama it creates the derived tag (rule 6);
on ds4 it starts the process, and a **different** model than the one already
resident is exit 1 naming the resident model and the memory both would want —
replacing the resident model is a deliberate act, and the tool gives it one
command and one refusal rather than doing it quietly.

```
SERVE OK engine=ollama model=gemma4:12b serve_as=gemma4-32k digest=sha256:e4dfa7a2c19d num_ctx=32768 keep_alive=30m temperature=0 seed=7 load=41s mem_free=338G load1=1.8 engines=2
SERVE REFUSED engine=ollama model=gemma4:12b: load1=14.2 over --max-load 8.0, mem_free=61G under --min-free 96G — remedy: retry without --max-load/--min-free to serve anyway, or wait
```

**What `serve` deliberately does not check:** what will call the model, whether
the weights are trustworthy (rule 4: the digest is printed, and compared only
against a digest you supplied), and whether the box is busy enough to matter
unless you gave it a number.

`serve --stop` unloads (ollama) or ends the process this tool started (ds4). A
model already stopped is exit 0 and says so: stopping twice is not an error.

**`worker`** writes one `nova-swarm` worker description for one served local
model, and **every field it writes is either read from the engine or given as a
flag**. Nothing is invented, because `nova-swarm` decodes the file strictly
(`internal/swarm/worker.go:26-39,65`, `DisallowUnknownFields`) and refuses an
empty required field (`worker.go:88-120`).

| description field | where it comes from |
|---|---|
| `name` | `--name` |
| `provider` | the adapter's provider id (`ollama`), or `--provider` where an adapter has none published |
| `model` | the served id: `serve_as` for ollama, the advertised id for ds4 |
| `base_url` | `--base`, or the adapter's constant — the engine's `/v1` |
| `env_var` | `--env-var` (a NAME, never a value) |
| `key_file` | `--key-file`, `stat`ed and never opened |
| `usage` | `--usage`, exactly `opencode` or `none` |
| `harness` | `--harness` |
| `harness_args` | `--harness-args`, which **must contain `{model}`** |
| `worker_dir` | `--worker-dir`, which **must be absolute** |
| `deadline` | `--deadline`, a Go duration |
| `board` | `--board`, omitted when not given |

Three refusals are this verb's whole reason to exist beyond writing JSON, and each
repeats a hurt `nova-swarm` already paid: `--harness-args` without `{model}` is
exit 2 (a description that names a model and never places it in the harness's argv
launched a harness that was told nothing, under a green `RUN OK` — `worker.go:110-120`);
a `--worker-dir` that is relative, or that does not exist, is exit 2 (relative
worker dirs made every path the child was handed a path that did not exist —
`worker.go:68-78`; a missing one is a `WalkDir` error inside `nova-swarm`'s
`RefreshSlot` — `worker.go:157,166` — at launch, under a green `RUN OK`); a `--key-file`
that is missing or **zero bytes** is exit 2 with the command that writes it
(`ReadKey` refuses an empty key file — `key.go:64`), because a local engine needs
no key and `nova-swarm` needs a key file anyway, so the placeholder is named here
rather than learned from a refusal two tools later.

The description carries **no `temperature`, no `seed` and no `num_ctx`**: they are
not fields the decoder accepts, and they are not needed there because `serve` baked
them into the served thing (rule 6). It carries no conditions and no `AGENTS.md`
(rule 11). It prints `workers=1` (rule 9).

```
WORKER OK engine=ollama model=gemma4-32k out=/Users/x/workers/gemma.json workers=1 provider=ollama harness=opencode deadline=20m base=http://127.0.0.1:11434/v1
WORKER REFUSED gemma4-32k: --key-file /Users/x/local.key is empty; nova-swarm refuses an empty key file — remedy: printf 'local\n' > /Users/x/local.key && chmod 600 /Users/x/local.key
```

**What `worker` deliberately does not check:** that the harness is installed, that
the provider id matches the harness's config, or that `nova-swarm` will like the
file — the last is not a guess but a **test**: demanded test 1 runs
`nova-swarm`'s own loader over what this verb wrote.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: status printed, a model serving or stopped (or already stopped), a description written |
| 1 | the verb ran and said **NO**: no engine answering at all; a `--expect-digest` mismatch; a derived tag that exists with different parameters; a ds4 `serve` over a different resident model; a `serve` over a `--max-load` or under a `--min-free` the caller gave |
| 2 | could not run: a missing flag (`--num-ctx`, `--engine`, `--model`, any `worker` field), a non-loopback `--base`, an unknown engine name, a `--num-ctx` that is not a multiple of 1024, `--harness-args` without `{model}`, a `--worker-dir` that is relative or does not exist, a missing or empty `--key-file`, `--seed` on ds4, `--provider` on an adapter that publishes one, an unwritable `--out`, bad invocation |

`REFUSED` is printed at both codes, and what it names says which: a threshold, a
digest, a parameter of an existing tag or a resident model — things the caller can
retry differently — is exit 1, the verb ran and said **NO**; a flag the tool could
not use at all — an empty `--key-file`, a missing `--worker-dir`, `--harness-args`
without `{model}` — is exit 2, could not run. `FAIL` stays reserved for a verb that
ran and broke (`SPEC.md` **Conventions**), and no verb here prints both for one
problem.

A refusal says **what the input wants**, in one line, with the command that
satisfies it, and never prints the contents of a path it could not use. Every
independent problem is reported at once.

## Output grammar

```
LOCAL OK engines=<n> answering=<n> loaded=<n> models=<n> mem_used=<n> mem_free=<n> mem_total=<n> wired_cap=<n|unset> load1=<f>
LOCAL ENGINE <name> state=<up|down|timeout> base=<url> [loaded=<n>] [advertised=<n>] [resident=<file|unknown (<why>)>] [ctx=<n>] [store=<dir|unknown (<why>)>]
LOCAL MODEL engine=<e> model=<ref> digest=<sha256:…> weights=<n> loaded=<yes|no> [num_ctx=<n>]
LOCAL NOTE <text>
LOCAL FAIL <what>: <reason>
LOCAL MORE kind=<kind> shown=<n> total=<n> remedy=<command>
SERVE OK engine=<e> model=<ref> serve_as=<ref> digest=<sha256:…> num_ctx=<n> keep_alive=<d> temperature=<0|unset> seed=<n|unset> load=<t> mem_free=<n> load1=<f> engines=<n> [pid=<n>]
SERVE OK stopped engine=<e> model=<ref> [already stopped]
SERVE REFUSED <ref>: <reason> [held_by=<user|unknown (<why>)>] — remedy: <command>
WORKER OK engine=<e> model=<ref> out=<path> workers=1 provider=<p> harness=<cmd> deadline=<d> base=<url>
WORKER REFUSED <ref>: <reason> — remedy: <command>
```

`temperature=0` on a `SERVE OK` is ollama's, where rule 6 baked it into the tag;
on ds4 nothing here sets it and the field is `temperature=unset` (**the engines**,
determinism row). Example lines carry illustrative values except where a
measurement is cited; the
measured numbers of this bench are in **the engines**, with their dates.
`digest=` is `sha256:` followed by the hex the engine reported, whole; the
examples above truncate it to fit the page. `OK` lines go to stdout; `FAIL` and
`REFUSED` lines go to stderr. An event is
exactly one line, and nothing a caller supplies can add a second: every value goes
through `internal/oneline`.

## First run

Six lines, pasted in order, nothing edited but the paths.

```
$ ollama pull gemma4:12b
$ nova-local status
$ nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --keep-alive 30m --seed 7
$ mkdir -p $PWD/worker && printf 'local\n' > $PWD/local.key && chmod 600 $PWD/local.key
$ nova-local worker --engine ollama --model gemma4-32k --out $PWD/workers/gemma.json --name gemma --harness opencode --harness-args 'run,--model,ollama/{model},--,{prompt}' --worker-dir $PWD/worker --key-file $PWD/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m
$ nova-swarm quickstart --pool $PWD/pool
```

**Every fact these six lines assume, stated.**

- **Before line 1**, both binaries are on PATH. Nothing in the six lines installs
  them: `go install ./cmd/nova-local ./cmd/nova-swarm` from a clone of
  `nova-tools` (Go 1.26, `go.mod`) puts them in `$(go env GOPATH)/bin`, or a
  release binary is copied onto PATH. That, and ollama, is the whole of *grab
  nova-local*.
- **Line 1** is the stranger's own tool, not this one: `nova-local` fetches
  nothing (**the engines** table). It assumes the ollama daemon is **installed and
  running** (`ollama serve`, or the app), that `gemma4:12b` is a tag in ollama's
  library, and roughly 8 GB of download. On a box set up to rule 15 the daemon is
  the box's LaunchDaemon and this pull lands in the shared store, so it is done
  once for every account, not once per account.
- **Line 2** assumes nothing and creates nothing. With the daemon down it prints
  one remedy line and exits 1, which is the first thing a stranger needs to know.
- **Line 3** creates the derived tag `gemma4-32k` (rule 6) and loads it once. It
  assumes `gemma4:12b` supports 32,768 tokens; it refuses nothing about the box,
  because no `--max-load` or `--min-free` was given (rule 8). `load=` on its
  output is the warm-up it just timed.
- **Line 4** makes the worker directory and writes the placeholder key file.
  `$PWD/worker` is the home copy `nova-swarm` refreshes into each slot
  (`worker.go:157`) and it may stay empty, but it must **exist**: `RefreshSlot`
  walks it and a missing one fails the launch under a green `RUN OK`. The first
  run creates it; `worker` refuses a `--worker-dir` that does not exist rather
  than writing a description that dies two tools later. The local engine wants no
  key; `nova-swarm` requires a non-empty key file for every worker (`key.go:64`),
  so one line of any text is the whole of it, and `nova-local` never opens it.
- **Line 5** names `gemma4-32k` — the `serve_as=` line 3 printed — and every field
  `nova-swarm`'s decoder requires, and **it is the acceptance**: what it writes is
  a description `swarm.LoadWorker` returns zero problems for and `swarm.ReadKey`
  accepts, which is a decode, not a run, and is demanded test 1(b). It creates
  `workers/` if it is missing, and writes exactly one file. `--harness-args`
  carries `ollama/{model}` and not `{model}`: `supervise.go:288` substitutes the
  description's bare `model` verbatim, and OpenCode's `--model` is
  `<provider>/<model>` — every description that has run on this bench spells it
  that way (`qwen3.6-32k.json`, `gemma4-32k.json`, `gemma.json` in
  `/Users/glenn/rowan-working/nova-swarm`, 2026-09-12).
- **Line 6** is `nova-swarm`'s, and it makes the pool. `quickstart` starts nothing
  and looks for no harness, so it is the last line that is true on a box with only
  ollama, and it prints the `nova-swarm run …` command as its own `QUICKSTART
  NOTE` — the stranger's **seventh** line, handed over by the tool rather than
  guessed. It assumes `nova-swarm` is installed, the same way line 1 assumes
  ollama.
- **Not in the six lines, and named in `README.md`**: `nova-swarm run` — the
  seventh line — refuses before any pool work when the harness the description
  names is not on PATH (`cmd/nova-swarm/main.go:451`, exit 2 naming it), so
  **OpenCode is a prerequisite of the seventh line, not of these six**. The six
  were written as an acceptance and the seventh was carrying it; the acceptance
  now sits on line 5, where nothing has to be installed for it to be true.

OpenCode needs no config of the stranger's either. `nova-swarm` writes the
provider block into the slot at every start, after refreshing it
(`internal/swarm/run.go:451,461` → `worker.go:219-243`), and this is the file the
runs on this bench actually used, whole:

```json
{
  "$schema": "https://opencode.ai/config.json",
  "provider": {
    "ollama": {
      "models": { "qwen3.6-32k": {} },
      "options": {
        "apiKey": "{env:OLLAMA_NOKEY}",
        "baseURL": "http://127.0.0.1:11434/v1"
      }
    }
  }
}
```

`/Users/glenn/rowan-working/nova-swarm/worker-qwen3.6-32k-1/opencode.json`,
2026-09-12 — the slot of the run in `run-local-3.log`. That block is what makes
`ollama/qwen3.6-32k` resolve, and it is written, not installed: **an earlier
revision of this spec required a custom provider named `ollama` in the stranger's
global `~/.config/opencode/opencode.json`, which is false** — that file on this box
declares only `inception`, and the slot file above overwrites whatever the worker
directory carried. Installing OpenCode is the stranger's; configuring it is not,
and neither is a dependency of this binary (rule 1).

The `README.md` `### First run` is these six lines with their output, plus the
seventh line and the one prerequisite it alone carries, executed by test 1.

## Deliberately not in this tool

Each cut names where the thing it did now lives.

- **`pull`** — `ollama pull <tag>` already exists and is one word; on ds4 the
  weights are a file antirez publishes and a person downloads. A fetcher here
  would re-implement two downloaders and make this binary the thing that brings
  the largest untrusted input on the bench onto disk. **Where it lives:** the
  engine's own tool, and the person's own download. The digest it would have
  checked lives on `status`'s and `SERVE OK`'s lines and in `--expect-digest`
  (rule 4).
- **`trust` / `untrust` and the lockfile** — a trust ladder is a ruling about what
  production may call, which is policy, and Glenn cut the policy the same night
  (*"I think it should just run the model"*). It also could not be built as
  drafted: `serve` gated on a lockfile it had no flag to read. **Where it lives:**
  with the evaluation tool, which would have the evidence a promotion needs, and
  with it the practice and the lesson that a gate and its writer belong in one
  binary — 22 days, 2026-08-11 to 2026-09-02, in which the tier could refuse a
  model and could not trust one (**Sources**).
- **`stop`** as its own verb — folded into `serve --stop`, which takes the same
  `--engine` and `--model` and is the same act reversed. Two verbs where one flag
  does.
- **`fit`**, and the `fits=beside|quiet|no` verdict — cut. **Where it lives:**
  `weights=` on each model line beside `mem_free=` and `mem_total=` on `LOCAL OK`
  (rule 10). The verdict needed a headroom nobody has measured and disagreed with
  itself at the boundary; two numbers do not.
- **`eval` and `compare`** — scoring a model against a reference read, and
  antirez's audit technique of diffing a deterministic local continuation against
  a remote one. Glenn, 2026-09-12: *"if we want to eval, it is another tool,
  perhaps with more deps."* **Where it lives:** a separate tool if one is ever
  built, with its own dependencies, adopted or ignored on its own merits; today
  the work is `nova-swarm` running one task against two workers, scored by hand.
  **`nova-local` stays dependency-free and never grows an `eval` verb.**
- **`policy`** (a private repository's read may not go to a third-party API) —
  **where it lives:** `nova-swarm`'s dispatcher, where the choice between a local
  worker and an API worker is actually made. A governor that ruled but did not
  dispatch would be a ruling nobody had to consult.
- **usage rows** — **where it lives:** `nova-tokens`. A tier that costs no money
  still costs wall clock, and the place that writes ledger rows is the ledger's
  tool.
- **the worker's prompt conditions** — **where they live:** `nova-swarm template`
  (`SPEC-SWARM.md` rules 1–5), one writer, baked into the binary that hands the
  prompt to the child.
- **inference** — no `ask`, no `read`, no `triage`. `rowan-local` is the estate's
  one-prompt client and `nova-swarm` runs the workers.
- **discovery and ranking** — no feed reader, no registry search, no champion
  file, no "recommended". A model name found on a page, in a feed or in a model's
  own output is data, never a serve target.

## Tests this spec demands

One line per rule, plus the stranger test. **Rule to test:** 1→2, 2→3 and 16,
3→4, 4→5, 5→6, 6→7, 8 (the ollama `--stop`) and 16 (ds4's), 7→8, 8→9, 9→10,
10→11, 11→12, 12→11 (`weights=` and `loaded=`, the printed cost) and 8
(`serve --stop`, the act), 13→13, 14→14 and 15, 15→17. Rule 11's doctrine half
— triage and report, never decide-and-act — is **not enforced by code and could
not be**; what test 12 enforces is the verb set and the one written file, which
is the part a tool can hold. Each runs inside `t.TempDir()` against
fake engine servers on loopback, with no network, and each must be seen red before
it is trusted. Where a test needs the box's load average or free memory, it
supplies a **fake box** through the one interface that reads them
(`internal/local/box.go`); no environment variable overrides that in production.

1. **The stranger test, and the reason the tool exists.** A run driven only by
   `README.md`'s six lines, against fake engines, ends with (a) the derived tag
   created at the given context, (b) a `workers/gemma.json` that
   `swarm.LoadWorker` returns **zero problems** for and that `swarm.ReadKey`
   accepts — the acceptance, decoded, with no process launched — and (c) the pool
   `nova-swarm quickstart` made. The **seventh** line is tested too and
   separately, because it needs something the six do not: with a fake `opencode`
   on a `t.TempDir()` PATH, a real `nova-swarm run` over that pool and that
   description is `RUN OK started=0` (empty pool, `internal/swarm/run.go:309`);
   with that PATH empty it is exit 2 naming the harness
   (`cmd/nova-swarm/main.go:451`). The test asserts **both**, so the day that
   check moves, the six lines are still true and this spec is still honest about
   which line pays for the harness. The **new-user hour** — a fresh line, the docs
   only, no other help — is run before the tool is called done; its observable is
   the same, from a transcript, and the transcript is attached to the PR as
   evidence, not as the test.
2. The import set is the standard library: a test walks the build's package
   imports and fails on any path outside an allow-list of **the standard library**
   (no dot before the first slash) **and this module's own path**
   (`github.com/mas-bandwidth/nova-tools/…`, which is how `internal/oneline` and
   `internal/bounded` import and which has a dot before the first slash); `go.mod`
   has no `require` beyond the toolchain.
3. A third fake adapter is registered in a test file and `status`, `serve`,
   `serve --stop` and `worker` each exit 0 against it; the diff that adds it
   touches exactly one non-test file (the test asserts the adapter registry is the
   only place the three verbs name an engine, by running them through the
   interface, not by reading the diff).
4. `--base http://10.0.0.5:11434/v1` is exit 2 on every verb, naming the host;
   `--base http://127.0.0.1:9999/v1` and `http://[::1]:9999/v1` are accepted and
   the port is used; the default base is printed on the `LOCAL ENGINE` line.
5. Every `LOCAL MODEL` and `SERVE OK` line carries `digest=` as the fake engine
   reported it; `serve --expect-digest` with a different value is exit 1 naming
   both digests and serving nothing; with the same value, exit 0. No verb reads or
   writes any lockfile: the tool's whole file output in a run is the `--out` path.
6. `serve` without `--num-ctx` is exit 2 and the line contains `refusing to
   guess`; `status` prints `num_ctx=` only for a model the fake `/api/ps` reports
   loaded, with the value that endpoint gave, and prints no `num_ctx` for the
   others.
7. `serve --engine ollama --model m:7b --num-ctx 32768 --seed 7` sends a create
   for `m-32k` — the `:7b` stripped, `32768/1024` — carrying `num_ctx 32768`,
   `temperature 0`, `seed 7`, reads `/api/show` back, and prints `serve_as=m-32k`;
   a second identical `serve` creates nothing and exits 0 (idempotent) **even
   though** the fake `/api/show` also reports `top_k` and `top_p` the tool never
   set; with the tag present at `num_ctx 4096`, or at a different parent,
   `temperature` or `seed`, it is exit 1 naming both values and the `ollama rm`
   remedy — including the collision case, `m2:7b` deriving the same `m-32k` over
   another parent; `--num-ctx 30000` is exit 2 naming 29696 and 30720; `serve
   --seed` on the ds4 adapter is exit 2 naming the determinism row.
8. `serve` sends exactly one warm-up request carrying `keep_alive`, and `load=` on
   its line is the wall clock the injected clock advanced across that request
   (fake engine sleeps 3 s, `load=3s`); `serve` with no `--keep-alive` sends `30m`
   and prints `keep_alive=30m`; `serve --stop` on ollama sends exactly one request
   carrying `keep_alive: 0` and nothing else, the derived tag is still listed
   afterwards, and a second `serve --stop` is exit 0 saying `already stopped`; no
   `status` line contains `load=` or `gen=`.
9. With a fake box at `load1=14.2, mem_free=61G`: `serve` with no threshold flags
   is exit 0 and its `SERVE OK` line carries `load1=14.2 mem_free=61G engines=<n>`;
   with `--max-load 8.0` it is exit 1 and the `SERVE REFUSED` line carries both
   the measured and the asked numbers; with `--max-load 20.0 --min-free 16G`, exit
   0.
10. `worker`'s line contains `workers=1`, and the written JSON contains no
    `max_workers`, `temperature`, `seed` or `num_ctx` key (the decoder rejects
    unknown fields; the test decodes with `DisallowUnknownFields` and asserts nil
    error).
11. `LOCAL OK` carries `mem_free=` and `mem_total=` from the fake box read **at
    the moment of the call**: two `status` calls against a box whose free memory
    changed between them print the two different numbers; every `LOCAL MODEL` line
    carries `weights=` as the fake `/api/tags` reported it and `loaded=` as the
    fake `/api/ps` reported it, which is the whole of rule 12's printed cost; no
    line in any verb's output contains `fits=`.
12. The verb set is exactly `status`, `serve`, `worker`: any other first argument
    is exit 2 naming the door, and a run of all three leaves exactly one new file
    on disk (the `--out` path) and no bytes under `$HOME`.
13. `status` over a fake engine advertising 400 models prints at most `--max`
    model lines plus one `MORE` line, and the whole stdout is under **4,096
    bytes**; with nothing answering it prints one remedy line, no model lines, and
    exits 1.
14. No value read from any file reaches an argv, a log or a printed line: the ds4
    child's argv holds the model path, the context, the host and the port and
    nothing else; the key file's contents (a recognisable sentinel string) appear
    in no output stream and no written file, and the process never opens it (the
    test makes it mode `0000`: `worker` still succeeds, since `stat` is enough);
    an empty key file is exit 2 carrying the `printf … && chmod 600` remedy; no
    child process argv this binary spawns begins with `sh`, `bash` or `zsh`.
15. `worker` refusals, each exit 2 with the field named: `--harness-args` without
    `{model}`; a relative `--worker-dir`; a `--worker-dir` that does not exist; a
    missing `--key-file`; a `--usage` that is neither `opencode` nor `none`; a
    `--deadline` that is not a Go duration; `--provider` given on ollama, which
    publishes one. All of them at once are reported in one run, not one per run.
16. A fake ds4 advertising two ids with one loaded gives `advertised=2
    resident=<the -m file>`; with no process it gives `resident=unknown (<why>)`
    and never a table's preference. `serve` against a ds4 already resident on a
    different model is exit 1 naming the resident model; `serve --stop` twice is
    exit 0 both times, the second saying `already stopped`.

17. **Two accounts, one engine, one store.** `status` is run twice against the
    same fake engine under two different `HOME`/`USER` pairs and the two stdouts
    are **byte-identical**, `store=` included; an engine whose store resolves
    inside either home — rather than under `/Users/Shared/nova-local` or
    `/var/lib/nova-local` — prints one `LOCAL NOTE` naming it and stays exit 0, because
    reporting it is the whole of this tool's part in the rule. `serve` on the
    process-starting adapter, against a port a fake holder is already listening
    on, is exit 1 carrying the port and `held_by=<user>`, and `held_by=unknown
    (<why>)` where the owner cannot be read — never a name the tool inferred from
    anything but the socket. No verb writes a LaunchDaemon plist, a systemd unit
    or anything under `/Library` (the test asserts the run's whole file output is
    still the `--out` path, as test 12 does).

Plus the house standard: a usage banner ending in a runnable `example:` block,
refusals reporting every independent problem at once, a `### First run` in
`README.md` pinned by an executing test, and three-GOOS builds green.

## The work list

Go under `cmd/nova-local`, the way `cmd/nova-bus` is built: standard library only,
no hardcoded paths, no default paths beyond the three printed ones, the exit
grammar above, `internal/oneline` for every printed value, `internal/bounded` for
every listing.

1. **`internal/local/engine.go`** — the adapter interface (name, base, health,
   list, serve, stop), the loopback pin, the `--timeout` budget, the registry, and
   the adapter's declared box store and port holder (rule 15). Tests 3, 4, 17.
2. **`internal/local/box.go`** — load average, free and total memory, the live
   wired cap, read at the moment of the call, behind one interface a test can
   fake. Tests 9, 11.
3. **`internal/local/ollama.go`** — `/api/tags`, `/api/ps`, `/api/show`, the
   derived tag (`/api/create`), the warm-up load with `keep_alive`, the unload.
   Tests 5, 6, 7, 8.
4. **`internal/local/ds4.go`** — the process with the model path, context, host
   and port; advertised versus resident; the four remaining unknowns **read off
   `ds4_server.c` and the live launch agent before this file is written**. Tests
   14, 16.
5. **`internal/local/worker.go`** — the description in `nova-swarm`'s exact schema,
   every field from a flag or the adapter, the three refusals, the key file
   `stat`ed and never opened. Tests 10, 14, 15.
6. **`cmd/nova-local/main.go`** — three verbs, the banner, the quickstart notes
   folded into `status`, refusals that name the next command. Tests 1, 12, 13.
7. **`README.md`'s `### First run`** — the six lines with their output, the
   seventh line, and the one sentence saying OpenCode is that line's prerequisite
   and is configured by `nova-swarm`, not by the stranger (rule 1). Test 1.

**Owed outside this tool.** A sentence for SPEC-SWARM so `workers=1` is carried by
the schema rather than by an operator: *"A worker description may declare
`max_workers: <n>`. `run --workers <n>` above it is a refusal, not a clamp: a
local engine is a queue, and a pool that reports six workers against one engine
reports a number that is not true."* And `llama.cpp`-server and `mlx-lm` adapters,
each one file, when somebody wants them.

## DECIDED IN THE DRAFT

The drafter's choices, not the record's, each with the commit that made it. Each
is still a place to push back; the argument for each is the rule it points at.

1. Three verbs — `status`, `serve`, `worker`; `stop` is `serve --stop`, and
   `pull`, `trust` and `untrust` go with the lockfile (419382e).
2. `worker` survives the cut and `stop` does not: on this bench a local model runs
   through `nova-swarm`, and a served model nothing can call is one step short
   (419382e).
3. The digest is printed and compared on request, never enforced —
   `--expect-digest` is one flag to delete (419382e).
4. `serve` creates a derived tag on ollama, the only mechanism that makes
   `num_ctx` hold for a `/v1` caller (419382e).
5. No thresholds of the tool's own; `--max-load` and `--min-free` exist for an
   operator who wants one (rule 8, 419382e).
6. No fit verdict — two numbers instead of three words (419382e).
7. `--base` defaults to the adapter's constant rather than being a constant only
   (419382e).
8. `status --timeout` defaults to 2 s, and a slow engine is `state=timeout` at
   exit 0 (808b08d).
9. The prompt conditions are not written here at all, not even quoted; `nova-swarm`
   is the one writer (419382e).
10. `llama.cpp` and `mlx-lm` are named and not built, which is the test of rule 2
    (808b08d).
11. No `--force` anywhere, and every refusal's remedy is a different command
    (808b08d).
12. Four ds4 unknowns stay unknowns; the port and bind-host flags are closed from
    `ds4_server.c` (419382e).
13. **The six lines end at `nova-swarm quickstart`, and `nova-swarm run` is the
    seventh**, because `run` refuses without the harness on PATH; the acceptance
    moves to line 5, where it is a decode and needs nothing installed (this
    revision).
14. The derived tag strips the reference's `:<tag>`, so the collision is caught by
    comparing the parent rather than by the name (rule 6, this revision).
15. **`nova-local` reports and refuses against rule 15 but does not install it**:
    the LaunchDaemon, the systemd unit and the shared store are the box recipe's,
    because a tool that writes under `/Library` is an outbound actor and this one
    writes exactly one file (rule 11). Glenn's requirement is a requirement on the
    setup; what the tool owes it is `store=`, the note, and the `held_by=` refusal
    (this revision).

## Sources

A `memory/…` path is in **Rowan's self repo** (`/Users/glenn/rowan-new`); a
`standard/…` path is in `/Users/glenn/rowan-working/standard`. Neither is in
`nova-tools`. The rules above carry the quote; the path is here.

- `memory/local-model-doctrine.md` — rules 8 and 11; `:105` is *"Have fun!!! a
  lock is probably not needed"*, 2026-09-12.
- `memory/the-machine-is-the-mandate.md` — rules 10 and 12; `:24` and `:43-44` are
  the ds4 Flash and PRO measurements quoted in **the engines**.
- `memory/model-trust.md` — rule 4 and the 2026-07-19 poisoned-model line ·
  `memory/a-guard-without-its-writer.md` — the 22-day guard ·
  `memory/ready-is-a-measurement.md` — rule 8 ·
  `memory/the-ranking-encodes-someone-elses-constraint.md` — rule 10 ·
  `memory/tool-output-costs-tokens.md` — rule 13.
- `standard/MODELS.md:100`, 2026-09-07 — rule 5's 4,096-token cap.
- nova-tools issue #75, 2026-09-12 — rule 7's 2m48s · rowan-tools#60, 2026-09-03 —
  `advertised` versus `resident`.
- `/Users/glenn/rowan-working/nova-swarm`, 2026-09-12 — the worker descriptions,
  the by-hand Modelfiles behind rule 6, and the slot `opencode.json` quoted in
  **first run**.
- `ds4_server.c` — antirez's DwarfStar checkout on the Studio, owned by another
  bench user and **not readable from this one**: every `ds4_server.c:<n>` in this
  document is carried from the record and is **unverified on this bench**. Work
  list 4 re-reads them on the box that owns the checkout and writes that path and
  its commit beside the first citation before the ds4 adapter is written.
