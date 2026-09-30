# Deprecated tools: the first-run transcripts their tests execute

## nova-work

No fixture: the graph file is created by the run itself under `--graph`, and every
line below is local — plain JSON nodes and `:deps` edges, no Redis, no remote, no
network. A `:deps` cycle is refused at exit 2 before anything is written.

The bounded reader for a `.work` plan. No fixture and no network: create the
exact local input before the transcript, then every line below reads local bytes
alone:

```sh
printf '%s\n' '(:plan :version 1 (:node :id "n1" :kind docs))' > ./work.work
```

This minimal plan demonstrates `plan check`; `plan expand` also requires each
node to declare `:output`.

`cmd/nova-work/firstrun_test.go` performs that setup and runs each `$` line
against it, so the `./work.work` below is a fresh file per run.

### First run

```text
$ nova-work dependencies --graph ./deps.json --node b
DEPENDENCIES OK nodes=1 edges=0

$ nova-work dependencies --graph ./deps.json --node a --needs b
DEPENDENCIES OK nodes=2 edges=1

$ nova-work ready --node a --graph ./deps.json
READY node=a ready=false blocker=b state=open resolver="nova-merge queue"

$ nova-work ready --node b --graph ./deps.json
READY node=b ready=true

$ nova-work plan check --file ./work.work
PLAN OK file=./work.work bytes=47 version=1 nodes=1 edges=0
```

The two verbs read one plan and one graph as data; a `:deps` cycle is refused at
exit 2 before anything is written.

### Refusals

```text
$ nova-work
WORK REFUSED: a verb is required; run: nova-work help

$ nova-work dependencies --graph ./deps.json --node b --needs a
nova-work dependencies: rule 3: :deps edges contain a cycle: b -> a -> b; run: nova-work help
```

Both exit 2 and print one line on stderr, and the two spellings are deliberate
rather than a drift: `WORK REFUSED:` is what the client spec gives an invocation
that could not run at all, and `nova-work <verb>:` is what a verb that ran and read
its input says about the input. `cmd/nova-work/firstrun_test.go` executes this
block as well as the one above; until 2026-09-19 it executed neither refusal, and
what that cost is written below.

### The event bridge

`nova-work events` publishes the pub/sub messages `nova-merge react` subscribes to
(docs/SPEC-JOBS.md, "Events, not ticks"). It makes no model call and writes no
record: the bus is a signal, git is the record.

It has no `$` line above because it cannot have one. The relay needs a local Redis
(`--redis <addr>`), and naming the repository with `--repo` switches ON a
`gh pr list` fallback that reaches the forge — without `--repo` only the stream is
bridged. `cmd/nova-work/events_log_test.go` drives it against a miniredis and a
fake forge, and the usage banner's `example:` block runs it without `--repo`. That
is where a line needing a running service belongs: every `$` line in this file is
one a stranger can type on a fresh bench.

This description used to head a SECOND `## nova-work` section further down this
file. Because `onboarding.Section` reads the first match of a name, no test ever
executed it, and it drifted into a refusal sentence the binary had stopped printing
(`nova-work: no verb given`) and an `events` line carrying `--repo`. Both
reproduced as DEFECT on space and on hulk in the 2026-09-18 two-bench run while
every test in this repository was green. `internal/ci/onboarding_test.go` now
refuses a repeated `## ` heading here.

