# nova-local dogfood — Johnny Grok, 2026-10-06

Read as a stranger: `nova-local -h`, `nova-local help`, `nova-local <verb> -h`, and the page under `docs/` (`docs/SPEC-LOCAL.md`, the nova-local section of `docs/CLI.md`, the nova-local first run in `docs/TESTS.md`). Used the installed binary `nova-local v1.2.0-dev.d165b531 linux/amd64 go1.27.1`. Scratch directory on the bench, `NOVA_AI_ROOT` pointed at that scratch store. No ollama was installed, no server was started, and nothing was pointed at `127.0.0.1:6380`, `6381`, `6390`, or `100.76.29.55`.

## Findings

1. `--dry-run` on `serve` and `worker` throws away the real refusal and claims a write that did not happen.
   Command: `nova-local serve --dry-run --engine ollama --model gemma4:12b --num-ctx 32768 --max-load 0.0000001 --base http://127.0.0.1:11435/v1`
   Printed:
   ```
   SERVE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   ```
   The same flags without `--dry-run` printed the box refusal (`load1 is 2.87 and --max-load asks at most 0.0000001; run: nova-local status --list`) and did not dial. `worker --dry-run` with a closed `--base` and a real `--out` printed the same `Call.DryRun` sentence. `--json` on the serve dry-run carried that why and no remedy. The scratch `--out` file was still the sentinel I put there; no description was written. I expected `--dry-run` to print the refusal it prints without the flag (docs/SPEC-LOCAL.md: dry-run reads all of it and creates, loads, and writes nothing), not an internal fault with no `run:`.
   Grade: URGENT.

2. A `--num-ctx` below 1024 tells the caller to pass 0, and 0 is refused.
   Command: `nova-local serve --engine ollama --model gemma4:12b --num-ctx 1000`
   Printed:
   ```
   SERVE REFUSED: --num-ctx 1000 is not a multiple of 1024, so the derived tag's name would round; use 0 or 1024; run: nova-local help
   ```
   Command: `nova-local serve --engine ollama --model gemma4:12b --num-ctx 0`
   Printed:
   ```
   SERVE REFUSED: --num-ctx wants the context in tokens, above 0 (got 0); refusing to guess; run: nova-local help
   ```
   I expected the two nearest multiples the verb will accept. `--num-ctx 1025` does that (`use 1024 or 2048`). A missing `--num-ctx` is also reported as `got 0`.
   Grade: URGENT.

3. `serve --stop` against an engine that is not there has no remedy.
   Command: `nova-local serve --stop --engine ollama --model gemma4-32k --base http://127.0.0.1:11435/v1`
   Printed:
   ```
   SERVE FAILED: the engine did not unload gemma4-32k: Get "http://127.0.0.1:11435/api/tags": dial tcp 127.0.0.1:11435: connect: connection refused
   ```
   I expected the line `serve` without `--stop` prints for the same situation (`no engine answers; start one, such as the ollama daemon; run: ollama serve`). Nothing was listening, so nothing was unloaded. `serve --stop --dry-run` did print `SERVE OK stopped=planned ... dry_run=true` and exited 0.
   Grade: URGENT.

4. A worker whose engine is down reports a raw dial and skips local problems the help already names.
   Command: `nova-local worker --engine ollama --model gemma4-32k --out /home/nova/rowan-working/friends/johnny/jobs/dogfood-antigravity-local-b.w1~15.g5/scratch/missing-dir/gemma.json --name gemma --harness true --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir /home/nova/rowan-working/friends/johnny/jobs/dogfood-antigravity-local-b.w1~15.g5/scratch/work/home --key-file /home/nova/rowan-working/friends/johnny/jobs/dogfood-antigravity-local-b.w1~15.g5/scratch/work/local.key --env-var OLLAMA_API_KEY --usage none --deadline 20m --base http://127.0.0.1:11435/v1`
   Printed:
   ```
   WORKER FAILED: the engine's models could not be read: Get "http://127.0.0.1:11435/api/tags": dial tcp 127.0.0.1:11435: connect: connection refused; run: nova-local status --engine ollama
   ```
   I expected the missing `--out` directory in this run (the flag says the directory must exist) and a down engine named the way `status` names it (`run: ollama serve`). The same call with `--harness opencode`, which is not on `PATH`, also did not say the harness was missing. Nothing was written.
   Grade: NEXT.

