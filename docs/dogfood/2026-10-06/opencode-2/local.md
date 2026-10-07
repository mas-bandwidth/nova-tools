# nova-local dogfood, 2026-10-06 (opencode-2)

Tool: nova-local. Build: `nova-local v1.0.1-0.20261006183932-5844884e267c darwin/arm64 go1.26.6`.
Run cold, from the binary's own help (`nova-local`, `nova-local help`, `nova-local <verb> -h`)
and its page under `docs/` (`docs/CLI.md`, section `## nova-local`, and the spec it links,
`docs/SPEC-LOCAL.md`) only, with every verb at least once against one scratch AI root, one
scratch out directory and the tool's own `cmd/nova-local/testdata` fixture. No engine answered
at `http://127.0.0.1:11434` or at a tailnet address in 100.64.0.0/10, and this card's rules
forbid starting one here ("Never start a server on this machine"), so every engine-facing form
ran to its failure or refusal line and the success paths (`status` OK with its ENGINE and MODEL
lines, `serve` create/load/`serve_as=`, `worker`'s JSON write) are reported not done below.
Commands were typed with

    S=$PWD/scratch
    T=$PWD/repo/cmd/nova-local/testdata
    export NOVA_AI_ROOT=$S/ai
    mkdir -p $S/ai/shared/models/ollama $S/out

so `status` read a scratch store and `worker`'s `--out` was under `$S/out`; no real store was
touched. The card names the base `5844884e267c`, and that is the checkout these findings were
recorded at.

## 1. `serve --dry-run` answers with the skeleton's internal guard instead of a plan — URGENT

**Command:**

    ./nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7 --dry-run

**Printed:**

    SERVE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written

exit 1, on stderr; `--json` carries the same `why` with no `remedy`. `--dry-run` is in serve's
own usage line and its flags list ("print what the verb would write and write nothing"), and the
spec's `serve` section says "`--dry-run` reads all of it and creates and loads nothing".

**Expected:** `SERVE OK ... dry_run=true` naming the derived tag it would create, the context and
the box facts it reads first, dialling nothing that changes the engine; instead the one flag that
lets a reader see a serve before it makes one answers with the skeleton's internal guard sentence
and no remedy.

**Grade:** URGENT

## 2. `worker --dry-run` answers with the same internal guard on the verb whose only effect is one file — URGENT

**Command:**

    ./nova-local worker --engine ollama --model gemma4-32k --out $S/out/gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir $T/home --key-file $T/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m --dry-run

**Printed:**

    WORKER FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written

exit 1. The same command without `--dry-run`, with no engine answering, wrote nothing and printed
the clean failure `WORKER FAILED: the engine's models could not be read: Get
"http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused; run:
nova-local status --engine ollama`, so the real path already refuses to write on a bad engine.

**Expected:** the plan the real run would write — the worker description JSON — because worker's
own effect line is "local write", the spec calls the description "nova-swarm's worker schema", and
`--dry-run` is documented as the run that "does all of it and writes nothing".

**Grade:** URGENT

## 3. `serve --stop` names the raw dial error where `serve` names the remedy — NEXT

**Command:**

    ./nova-local serve --stop --engine ollama --model gemma4-32k

**Printed:**

    SERVE FAILED: the engine did not unload gemma4-32k: Get "http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused

exit 1, and the line carries no `run:` clause. The serving form on the same absent engine prints
the clean line `SERVE FAILED: no engine answers; start one, such as the ollama daemon; run: ollama
serve`.

**Expected:** the stop path should read the engine the way `serve` does and carry the same remedy,
because `--stop` is the other half of the one documented verb and the spec makes it "one request
with `keep_alive 0`"; a reader who asked to unload a model is told a Go dial error instead of the
command that starts the engine.

**Grade:** NEXT

## 4. `serve --stop --dry-run` returns OK with a `stopped=planned` value the help and spec never name — NEXT

**Command:**

    ./nova-local serve --stop --engine ollama --model gemma4-32k --dry-run

**Printed:**

    SERVE OK stopped=planned engine=ollama model=gemma4-32k dry_run=true

exit 0; `--json` gives the same value as `{"result":{"verb":"serve","status":"ok","exit":0},"facts":{"stopped":"planned","engine":"ollama","model":"gemma4-32k","dry_run":true}}`.
The spec's stop result is `SERVE OK stopped=yes engine=<e> model=<tag>`, and the two other
documented dry runs (findings 1 and 2) fail rather than plan.

**Expected:** a plan line in the one documented grammar, or the same guard as serve and worker; a
third value for `stopped` is a shape a parser cannot have been written for, and `--dry-run` is not
in the `serve --stop` usage line.

**Grade:** NEXT

## 5. `--keep-alive` takes any string though its usage spells a duration, and the 30m default is not in the help — NEXT

**Command:**

    ./nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --keep-alive nonsense

**Printed:**

    SERVE FAILED: no engine answers; start one, such as the ollama daemon; run: ollama serve

exit 1: the value was accepted and the engine was dialled. serve's usage line writes
`[--keep-alive <d>]` while its flags list names the flag `<string>`; by contrast `--min-free
nonsense` and `--max-load notanumber` are refused at exit 2, and the spec names a `30m` default
that no help line prints.

**Expected:** exit 2 naming the Go duration it wants, so a typo does not reach an engine that
would keep a model loaded for a value it cannot parse, and the help should print the `30m` default
the spec names.

**Grade:** NEXT

## 6. `--base` with a port no host can have is reported as an absent engine — NEXT

**Command:**

    ./nova-local status --engine ollama --base http://127.0.0.1:99999/v1 --timeout 500ms

**Printed:**

    STATUS FAILED: no engine answers (1 asked); start one, such as the ollama daemon; run: ollama serve

exit 1. A port above 65535 cannot be an engine's `/v1`: the help says `--base` wants "the engine's
/v1 URL, loopback or a tailnet address", and the tool refuses a scheme-less or non-http value at
exit 2 with the shape it wants.

**Expected:** the same shape refusal for a port that cannot be connected to; the absent-engine
exit 1 sends the reader to `ollama serve` for what is a typo in a flag.

**Grade:** NEXT

## 7. `--expect-digest` takes any string, not the `sha256:...` it documents — NEXT

**Command:**

    ./nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --expect-digest deadbeef

**Printed:**

    SERVE FAILED: no engine answers; start one, such as the ollama daemon; run: ollama serve

exit 1. The flag's own text is "the digest the model must have (sha256:...)", and the spec makes a
differing digest exit 1 naming both values, so a value that is not a digest can only ever refuse —
after a model is pulled and read.

**Expected:** exit 2 refusing a value that is not the `sha256:...` shape it asks for, the way
`--engine`, `--min-free` and `--max-load` hold their shapes.

**Grade:** NEXT

## 8. `status --base` alone is refused, though its usage lists `--base` beside `--engine` and its help never says it needs one — NEXT

**Command:**

    ./nova-local status --base http://127.0.0.1:11434/v1 --timeout 500ms

**Printed:**

    STATUS REFUSED: --base names one engine's endpoint, so it wants --engine; run: nova-local help

exit 2. The refusal names the fix, so recovery is one turn; the friction is that the usage line
`status [--engine <name>] [--base <url>]` and the flag text "the engine's /v1 URL" read as
independent, and a reader who knows the engine's address learns by failing that `--engine ollama`
must be repeated.

**Expected:** the flag's help to say it is used with `--engine`, or the usage to bracket it under
the engine rather than beside it.

**Grade:** NEXT

## 9. `worker` dials the engine before it checks the harness, the out directory or the board — NEXT

**Command:**

    ./nova-local worker --engine ollama --model gemma4-32k --out $S/nope/gemma.json --name gemma --harness nosuchcmd12345 --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir $T/home --key-file $T/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m --board notaboard

**Printed:**

    WORKER FAILED: the engine's models could not be read: Get "http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused; run: nova-local status --engine ollama

exit 1. `--harness` is documented "the harness command, found on PATH", `--out` "the file the
description is written to; its directory must exist", and `--board` "the board issue the worker
reports to, owner/repo#n"; none of the three was named, and the same engine line answers a missing
out directory and a malformed board too. What worker does check without an engine it names well:
one run with no flags prints all eleven missing required flags at once.

**Expected:** every precondition the tool can check for itself before the dial, named in the same
run (a missing `nosuchcmd12345` on PATH, a missing out directory, a board that is not
`owner/repo#n`), so a reader with two problems is not told one and then made to wait on a dial that
cannot change either answer.

**Grade:** NEXT

## 10. A threshold refusal's remedy is `nova-local status --list`, which does not move the number — NEXT

**Command:**

    ./nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --max-load 0.0001

**Printed:**

    SERVE FAILED mem_used=302532362240 mem_free=247223451648 mem_total=549755813888 wired_cap=unset load1=24.40: load1 is 24.40 and --max-load asks at most 0.0001; run: nova-local status --list

exit 1; the JSON twin carries the same `remedy`. The line names what was measured and what was
asked, as the spec's rule 8 says, and the box facts are real.

**Expected:** a breadcrumb that changes the number — a `--max-load` above the measured load, or to
wait — the way the `--base` refusal names the value it wants; `status --list` is another read of
the same box and leaves the serve unable to run.

**Grade:** NEXT

## 11. `--timeout` accepts a negative duration — NEXT

**Command:**

    ./nova-local status --engine ollama --timeout -5s

**Printed:**

    STATUS FAILED: no engine answers (1 asked); start one, such as the ollama daemon; run: ollama serve

exit 1. `--timeout` is documented "how long each engine has to answer"; `--timeout bogus` is
refused at exit 2 but a negative duration is accepted and never held before the engine call. Every
address I could name refused the connection immediately, so I could not watch a negative value
extend or shorten a real wait; what is certain is that a value the flag cannot mean reaches the
call.

**Expected:** exit 2 refusing a duration at or below zero.

**Grade:** NEXT

## 12. `--env-var` accepts a string that cannot be an environment variable name — NEXT

**Command:**

    ./nova-local worker --engine ollama --model gemma4-32k --out $S/out/gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir $T/home --key-file $T/local.key --env-var "FOO BAR" --usage opencode --deadline 20m

**Printed:**

    WORKER FAILED: the engine's models could not be read: Get "http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused; run: nova-local status --engine ollama

exit 1: the value was accepted and the engine was dialled. The flag's text is "the NAME of the
variable the provider reads (OLLAMA_API_KEY), never a value".

**Expected:** exit 2 refusing a name that is not one (a letter or `_`, then letters, digits or `_`),
so a description cannot name a variable no harness can read.

**Grade:** NEXT

## What was not done

- No engine answered at the default base or at a tailnet base, and the card forbids starting a
  server on this machine, so `status` never printed an ENGINE `state=up` line, its `--list` MODEL
  lines, the `store=`/`shared=` line or a `MORE` line; `serve` never created or loaded a derived
  tag and never printed `serve_as=` or `load=`; `serve --stop` never reached `already stopped`;
  `worker` never wrote a description. The `--require-shared-store` refusal and the
  `--expect-digest`-differs refusal need an engine to reach and were not seen.
- `--json` was not exercised on a success value, only on refusals and failures.

## What worked (no finding, kept short)

The banner, `help`, every verb's `-h` and `help <verb>` answer on stdout at exit 0; `-h` wins over
a bad flag and over a positional, so help is never a refusal. The bare command, an unknown verb
and an unknown flag each name the door (`LOCAL REFUSED: no verb given; the verbs are status,
serve, worker, version; run: nova-local help`, `STATUS REFUSED: unknown flag --bogus; the flags of
status are --base, --engine, --json, --list, --max, --timeout; run: nova-local status -h`), and a
positional is refused as one. Refusals name what an input wants: `--num-ctx` missing, zero or not
a multiple of 1024 (`use 29696 or 30720`), a `--seed` that is not a whole number, malformed
`--min-free`/`--max-load`, `--engine` outside `ollama`, a `--base` that is not http or resolves off
loopback and 100.64.0.0/10, `--usage` outside `opencode|none`, `--deadline` that is not a Go
duration, an empty `--key-file` with the `printf` that writes one, a relative `--worker-dir`,
`--max` negative, a malformed `--timeout`, and `--harness-args` without `{model}`. `--json` is the
same value as the lines for `version`, for a refusal (`status` "refused") and for a failure
(`serve` "failed"), and one run reports every missing required flag at once (eleven for `worker`,
three for `serve`). The banner's three `example:` lines run as printed from a checkout root, each
answering exit 1 on the absent engine. `serve --stop --dry-run` is the one dry run that plans
instead of dialling (finding 4), so the guard in findings 1 and 2 is not universal.

## Gate

    go test -count=1 -timeout 600s ./internal/docs ./internal/ci

    ok  	github.com/mas-bandwidth/nova-tools/internal/docs	1.910s
    ok  	github.com/mas-bandwidth/nova-tools/internal/ci	10.689s

The card's named test `TestDocsTreeIsConsistent` does not exist at this tip:
`go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent` reports
`ok  github.com/mas-bandwidth/nova-tools/internal/docs  0.008s [no tests to run]`. The two
packages above are the real gate, run on a build bench against this report's own new directory.
This report is the first `docs/dogfood/` file at this base, so the commit also carries the one
catalog row (`internal/docs/catalog.go`) and the one regenerated map row (`docs/AGENTS.md`) that
the docs guard requires for the new directory.

READ 7/10 — the banner, the per-verb helps, the refusal grammar and the JSON twin answer a cold
reader fast and truly, and the tool refuses to guess on every input it can check; held down by the
two documented `--dry-run` paths that answer with the skeleton's internal guard, the usage line
that spells a duration where the flag takes any string, the three unvalidated shapes
(`--keep-alive`, a `--base` port, `--expect-digest`), `--base`'s unspoken need for `--engine`, the
stop path's raw dial error and its undocumented `stopped=planned`, and the two undocumented
defaults.

USE 4/10 — every verb ran at least once with its real flags against a scratch AI root and a
scratch out directory, and the refusals and failures were read at the tip; the engine-facing half
could not run at all here (no engine, and starting one is forbidden by the card), so `status` OK,
`serve` and `worker`'s write are unproven by use.

urgent=2 next=10
