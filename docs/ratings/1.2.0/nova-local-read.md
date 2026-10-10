# nova-local READ and USE rating, nova-tools 1.2.0

Rater: a cold rater,
build: 17ec8d256a04
READ: 7/10
USE: 7/10

## Reasons

READ. The README states the tool in one sentence: "run local models: what an engine has, one model served at a chosen context, and a worker description nova-swarm accepts". The First run block shows status, serve, and worker commands with expected output.

What holds READ at 7: the serve verb uses --num-ctx which is documented as "context size" but the README doesn't say what a reasonable value is for different model sizes. The worker verb uses --harness-args with a comma-separated list that a cold reader cannot parse without the spec.

USE. The tool talks to an engine at an address (default 127.0.0.1:11434). The status verb shows engine state and memory. The serve verb starts a model. The worker verb produces a JSON file for nova-swarm.

What holds USE at 7: the worker verb requires a key-file and worker-dir but doesn't show how to create them. The --env-var flag accepts OLLAMA_API_KEY but the README doesn't show an example of passing other env vars.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | README.md:25-28 | The --num-ctx flag is used but the README doesn't suggest reasonable values for different models | Add a note suggesting context sizes for common models | S |
| 2 | README.md:35-38 | The worker example uses --harness-args with comma-separated values that aren't explained | Add a flag description or example breakdown | S |
| 3 | README.md:31-34 | The worker verb requires key-file and worker-dir but shows no setup example | Add a setup section showing key and worker dir creation | M |

## Good, keep

The install section shows the go install pattern. The status verb shows both engine and memory information. The serve verb shows the expected output format.

## Compared with earlier ratings

| earlier | now | evidence |
|---|---|---|
| 1.1.0: nova-local did not exist | NEW | first 1.2.0 rating |
