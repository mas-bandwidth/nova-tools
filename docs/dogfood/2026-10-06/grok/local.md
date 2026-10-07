# nova-local dogfood, 2026-10-06 (grok)

Tool: nova-local. Build: `nova-local v1.0.1-0.20261006183932-5844884e267c darwin/arm64 go1.26.6`.
Read cold, from the binary's own help only (`nova-local`, `nova-local help`, `nova-local <verb> -h`)
and its page under `docs/` (`docs/CLI.md`, section `## nova-local`, and the spec it links,
`docs/SPEC-LOCAL.md`), then used for real against one scratch AI root and one scratch out
directory. No engine answered at `http://127.0.0.1:11434` or at a tailnet address in
100.64.0.0/10, and this card's rules forbid starting a server on this machine, so every
engine-facing form ran to its failure or refusal line; the three success paths (`status` OK with
its ENGINE and MODEL lines, `serve` create/load/`serve_as=`, `worker`'s JSON write) are reported
not done at the end. The commands below were typed with

    S=$PWD/scratch
    export NOVA_AI_ROOT=$S/ai
    mkdir -p $S/ai/shared/models/ollama $S/out

so `status` read a scratch store and `worker` was pointed at a scratch out path. The card names
the base `5844884e267c` and that is the checkout these findings were recorded at.

## 1. `serve --dry-run` fails the skeleton's unimplemented-dry-run guard — URGENT

**Command as typed:**

    nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --dry-run

**Printed (first 3 lines):**

    SERVE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written

exit 1. `--dry-run` is in serve's own usage line and its flags list ("print what the verb would
write and write nothing"), and `docs/SPEC-LOCAL.md` says "`--dry-run` reads all of it and creates
and loads nothing".

**Expected:** the plan the real run would take — the derived tag `gemma4-32k`, the context, the
box facts read first — printed on stdout at exit 0, as the flag promises; instead the one flag
that lets a stranger see a serve before making one answers with the skeleton's internal guard and
a sentence ("it may have written") that is true of neither the dry run nor the real serve.

**Grade:** URGENT

## 2. `worker --dry-run` fails the same guard on the verb that writes one file — URGENT

**Command as typed:**

    nova-local worker --engine ollama --model gemma4-32k --out ./gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir $PWD/cmd/nova-local/testdata/home --key-file $PWD/cmd/nova-local/testdata/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m --dry-run

**Printed (first 3 lines):**

    WORKER FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written

exit 1. The same command without `--dry-run` (and with no engine answering) refused before
writing anything, so the real path already does the safe thing; the dry run is the only path that
fails.

**Expected:** the worker description the real run would write, on stdout, with nothing at `--out`,
because worker's own effect line is "local write" and `--dry-run` is its documented plan.

**Grade:** URGENT

## 3. `serve --stop` prints a raw dial error where `serve` prints the remedy — NEXT

**Command as typed:**

    nova-local serve --stop --engine ollama --model gemma4-32k

**Printed (first 3 lines):**

    SERVE FAILED: the engine did not unload gemma4-32k: Get "http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused

exit 1, with no `run:` clause, while `serve` on the same absent engine prints
`SERVE FAILED: no engine answers; start one, such as the ollama daemon; run: ollama serve`.
`docs/SPEC-LOCAL.md` gives the verb the shape `SERVE FAILED: <reason>; run: <command>`.

**Expected:** the same "start an engine" remedy the serving half prints; a reader who asked to
unload a model is handed a Go dial error and no next command.

**Grade:** NEXT

## 4. `status --base` cannot stand alone although the usage line lists it as its own flag — NEXT

**Command as typed:**

    nova-local status --base http://<tailnet-host>:11434/v1 --timeout 1s

(the typed address was one host in 100.64.0.0/10, written here as the range's placeholder)

**Printed (first 3 lines):**

    STATUS REFUSED: --base names one engine's endpoint, so it wants --engine; run: nova-local help

exit 2. The usage line in `nova-local -h`, in `status -h` and in `docs/CLI.md` is
`nova-local status [--engine <name>] [--base <url>] ...`, which reads as two independent optional
flags; with one engine built, a reader who wants to check one endpoint has to guess the coupling.

**Expected:** either `--base` alone selects the only engine, or the usage line and the flag text
say `--base` is only valid with `--engine`, so the refusal is not the first place the coupling is
stated.

**Grade:** NEXT

## 5. The `--num-ctx` remainder remedy names 0, which the same flag refuses — NEXT

**Command as typed:**

    nova-local serve --engine ollama --model gemma4:12b --num-ctx 1000

**Printed (first 3 lines):**

    SERVE REFUSED: --num-ctx 1000 is not a multiple of 1024, so the derived tag's name would round; use 0 or 1024; run: nova-local help

exit 2. The two nearest multiples are named, but `--num-ctx 0` is itself refused:
`SERVE REFUSED: --num-ctx wants the context in tokens, above 0 (got 0); refusing to guess`.

**Expected:** a remedy whose every named value can be typed — the two nearest usable multiples
(for 1000, only 1024) — rather than one that sends the reader to a second refusal.

**Grade:** NEXT

## 6. `--keep-alive` takes any string and is never held to a duration — NEXT

**Command as typed:**

    nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --keep-alive bogus

**Printed (first 3 lines):**

    SERVE FAILED: no engine answers; start one, such as the ollama daemon; run: ollama serve

exit 1, the engine dial, not a flag refusal. Every other typed serve flag is held before the
dial (`--min-free 512MB`, `--max-load lots`, `--seed abc` all refuse at exit 2), and the usage
line spells this one `[--keep-alive <d>]` with the default `30m`.

**Expected:** exit 2 naming the Go duration it wants, so a typo cannot reach an engine and ask it
to hold a model for a value it cannot parse.

**Grade:** NEXT

## 7. `--expect-digest` takes any string, not the `sha256:...` it documents — NEXT

**Command as typed:**

    nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --expect-digest nope

**Printed (first 3 lines):**

    SERVE FAILED: no engine answers; start one, such as the ollama daemon; run: ollama serve

exit 1. The flag's own text is "the digest the model must have (sha256:...)", and rule 4 makes a
differing digest exit 1 only after a model is read, so a value that is not a digest can never do
anything but refuse later.

**Expected:** exit 2 refusing a value that is not the `sha256:...` shape, the way `--engine`,
`--min-free` and `--max-load` hold their shapes before the dial.

**Grade:** NEXT

## 8. `worker` dials the engine before it checks the harness, the out directory or the board — NEXT

**Command as typed:**

    nova-local worker --engine ollama --model gemma4-32k --out ./nodir/w.json --name g --harness nosuchcmd12345 --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir $PWD/repo/cmd/nova-local/testdata/home --key-file $PWD/repo/cmd/nova-local/testdata/local.key --env-var X --usage none --deadline 20m --board not-a-board

**Printed (first 3 lines):**

    WORKER FAILED: the engine's models could not be read: Get "http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused; run: nova-local status --engine ollama

exit 1, and none of the three local problems is named. `--harness` is documented "found on PATH",
`--out` "the file the description is written to; its directory must exist", and `--board` "the
board issue the worker reports to, owner/repo#n".

**Expected:** every problem the tool can check for itself before the dial, named in the same run,
as the flags-only path already does (one run with nothing passed prints all eleven missing
required flags at once).

**Grade:** NEXT

## 9. One failing engine never prints the documented ENGINE state line, and a timeout is a hard failure — NEXT

**Command as typed:**

    nova-local status --engine ollama --base http://<tailnet-host>:11434/v1 --timeout 3s

**Printed (first 3 lines):**

    STATUS FAILED: no engine answers (1 asked); start one, such as the ollama daemon; run: ollama serve

exit 1, after the full 3 s. `docs/CLI.md` says status prints "one `STATUS ENGINE name=
state=up|down|timeout base=` line per engine", and `docs/SPEC-LOCAL.md` says "a slow engine is
`state=timeout` at exit 0"; with the one built engine either down or slow, neither line is
reachable, and a timeout is reported exactly like a refused connection (this one is also
indistinguishable from a typo in the port, which is reported the same way).

**Expected:** the engine's own `STATUS ENGINE ... state=timeout` (and the box facts) when the
engine was asked and did not answer in `--timeout`, so a reader can tell "nothing is listening"
from "it is listening but slow", which is the whole reason the state word exists.

**Grade:** NEXT

## What was not done

- No engine answered at the default base or at a tailnet base, and the card forbids starting a
  server on this machine, so `status` never printed a `STATUS ENGINE state=up` line, its `--list`
  MODEL lines or its `store=`/`shared=`, `serve` never created or loaded a derived tag and never
  printed `serve_as=` or `load=`, `serve --stop` never reached `already stopped`, and `worker`
  never wrote a description. The `--require-shared-store` and `--expect-digest`-differs refusals
  need an engine to reach and were not seen.
- `--json` was seen only on refusals and failures, never on a success value.

## What worked (kept short)

The banner, `help`, every verb's `-h` and `help <verb>` answer on stdout at exit 0; the bare
command, an unknown verb, an unknown flag and a stray positional each name the door and, where
close, the nearest name (`did you mean serve?`, `did you mean --engine?`). Refusals name what an
input wants: a missing or zero `--num-ctx`, a `--seed` that is not a whole number, malformed
`--min-free`/`--max-load`, `--engine` outside `ollama`, a non-http `--base` or one resolving off
loopback and 100.64.0.0/10, `--usage` outside `opencode|none`, `--deadline` that is not a Go
duration, an empty `--key-file` with the `printf` that writes one, a relative or absent
`--worker-dir`, and `--harness-args` without `{model}`. `--json` was the same value as the lines
on `version`, on a refusal and on a failure, and the threshold path read the box for real
(`mem_used`, `mem_free`, `mem_total`, `wired_cap=unset`, `load1`).

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	1.736s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	13.906s

The exact lines are pasted into the job's `outbox/dogfood-grok-local-b.w1~15.g7/REPORT.draft.md`.
The card's named test `TestDocsTreeIsConsistent` names no test at this tip:
`go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent` prints
`ok  github.com/mas-bandwidth/nova-tools/internal/docs  0.008s [no tests to run]`, exit 0. This
report is the first `docs/dogfood/` file at this base, so the commit also carries the one catalog
row (`internal/docs/catalog.go`) and the one regenerated map row (`docs/AGENTS.md`) the docs
guard needs for the new directory, exactly as the first dogfood record on the integration branch
did.

READ 7/10 — the banner, the per-verb flags, the exit table and SPEC-LOCAL answer a cold reader
fast and mostly truly, and the refusals say what each input wants; held down by the two
documented `--dry-run` paths that answer with the skeleton's internal guard, a usage line that
lists `--base` as independent when it is not, and unheld `--keep-alive` and `--expect-digest`
shapes.

USE 5/10 — every verb ran at least once with its real flags against a scratch AI root, and the
refusal and failure paths were read at the tip; but the promised dry-run plans fail, `serve
--stop` and `worker` answer a local problem with a raw engine dial error, and with no engine (and
starting one forbidden by the card) the engine-facing half — status OK, serve create/load,
worker's write — could not be used at all.

urgent=2 next=7
