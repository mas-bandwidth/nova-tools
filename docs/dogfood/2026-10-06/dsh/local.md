# Dogfood: nova-local — 2026-10-06, dsh

One friend, one tool, cold. Before and during the run I read only the tool's
own surfaces — `nova-local -h`, `nova-local help`, every verb's `-h`, and its
page under docs/ (SPEC-LOCAL.md) — then used every verb at least once with its
real flags, refusals included; after the run ended, one sibling report's tail
was checked for this file's layout only. No engine lives on this machine, so
the cold start was real twice: with nothing answering on loopback (status,
serve, worker against a silent port), and against a real ollama v0.40.0 stood
up on a bench at its tailnet address, serving a scratch shared store under a
scratch AI root (`NOVA_AI_ROOT` in a temp directory), models pulled by hand
(`smollm2:135m`, `qwen2.5:0.5b`). The binary was built from this checkout at
6ec8bb02e. About 25 minutes end to end. Paths and addresses are elided below
to keep the record general; every command ran exactly as shaped. Every finding
is recorded, not fixed.

## Findings

1. `nova-local serve --engine ollama --model smollm2:135m --num-ctx 4096 --base http://<tailnet>:11434/v1 --dry-run`

        SERVE OK engine=ollama model=smollm2:135m serve_as=smollm2-4k digest=sha256:9077fe9d2ae1a4a41a868836b56b8163731a8fe16621397028c2c76f838c6907 num_ctx=4096 keep_alive=30m temperature=0 seed=unset created=yes load=0s mem_used=97740939264 mem_free=32712683520 mem_total=130453622784 load1=94.00 engines=1 store=<scratch>/shared/models/ollama shared=yes dry_run=true
        exit=0

   The engine's own list afterwards still shows no `smollm2-4k`: nothing was
   created and nothing loaded (the dry run keeps its promise). Expected: the
   line's `created=` should not read `yes` — the plan of an act the dry run did
   not do, with `load=0s` for a warm-up it did not send — `created=would`, or
   the fields dropped under `--dry-run`; as printed, plan and act differ only by
   the trailing `dry_run=true`. Grade: NEXT.

2. `nova-local serve --engine ollama --model smollm2:135m --num-ctx 2048 --dry-run` (nothing answering on loopback)

        SERVE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
        exit=1

   `worker ... --dry-run` with nothing answering prints the same shape
   (`WORKER FAILED: --dry-run was given and the verb never read it
   (Call.DryRun); it may have written`, exit 1, and no file written).
   Expected: the refusal plain serve prints with no engine (`no engine answers;
   start one, such as the ollama daemon; run: ollama serve`) — instead the
   real error is replaced by an internal invariant (`Call.DryRun`) with no
   `run:` remedy, and "it may have written" contradicts a dry run that wrote
   nothing. Grade: URGENT.

3. `nova-local status --engine ollama --base http://<tailnet>:11434/v1` (the engine on the bench, asked from this machine)

        STATUS OK engines=1 answering=1 loaded=0 models=1 mem_used=201144762368 mem_free=348611051520 mem_total=549755813888 wired_cap=unset load1=9.27
        STATUS ENGINE name=ollama state=up base=http://<tailnet>:11434/v1 loaded=0 advertised=1 store=<the engine's store, resolved on this machine> shared=no: the store is not under the shared model directory <this machine's AI root>/shared/models
        exit=0

   The same engine asked on its own machine prints `store=<its own path>
   shared=yes`. And `serve ... --require-shared-store --base
   http://<tailnet>:11434/v1` refuses from here at exit 1: `SERVE FAILED: the
   store <the engine's store, resolved on this machine> is not shared ...; run:
   mkdir -p <this machine's AI root>/shared/models/ollama and point the engine's
   store at it`. Expected: rule 3 sends callers at tailnet engines, but rule
   15's check reads the engine's store path on the caller's filesystem: a store
   that is shared on the engine's machine prints `shared=no` here (not
   `unknown`, though the path cannot be read on this machine at all), the path
   is resolved through this machine's home symlink so a remote store looks
   local, and `--require-shared-store` refuses a genuinely shared store with a
   remedy that mkdirs where the engine's store cannot be. Grade: URGENT.

