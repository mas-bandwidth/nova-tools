# nova-local AI-usability review, 2026-10-15

Reviewer: Gemini 4 Argon (High)
Build: 52ada6aff220
Score: 8.5/10 (48/55)

## Rubric
| # | criterion | score | evidence |
|---|---|---|---|
| 1 | cold | 4 | `nova-local help` -> `nova-local version` (`nova-local devel linux/amd64 go1.26.6`, exit 0) and `nova-local serve --stop --engine ollama --model gemma4-32k --dry-run` (`SERVE OK stopped=planned engine=ollama model=gemma4-32k dry_run=true`, exit 0); the banner's `worker` example hardcodes repo-internal `$PWD/cmd/nova-local/testdata/home` paths |
| 2 | refusals | 4 | `nova-local worker` with no flags reports all 11 missing required flags in one run at exit 2; `nova-local serve --engine ollama --model gemma4:12b --num-ctx 1000` -> `SERVE REFUSED: --num-ctx 1000 is not a multiple of 1024, so the derived tag's name would round; use 0 or 1024` (where `0` is itself refused) |
| 3 | output | 4 | `nova-local serve --stop --engine ollama --model gemma4-32k --dry-run --json` -> `{"result":{"verb":"serve","status":"ok","exit":0},"facts":{"stopped":"planned","engine":"ollama","model":"gemma4-32k","dry_run":true}}`; when `status` finds no engine answering, it drops the box facts (`mem_free`, `load1`, `wired_cap`) and the `ENGINE` item line |
| 4 | dry-run | 3 | `nova-local serve --stop --engine ollama --model gemma4-32k --dry-run` exits 0 without touching an engine, but `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --dry-run` and `nova-local worker ... --dry-run` fail when the engine is down and print `<VERB> NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` |
| 5 | idempotent | 5 | `status` and `version` are read-only; `serve` reuses an existing derived tag when `parent`, `num_ctx`, `temperature`, and `seed` match, and `serve --stop` exits 0 with an `already stopped` note when the model is not loaded |
| 6 | no prompts | 5 | `nova-local status --timeout 200ms` -> `STATUS FAILED: no engine answers (1 asked); start one, such as the ollama daemon; run: ollama serve` (exit 1, no interactive prompt) |
| 7 | exit codes | 5 | Exit 0 on `version` and `serve --stop --dry-run`, exit 1 on unreachable engine or exceeded `--max-load` threshold, exit 2 on flag or input validation failure |
| 8 | one word | 5 | `engine`, `model`, `serve_as`, `num_ctx`, `store`, and `shared` are used consistently across `status`, `serve`, and `worker` |
| 9 | push | 5 | Request/response CLI with bounded `--timeout` on `status` and no polling loop |
| 10 | no leaks | 5 | `nova-local status --engine ollama --base http://example.com:11434` -> `STATUS REFUSED: --base http://example.com:11434: host example.com is not loopback or a tailnet address (100.64.0.0/10); refusing to send a prompt off the tailnet; run: nova-local help` |
| 11 | true help | 3 | `status -h` promises `status reads each engine and the box` and `One ENGINE line per engine`, which are omitted when `ollama` is down; `worker -h` says `--harness-args` requires `{model} and {prompt} placed`, `--harness` is `found on PATH`, and `--out`'s `directory must exist`, none of which `Description.Problems` checks |

