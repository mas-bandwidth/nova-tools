# nova-local dogfood — opencode-2, 2026-10-06

Built and used on a Linux bench (`<bench>`; the caller is a made-up actor, `--actor boss`) from the staged checkout at commit `7acb90e18a764f0e728cd5ed701196a34405a824` with `go build -o $JOB/bin/nova-local ./cmd/nova-local`, never the installed binary; the version line it printed is `nova-local v1.0.1-0.20261007211151-7acb90e18a76 linux/amd64 go1.26.6`. Read cold: `nova-local`, `nova-local help`, `nova-local help <verb>`, `nova-local <verb> -h`, and the tool's page under `docs/` (`docs/CLI.md`, section `## nova-local`, and the spec it links, `docs/SPEC-LOCAL.md`). Run for real: every verb — `status`, `serve`, `serve --stop`, `worker`, `version` — with its real flags against a scratch AI root at `/tmp/nl-dogfood-bb/ai`, a scratch out directory and a scratch worker home. No engine answered at `http://127.0.0.1:11434` or at any address in the tailnet range, and the card forbids starting a server, so every engine-facing success path is named not run under `## What held`; all twelve findings below are one-line outputs and each marker `(one line printed)` says so.

## Findings

1. `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7 --dry-run`
   Printed:
   ```
   SERVE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   (one line printed)
   ```
   I expected the plan the real run would take, `SERVE OK ... dry_run=true` naming the derived tag `gemma4-32k`, because `--dry-run` is in serve's own usage and flag list ("print what the verb would write and write nothing") and the spec says it "reads all of it and creates and loads nothing".
   Grade: URGENT (help that lies)

2. `nova-local worker --engine ollama --model gemma4-32k --out /tmp/nl-dogfood-bb/out/gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir /tmp/nl-dogfood-bb/work/home --key-file /tmp/nl-dogfood-bb/work/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m --dry-run`
   Printed:
   ```
   WORKER FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   (one line printed)
   ```
   I expected the worker description JSON the real run would write, because worker's own effect line is "local write" and its `--dry-run` flag is documented "print what the verb would write and write nothing"; the same command without `--dry-run` wrote nothing and named the absent engine cleanly.
   Grade: URGENT (help that lies)

3. `nova-local serve --stop --engine ollama --model gemma4-32k`
   Printed:
   ```
   SERVE FAILED: the engine did not unload gemma4-32k: Get "http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused
   (one line printed)
   ```
   I expected the one-grammar failure the serving form prints on the same absent engine, `SERVE FAILED: no engine answers; start one, such as the ollama daemon; run: ollama serve`, because the spec's stop result is a `SERVE FAILED: <reason>; run: <command>` line and a reader who asked to unload a model should be handed the command that starts the engine.
   Grade: URGENT (a refusal with no remedy)

4. `nova-local serve --engine ollama --model gemma4:12b --num-ctx 1000`
   Printed:
   ```
   SERVE REFUSED: --num-ctx 1000 is not a multiple of 1024, so the derived tag's name would round; use 0 or 1024; run: nova-local help
   (one line printed)
   ```
   I expected the two nearest contexts the verb will actually take; the first value it names is refused by the same verb, `nova-local serve --engine ollama --model gemma4:12b --num-ctx 0` printing `SERVE REFUSED: --num-ctx wants the context in tokens, above 0 (got 0); refusing to guess; run: nova-local help`, so the remedy names a value that cannot be used (while `--num-ctx 1025` correctly names `1024 or 2048`).
   Grade: URGENT (help that lies)

5. `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --keep-alive nonsense`
   Printed:
   ```
   SERVE FAILED: no engine answers; start one, such as the ollama daemon; run: ollama serve
   (one line printed)
   ```
   I expected exit 2 naming the Go duration the flag wants, the way `--min-free nonsense` and `--max-load notanumber` are refused before the dial, because serve's usage spells this flag `[--keep-alive <d>]` and the spec gives it a `30m` default; instead the value reached the engine call.
   Grade: NEXT (a missing flag)

6. `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --expect-digest deadbeef`
   Printed:
   ```
   SERVE FAILED: no engine answers; start one, such as the ollama daemon; run: ollama serve
   (one line printed)
   ```
   I expected exit 2 refusing a value that is not the `sha256:...` shape the flag's own text asks for ("the digest the model must have (sha256:...)"), because a value that cannot be a digest can only ever refuse after a model is pulled and read.
   Grade: NEXT (a missing flag)

7. `nova-local status --engine ollama --base http://127.0.0.1:99999/v1 --timeout 500ms`
   Printed:
   ```
   STATUS FAILED: no engine answers (1 asked); start one, such as the ollama daemon; run: ollama serve
   (one line printed)
   ```
   I expected the shape refusal for a port above 65535, because the help says `--base` wants "the engine's /v1 URL, loopback or a tailnet address" and the tool refuses a scheme-less or non-http value at exit 2; instead a typo in a flag is reported as an absent engine and sends the reader to `ollama serve`.
   Grade: NEXT (a missing flag)

