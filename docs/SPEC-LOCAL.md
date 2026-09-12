# nova-local — specification

**What this tool is for, in one sentence (Glenn, 2026-09-12):** *"somebody should
be able to grab nova-local and run local models."*

Everything below serves that sentence or is cut. Glenn cut it three more times the
same night — *"I want to not overengineer this. I think it should just run the
model."*, *"no deps."*, *"if we want to eval, it is another tool"* — and three cold
reads held the drafts that grew past those sentences. This revision is three verbs,
re-derived from the rules rather than patched.

`nova-local` is one binary. It does not run inference, fetch weights or judge models.
It makes a local engine usable: **what is here** (`status`), **one model served at a
context you chose** (`serve`), **a description the thing that will call it accepts**
(`worker`). `status` is a report and exits 0 whenever it ran, except when no engine
answers at all; `serve` and `worker` are walls. The gate before the tool is done is
the **new-user hour**: a fresh AI line, given `README.md` and nothing else, on a box
with ollama running and the box recipe applied, pastes the six lines and the seventh
the tool hands it, and ends with a model serving and a worker description
`nova-swarm` accepts. That is demanded test 1.

This spec is normative; if the code and this document disagree, one of them has a bug
and the tests decide which. It stands beside [SPEC.md](SPEC.md), whose
**Conventions** — exit codes, no guessed paths, the one-line grammar, cap-and-count,
`internal/oneline`, `internal/bounded` — apply unchanged and are not restated. The
**box's** side of rule 15 (the daemon, the store's owner, the one-time move) is
[BOX-LOCAL.md](BOX-LOCAL.md), which `nova-line`'s recipe will consume. Dates are
**UTC** (the rulings were 2026-09-11 evening EDT = 2026-09-12 UTC) and every number
carries its date and source; this box's measured *state* is dated in BOX-LOCAL.md,
because state moves and a rule does not.

## DATA

