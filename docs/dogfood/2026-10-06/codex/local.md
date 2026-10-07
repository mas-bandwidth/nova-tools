# nova-local dogfood, 2026-10-06 (codex)

Tool: nova-local. Build: `nova-local v1.0.1-0.20261006183932-5844884e267c darwin/arm64 go1.26.6`.
Run cold, from the binary's own help (`nova-local`, `nova-local help`, `nova-local <verb> -h`)
and its page under `docs/` (`docs/CLI.md`, section `## nova-local`, and the spec it links,
`docs/SPEC-LOCAL.md`) only, with every verb at least once against one scratch AI root, one
scratch out directory and the tool's own `cmd/nova-local/testdata` fixture. No engine answered
at `http://127.0.0.1:11434` or at a tailnet address in 100.64.0.0/10, and this card's rules
forbid starting one here ("Never start a server on this machine"), so every engine-facing form
ran to its failure or refusal line and the three success paths (`status` OK with its ENGINE and
MODEL lines, `serve` create/load/`serve_as=`, `worker`'s JSON write) are reported not done
below. Commands were typed with

    S=$PWD/scratch
    export NOVA_AI_ROOT=$S/ai
    mkdir -p $S/ai/shared/models/ollama $S/out

so `status` read a scratch store and `worker` wrote under `$S/out`; no real store was touched.
The card names the base `5844884e267c`, and that is the checkout these findings were recorded
at. The card's own named test does not exist at this tip (see Gate).

## 1. `serve --dry-run` fails the skeleton's unimplemented-dry-run guard — URGENT

**Command:**

    nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --dry-run

**Printed:**

    SERVE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written

exit 1, on stderr. `--dry-run` is in serve's own usage line and its flags list ("print what the
verb would write and write nothing"), and the standard gives every verb with `DryRun` its dry
run by construction. A serve writes nothing on this machine anyway ("nothing is written on this
machine"), so the dry run is exactly the read a stranger wants before it makes a derived tag and
loads a model.

**Expected:** `SERVE OK ... dry_run=true` naming the tag it would create (`gemma4-32k`), the
context and the box facts it reads first, and dialling nothing that changes the engine; instead
the one flag that lets a reader see a serve before it makes one answers with the skeleton's
internal guard sentence and no remedy.

**Grade:** URGENT

## 2. `worker --dry-run` fails the same guard on the verb whose only effect is one file — URGENT

**Command:**

    nova-local worker --engine ollama --model gemma4-32k --out ./gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir $PWD/cmd/nova-local/testdata/home --key-file $PWD/cmd/nova-local/testdata/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m --dry-run

**Printed:**

    WORKER FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written

exit 1. The same command without `--dry-run`, with no engine answering, wrote nothing and
printed the clean failure `WORKER FAILED: the engine's models could not be read: Get "http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused; run: nova-local status --engine ollama`, so the real path already refuses to write on a bad engine.

**Expected:** the plan the real run would write — the worker description JSON — because worker's
own effect line is "local write", its page calls the description "the one JSON file nova-swarm
reads", and `--dry-run` is the documented way to read it before it lands.

**Grade:** URGENT

## 3. `serve --stop` names the raw dial error where `serve` names the remedy — NEXT

**Command:**

    nova-local serve --stop --engine ollama --model gemma4-32k

**Printed:**

    SERVE FAILED: the engine did not unload gemma4-32k: Get "http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused

exit 1, and the line carries no `run:` clause. The serving form on the same absent engine prints
the clean line `SERVE FAILED: no engine answers; start one, such as the ollama daemon; run: ollama serve`.

**Expected:** the stop path should read the engine the way `serve` does and carry the same
remedy, because `--stop` is the other half of the one documented verb and rule 12 makes it "one
request with `keep_alive 0`"; a reader who asked to unload a model is told a Go dial error
instead of the command that starts the engine.

**Grade:** NEXT

## 4. `--keep-alive` takes any string and is never held to a duration — NEXT

**Command:**

    nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --keep-alive nonsense

**Printed:**

    SERVE FAILED: no engine answers; start one, such as the ollama daemon; run: ollama serve

exit 1. Every other typed flag is held before the engine is dialled (`--min-free nonsense` and
`--max-load notanumber` are refused at exit 2; `--deadline bogus` is refused), and the usage line
spells this one `[--keep-alive <d>]` with the default `30m`.

**Expected:** exit 2 naming the Go duration it wants, so a typo does not reach an engine that
would keep a model loaded for a value it cannot parse.

**Grade:** NEXT

## 5. `--base` with a port no host can have is reported as an absent engine, not refused — NEXT

**Command:**

    nova-local status --engine ollama --base http://127.0.0.1:99999 --timeout 500ms

**Printed:**

    STATUS FAILED: no engine answers (1 asked); start one, such as the ollama daemon; run: ollama serve

exit 1. A port above 65535 cannot be an engine's `/v1`: the help says `--base` wants "the
engine's /v1 URL, loopback or a tailnet address", and the tool refuses a scheme-less or
non-http value at exit 2 with the shape it wants.

**Expected:** the same shape refusal for a port that cannot be connected to; the absent-engine
exit 1 sends the reader to `ollama serve` for what is a typo in a flag.

**Grade:** NEXT

## 6. `--expect-digest` takes any string, not the `sha256:...` it documents — NEXT

**Command:**

    nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --expect-digest deadbeef

**Printed:**

    SERVE FAILED: no engine answers; start one, such as the ollama daemon; run: ollama serve

exit 1. The flag's own text is "the digest the model must have (sha256:...)", and rule 4 makes a
differing digest exit 1 naming both values, so a value that is not a digest can only ever
refuse — after a model is pulled and read.

**Expected:** exit 2 refusing a value that is not the `sha256:...` shape it asks for, the way
`--engine`, `--min-free` and `--max-load` hold their shapes.

**Grade:** NEXT

## 7. `worker` dials the engine before it checks the harness, the out directory or the board — NEXT

**Command:**

    nova-local worker --engine ollama --model gemma4-32k --out ./gemma.json --name gemma --harness nosuchcmd12345 --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir $PWD/cmd/nova-local/testdata/home --key-file $PWD/cmd/nova-local/testdata/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m

**Printed:**

    WORKER FAILED: the engine's models could not be read: Get "http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused; run: nova-local status --engine ollama

exit 1. `--harness` is documented "the harness command, found on PATH", `--out` "the file the
description is written to; its directory must exist", and `--board` "the board issue the worker
reports to, owner/repo#n"; none of the three was named, and the same engine line answers a
missing `--out` directory and `--board notaboard` too. What worker does check without an engine
it names well: one run with no flags prints all eleven missing required flags at once.

**Expected:** every precondition the tool can check for itself before the dial, named in the
same run (a missing `nosuchcmd12345` on PATH, a missing out directory, a board that is not
`owner/repo#n`), so a reader with two problems is not told one and then made to wait on a
2-second dial that cannot change either answer.

**Grade:** NEXT

## 8. A threshold refusal's remedy is `nova-local status --list`, which does not move the number — NEXT

**Command:**

    nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --max-load 0.0001

**Printed:**

    SERVE FAILED mem_used=216107368448 mem_free=333648445440 mem_total=549755813888 wired_cap=unset load1=20.55: load1 is 20.55 and --max-load asks at most 0.0001; run: nova-local status --list

exit 1; the JSON twin carries the same `remedy`. The line names what was measured and what was
asked, as rule 8 says, and the box facts are real.

**Expected:** a breadcrumb that changes the number — a `--max-load` above the measured load, or
to wait — the way the `--base` and `--engine` refusals name the value they want; `status --list`
is another read of the same box and leaves the serve unable to run.

**Grade:** NEXT

## What was not done

- No engine answered at the default base or at a tailnet base, and the card forbids starting a
  server on this machine, so `status` never printed an ENGINE `state=up` line, its `--list`
  MODEL lines or the `store=`/`shared=` line, `serve` never created or loaded a derived tag and
  never printed `serve_as=` or `load=`, `serve --stop` never reached `already stopped`, and
  `worker` never wrote a description. The `--require-shared-store` refusal and the
  `--expect-digest`-differs refusal need an engine to reach and were not seen.
- `--json` was not exercised on a success value, only on refusals and failures.

## What worked (no finding, kept short)

The banner, `help`, every verb's `-h` and `help <verb>` answer on stdout at exit 0; the bare
command, an unknown verb and an unknown flag each name the door (`LOCAL REFUSED: no verb given; the verbs are status, serve, worker, version; run: nova-local help`, `STATUS REFUSED: unknown flag --bogus; the flags of status are --base, --engine, --json, --list, --max, --timeout; run: nova-local status -h`), and a positional is refused as one. Refusals name what an input wants:
`--num-ctx` missing, zero or not a multiple of 1024 (`use 29696 or 30720`), a `--seed` that is
not a whole number, malformed `--min-free`/`--max-load`, `--engine` outside `ollama`, a `--base`
that is not http or resolves off loopback and 100.64.0.0/10, `--usage` outside `opencode|none`,
`--deadline` that is not a Go duration, an empty `--key-file` with the `printf` that writes one,
a relative `--worker-dir`, and `--harness-args` without `{model}`. `--json` is the same value as
the lines for `version`, for refusals (`status` "refused") and for failures (`serve` "failed"),
and the threshold path reads the box for real (`mem_used`, `mem_free`, `mem_total`,
`wired_cap=unset`, `load1`).

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	1.888s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	13.422s

The card's named test `TestDocsTreeIsConsistent` does not exist at this tip:
`go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent` reports
`ok  github.com/mas-bandwidth/nova-tools/internal/docs  0.010s [no tests to run]`. The two
packages above are the real gate, run on a build bench
against this report's own new directory. This report is the first `docs/dogfood/` file at this
base, so the commit also carries the one catalog row (`internal/docs/catalog.go`) and the one
regenerated map row (`docs/AGENTS.md`) that the docs guard requires for the new directory,
exactly as the first dogfood record on the integration branch did.

READ 8/10 — the banner, the per-verb helps, the refusal grammar and the JSON twin answer a cold
reader fast and truly, and the tool refuses to guess on every input it can check; held down by
the two documented `--dry-run` paths that answer with the skeleton's internal guard, the three
unvalidated flags (`--keep-alive`, `--base` port, `--expect-digest`), the stop path's raw dial
error, worker's preconditions that wait behind the engine, and a threshold remedy that does not
move the number.

USE 4/10 — every verb ran at least once with its real flags against a scratch AI root and a
scratch out directory, and the refusals and failures were read at the tip; the engine-facing
half could not run at all here (no engine, and starting one is forbidden by the card), so
`status` OK, `serve` and `worker`'s write are unproven by use.

urgent=2 next=6