5. The banner says every verb takes `--json`, and `help` does not.
   Command: `nova-local help --json`
   Printed:
   ```
   {"result":{"verb":"","status":"refused","exit":2,"remedy":"nova-local help","why":["unknown verb \"--json\"; the verbs are status, serve, worker, version"]},"facts":{}}
   ```
   I expected JSON of the help text. `nova-local version --json` is the version object; `help --json` looks up a verb named `--json`.
   Grade: NEXT.

6. The banner's worker example, pasted in an empty directory, exits 2.
   Command: `nova-local worker --engine ollama --model gemma4-32k --out ./gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir $PWD/cmd/nova-local/testdata/home --key-file $PWD/cmd/nova-local/testdata/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m`
   Printed (the shell had expanded `$PWD` to the scratch directory):
   ```
   WORKER REFUSED: --worker-dir /home/nova/rowan-working/friends/johnny/jobs/dogfood-antigravity-local-b.w1~15.g5/scratch/work/cmd/nova-local/testdata/home is not a directory: mkdir -p /home/nova/rowan-working/friends/johnny/jobs/dogfood-antigravity-local-b.w1~15.g5/scratch/work/cmd/nova-local/testdata/home; run: nova-local help
   WORKER REFUSED: --key-file /home/nova/rowan-working/friends/johnny/jobs/dogfood-antigravity-local-b.w1~15.g5/scratch/work/cmd/nova-local/testdata/local.key does not exist; the local engine wants no key, and nova-swarm a non-empty file: printf 'local\n' > /home/nova/rowan-working/friends/johnny/jobs/dogfood-antigravity-local-b.w1~15.g5/scratch/work/cmd/nova-local/testdata/local.key && chmod 600 /home/nova/rowan-working/friends/johnny/jobs/dogfood-antigravity-local-b.w1~15.g5/scratch/work/cmd/nova-local/testdata/local.key; run: nova-local help
   ```
   I expected the `example:` line to run (exit 0 or 1) from the directory I was in, or the banner to say it needs this repo's `cmd/nova-local/testdata`. The page says "from a checkout root". The refusal's `printf` remedy is the right one; the example still does not run as pasted.
   Grade: NEXT.

7. The fleet section's `--concurrency` flag is not a flag.
   Command: `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --concurrency 1`
   Printed:
   ```
   SERVE REFUSED: unknown flag --concurrency; the flags of serve are --base, --dry-run, --engine, --expect-digest, --json, --keep-alive, --max-load, --min-free, --model, --num-ctx, --require-shared-store, --seed, --stop; run: nova-local serve -h
   ```
   I expected the command in the fleet section of `docs/SPEC-LOCAL.md` (`serve --base http://<host>:11434/v1 --concurrency <n>` prints a `nova-config route add` line) to exist, or that section not to show it. The section also says the flag is not built.
   Grade: NEXT.

What did hold: `status` with no engine is one line, exit 1, `run: ollama serve`. A host outside loopback and the tailnet is exit 2 and names the address it resolved. Missing flags are one line each and say what the flag wants. The box thresholds, without `--dry-run`, both print and do not dial. `serve` without `--dry-run` and without `--stop` names `ollama serve`. The key file's text never appeared in any output. `serve --stop --dry-run` plans and writes nothing.

READ 7/10 — the banner, the verb help, and the spec agree on the three verbs, the exit table, and the host wall, and a bad flag says what it wants; the `--json` sentence and the fleet command do not match the binary.

USE 5/10 — status and the non-dry-run refusals are something a stranger can act on, and nothing in the scratch store was touched, but `--dry-run`, the flag the help gives for writing nothing, replaces that refusal with an internal fault and no remedy on the no-engine path a first run actually hits.

urgent=3 next=4