| | |
|---|---|
| language | Go — `cmd/nova-local`, **standard library only** |
| kind | engine runner. Not an inference client, not a fetcher, not a judge, not an outbound actor |
| engines | **ollama** (many models, one daemon) · **ds4** (antirez's DwarfStar, one resident model per process). Both adapters |
| pin | **loopback only.** `--base` defaults to the adapter's constant; a non-loopback host is exit 2 |
| reach | **box-level.** One engine per port, one shared weight store, every account on the box reaching both (rule 15) |
| secrets | **none.** Opens no key file, exports no key, prints no credential. It `stat`s the key file a description names |
| exit | `0` did it · `1` did it and said **NO** · `2` could not run |
| verbs | `status` · `serve` (with `--stop`) · `worker` — three, no others |

## The rules, numbered

Every rule is normative and has one line in **tests this spec demands**.

1. **No dependencies** (Glenn: *"no deps"*). One Go binary from the standard library
   alone — `net/http`, `crypto/sha256`, `os/exec` for the ds4 process — with no
   third-party module in its `go.mod` graph and **no shelling out to learn what the
   standard library cannot read**. What the stranger installs is not this binary's
   dependency: `nova-swarm` reads the description `worker` writes, runs the harness and
   points it at the local engine itself (`worker.go:219-243`).

2. **An engine is an adapter, and adding one changes nothing else.** An adapter declares
   six things: name, default base URL, health path, how it lists models, how it serves
   one at a given context, how it stops one. `llama.cpp`'s server and `mlx-lm` are named
   and **not built**; adding either must be one file and no change to a verb.

3. **Loopback only, and the port is the operator's.** A `--base` whose host is not a
   loopback address is exit 2 naming the host: a port moves because a person moved it, a
   host may not, because a local tier that can be pointed at a remote endpoint is not a
   local tier. The default is printed on every `LOCAL ENGINE` line.

4. **A model is named by `(engine, reference, digest)`, and the digest is printed, never
   ruled on** — the bytes under a name are the only provenance this tool can see
   (`gemma4:12b` and `gemma4-32k:latest` differ here, 2026-09-12). `--expect-digest` is
   optional and, differing, is exit 1 naming both; an engine reporting none prints
   `digest=none (<e> reports none)`, because this tool will not hash a 434 GiB GGUF to
   invent one. **No lockfile, no quarantine, no trust state**: Glenn cut them, and the
   ladder they served belongs with the evaluation tool that would have the evidence.

5. **`--num-ctx` has no default.** ollama's is small — reported as a silent cap at 4,096
   tokens whatever the model supports (`standard/MODELS.md:100`, 2026-09-07) — so a long
   prompt comes back as a confident answer about a truncated input with no error. `serve`
   without it is exit 2 and `refusing to guess`; `status` asserts no default, printing
   as `num_ctx=` the `context_length` `/api/ps` reports for a loaded model (ds4's is its
   `--ctx`) and nothing otherwise — one thing, one spelling, on every line.

6. **`serve` makes the served thing and prints the name the harness must use.** ollama
   persists per-model options only through a Modelfile and a `/v1` caller sends no
   `num_ctx`, so a model "configured" in place reloads at the daemon's default. `serve
   --engine ollama` therefore **creates a derived tag** `<name>-<ctx>k` with `num_ctx`,
   `temperature 0` and the given `seed` baked in, reads `/api/show` back, and prints
   `serve_as=`. `<name>` is the reference with `:<tag>` and any registry prefix stripped
   (`gemma4:12b` → `gemma4-32k`), so a shared name derives one tag and the collision is
   caught by the parent, not the name; `<ctx>k` is `--num-ctx / 1024`, and a remainder is
   exit 2 naming the two nearest multiples, because a tag whose name rounds lies about
   the context it holds. An existing tag is compared on exactly four things — parent
   (`/api/show`'s `details`/`FROM`), `num_ctx`, `temperature`, `seed` — used as it stands
   when all four match, any one different exit 1 naming both values with `ollama rm
   <tag>`. Only those four, because `/api/show` also reports inherited fields this tool
   never set (`top_k`, `top_p`) and a tool comparing those would refuse its own tag. ds4 has no derived tag: the context is `--ctx` on the process and `serve_as`
   is the id it advertises.

7. **`keep_alive` is on, and load time is measured here, once, by the thing that caused
   it.** `serve` sends one warm-up `/api/generate` with an empty prompt and `keep_alive`,
   timed on its own wall clock; that is `load=`. Nothing else here reports a duration —
   `/api/ps` reports none, so `status` prints no `load=` and no `gen=`. Gemma 4 12B's
   first `nova-swarm` task took **2m48s wall clock** (issue #75, 2026-09-12) and how much
   was load **is not in that record**: one number cannot tell a cold start from a slow
   model.

8. **READY is a measurement, printed — and a refusal only against a value the operator
   gave.** `serve` reads the 1-minute load average, free memory and the engines already
   answering **before** starting anything, and prints them on every `SERVE OK` and
   refusal. It has **no threshold of its own** (Glenn, 2026-09-09: *"Have fun!!! a lock
   is probably not needed"*); `--max-load` and `--min-free` are optional and, exceeded,
   are exit 1 naming measured and asked.

9. **One local worker at a time.** An engine is a queue: a second concurrent worker
   interleaves two queues and makes both slower while the pool reports two running.
   `worker` prints `workers=1` and the operator passes `--workers 1`. (`nova-swarm`'s
   decoder has no field for it; `max_workers` is owed work, filed as an issue.)

10. **The memory is read at the moment of the call, and printed — not judged.** `status`
    prints the weights the engine reports per model and, once on `LOCAL OK`, `mem_free`,
    `mem_total` and the live `wired_cap`. No verdict: *fits* was three words over two
    numbers. The memory is never cached between calls.

11. **Triage and report; never decide-and-act; never the safety arbiter.** What a local
    model returns is a hint, untrusted data. No verb merges, sends, deletes or rules on
    safety, and this tool writes exactly one file: the `--out` path. **The prompt
    conditions are not written here** — `nova-swarm` owns them (`SPEC-SWARM.md` rules
    1–5) and two writers of one text drift. The doctrine sentence is not enforceable by
    code; the verb set and the one written file are, and are tested.

12. **Every model has a caller or it leaves.** Two printed fields and one flag: every
    `LOCAL MODEL` line carries `weights=` and `loaded=`, and `serve --stop` is the act.

13. **Every listing is bounded.** Counts by default, a list behind `--list`, capped by
    `--max` (default 20, `0` for all), one `MORE` line carrying the remedy. A degenerate
    state prints one remedy line, never the state.

14. **Content on stdin, never in argv; no key, ever.** No prompt, task text or model
    output reaches a command line, a log or a printed line. This binary has no shell,
    never executes what a model returned, and **never opens the key file** a description
    names: it `stat`s it and refuses an empty one with the command that writes it.

15. **The weights are one store on the box, and this tool reads it, reports it and
    refuses a store outside the shared root (`shared=no`) when asked** (Glenn, bus,
    2026-09-12, no receipt in `memory/` yet: *"I would like
    for local models to be accessible both here in this admin account, and in your rowan
    account. This is a requirement for this local setup and nova-local."* / *"I'd really
    not like to the local models to stay in rowans account, but move to a shared location
    explicitly."*). The store is **`/Users/Shared/nova-local/models/<engine>`** on darwin
    and **`/var/lib/nova-local/models/<engine>`** on linux; `nova-local` reads and serves
    from whatever store the engine reports and **installs nothing** — on a box more than
    one account uses, that store is the path above, and the daemon, the directory's owner
    and mode and the one-time move that make it so are the box recipe's,
    [BOX-LOCAL.md](BOX-LOCAL.md).

    **It reads** only what an engine reports, through what a non-root process can read
    with the standard library. **ollama**: `/api/tags` for the model list and
    `/api/show`'s modelfile for each model's blob path (`FROM <store>/blobs/sha256-…`);
    `store=` is that path's `blobs` parent **resolved through symlinks, every component**,
    as ds4's `-m` path already is — the recipe makes the service user's
    `~/.ollama/models` a symlink into the shared store, so an unresolved `FROM` reports a
    home for a store that is not one — and the resolved path is both what `store=` prints
    and what the shared-root test below reads. **A component that does not exist ends
    resolution**: the longest existing prefix is resolved and the rest is appended as
    spelled, which is what the shared-root test then reads; resolution that fails for any
    other reason is `store=unknown (<why>)` and no verdict (below). No tag means
    `store=unknown (no tag to show)`. **ds4**: the `-m <gguf>` path from the process's own arguments, **only when
    the process is this user's** — another user's is `store=unknown (process is uid
    <n>'s)`, because another uid's argv is root's to read on darwin and this binary does
    not exec `ps` or `lsof` to get around it (rule 1).

    **It reports, always**: `store=` and `shared=<yes|no (<why>)|unknown>` on every `LOCAL
    ENGINE` line and on the serving `SERVE OK` line — not on `SERVE OK stopped`, which
    names only what it stopped — read at the moment of the call and never judged — the
    same kind of fact as `load1=` (rules 8 and 10) — and a store it could not read is
    `unknown`, not a verdict, because a tool does not rule on a fact it does not have.
    `shared=yes` is exactly a resolved `store=` **under this box's shared root** — the
    two paths above, matched a whole component at a time, never as a spelling like
    `/Users/Shared/nova-local…`, which also matches a `nova-local-scratch` beside it —
    and **every** other resolved store is `shared=no` carrying the reason it is not:
    `shared=no (under a home)` when the resolved store lies under a home directory — the
    caller's, read through the box interface (the passwd home of the calling uid, and
    `$HOME` when it differs, each taken as a resolved directory), or any **other**
    account's, which on this box is a path under `/Users` (`/home` on linux) whose next
    component is not `Shared` — and `shared=no (elsewhere)` for anything else. Both are
    keyed on the shared root and nothing else: a store under another account's home is no
    more shared than one under the caller's and reports the same `no`, for
    [BOX-LOCAL.md](BOX-LOCAL.md)'s reason. Whose home it is is the printed **reason** only.
    **It refuses only against a value the operator gave** (rule 8's shape): with
    `--require-shared-store`, `serve` is exit 1 on `shared=no` — naming the store, the
    reason, the remedy that is the recipe's own first line,
    `sudo mkdir -p /Users/Shared/nova-local/models/<e>`, and [BOX-LOCAL.md](BOX-LOCAL.md)
    — never on `shared=unknown`, which is not a verdict (above), and never on `status`,
    which would only reprint the `store=` the refusal has just named. Without the flag it
    serves and prints `shared=no`.
    On a box more than one account uses, the requirement is enforced by the recipe's
    step 4 — `status` from both accounts showing `shared=yes` — and thereafter by
    `status` never lying about it; whatever invokes `serve` there passes the flag.
    **Which** thing does is open in one place, [BOX-LOCAL.md](BOX-LOCAL.md)'s list of
    what the recipe must still decide. `status` never refuses.

## The engines

| | **ollama** | **ds4** |
|---|---|---|
| what it is | a daemon serving many installed models | antirez's DwarfStar: a checkout built by `make`, one process per resident model |
| default base | `http://127.0.0.1:11434/v1` | `http://127.0.0.1:8000/v1` (`ds4_server.c:14043`, `.port = 8000`) |
| health | `GET /api/tags` on the daemon root | `GET /v1/models` |
| a model is | a tag, `<name>:<tag>` | a **GGUF file** on disk |
| arrives by | the stranger's own `ollama pull <tag>` — **this tool fetches nothing** | the stranger's own download; antirez publishes these weights himself, so they are in no catalogue |
| loaded | several, as memory allows | **exactly one**, for the life of the process |
| digest | the manifest digest from `/api/tags` | none — `digest=none (ds4 reports none)` |
| `serve` | create `<name>-<ctx>k`, read `/api/show` back, one warm-up load with `keep_alive` | start `ds4_server -m <gguf> --ctx <n> --host 127.0.0.1 --port <port of --base>` (`--host` `:14130`, `--port` `:14132`, `-c/--ctx` `:14122`) |
| `serve --stop` | one request with `keep_alive: 0`; the derived tag stays on disk | end the process this tool started (`pid` from `SERVE OK`) |
| determinism | baked into the derived tag (`temperature 0`, `--seed`) | **per request**, not per process: `serve --seed` on ds4 is exit 2 naming this row, because this tool sends no inference requests |
| harness id | provider `ollama`, model the derived tag. OpenCode's `--model` is `<provider>/<model>`, so the harness argv carries `ollama/{model}` while the description's `model` stays the bare served id (`worker.go:229`) | **unknown** — the spelling an OpenAI-compatible client must send, which `worker` needs and cannot guess |

`ds4_server.c` is antirez's checkout at `/Users/rowan/rowan-working/ds4`, readable
from this bench; every line cited above was read at commit **c0a6119f** (2026-09-12).
Three unknowns stay open and are **read off that source before
`internal/local/ds4.go` is written**: whether any endpoint reports load progress (so
`state=loading` would be an inference and must be labelled one); whether the process
has a shutdown path of its own; and the harness id. ds4's measured cost, dated
(`memory/the-machine-is-the-mandate.md:24,43-44`): V4 Flash 2-bit, 81 GB resident,
~42 tokens/s (2026-09-03); PRO 2-bit, 434.5 GiB at 32K, 13.8 tokens/s (2026-09-06),
and only under `iogpu.wired_limit_mb=480000`, which is **per boot** and was raised by
hand — `status` reads the live cap and never assumes the raised value.

## The verbs

```
nova-local status [--engine <name>] [--base <url>] [--list] [--max <n>] [--timeout <s>]

nova-local serve  --engine <name> --model <ref> --num-ctx <n> [--base <url>]
                  [--keep-alive <d>] [--seed <n>] [--expect-digest <sha256:…>]
                  [--max-load <f>] [--min-free <size>] [--require-shared-store]
nova-local serve  --stop --engine <name> --model <ref> [--base <url>]

nova-local worker --engine <name> --model <ref> --out <file> --name <text>
                  --harness <cmd> --harness-args <a,b,{model},…> --worker-dir <abs dir>
                  --key-file <file> --env-var <NAME> --usage <opencode|none>
                  --deadline <duration> [--base <url>] [--board <owner/repo#n>]
                  [--provider <id>]
```

**No guessed anything.** No default engine, context, model or output path, and on
`worker` no default for any field the description carries; a missing one is exit 2 and
`refusing to guess`. The only defaults are `--base` (the adapter's constant),
`--timeout` (2 s), `--max` (20) and `--keep-alive` (`30m`), each printed on the line
that used it. `--provider` is required exactly where the adapter publishes none (ds4)
and exit 2 where one is published. `--harness-args` is split on `,` with no escape, so
an argument containing a comma is not expressible and is exit 2. There is **no
`--force`**: every refusal names a different command as its remedy, says in one line
what the input wants, and never prints the contents of a path it could not use. `OK`
goes to stdout, `FAIL` and `REFUSED` to stderr, every value through
`internal/oneline`. `REFUSED` is printed at both failing codes and what it names says
which: something the caller can retry differently is exit 1, a flag or a named input
the tool could not use at all is exit 2; `FAIL` stays reserved for a verb that ran and
broke.

### `status`

**Reads:** each adapter's health path inside `--timeout`; `/api/tags`, `/api/ps`,
`/api/show` (ollama) or `/v1/models` and this user's own ds4 process arguments; the
box's load average, memory and live wired cap, at the moment of the call. Creates
nothing, writes nothing.

```
LOCAL OK engines=<n> answering=<n> loaded=<n> models=<n> mem_used=<n> mem_free=<n> mem_total=<n> wired_cap=<n|unset> load1=<f>
LOCAL ENGINE <name> state=<up|down|timeout> [status=<code>] base=<url> [loaded=<n>] [advertised=<n>] [resident=<file|unknown (<why>)>] [resident_bytes=<n|unknown (<why>)>] [num_ctx=<n>] store=<dir|unknown (<why>)> shared=<yes|no (<why>)|unknown>
LOCAL MODEL engine=<e> model=<ref> digest=<sha256:…|none (<e> reports none)> weights=<n> loaded=<yes|no> [num_ctx=<n>]
LOCAL NOTE <text>
LOCAL MORE kind=<kind> shown=<n> total=<n> remedy=<command>
LOCAL FAIL <what>: <reason>
```

`state=up` is a **2xx on the health path inside `--timeout`** and nothing weaker, so
another program on port 8000 is `state=down status=404`. `advertised` is what the
server answers to; `resident` is what was loaded, and it is **two facts, not one**: the
`-m` path **resolved through symlinks**, and `resident_bytes=`, that resolved file's
size from one `stat`. Both, because a name can lie two ways and resolution only catches
one of them. `ds4flash.gguf` on this box is a **symlink** to the 464 GB Pro file
(`ls -li`, 2026-09-12: `lrwxr-xr-x`, and the Pro inode's link count is **1**; the
bake-off's "same inode" — `research/2026-09-12-local-model-bakeoff.md` on standard,
`c94ee4d` — is `stat` through that link, not `lstat`), so **resolution alone catches
this one**: the resolved `-m` path prints the Pro name. `resident_bytes=` is here for the
case resolution **cannot** catch — a hard link, a second name for one inode, invisible to
any resolution, where the path alone says Flash while Pro is what is loaded and the size
is the only fact that separates them — which is the case test 16 fakes. A file it could
not `stat` is
`resident_bytes=unknown (<why>)`, not a guess (rule 15's shape). With nothing answering
it prints one remedy line naming `ollama serve`, never the state (rule 13).

**Refuses** nothing, except **no engine answering at all**: exit 1, one remedy line; a
slow engine is `state=timeout` at exit 0. **Demanded test:** 11, 13, 17 — 400 models
bounded under 4,096 bytes, and two runs under different `HOME`/`USER` pairs
byte-identical, `store=` and `shared=` included.

### `serve` (with `--stop`)

**Reads:** the box (rule 8) **before** starting anything; `/api/show` for the four
compared parameters and the store, or the port and this user's own process (ds4); its
own wall clock across the warm-up.

```
SERVE OK engine=<e> model=<ref> serve_as=<ref> digest=<sha256:…|none (<e> reports none)> num_ctx=<n> keep_alive=<d> temperature=<0|unset> seed=<n|unset> load=<t> mem_free=<n> load1=<f> engines=<n> store=<dir|unknown (<why>)> shared=<yes|no (<why>)|unknown> [pid=<n>]
SERVE OK stopped engine=<e> model=<ref> [already stopped]
SERVE REFUSED <ref>: <reason> — remedy: <command>
```

`temperature=0` is ollama's, baked into the tag; on ds4 nothing sets it and the field
is `unset`. `--stop` unloads (ollama) or ends the process this tool started (ds4);
already stopped is exit 0 and says so.

**Refuses**, each exit 1 and each carrying `— remedy: <command>`: an `--expect-digest`
that differs, naming both, remedy `nova-local status --engine <e> --list`; an existing
derived tag differing in parent, `num_ctx`, `temperature` or `seed`, naming both values,
remedy `ollama rm <tag>`; a ds4 already resident on a **different** model, naming it and
the memory both would want, remedy `nova-local serve --stop --engine ds4 --model
<resident>`; a load or free memory past a `--max-load` or `--min-free` **the caller
gave**, naming measured and asked, remedy `nova-local status --list`; a store outside
the shared root — `shared=no`, either reason — **only when `--require-shared-store` was
given**,
with rule 15's naming and remedy; and something
already listening on the port an adapter would start a process on, naming **the port**, remedy `nova-local
status` — no holder is named, because the socket's owner is not readable from a non-root
process with the standard library. A missing `--num-ctx`, one with a remainder,
`--seed` on ds4 and a non-loopback `--base` are exit 2. **Demanded test:** 5–9, 16, 17 — the tag idempotent,
one warm-up timed by the injected clock, and the port refusal carrying the port and
**no** `held_by`.

### `worker`

**Reads:** the served id from the adapter (`serve_as`, or ds4's advertised id), the
adapter's provider id and base URL, and its flags. It `stat`s the key file, never
opens it, and writes exactly one file.

```
WORKER OK engine=<e> model=<ref> out=<path> workers=1 provider=<p> harness=<cmd> deadline=<d> base=<url>
WORKER REFUSED <ref>: <reason> — remedy: <command>
```

Every field is read from the adapter or given as a flag, because `nova-swarm` decodes
strictly (`worker.go:26-39,64`, `DisallowUnknownFields`) and refuses an empty required
field (`worker.go:88-120`): `name`, `provider`, `model`, `base_url`, `env_var`,
`key_file`, `usage`, `harness`, `harness_args`, `worker_dir`, `deadline`, and `board`
omitted when not given. It carries **no `temperature`, `seed` or `num_ctx`** — the
decoder has no such fields and `serve` baked them into the served thing — no
conditions and no `AGENTS.md` (rule 11).

**Refuses**, each exit 2 with the field named and the command that satisfies it, all
independent problems in one run: `--harness-args` without `{model}` (a description
that names a model and never places it in the harness's argv launched a harness told
nothing, under a green `RUN OK` — `worker.go:110-120`); a `--worker-dir` relative or
absent (`RefreshSlot` walks it — `worker.go:157,166` — and fails the launch under a
green `RUN OK`); a `--key-file` missing or **zero bytes**, with `printf 'local\n' >
<path> && chmod 600 <path>` (`key.go:64`); a bad `--usage`; a `--deadline` that is not
a Go duration; `--provider` where one is published. One refusal is exit **1**, because
the caller can retry it: a `--model` the engine does not advertise — a description for a
tag nobody served is the one-step-short case of DECIDED 2 — naming it, remedy
`nova-local serve --engine <e> --model <parent> --num-ctx <n>`. **Demanded test:** 10,
14, 15 —
the written JSON decodes with **zero problems**, the key file is never opened or
printed, and every refusal is reported at once.

## First run

Six lines pasted in order, nothing edited but the paths — and a seventh the tool hands
over.

```
$ ollama pull gemma4:12b
$ nova-local status
$ nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --keep-alive 30m --seed 7
$ mkdir -p $PWD/home $PWD/workers && printf 'local\n' > $PWD/local.key && chmod 600 $PWD/local.key
$ nova-local worker --engine ollama --model gemma4-32k --out $PWD/workers/gemma.json --name gemma --harness opencode --harness-args 'run,--model,ollama/{model},--,{prompt}' --worker-dir $PWD/home --key-file $PWD/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m
$ nova-swarm quickstart --pool $PWD/pool
$ nova-swarm run --pool $PWD/pool --workers 1 --hours 1 --worker $PWD/workers/gemma.json
```

**Every fact these lines assume, stated.**

- **Before line 1**, one thing they do not do. Both binaries are on PATH — `go install
  ./cmd/nova-local ./cmd/nova-swarm` from a clone (Go 1.26, `go.mod`) puts them in
  `$(go env GOPATH)/bin`, which a fresh Mac does **not** have on PATH. The box recipe
  ([BOX-LOCAL.md](BOX-LOCAL.md)) is **not** a prerequisite of these six lines: line 3
  serves from whatever store the engine reports and prints `shared=no` for any store
  outside the shared root. It is the prerequisite of a box more than one account uses (rule 15), where it
  is a 162 GB move done once (BOX-LOCAL.md's dated table).
- **Line 1** is the stranger's own tool: it assumes the ollama daemon is running, that
  `gemma4:12b` is a tag in ollama's library, and one download of the weights, whose size
  this document has not measured. With the recipe applied it lands in the shared store,
  so it is done once for the box.
- **Line 2** assumes nothing and creates nothing; with the daemon down it prints one
  remedy line and exits 1, which is the first thing a stranger needs to know.
- **Line 3** creates `gemma4-32k` and loads it once; it assumes `gemma4:12b` supports
  32,768 tokens. `--seed 7` is **arbitrary** — the seed this box's hand-made tags carry;
  omit it and the tag is made without one. No threshold flag is given, so nothing about
  the box is refused, and `load=` is the warm-up it just timed.
- **Line 4** makes the worker directory, line 5's output directory and the placeholder
  key file. `$PWD/home` is the home copy `nova-swarm` refreshes into each slot
  (`worker.go:157`); it may stay empty but it must **exist**, and `--out`'s parent must
  exist too, because `worker` makes no directory. The local engine wants no key;
  `nova-swarm` requires a non-empty key file (`key.go:64`), so one line of any text is
  the whole of it. `$PWD` must contain no spaces.
- **Line 5** names `gemma4-32k` — the `serve_as=` line 3 printed — and **is the
  acceptance**: the twelve keys it emits are the decoder's twelve (`worker.go:26-39`)
  with no extra, every `want()` at `worker.go:93-100` non-empty, `usage` accepted,
  `{model}` placed, the deadline parsed and `ReadKey` accepting `local\n`, so
  `swarm.LoadWorker` returns **zero problems** — a decode, not a run, needing nothing
  installed. `--harness-args` carries `ollama/{model}` and not `{model}`:
  `supervise.go:288` substitutes the bare `model` verbatim and OpenCode's `--model` is
  `<provider>/<model>`.
- **Line 6** makes the pool and **starts nothing**: `cmdQuickstart`,
  `cmd/nova-swarm/main.go:1005-1029`, is a flag parse, a `MkdirAll`, a pool open, a
  `List(Pending)` and four `Fprintf`s — no exec, no `LookPath`, no network.
- **Line 7** is the one `quickstart` hands over, as a **template**: its `QUICKSTART
  NOTE` prints `--workers <n> --hours <h> --worker <file>` with three blanks, and the
  line above fills them — `--workers 1` by rule 9, `--hours 1` because `run` requires an
  hours greater than zero, `--worker` the path line 5 wrote. **OpenCode is this line's
  prerequisite and not the six lines'**: `run` refuses before any pool work when the
  harness is not on PATH (`main.go:451`, exit 2 naming it). Over an empty pool it is
  `RUN OK started=0` (`run.go:308-310` breaks on the empty pool, `:321` prints it). One
  more program is needed only once a task
  runs: `usage: opencode` reads OpenCode's database with `sqlite3` (`opencode.go:32,75`),
  which macOS ships and a linux box may not.

OpenCode needs no config of the stranger's: `nova-swarm` writes the provider block into
the slot at every start (`run.go:462` → `worker.go:219-243`), and the three keys that
matter are the served id under `provider.ollama.models`, an `options.apiKey` of
`{env:<the description's env_var>}`, and an `options.baseURL` of the engine's `/v1`.
Whether OpenCode accepts that block with no `npm` key is a fact about OpenCode and not
about this repo — **the one fact in these seven lines no test here can see**, and the
thing the new-user hour's transcript exists to prove.
[`docs/CLI.md`](CLI.md)'s `### First run` **will be** these lines with their output plus the
sentence naming OpenCode as the seventh line's prerequisite, executed by test 1 (work
list 7; `docs/CLI.md` has no `nova-local` section today).

## Deliberately not in this tool

- **`pull`** — the engine's own tool and the person's own download; the digest is on
  `status`'s and `SERVE OK`'s lines and in `--expect-digest`.
- **`trust` / `untrust` and the lockfile** — policy, cut by Glenn; with the evaluation
  tool, and with them the lesson that a gate and its writer belong in one binary.
- **`stop`** as its own verb — folded into `serve --stop`. **`fit`** and `fits=` — it
  needed a headroom nobody has measured; two printed numbers do not.
- **`eval` and `compare`** — Glenn: *"if we want to eval, it is another tool, perhaps
  with more deps."* **`nova-local` never grows an `eval` verb.**
- **`policy`** — `nova-swarm`'s dispatcher, where local-or-API is chosen. **usage rows**
  — `nova-tokens`. **the prompt conditions** — `nova-swarm template`.
- **inference** — no `ask`, no `read`, no `triage`; `rowan-local` is the estate's
  one-prompt client and `nova-swarm` runs the workers.
- **discovery and ranking** — no feed reader, no registry search, no champion file. A
  model name found on a page or in a model's own output is data, never a serve target.
- **the daemon, the store's owner and the one-time move** —
  [BOX-LOCAL.md](BOX-LOCAL.md). A tool writing under `/Library` would be an outbound
  actor, and this one writes exactly one file.

## Tests this spec demands

One line per rule. **Rule to test:** 1→2, 2→3 and 16, 3→4, 4→5, 5→6, 6→7 and 8, 7→8,
8→9, 9→10, 10→11, 11→12, 12→11 and 8, 13→13, 14→14 and 15, 15→17. Rule 11's doctrine
half is **not enforced by code and could not be**; test 12 enforces the verb set and
the one written file. Each test runs in `t.TempDir()` against fake engines on loopback,
no network, and must be seen red first; where one needs the box's load or free memory it
supplies a **fake box** through the one interface that reads them
(`internal/local/box.go`), which no environment variable overrides in production.

1. **The stranger test.** `README.md`'s six lines against fake engines end with the
   derived tag at the given context, a `workers/gemma.json` that `swarm.LoadWorker`
   returns **zero problems** for and `swarm.ReadKey` accepts, and the pool `quickstart`
   made — no process launched. The seventh line is asserted separately and **both
   ways**: with a fake `opencode` on a `t.TempDir()` PATH, `RUN OK started=0`; with that
   PATH empty, exit 2 naming the harness. The new-user hour's transcript is PR evidence,
   not the test.
2. The import walk fails on any path outside the standard library (no dot before the
   first slash) **and this module's own path**; `go.mod` has no `require`.
3. A third fake adapter in a test file: all three verbs exit 0 against it, and the diff
   that adds it touches one non-test file.
4. `--base http://10.0.0.5:11434/v1` is exit 2 on every verb naming the host;
   `127.0.0.1:9999` and `[::1]:9999` are used; the default is printed.
5. `digest=` is as the fake reported on every model and `SERVE OK` line, `none (ds4
   reports none)` where none is reported; `--expect-digest` differing is exit 1 naming
   both and serving nothing; every `SERVE REFUSED` line, here and in tests 9, 16 and 17,
   carries `— remedy: <command>`; the run's whole file output is the `--out` path.
6. `serve` without `--num-ctx` is exit 2 containing `refusing to guess`; `num_ctx=` is
   printed only for a model the fake `/api/ps` reports loaded.
7. `--model m:7b --num-ctx 32768 --seed 7` creates `m-32k` with those three baked and
   prints `serve_as=m-32k`; an identical second run creates nothing and exits 0 **even
   though** the fake reports `top_k`/`top_p` the tool never set; a different parent,
   `num_ctx`, `temperature` or `seed` — including the collision `m2:7b` — is exit 1
   naming both values and `ollama rm`; `--num-ctx 30000` is exit 2 naming 29696 and
   30720; `--seed` on ds4 is exit 2 naming the determinism row.
8. Exactly one warm-up carries `keep_alive` and `load=` is the injected clock's advance
   across it (fake sleeps 3 s → `load=3s`); no `--keep-alive` sends and prints `30m`;
   `--stop` sends one request carrying `keep_alive: 0` and nothing else, the tag is
   still listed, a second `--stop` is exit 0 `already stopped`; no `status` line carries
   `load=` or `gen=`.
9. Fake box at `load1=14.2, mem_free=61G`: no flags is exit 0 with both numbers on the
   line; `--max-load 8.0` is exit 1 carrying measured and asked; `--max-load 20.0
   --min-free 16G` is exit 0.
10. `worker`'s line carries `workers=1` and the JSON has no `max_workers`,
    `temperature`, `seed` or `num_ctx` key (decoded with `DisallowUnknownFields`).
11. `mem_free=`/`mem_total=` are read at the moment of the call — two calls across a
    change print two numbers; every model line carries `weights=` and `loaded=` as the
    fakes reported; no line carries `fits=`.
12. Any first argument but the three is exit 2 naming the door, and a run of all three
    leaves one new file on disk and no bytes under `$HOME`.
13. 400 advertised models print at most `--max` lines plus one `MORE`, stdout under
    **4,096 bytes**; nothing answering is one remedy line at exit 1.
14. The ds4 child's argv holds the model path, context, host and port and nothing else;
    the key file's contents appear nowhere and it is never opened (mode `0000` still
    succeeds); an empty key file is exit 2 with the `printf … && chmod 600` remedy; no
    child argv begins with `sh`, `bash` or `zsh`.
15. `worker`'s refusals, each exit 2 naming the field, all in one run: no `{model}`; an
    argument carrying a comma; a `--worker-dir` relative or absent; a missing
    `--key-file`; a bad `--usage`; a bad `--deadline`; `--provider` on ollama. Plus the
    one at exit 1: a `--model` the fake does not advertise, remedy `nova-local serve`.
16. A fake ds4 with two ids and one loaded gives `advertised=2 resident=<the -m file,
    symlinks resolved> resident_bytes=<that file's size>` and `digest=none (ds4 reports
    none)`; a `-m` path that is a **hard link** to a second, larger fake prints the
    linked name and the **larger** size, which is the assertion that can say NO to a
    path-only answer; an unstattable file is `resident_bytes=unknown (<why>)` at exit 0;
    no process gives `resident=unknown (<why>)`; a fake process of **another uid** gives
    `store=unknown (process is uid <n>'s)` and `serve --require-shared-store` is **exit 0**
    with `shared=unknown`, the assertion that the flag never fires on a non-verdict;
    `serve` over a different resident model is exit 1 naming
    it; `--stop` twice is exit 0 both times.
17. **Two accounts, one store.** Two runs under different `HOME`/`USER` pairs are
    **byte-identical**, `store=` and `shared=` included, for a store under the shared root
    and for one under `/Users/<other>`; the faked-home cases below are single runs, whose
    `shared=` reason is relative to that run's caller. `store=` is the `blobs` parent
    of the fake's `FROM` path, `unknown (no tag to show)` with no tag. A store under
    **either** home — the caller's, and the one that is the **other** account's in that
    run — is `serve --require-shared-store` exit 1 naming the store, the reason and
    BOX-LOCAL.md, and plain `serve` exit 0 printing `shared=no (under a home)`, while
    `status` stays exit 0 with the same value; the other account's is the case that can
    say NO to a caller-relative test. The caller's-home half is driven by a **faked
    home** through the box interface (`internal/local/box.go`) — passwd home and `$HOME`
    two different temp directories, neither under `/Users` — each refused in its own run,
    the case that can say NO to a `$HOME`-only or spelled-prefix implementation. A store
    under neither root is `shared=no (elsewhere)` and exit 1 under the flag too. A fake
    `FROM` under `/Users/Shared/nova-local/models/ollama` is exit 0 with `shared=yes`
    under the same flag; a fake `FROM` under
    `/Users/Shared/nova-local-scratch/models/ollama` is `shared=no (elsewhere)` and exit 1
    under the flag — rule 15's neighbour, the case that says **NO** to a spelled-prefix
    `yes`; and a fake store inside a home that is a
    **symlink** to that shared path prints the resolved shared `store=` with `shared=yes`
    and is exit 0, the case that can say NO to an unresolved path. The `/Users/…` fakes
    here are paths whose components do not exist on the test box — the
    longest-existing-prefix case of rule 15 — and the test creates nothing outside
    `t.TempDir()`. A fake holder on the port gives exit 1 carrying the port and **no**
    `held_by`. No verb writes a plist, a unit, or anything under `/Library`.

Plus the house standard: a usage banner ending in a runnable `example:` block, refusals
reporting every independent problem at once, a `### First run` in `docs/CLI.md` pinned by
an executing test, and three-GOOS builds green.

## The work list

Go under `cmd/nova-local`, the way `cmd/nova-bus` is built: standard library only, no
hardcoded paths, the exit grammar above, `internal/oneline` for every printed value,
`internal/bounded` for every listing.

1. **`internal/local/engine.go`** — the adapter interface, the loopback pin, the
   `--timeout` budget, the registry, rule 15's shared-root test, the `shared=no` reason
   (the home read from `box.go`) and
   `--require-shared-store`. Tests 3, 4, 17.
2. **`internal/local/box.go`** — load average, memory, the live wired cap **and the
   caller's home** (the passwd home of the calling uid, and `$HOME`), read at the
   moment of the call, behind one interface a test can fake. Tests 9, 11, 17.
3. **`internal/local/ollama.go`** — `/api/tags`, `/api/ps`, `/api/show` (parameters
   **and** the `FROM` blob path), the derived tag via `/api/create`, the warm-up, the
   unload. Tests 5, 6, 7, 8, 17.
4. **`internal/local/ds4.go`** — the process with model path, context, host and port;
   advertised versus resident, symlinks resolved and the resolved file `stat`ed for
   `resident_bytes=`; the three unknowns read off
   `ds4_server.c` first. Tests 14, 16.
5. **`internal/local/worker.go`** — the description in `nova-swarm`'s exact schema,
   every field from a flag or the adapter, the refusals, the key file `stat`ed and never
   opened. Tests 10, 14, 15.
6. **`cmd/nova-local/main.go`** — three verbs, the banner, the nothing-answering remedy
   line folded into `status`, refusals that name the next command. Tests 1, 12, 13.
7. **`docs/CLI.md`'s `### First run`** — the six lines with their output, the seventh, and
   the sentence naming OpenCode as its prerequisite. Test 1.

Owed outside this tool and filed as nova-tools issues rather than drafted here: a
`max_workers` field for `nova-swarm`'s schema so rule 9 is carried by the schema rather
than by an operator, and `llama.cpp`-server and `mlx-lm` adapters.

## DECIDED IN THE DRAFT

The drafter's choices, not the record's, each with the commit that made it; each is
still a place to push back.

1. Three verbs; `stop` is `serve --stop`; `pull`, `trust`, `untrust` go with the lockfile (419382e).
2. `worker` survives the cut and `stop` does not: a served model nothing can call is one step short (419382e).
3. The digest is printed and compared on request, never enforced (419382e).
4. `serve` creates a derived tag on ollama, the only mechanism that makes `num_ctx` hold for a `/v1` caller (419382e).
5. No thresholds of the tool's own; `--max-load` and `--min-free` are the operator's (419382e).
6. No fit verdict — two numbers instead of three words (419382e).
7. `--base` defaults to the adapter's constant rather than being a constant only (419382e).
8. `status --timeout` defaults to 2 s and a slow engine is `state=timeout` at exit 0 (808b08d).
9. The prompt conditions are not written here at all, not even quoted (419382e).
10. `llama.cpp` and `mlx-lm` are named and not built, which is the test of rule 2 (808b08d).
11. No `--force` anywhere; every refusal's remedy is a different command (808b08d).
12. The ds4 unknowns stay unknowns; the port and bind-host flags are closed from `ds4_server.c`, which is readable from this bench at c0a6119f (2e858f8, corrected this revision).
13. The six lines end at `quickstart` and `run` is the seventh, because `run` refuses without the harness on PATH (2e858f8).
14. The derived tag strips the reference's `:<tag>`, so a collision is caught by the parent rather than the name (2e858f8).
15. Rule 15 is what an engine reports — `store=` from `/api/show`'s `FROM` path or this user's own process argv, and a port refusal with no `held_by` — because the two reads the previous revision demanded are not available to a non-root account through the standard library on the platform it named; the daemon, the owner and the move are BOX-LOCAL.md (this revision).
16. A store outside the shared root (`shared=no`, either reason) is **printed always and refused only under `--require-shared-store`** (2026-09-12, re-derived from rules 8 and 10 on read 5): one clause had three answers in three revisions — a `LOCAL NOTE` at exit 0, a wall, then this — and the oscillation ends because a refusal firing on every one-account laptop and on no box after the recipe enforces the two-account requirement exactly where it does not apply. It is enforced at BOX-LOCAL.md's step 4 instead (this revision).

## Sources

`memory/…` is Rowan's self repo (`/Users/glenn/rowan-new`); `standard/…` is
`/Users/glenn/rowan-working/standard`; `ds4_server.c` is antirez's checkout at
`/Users/rowan/rowan-working/ds4`, readable from this bench at c0a6119f.

- `memory/local-model-doctrine.md:105`, 2026-09-09 — rule 8's *"a lock is probably not needed"* (receipt `inbox/2026-09-10-bench-note-local-models-on-the-studio.md`).
- `memory/the-machine-is-the-mandate.md:24,43-44` — ds4's Flash and PRO measurements · `memory/model-trust.md`, `a-guard-without-its-writer.md`, `ready-is-a-measurement.md`, `tool-output-costs-tokens.md` — rules 4, 8, 13.
- `standard/MODELS.md:100`, 2026-09-07 — rule 5's 4,096-token cap.
- nova-tools issue #75, 2026-09-12 — rule 7's 2m48s · rowan-tools#60, 2026-09-03 — `advertised` versus `resident`.
- Rule 15's two quotes are from the bus, 2026-09-12, with **no receipt in `memory/` yet**; the box's measured state is dated in [BOX-LOCAL.md](BOX-LOCAL.md).