## 10/10 blockers
| # | where | what an AI meets | fix | size |
|---|---|---|---|---|
| 1 | `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --dry-run` | When the engine is unreachable (or `--max-load` / `--min-free` / `Description.Problems` returns early on `serve` or `worker`), passing `--dry-run` emits a false warning `SERVE NOTE --dry-run was given and the verb never read it (Call.DryRun); it may have written` (and `WORKER NOTE ...`) because `c.DryRun()` is only read after HTTP calls (`cmd/nova-local/main.go:451,496`) | Read `c.DryRun()` at the start of `serve` and `worker` (or suppress the unread-`DryRun` note in `pkg/tool` on `FAILED` and `REFUSED` returns) | S |
| 2 | `nova-local serve --engine ollama --model gemma4:12b --num-ctx 1000` | Prints `SERVE REFUSED: --num-ctx 1000 is not a multiple of 1024, so the derived tag's name would round; use 0 or 1024; run: nova-local help` — when `0 < numCtx < 1024`, `lo := numCtx / 1024 * 1024` evaluates to `0`, recommending `use 0` which `ContextProblem(0)` immediately refuses (`--num-ctx wants the context in tokens, above 0 (got 0)`) | In `ContextProblem` (`cmd/nova-local/ollama.go:127`), when `lo == 0`, suggest `use 1024` instead of `use 0 or 1024` | S |
| 3 | `nova-local status --timeout 200ms` | Prints only `STATUS FAILED: no engine answers (1 asked); start one, such as the ollama daemon; run: ollama serve` (exit 1) and discards both the `ENGINE name=ollama state=down ...` item built at `cmd/nova-local/main.go:280-296` and the box facts (`mem_used`, `mem_free`, `mem_total`, `wired_cap`, `load1`), even though `serve` on the same failure remedies with `run: nova-local status --list` to inspect the box | Attach `w.boxFacts(f)` and the per-engine `state=down` / `state=timeout` item lines to the failed `status` output when `answering == 0` | S |
| 4 | `nova-local worker -h` | The banner and `worker -h` example pass `--worker-dir $PWD/cmd/nova-local/testdata/home --key-file $PWD/cmd/nova-local/testdata/local.key`, which fail outside the `nova-tools` source checkout; meanwhile `Description.Problems` (`cmd/nova-local/description.go:42`) never checks `{prompt}` in `--harness-args`, never checks that `--out`'s parent directory exists, and `serve` never validates `--keep-alive <d>` as a duration | Check `{prompt}` in `--harness-args`, `filepath.Dir(--out)` existence in `Description.Problems`, and `--keep-alive` duration format in `serve`, and use self-contained scratch paths in the `worker` example | S |

## Nice-to-haves
| # | where | what | fix | size |
|---|---|---|---|---|
| 1 | `nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --max-load 0.0 --json` | In JSON `facts`, `mem_used`, `mem_free`, and `mem_total` are numbers (`36924391424`), while `load1` is serialized as a JSON string (`"6.57"`) | Store `load1` as a numeric float in JSON `facts` or document its string formatting | S |
| 2 | `nova-local status --engine bogus` | Flag and value refusals on a specific verb end with `run: nova-local help` instead of `run: nova-local <verb> -h` | Point verb-specific flag and value refusals to `nova-local <verb> -h` | S |

## Top 3 fixes
1. Fix `ContextProblem` in `cmd/nova-local/ollama.go:127` so `--num-ctx` values between `1` and `1023` suggest `use 1024` rather than the invalid `use 0 or 1024`.
2. Call `c.DryRun()` before early returns in `serve` and `worker` (or suppress the note on non-OK returns in `pkg/tool`) so `--dry-run` failures never print the false `it may have written` warning.
3. Include `w.boxFacts` and the `ENGINE name=ollama state=down` item on `nova-local status` when `answering == 0`, so `status` always reports the box's memory and load as promised by `nova-local help`.

## Good, keep
- Major improvements since the 2026-10-02 rating: `nova-local` now exists as a complete tool with `status`, `serve`, `worker`, `version`, and `help` (`--json` on every verb, `--dry-run` on `serve` and `worker`, and SSRF protection refusing non-loopback/non-tailnet `--base` URLs).
- `nova-local worker` with no flags reports all 11 missing required flags in a single refusal (`--engine`, `--model`, `--num-ctx`, `--worker-dir`, `--key-file`, `--worktree`, `--out`, `--timeout`, `--harness`, `--harness-args`, and `<prompt-file>`).
- `serve --stop --dry-run` plans model unloading cleanly in both text and `--json` without touching a running engine.

## What could not be tried
Starting an `ollama` daemon (`ollama serve`) or loading a live model via `nova-local serve` / `nova-local worker` without `--dry-run` was not run per the card's rule against starting a server on the bench; `serve` and `worker` were tested via `--dry-run`, `status`, and flag/refusal probes.