8. `nova-local worker --engine ollama --model gemma4-32k --out /tmp/nl-dogfood-bb/nope/gemma.json --name gemma --harness nosuchcmd12345 --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir /tmp/nl-dogfood-bb/work/home --key-file /tmp/nl-dogfood-bb/work/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m --board notaboard`
   Printed:
   ```
   WORKER FAILED: the engine's models could not be read: Get "http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused; run: nova-local status --engine ollama
   (one line printed)
   ```
   I expected the local problems this run also carries to be named in the same run — `--out`'s directory does not exist, `--harness nosuchcmd12345` is not on PATH, and `--board notaboard` is not `owner/repo#n` — because the tool can check each for itself, and one run with no flags already names all eleven missing required flags at once; instead the dial answers first and the local faults wait behind it.
   Grade: NEXT (friction)

9. `nova-local status --base http://127.0.0.1:11434/v1 --timeout 500ms`
   Printed:
   ```
   STATUS REFUSED: --base names one engine's endpoint, so it wants --engine; run: nova-local help
   (one line printed)
   ```
   I expected to be able to point the only engine at its known address, because the usage line `status [--engine <name>] [--base <url>]` and the flag text "the engine's /v1 URL" read as independent; the refusal does name the fix, but a reader learns by failing that `--engine ollama` must be repeated.
   Grade: NEXT (unclear help)

10. `nova-local status --engine ollama --timeout -5s`
    Printed:
    ``` STATUS FAILED: no engine answers (1 asked); start one, such as the ollama daemon; run: ollama serve (one line printed) ```
    I expected exit 2 refusing a duration at or below zero, because `--timeout` is documented "how long each engine has to answer" and `--timeout bogus` is refused at exit 2; a negative value is accepted and reaches the engine call.
    Grade: NEXT (a missing flag)

11. `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --max-load 0.0001`
    Printed:
    ``` SERVE FAILED mem_used=53821321216 mem_free=76632301568 mem_total=130453622784 load1=28.23: load1 is 28.23 and --max-load asks at most 0.0001; run: nova-local status --list (one line printed) ```
    I expected a breadcrumb that can change the number — a `--max-load` above the measured load, or a wait — because the line names what was measured and what was asked, and `nova-local status --list` is another read of the same box that leaves the serve unable to run.
    Grade: NEXT (unclear help)

12. `nova-local worker --engine ollama --model gemma4-32k --out /tmp/nl-dogfood-bb/out/gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir /tmp/nl-dogfood-bb/work/home --key-file /tmp/nl-dogfood-bb/work/local.key --env-var "FOO BAR" --usage opencode --deadline 20m`
    Printed:
    ``` WORKER FAILED: the engine's models could not be read: Get "http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused; run: nova-local status --engine ollama (one line printed) ```
    I expected exit 2 refusing a value that cannot be an environment variable name, because the flag's text is "the NAME of the variable the provider reads (OLLAMA_API_KEY), never a value"; instead `FOO BAR` was accepted and the engine was dialled.
    Grade: NEXT (a missing flag)

## What held

`version` ran clean and printed the one version line at exit 0, and `version --json` the same value as JSON; `help`, `help <verb>` and every `<verb> -h` answered on stdout at exit 0, and the bare command, an unknown verb and an unknown flag each named the door and what there is. `status` ran to its one honest line, `STATUS FAILED: no engine answers (1 asked); start one, such as the ollama daemon; run: ollama serve`, and `--list`, `--max` and `--json` were exercised on it. `serve`'s refusals name what each input wants and report every missing required flag at once (three for `serve`, eleven for `worker`): a missing `--num-ctx`, a `--seed` that is not a whole number, a non-http or off-loopback `--base`, malformed `--min-free`/`--max-load`, `--usage` outside `opencode|none`, a `--deadline` that is not a Go duration, `--harness-args` without `{model}`, a relative `--worker-dir`, and a missing `--key-file` with the `printf` remedy, each at exit 2. `serve --stop --dry-run` planned and exited 0 (`SERVE OK stopped=planned engine=ollama model=gemma4-32k dry_run=true`), and `--json` rendered the same value as the lines for `version`, for a refusal and for a failure. Not run: the engine-facing success paths (`status` OK with its ENGINE and MODEL lines, `serve` create/load and `serve_as=`/`load=`, `worker`'s description write, and the `--require-shared-store` and `--expect-digest`-differs refusals) were unreachable because no engine answered and starting one is forbidden here.

READ 7/10 — the banner, the per-verb helps, the refusal grammar and the JSON twin answer a cold reader fast and truly, and the tool refuses to guess on every input it can check; held down by the two documented `--dry-run` paths that answer with the skeleton's internal guard, the `--num-ctx` remedy that names a value the tool refuses, the stop path's raw dial error, and five shapes the flags document but do not hold.

USE 4/10 — every verb ran at least once with its real flags against a scratch AI root, a scratch out directory and a scratch worker home, and the refusals and failures were read at the tip; the engine-facing half could not run at all here (no engine answered, and starting one is forbidden by the card), so `status` OK, `serve`'s create/load and `worker`'s write are unproven by use.

urgent=4 next=8