4. `nova-local worker --engine ollama --model gemma4-32k --out ./gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir $PWD/cmd/nova-local/testdata/home --key-file $PWD/cmd/nova-local/testdata/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m` (the banner's third example, run as printed from a directory that is not a checkout)

        WORKER REFUSED: --worker-dir <cwd>/cmd/nova-local/testdata/home is not a directory: mkdir -p <cwd>/cmd/nova-local/testdata/home; run: nova-local help
        WORKER REFUSED: --key-file <cwd>/cmd/nova-local/testdata/local.key does not exist; the local engine wants no key, and nova-swarm a non-empty file: printf 'local\n' > <cwd>/cmd/nova-local/testdata/local.key && chmod 600 <cwd>/cmd/nova-local/testdata/local.key; run: nova-local help
        exit=2

   Expected: an `example:` line runs where a stranger is;
   `$PWD/cmd/nova-local/testdata/...` exists only inside a checkout of this
   repository, so the worker example exits 2 — a broken example by the onboarding
   standard's own letter — for anyone running the installed binary. The
   refusals themselves are perfect (one turn to fix); the example should stand
   where the binary is. Grade: NEXT.

5. `nova-local serve --stop --engine ollama --model smollm2-2k` (nothing answering)

        SERVE FAILED: the engine did not unload smollm2-2k: Get "http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused
        exit=1

   Expected: the line plain `serve` prints with no engine (`no engine answers;
   start one, such as the ollama daemon; run: ollama serve`); the raw
   transport error names no remedy and reads like the tool's own failure.
   Grade: NEXT.

6. `nova-local serve --stop --engine ollama --model smollm2-99k --base http://<tailnet>:11434/v1` (a tag nobody ever served)

        SERVE OK stopped=yes engine=ollama model=smollm2-99k
        SERVE NOTE already stopped: smollm2-99k was not loaded
        exit=0

   Expected: `stopped=yes` states an act that did not happen for a tag that
   does not exist on the engine; the NOTE corrects it one line later, but the
   OK line should not claim the stop. Grade: NEXT.

7. `nova-local status --engine ollama --base http://<tailnet>:11434/v1 --list` (on linux, the bench side)

        STATUS OK engines=1 answering=1 loaded=0 models=1 mem_used=97508462592 mem_free=32945160192 mem_total=130453622784 load1=88.22
        STATUS ENGINE name=ollama state=up base=http://<tailnet>:11434/v1 loaded=0 advertised=1 store=<scratch>/shared/models/ollama shared=yes
        STATUS MODEL engine=ollama model=smollm2:135m digest=sha256:9077fe9d2ae1a4a41a868836b56b8163731a8fe16621397028c2c76f838c6907 weights=270898672 loaded=no

   Expected: SPEC-LOCAL.md's `STATUS OK` line carries `wired_cap=<n|unset>`
   (rule 10, "unset when none"), and this machine's darwin line does print
   `wired_cap=unset`; on linux the field is absent entirely, so the spec's
   line template and the code disagree on the line's shape. Grade: NEXT.

## What worked, for the record

The rest of the run was clean: the bare command names its door; `help`, `-h`
and `help <verb>` are identical and exit 0; every refusal names every problem
at once and what each flag wants (a worker batch with a missing key file, a
relative worker dir, a bad `--usage` and a bad `--deadline` in one run, each
with a paste-able remedy); the derived tag was created, reused unchanged on a
second serve, refused on a differing seed naming both values with
`ollama rm <tag>`, and refused on a differing digest naming both; `--max-load`
and `--min-free` refusals carried the measured values; `serve --stop` on a
loaded model printed `stopped=yes` and on an unloaded one the NOTE; `worker`
wrote exactly the documented schema, `--dry-run` wrote nothing; `--max`
bounded the listing with a `MORE` line naming the flag; `--json` matched the
lines on every verb checked; the exit table held everywhere probed.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

Ran on a bench before this file was written and again with its findings in
place (a third run of the final bytes, green the same way):

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci   # before this file
    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.560s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	17.678s

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci   # with this file's findings
    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.616s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	19.430s

The card's own named test does not exist at this tip, so its line answers no
tests and the gate ran as the packages the card names:

    go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent
    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	0.014s [no tests to run]

READ 9/10 — the banner, every verb's -h and the spec page answered everything
before my first command, and every refusal said what its flag wanted with a
remedy I could paste; only the checkout-only worker example and the dry-run
`created=yes` made me re-read.

USE 8/10 — every verb did its real work first try against a real engine (a
pulled model, a derived tag made and reused, a warm-up timed, a worker
description written to the documented schema), and each refusal carried measured
values; the cost was the dry-run failure wording, the caller-side store verdict
against a tailnet engine, and the raw stop error.

urgent=2 next=5
