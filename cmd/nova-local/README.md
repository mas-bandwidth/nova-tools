# nova-local

## What it is

nova-local: run local models: what an engine has, one model served at a chosen context, and a worker description nova-worker accepts

## Why use it

Run a model on your own machine, or on one of your fleet.

## Install

```sh
go install github.com/mas-bandwidth/nova-tools/cmd/nova-local@latest
nova-local version
```

## First run

The transcript below is executed line for line by the docs tests; the full record is in [docs/TESTS.md](../../docs/TESTS.md#nova-local).

Fixture: `cmd/nova-local/testdata/`: the worker directory (`home/`) and the
placeholder key file (`local.key`). `cmd/nova-local/firstrun_test.go` runs each
`$` line from a checkout root in one sitting against a fake ollama daemon that
has `gemma4:12b` in the shared store of the AI root `/ai`, a box at load 14.2
with 61 GiB free of 128, and a clock the warm-up advances by three seconds; no
engine, network or real time is used. `./gemma.json` is a file in the test's
own directory.

```text
$ nova-local status
STATUS OK engines=1 answering=1 loaded=0 models=1 mem_used=71940702208 mem_free=65498251264 mem_total=137438953472 wired_cap=unset load1=14.20
STATUS ENGINE name=ollama state=up base=http://127.0.0.1:11434/v1 loaded=0 advertised=1 store=/ai/shared/models/ollama shared=yes

$ nova-local serve --engine ollama --model gemma4:12b --num-ctx 32768 --seed 7
SERVE OK engine=ollama model=gemma4:12b serve_as=gemma4-32k digest=sha256:c0e0c3e5b4a1 num_ctx=32768 keep_alive=30m temperature=0 seed=7 created=yes load=3s mem_used=71940702208 mem_free=65498251264 mem_total=137438953472 wired_cap=unset load1=14.20 engines=1 store=/ai/shared/models/ollama shared=yes

$ nova-local worker --engine ollama --model gemma4-32k --out ./gemma.json --name gemma --harness opencode --harness-args run,--model,ollama/{model},--,{prompt} --worker-dir $PWD/cmd/nova-local/testdata/home --key-file $PWD/cmd/nova-local/testdata/local.key --env-var OLLAMA_API_KEY --usage opencode --deadline 20m
WORKER OK engine=ollama model=gemma4-32k out=./gemma.json workers=1 provider=ollama harness=opencode deadline=20m base=http://127.0.0.1:11434/v1
WORKER NOTE run it one worker at a time (--workers 1): an engine is a queue, and two workers interleave it
```

## Verbs

The [nova-local section of the command reference](../../docs/CLI.md#nova-local) documents every verb's flags, effect and exit codes.

- `status`
- `serve`
- `worker`
- `version`
- `help`

## Spec

The contract is [docs/SPEC-LOCAL.md](../../docs/SPEC-LOCAL.md).
