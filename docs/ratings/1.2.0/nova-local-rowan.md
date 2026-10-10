# nova-local READ and USE rating, nova-tools 1.2.0

Rater: a cold rater, mercurys-2.5 under opencode
Build: 17ec8d256a04943b921987ebd9c19458917b19e9
READ: 7.5/10
USE: 7/10

nova-local: run local models: what an engine has, one model served at a chosen context, and a worker description nova-swarm accepts. The tool has verbs: status, serve, worker, version, help.

## Reasons

READ. The README shows status, serve, and worker examples with concrete output. The first-run transcript is complete and runnable. The spec is in docs/SPEC-LOCAL.md. The serve verb has many flags but they are documented. What holds the score: the worker verb has many flags with no grouping.

USE. The status command shows engine state. The serve command registers a model. The worker command creates a worker spec. What holds the score: there is no way to test a worker configuration without actually running a swarm.

## Findings

| # | where | finding | fix | size |
|---|---|---|---|---|
| 1 | cmd/nova-local/main.go:worker | worker --help shows many flags without grouping | group flags as engine, output, worker config, and options | S |
| 2 | cmd/nova-local/main.go:serve | serve --help does not list available models from the store | add a --list-models flag | S |
| 3 | cmd/nova-local/main.go:worker | no --dry-run for worker to preview the output file | add --dry-run that prints the worker spec to stdout | M |

## Good, keep

- Clear first-run transcript with realistic output.
- Status shows detailed engine state including memory and load.
- Worker output can be saved to a file for swarm use.

## Compared with earlier ratings

This tool was not rated in 1.1.0 as it did not exist. The first rating shows a functional local model runner.
