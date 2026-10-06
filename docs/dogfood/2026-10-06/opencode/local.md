# nova-local — dogfood report, 2026-10-06, rater: opencode

Read cold: `nova-local -h`, `nova-local help`, `nova-local <verb> -h`, `nova-local help <verb>`, and
docs/SPEC-LOCAL.md, then used for real: the binary built at tip 4b29da81a from `cmd/nova-local`, run
from a scratch temp dir and from the checkout root. No ollama daemon runs on this machine and no
server was started, so every engine-touching line was met as an engine-down line. A finding is
recorded here, never fixed.

1. `nova-local serve --engine ollama --model tiny:1 --num-ctx 32768 --dry-run`
   printed:
   SERVE FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   I expected the advertised dry run: help lists [--dry-run] and `--dry-run  print what the verb
   would write and write nothing`; the spec says `--dry-run` "reads all of it and creates and loads
   nothing", so with no engine answering it should print the same `no engine answers ...; run:
   ollama serve` line the identical call without --dry-run prints. Instead the skeleton's internal
   seam complaint replaces the true reason, drops its remedy, and "it may have written" is false —
   serve writes nothing on this machine (the same call writes the raw reason without --dry-run).
   Every serve --dry-run that fails before the verb reads the flag lands here; the JSON carries no
   `remedy` key.
   GRADE: URGENT (help advertises a form that cannot run; a refusal with no remedy).

2. `nova-local worker --engine ollama --model tiny-32k --out ./w.json --name tiny --harness opencode --harness-args run,ollama/{model},{prompt} --worker-dir $PWD/wd --key-file $PWD/key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m --dry-run`
   printed:
   WORKER FAILED: --dry-run was given and the verb never read it (Call.DryRun); it may have written
   I expected the same engine reason the identical call without --dry-run prints — `the engine's
   models could not be read ...; run: nova-local status --engine ollama` — or the plan. Same seam
   message as finding 1, same lost remedy, and the warning is doubly wrong here: `ls w.json` says
   No such file or directory — it did not write. `worker --dry-run`, the one form a stranger can
   try worker with, is dead whenever the verb returns before its last line (engine down, model not
   served, description invalid).
   GRADE: URGENT (a refusal with no remedy; help that lies).

3. `nova-local serve --stop --engine ollama --model tiny-32k`
   printed:
   SERVE FAILED: the engine did not unload tiny-32k: Get "http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused
   I expected the spec's shape `SERVE FAILED: <reason>; run: <command>` — the same dead engine met
   the same verb without --stop prints `SERVE FAILED: no engine answers; start one, such as the
   ollama daemon; run: ollama serve`. The --stop path quotes the raw Go error and carries no remedy
   at all; `--json` shows no `remedy` key.
   GRADE: URGENT (a refusal with no remedy, while its sibling path remedies the same situation).

4. `nova-local worker --engine ollama --model tiny-32k --out ./w.json --name tiny --harness opencode --harness-args run,ollama/{model},{prompt} --worker-dir $PWD/wd --key-file $PWD/key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m`
   printed:
   WORKER FAILED: the engine's models could not be read: Get "http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused; run: nova-local status --engine ollama
   I expected the tool's own words — status and serve print `no engine answers ...; run: ollama
   serve` for this same situation. The remedy is there, but the reason embeds a raw Go dial error;
   three verbs, three spellings of one fact.
   GRADE: NEXT.

5. `nova-local worker --engine ollama --model tiny-32k --out ./nodir/w.json --name tiny --harness nosuchharness123 --harness-args run,ollama/{model},{prompt} --worker-dir $PWD/wd --key-file $PWD/key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m --board notaboard`
   printed:
   WORKER FAILED: the engine's models could not be read: Get "http://127.0.0.1:11434/api/tags": dial tcp 127.0.0.1:11434: connect: connection refused; run: nova-local status --engine ollama
   I expected the one-run-all-problems batch to name these too: --out's help says "its directory
   must exist", --harness says "found on PATH", --board says "owner/repo#n". The exit-2 batch does
   cover {model}, worker-dir, key-file, usage and deadline — but --out's directory, --harness on
   PATH and --board's shape surface (if they are checked at all) only after the engine read, so
   with the engine down a caller fixes one turn per flag instead of one turn per call.
   GRADE: NEXT.

6. `nova-local serve --engine ollama --model tiny:1 --num-ctx 512`
   printed:
   SERVE REFUSED: --num-ctx 512 is not a multiple of 1024, so the derived tag's name would round; use 0 or 1024; run: nova-local help
   I expected the two nearest multiples it names to be values it would accept; `serve --engine
   ollama --model tiny:1 --num-ctx 0` answers `--num-ctx wants the context in tokens, above 0 (got
   0); refusing to guess`. A caller that takes the suggestion it is handed walks into a refusal.
   GRADE: NEXT.

7. `nova-local serve -h`
   printed:
   effect: delivery: sends beyond this machine; it asks the engine to create the derived tag and load it (--stop unloads it); nothing is written on this machine
   I expected the effect line to match the default --base — the loopback ollama on this machine —
   and the store rules: serving writes the derived tag's model file into `<ai-root>/shared/models`
   on this machine (spec rule 15). "sends beyond this machine" and "nothing is written on this
   machine" point a stranger's sandboxing the wrong way for the default call; the delivery label
   fits only a tailnet --base.
   GRADE: NEXT.

8. `nova-local worker --engine ollama --model gemma4-32k --out ./gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir $PWD/cmd/nova-local/testdata/home --key-file $PWD/cmd/nova-local/testdata/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m`
   printed (outside a checkout; the long expanded path elided):
   WORKER REFUSED: --worker-dir …/cmd/nova-local/testdata/home is not a directory: mkdir -p …/cmd/nova-local/testdata/home; run: nova-local help
   WORKER REFUSED: --key-file …/cmd/nova-local/testdata/local.key does not exist; the local engine wants no key, and nova-swarm a non-empty file: printf 'local\n' > …/cmd/nova-local/testdata/local.key && chmod 600 …/cmd/nova-local/testdata/local.key; run: nova-local help
   I expected the banner's `example:` block to run wherever the binary is; it runs from the
   checkout root (there I got exit 1 with a remedy), but its two fixture paths exist only inside
   this repository, so pasted anywhere else the advertised example exits 2 — which the standard
   counts as a broken example. docs/CLI.md says "From a checkout root"; the banner does not.
   GRADE: NEXT.

What was good, and should not be lost: the empty invocation, `bogusverb`, `status --bogus` and
`status extraarg` each answer in one line with the names there are and a `run:` remedy; `serve`
with no flags names all three missing flags at once, each with "refusing to guess"; the
`--num-ctx 30000` refusal names 29696 and 30720, which divide exactly; the --engine/--base walls
hold (8.8.8.8 refused by name, a tailnet address and 127.1.2.3 accepted, an unresolvable name
refused as unverifiable); `worker` refuses a relative --worker-dir, a missing and an empty
--key-file, a bad --usage and --deadline in one run with the spec's own `printf 'local\n' >`
remedy; exit codes matched the banner's table everywhere I looked; `--json` mirrors the lines from
one value; `version` prints the one line.

READ 7/10 — the banner, every verb's -h and docs/SPEC-LOCAL.md speak with one voice and every wall I probed said what it wanted, but three lines mislead a cold reader: the advertised --dry-run cannot run, serve's effect line denies this machine, and the example quietly needs a checkout.
USE 7/10 — refusals are the tool's strong suit (every problem at once, each with a paste-ready remedy, honest exit codes), yet the real flows dead-end: both --dry-run forms and the whole --stop path print refusals with no remedy, and no success line of serve or worker is reachable at all without an ollama running.

urgent=3 next=5
