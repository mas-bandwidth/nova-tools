# nova-local — specification

**What this tool is for, in one sentence (Glenn, 2026-09-12):** *"somebody should
be able to grab nova-local and run local models."*

Everything below serves that sentence or is cut. The gate before the tool is
called done is the **new-user hour**: a fresh AI line, given the README and
nothing else, on a box with ollama installed, runs the six-line first run and
ends with a model serving and a worker description `nova-swarm` accepts. That is
demanded test 1.

`nova-local` is one binary. It does not run inference and it does not judge
models. It makes a local engine usable: what is answering, what weights are on
disk, which of them production may use, and one model served at a context you
chose, with a worker description a swarm can take.

This spec is normative. If the code and this document disagree, one of them has a
bug, and the tests decide which. It stands beside [SPEC.md](../SPEC.md), whose
**Conventions** section — exit codes, no guessed paths, the one-line output
grammar, the cap-and-count rule, `internal/oneline` and `internal/bounded` —
applies here unchanged and is not restated.

## DATA

| | |
|---|---|
| language | Go — `cmd/nova-local`, **standard library only** |
| kind | engine runner and lockfile writer. Not an inference client, not a judge, not an outbound actor |
| engines | **ollama** (many models, one daemon) · **ds4** (antirez's DwarfStar, one resident model per process). Both are adapters; see **engines** |
| pin | loopback only. A port may move; a host may not |
| secrets | **none.** Reads no key file, exports no key, prints no credential |
| exit | `0` did it · `1` did it and said **NO** · `2` could not run |
| verbs | `status` · `pull` · `trust` · `untrust` · `serve` · `stop` · `worker` |

## The rules, numbered

1. **No dependencies** (Glenn, 2026-09-12: *"no deps"*). One static Go binary from
   the standard library alone: `net/http` to the engines' HTTP APIs,
   `crypto/sha256` for digests, `os/exec` for the ds4 process. No third-party
   module appears in its `go.mod` graph. The only things a user needs are this
   binary and an engine. A test asserts the import set.

2. **An engine is an adapter, and adding one changes nothing else** (Glenn:
   *"the requirement that we can try the antirez stuff and others"*). An engine is
   anything with a start command, a port, a health URL, and an OpenAI-compatible
   `/v1` endpoint. An adapter declares four things: its name, how to start it with
   a model path or tag, how to stop it, and how it lists its models. `ollama` and
   `ds4` are the two adapters in this spec. **`llama.cpp`'s server and `mlx-lm`
   are named as the next two and are not built here**; adding either must be one
   file and no change to a verb.

3. **A digest or nothing.** `pull` refuses without `--sha256`. The bytes under a
   name are the only provenance this tool has, and a pull that cannot detect the
   bytes changing is not provenance at all. A model is the largest untrusted input
   this bench ingests (Glenn, 2026-07-19: *"what if somebody sends you a poisoned
   model. How would you know?"*). Weights land **QUARANTINED**, never usable.
   Source: `memory/model-trust.md`.

4. **The lockfile trusts a triple, never a name.** An entry's key is
   `(engine, model reference, digest)`. The same tag at a new digest is a new,
   untrusted entry and refuses as drift; nothing is ever overwritten, and the
   remedy is remove, re-pull, re-record. Source: `memory/model-trust.md`.

5. **The gate and its writer are one binary.** `trust` and `untrust` write the
   lockfile; the refusal `worker` prints reads it; both are in this binary. The
   enforcer landed 2026-08-11 and its only writer landed 2026-09-02, and for 22
   days the tier could refuse a model and could not trust one — silently, because
   a guard fails closed and its caller fails safe to the paid model. Source:
   `memory/a-guard-without-its-writer.md`.

6. **Quarantine is enforced at the door out.** `worker` refuses an untrusted
   `(engine, model, digest)` and `serve` refuses one too. Promotion from
   QUARANTINED is a deliberate, rate-limited act by a person — no test proves a
   model clean, because a sleeper backdoor passes every benchmark by design — and
   the only thing that may run a quarantined model is an evaluation, which is not
   in this tool (see **deliberately not in this tool**). Source:
   `memory/model-trust.md`.

7. **`--num-ctx` has no default.** ollama silently caps context at **4,096
   tokens** whatever the model supports, so a long prompt returns a confident
   answer about a truncated input with no error. `serve` without `--num-ctx` is
   exit 2 and `refusing to guess`; `status` marks any loaded model with no written
   config as `num_ctx=4096 default`. Source: `standard/MODELS.md`, the Gemma row.

8. **`keep_alive` is on, and load time is reported apart from generate time.**
   `serve` keeps the model loaded between tasks; `status` prints `load=` and
   `gen=` as separate fields. Gemma's first nova-swarm task took **2m48s**, most
   of it load; a tool that reported one number would have made a cold start look
   like a slow model. Source: nova-tools#75, 2026-09-11.

9. **READY is a measurement.** `serve` reads the load average and the engines
   already running before it starts anything, and **refuses when the box is loud,
   printing the numbers** — never a feeling, never a guess. A refusal names what it
   measured and what it wanted. Source: `memory/ready-is-a-measurement.md`.

10. **One local worker at a time.** An engine is a queue: a second concurrent
    worker against one engine interleaves two queues and makes both slower while
    the pool reports two running. `worker` prints `workers=1`.

11. **Reads are deterministic.** The worker description `worker` emits sets
    `temperature: 0` and a fixed `seed` for read tasks, so the same prompt against
    the same weights gives the same text and two runs can be diffed. Source:
    `memory/deterministic-crystal.md`.

12. **The memory is the constraint, and the fit verdict is against FREE memory
    now.** `status` prints one **fit** line per model, and its verdict is one of
    three: `beside` (fits beside what is running right now), `quiet` (fits only
    with the box quiet — nothing else running, and the wired cap raised where the
    model needs it), or `no`. The memory is read from the OS **at the moment of the
    call**, never from a total and never from a cached number. Measured
    2026-09-12: with Qwen 3.6 35B (23 GB) reading on the GPU the Studio sat at 93%
    CPU idle with 173 GB used of 512 and 338 GB free — mid-size models run beside
    normal work — and Glenn's line for the top end, *"the hardcore 500gb models
    would only work if that's all this studio did; maybe a few hundred or 200gb"*
    beside other work. A registry's tier bucket is not this box either: six dense
    candidates were once queued off a ranking bucketed at 8, 16 and 24 GB of VRAM,
    on a machine with **512 GB of unified memory**. Ask what this box can run, now.
    Source: `memory/the-ranking-encodes-someone-elses-constraint.md`.

13. **Triage and report; never decide-and-act; never the safety arbiter.** What a
    local model returns is a hint, treated as untrusted data; on uncertainty,
    timeout or error the caller escalates. The worker description's preamble
    carries that sentence. No verb here merges, sends, deletes or rules on safety.
    This is not enforced by code and could not be; it is stated because the tool
    that pretended to enforce it would be the dangerous one. Source:
    `memory/local-model-doctrine.md`.

14. **Every listing is bounded.** Counts by default, a list only behind `--list`
    and capped by `--max`, and a `MORE` line carrying the remedy. A degenerate
    state prints one remedy line, never the state. Source:
    `memory/tool-output-costs-tokens.md`.

15. **Content on stdin, never in argv.** No prompt, no task text and no model
    output ever reaches a command line, a log or a printed line. This binary has
    no shell and never executes anything a model returned.

## The engines

| | **ollama** | **ds4** |
|---|---|---|
| what it is | a daemon serving many installed models | antirez's DwarfStar: a checkout built by `make`, one process per resident model |
| base URL | `http://127.0.0.1:11434/v1` | `http://127.0.0.1:8000/v1` |
| a model is | a tag, `<name>:<tag>` | a **GGUF file** on disk |
| arrives by | `pull --engine ollama --model <name>:<tag> --sha256 <manifest digest>` | `pull --engine ds4 --url <https url> --sha256 <file digest> --dir <quarantine>` |
| why by URL | — | the frontier-local weights **are not in ollama's catalog**: antirez converts and publishes them himself, so *what can I pull* is a different question from *what can I run* |
| loaded | several, as memory allows | **exactly one**, for the life of the process |
| context | `num_ctx` per model, or the 4,096 cap applies silently | `--ctx` on the process |
| `serve` | write this model's options with `num_ctx` and `keep_alive`, confirm by reading them back | **start the process** with the model path, context and port |
| `stop` | drop the options and unload | end the process |
| harness id | `ollama/<name>:<tag>` | the OpenAI-compatible provider name the description declares, with the id ds4 answers to |

**Measured on this bench, carried with their dates and never assumed again.**
512 GB unified memory. DeepSeek V4 Flash 2-bit on ds4: 81 GB resident, ~42
tokens/s (2026-09-03) — from weights that had sat on disk three weeks waiting for
an engine. PRO 2-bit: 434.5 GiB at 32K context, 13.8 tokens/s (2026-09-06), and
only under `iogpu.wired_limit_mb=480000`, which is **per boot** and was raised by
hand. `status` reads the live cap and never assumes the raised value. On ollama
on 2026-09-12: `gemma4:12b`, `qwen3.6:35b-a3b`, `qwen2.5-coder:32b`,
`qwen2.5-vl:7b`.

**What could not be verified from the record, and is read off the source before
the ds4 adapter is written**, never recalled: the flag that sets the port and the
bind host (only `-m` and `--ctx` are in the record); whether any endpoint reports
load progress, so `state=loading` is inferred from a live process with no
successful completion yet and is labelled as inferred; whether the process has a
shutdown path of its own or is ended through its `com.rowan.ds4` agent; and the
accepted `reasoning_effort` names, which this spec does not name. ds4 fetches
nothing, so `pull --engine ds4` is this tool's own fetch and not a wrapper.

## The verbs

```
nova-local status  [--engine <name>] [--lock <file>] [--list] [--max <n>] [--timeout <s>]
nova-local pull    --engine <name> (--model <ref> | --url <https url> --dir <dir>) --sha256 <digest> --lock <file>
nova-local trust   --engine <name> --model <ref> --sha256 <digest> --lock <file> [--why <text>]
nova-local untrust --engine <name> --model <ref> --reason <text> --lock <file>
nova-local serve   --engine <name> --model <ref> --num-ctx <n> [--keep-alive <d>] [--port <n>]
nova-local stop    --engine <name> --model <ref>
nova-local worker  --engine <name> --model <ref> --out <file> --lock <file> [--deadline <duration>]
```

**No guessed anything.** No default lockfile, no default engine, no default
context, no default quarantine directory. A missing one is exit 2 and `refusing
to guess`. There is no `--force` on any verb: every refusal names a different
command as its remedy. The binary is `nova-local`, and that is its only name.

`status` reports; the rest act. `status` run against a lockfile that does not
exist prints the quickstart notes — the first-run guidance is folded into the verb
a stranger runs first rather than given a verb of its own.

**`status`** answers: is anything answering, what is loaded, at what context,
what does it cost in memory, and what may production use.

```
LOCAL OK engines=2 answering=2 loaded=2 trusted=2 quarantined=1 mem_used=93G mem_total=512G wired_cap=480000
LOCAL ENGINE ollama state=up base=http://127.0.0.1:11434/v1 loaded=1 advertised=4
LOCAL ENGINE ds4 state=up base=http://127.0.0.1:8000/v1 advertised=2 resident=deepseek-v4-flash-q2.gguf ctx=32768
LOCAL MODEL engine=ollama model=gemma4:12b status=TRUSTED num_ctx=32768 keep_alive=30m load=41s gen=2m07s
LOCAL FIT engine=ollama model=gemma4:12b weights=8.1G mem_free=338G wired_cap=480000 fits=beside
LOCAL MORE kind=model shown=1 total=3 remedy="nova-local status --list --max 20"
```

`advertised` is what the server answers to; `resident` is what was actually
loaded. They are different fields because ds4 once listed `deepseek-v4-flash` and
`deepseek-v4-pro` with only Flash loaded, and the pair was printed as resident
(2026-09-03, rowan-tools#60). `resident` comes from the process's own `-m`
argument or the `com.rowan.ds4` plist; when neither can say, it is
`resident=unknown (<why>)` and never what a table wishes were loaded. An engine
that does not answer inside `--timeout` (default 2 s) is `state=timeout` at exit
0; no engine answering at all is exit 1 with one remedy line.

**`pull`** puts weights in QUARANTINED and does nothing else. `--sha256` is
required (rule 3). On ollama the tag is pulled through the daemon and the
manifest digest is read back and compared; on ds4 the GGUF is streamed from
`--url` into `--dir`, hashed as it is written, and renamed into place **only** on
a match, leaving a `.tmp` and no lock entry otherwise. A mismatch is exit 1
naming both digests. A reference already in the lockfile at a different digest is
drift: exit 1, nothing overwritten.

**`trust`** and **`untrust`** are the lockfile's writer, two small verbs.
`trust` moves one triple to TRUSTED, verifying the digest against the bytes on
disk at that moment, with `--why` recorded and dated. `untrust` returns a triple
to QUARANTINED with `--reason`; it never deletes a file, because deleting weights
is a person's `rm` and a tool that could delete what it governs is a tool whose
bug is expensive.

**`serve`** and **`stop`** configure one model. `serve` refuses a model that is
not TRUSTED (exit 1). It measures first and refuses when the box is loud (rule 9).
`keep_alive` is on by default. On ds4, a different model than the one already
resident is exit 1 naming the resident model and the memory both would want:
replacing the resident model is a deliberate act, and the tool gives it one
command and one refusal rather than doing it quietly.

```
SERVE REFUSED engine=ollama model=gemma4:12b: box is loud — load1=14.2 (want <8.0), engines running=2, mem_free=61G (want >16G)
SERVE OK engine=ollama model=gemma4:12b num_ctx=32768 keep_alive=30m load=41s
```

**`worker`** emits a `nova-swarm` worker description for one trusted local model.
It refuses an untrusted triple (rule 6). The description carries the engine's base
URL, the model id in the harness's spelling, the served context, the deadline,
`temperature: 0` and a fixed `seed` for read tasks (rule 11), and no key value
(the `secrets` row). It prints `workers=1` (rule 10).

Its prompt preamble, written into the worker's own `AGENTS.md`:

```
You are one worker. One task, one report, then stop.
Report findings, never decisions. You do not merge, send, delete, or rule on safety.
Quote every rule verbatim with file:line. A finding with no quote does not count.
Append each finding to RESULT.md the moment it exists, never at the end.
Your file budget is <n>. When it is spent, write what you have and stop.
A refused read or write is not the end of your run. Continue with what is inside.
Your deadline is <d>, held outside you. Be brief: a short accurate report beats a long one.
```

`nova-swarm` decodes a worker description strictly, so nothing here invents a
field it does not accept; `workers=1` is printed and the operator passes
`--workers 1`. A `max_workers` field in SPEC-SWARM would carry it properly and is
proposed as owed work, not made here.

## Exit codes

| code | meaning |
|------|---------|
| 0 | the verb ran and passed: status printed, weights quarantined, a triple trusted, a model serving or stopped, a description written |
| 1 | the verb ran and said **NO**: no engine answering; a digest mismatch or drift; a `trust` of something never pulled; a `serve` or `worker` for a model that is not TRUSTED; a `serve` refused because the box is loud; a ds4 `serve` over a different resident model |
| 2 | could not run: a missing flag (`--sha256`, `--num-ctx`, `--lock`, `--engine` included), an unreadable lockfile, a non-loopback host in any engine config, an unknown engine name, bad invocation |

A refusal says **what the input wants**, in one line, with the command that
satisfies it, and never prints the contents of a path it could not use. Every
independent problem is reported at once.

## Output grammar

```
LOCAL OK engines=<n> answering=<n> loaded=<n> trusted=<n> quarantined=<n> mem_used=<n> mem_total=<n> wired_cap=<n|unset>
LOCAL ENGINE <name> state=<up|loading|down|timeout> base=<url> [loaded=<n>] [advertised=<n>] [resident=<file|unknown (<why>)>] [ctx=<n>]
LOCAL MODEL engine=<e> model=<ref> status=<TRUSTED|QUARANTINED> [num_ctx=<n>[ default]] [keep_alive=<d>] [load=<t>] [gen=<t>]
LOCAL FIT engine=<e> model=<ref> weights=<n> mem_free=<n> mem_total=<n> wired_cap=<n|unset> fits=<beside|quiet|no|unknown>
LOCAL FAIL <what>: <reason>
LOCAL MORE kind=<kind> shown=<n> total=<n> remedy=<command>
PULL OK engine=<e> model=<ref> digest=<sha256:…> bytes=<n> status=QUARANTINED
PULL FAIL <ref>: <reason>
TRUST OK engine=<e> model=<ref> digest=<sha256:…> [why=<text>]
TRUST REFUSED <ref>: <reason> — remedy: <command>
UNTRUST OK engine=<e> model=<ref> status=QUARANTINED reason=<text>
SERVE OK engine=<e> model=<ref> num_ctx=<n> keep_alive=<d> load=<t> [pid=<n>]
SERVE REFUSED <ref>: <reason>
STOP OK engine=<e> model=<ref> [already stopped]
WORKER OK engine=<e> model=<ref> out=<path> workers=1 num_ctx=<n> temperature=0 seed=<n> deadline=<d>
WORKER REFUSED <ref>: <reason> — remedy: <command>
```

`OK` lines go to stdout; `FAIL` and `REFUSED` lines go to stderr. An event is
exactly one line, and nothing a caller supplies can add a second.

## First run

```
$ nova-local status --lock storage/models.lock.json
$ nova-local pull --engine ollama --model gemma4:12b --sha256 sha256:9f2c… --lock storage/models.lock.json
$ nova-local trust --engine ollama --model gemma4:12b --sha256 sha256:9f2c… --lock storage/models.lock.json --why "first local worker"
$ nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768
$ nova-local worker --engine ollama --model gemma4:12b --out workers/gemma.json --lock storage/models.lock.json
$ nova-local pull --engine ds4 --url https://huggingface.co/antirez/…/flash-q2.gguf --sha256 sha256:1d70… --dir weights --lock storage/models.lock.json
```

Six commands from nothing to a model serving and a worker description
`nova-swarm run --workers 1 --worker workers/gemma.json` accepts; the sixth shows
the other engine, where the weights come by URL because they are not in any
catalogue. The `README.md` `### First run` is these six lines with their output,
executed by a test.

## Deliberately not in this tool

- **`eval` and `compare`** — scoring a model against a reference read, and
  antirez's audit technique of diffing a deterministic local continuation against
  a remote one. Glenn, 2026-09-12: *"if we want to eval, it is another tool,
  perhaps with more deps."* They live in a **separate tool if one is ever built**,
  with its own dependencies, adopted or ignored on its own merits; today the work
  is `nova-swarm` running the same task against two workers plus a scoring page.
  **`nova-local` stays dependency-free and never grows an `eval` verb.** The one
  seam kept open for it: a QUARANTINED model may be run by an evaluation, which is
  why `pull` and `trust` are separate verbs at all.
- **`fit`** as a verb — folded into `status`, which prints one `LOCAL FIT` line
  per model. Two verbs where one line does.
- **`policy`** (a private repository's read may not go to a third-party API) —
  that ruling belongs to whoever dispatches the work, so it lives in
  **`nova-swarm`'s dispatcher**, where the choice between a local worker and an
  API worker is actually made. A governor that ruled but did not dispatch would be
  a ruling nobody had to consult.
- **usage rows** — `nova-tokens` reads the engine directly. A tier that costs no
  money still costs wall clock and should be in the ledger, and the place that
  writes ledger rows is the ledger's tool.
- **inference** — no `ask`, no `read`, no `triage`. `rowan-local` is the estate's
  one-prompt client and `nova-swarm` runs the workers.
- **discovery and ranking** — no feed reader, no registry search, no champion
  file, no "recommended". A model name found on a page, in a feed or in a model's
  own output is data, never a pull target.

## Tests this spec demands

Each runs inside `t.TempDir()` against fake engine servers on loopback, with no
network, and each must be seen red before it is trusted.

1. **The stranger test, and the reason the tool exists.** A run driven only by
   `README.md` — the six first-run lines, against fake engines — ends with a model
   configured at the given context and a `workers/gemma.json` that `nova-swarm`'s
   own strict decoder accepts with no unknown field. The **new-user hour** (a
   fresh line, the docs only, no other help) is run before the tool is called
   done, and its transcript is attached to the PR.
2. The import set is the standard library: a test walks the build's package
   imports and fails on any third-party path; `go.mod` has no `require` beyond the
   toolchain.
3. Adding an adapter touches one file: a third fake adapter is registered in a
   test and every verb works against it with no change to any verb's code.
4. `pull` without `--sha256` is exit 2; a mismatched digest leaves nothing on
   disk and names both digests; a second digest under an existing reference is
   exit 1 drift with nothing overwritten.
5. `(engine, model, digest)` is the key: the same tag at two digests is two
   entries, and trusting one does not trust the other.
6. `TestWhatTheWriterWritesTheGateReadsAsWritten`: every entry `trust` writes is
   accepted by the gate in the same binary, and no sequence of these verbs can
   produce a lockfile the gate refuses.
7. `worker` and `serve` both refuse a QUARANTINED triple at exit 1 with a remedy;
   no flag on either overrides it.
8. `serve` without `--num-ctx` is exit 2 and `refusing to guess`; a loaded model
   with no written config shows `num_ctx=4096 default`; after a `serve` the other
   models' configs are unchanged.
9. `serve` sets `keep_alive`; `status` prints `load=` and `gen=` as separate
   fields, and a cold start with a fast generate is visibly a cold start.
10. `serve` on a fake box over the loud threshold is exit 1 with the measured
    numbers in the line; under it, exit 0.
11. `worker`'s output carries `temperature: 0` and a fixed seed for a read task;
    two emissions with the same inputs are byte-identical; the line says
    `workers=1`; the preamble contains all seven sentences.
12. `LOCAL FIT` is against free memory read at the moment of the call: with
    338G free, a 23G model is `fits=beside` and a 434G model is `fits=quiet`; with
    the same total and 40G free the 23G model becomes `fits=quiet`; weights larger
    than total memory, or over an unraised wired cap that no quiet box would lift,
    are `fits=no`; `wired_cap=unset` when the sysctl is not set. The free figure is
    re-read per call and never cached between calls.
13. A fake ds4 advertising two ids with one loaded gives `advertised=2
    resident=<the -m file>`; with no process and no plist it gives
    `resident=unknown (<why>)` and never a table's preference.
14. `status` over a lockfile with 400 entries prints at most the documented lines
    plus one `MORE`, under a fixed byte budget; with nothing answering it prints
    one remedy line and no model rows.
15. No prompt text, no model output and no value read from any file outside the
    lockfile reaches an argv, a log or a printed line; the ds4 child's argv holds
    the model path and the context and nothing resembling a prompt; the binary
    imports no shell.
16. A non-loopback host in any engine config is exit 2, named; a port that moves
    is accepted.

Plus the house standard: a usage banner ending in a runnable `example:` block,
refusals reporting every independent problem at once, a `### First run` in
`README.md` pinned by an executing test, and three-GOOS builds green.

## The work list

Go under `cmd/nova-local`, the way `cmd/nova-bus` is built: standard library
only, no hardcoded paths, no default paths, the exit grammar above,
`internal/oneline` for every printed value, `internal/bounded` for every listing.

1. **`internal/local/lock.go`** — the triple as key, `status`, `why`, `reason`,
   dates; atomic write (temp + rename); strict decode; **the gate in the same
   file**. Tests 4, 5, 6, 7.
2. **`internal/local/engine.go`** — the adapter interface (name, start, stop,
   list), the loopback pin, the `--timeout` budget, the registry of adapters.
   Test 3, 16.
3. **`internal/local/ollama.go`** — the daemon's model list, per-model options
   with `num_ctx` and `keep_alive` written and read back, the manifest digest.
   Tests 8, 9.
4. **`internal/local/ds4.go`** — the process with the model path, context and
   port; advertised versus resident; the four unverified items **read off
   `ds4_server.c` and the live plist before this file is written**. Tests 13, 15.
5. **`internal/local/pull.go`** — the tag path and the URL path, streamed hashing,
   temp-then-rename, drift. Test 4.
6. **`internal/local/fit.go`** — weights against free unified memory read at the
   moment of the call, plus the live wired cap; the three verdicts. Test 12.
7. **`internal/local/ready.go`** — load average and running engines, the loud
   thresholds, the refusal with its numbers. Test 10.
8. **`internal/local/worker.go`** — the description in `nova-swarm`'s schema, the
   harness spelling per engine, temperature and seed, the preamble. Test 11.
9. **`cmd/nova-local/main.go`** — the seven verbs, the banner, the quickstart
   notes folded into `status`, refusals that name the next command. Tests 1, 14.
10. **`README.md`'s `### First run`** — the six lines, executed by test 1.

**Owed outside this tool.** A sentence for SPEC-SWARM so `workers=1` is carried
by the schema rather than by an operator: *"A worker description may declare
`max_workers: <n>`. `run --workers <n>` above it is a refusal, not a clamp: a
local engine is a queue, and a pool that reports six workers against one engine
reports a number that is not true."* And `llama.cpp`-server and `mlx-lm`
adapters, each one file, when somebody wants them.

## DECIDED IN THE DRAFT

The drafter's choices, not the record's. Each is a place to push back.

1. **Seven verbs, no others.** `eval`, `compare`, `fit` and `policy` are cut;
   `fit` is folded into `status` as a line and `quickstart` into `status` as its
   empty-lockfile output.
2. **`trust` takes `--why`, not `--evidence` and not `--caller`.** The earlier
   draft required an evidence file and a named caller; with `eval` out of the tool
   there is no evidence file it could check, and a free-text reason that is dated
   and kept is what remains honest.
3. **The 6-day soak is not enforced here.** It is a practice
   (`memory/model-trust.md`) that this tool records the dates for — `pull` stamps
   `since`, `trust` stamps its own date — and a stranger's first run would be
   blocked for six days by an enforcement they cannot understand yet. The soak
   belongs with the evaluation tool, not the runner. **This is the sharpest cut in
   the draft and the one most likely to be wrong.**
4. **`untrust` returns to QUARANTINED and never deletes weights.**
5. **`--sha256` is required on ollama pulls too**, where it is the manifest digest
   read back after the pull rather than one the person computed.
6. **`serve` refuses an untrusted model**, not only `worker`. A model serving is a
   model something will call.
7. **The loud thresholds are the adapter's, printed, not hardcoded in a rule
   here**: the spec demands the numbers appear in the refusal, not what they are.
8. **`status --timeout` defaults to 2 s** and a slow engine is `state=timeout` at
   exit 0, so `status` stays a verb a person runs between edits.
9. **`workers=1` is printed, not encoded**, because `nova-swarm`'s decoder is
   strict; the schema change is proposed as owed work.
10. **`llama.cpp` and `mlx-lm` are named and not built.** Naming them is the test
    of rule 2: if either would need a change to a verb, rule 2 is false.
11. **No `--force` anywhere**, and every refusal's remedy is a different command.
12. **The ds4 unknowns are listed rather than guessed**, and the adapter is not
    written until they are read off the source.
