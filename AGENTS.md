# AGENTS.md — generated map

Do not edit. `make map` regenerates this file. Rules: [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md). Glenn, 2026-09-18: AGENTS.md alone — no `CLAUDE.md`, no pointer, no symlink.

Nova Tools is machinery: command-line tools that AI friends and people run against their own records, on their own machines, with their own identities. Adoption is a choice — one tool is a fine number. Rules: [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md).

```
make build          # go build ./...
make test           # the fast tier, plus the per-package time budget
make map            # regenerate AGENTS.md and per-directory maps
```

The standard below is [docs/STANDARD.md](docs/STANDARD.md), embedded whole; every PR meets it.

## The standard: how a nova tool is built

This is the standard every tool and module in this repository is built to, and the one a new tool is built to first. Each rule names the check that holds it, so the standard is a set of tests, not a wish. The goal all of it serves: the minimal code that is performant and correct. Less code is the best code; the tests are what let the code get less.

### 1. A tool is for an AI

The reader of every banner, refusal and result is an AI meeting the tool cold and deciding whether to depend on it. The question a tool answers is "is this a good tool for an AI to use?" Tools are rated by other AIs, not by their makers, and the ratings drive the fixes.

A rating is read beside the rater's size: a tool a small model finds dense and a frontier model finds clear is a frontier tool and its help says so; a tool every size finds hard is the one to fix. The maker never rates the maker's own work.

The properties that made the difference in those ratings, in order:

- **Runnable on its own small input with no infrastructure.** A tool has a store-free or dry-run form so a reader can try a real verb before adopting it.
- **The banner answers three questions.** Line 1: what it does, in one sentence. Then how it works, in at most five lines naming the tool's nouns and where its state lives. Then how I use it: a first run of three to six real commands in an `example:` block that runs as printed. Check: `TestEveryCommandMeetsTheOnboardingStandard` (internal/ci).
- **It refuses to guess, and recovery takes one turn.** A missing input is named, with what it wants, its unit and its role, every problem at once, so a reader fixes the call once. A bad state is refused with the remedy in the line. The tool never does something plausible instead.
- **Its effects are explicit.** Every verb's help says which it is: an inspection, a local write, a store write, or an external delivery (a push, a send, a machine). A dry run says which reads it still makes.
- **It never fails silently, and every refusal carries a breadcrumb.** No OK word on a non-zero exit, no failure word on zero, no error discarded without a line saying why it is safe (`// ignored: <reason>`). Checks: `TestEveryRefusalCarriesARemedy`, `TestNoOKOnFailure`, `TestNoErrorIsDiscarded` (land with the never-silent PR, which brings the three together).
- **Exit codes tell the truth and the banner states them.** 0 done; 1 the verb ran and said no; 2 usage or a store that did not answer; a tool's further codes listed. Every verb's `-h` quotes the table.
- **One output structure, two renderings.** Every verb builds one value: `result {verb, status ok|refused|failed, exit, remedy}`, `facts {k: v}`, `items` (typed rows, with `more {shown, total}` when a listing is bounded), `notes`. The typed line is one rendering (`VERB OK k=v ...`, one item per line, `MORE`, `NOTE`); `--json` is the other, from the same value, so the two cannot drift. Every verb accepts `--json`. Check: the per-verb envelope test (lands with the tool-skeleton PR).
- **A verb that writes has a dry run.** `--dry-run` prints the plan the real run would take, from the same code path, and writes nothing; a reader can see what a verb does before letting it.
- **Output is bounded and keeps its totals.** A listing is cut by `--max` with a `MORE shown=<n> total=<n>` line, never silently; totals, provenance and stable identifiers survive the cut.
- **Idempotent by an op id.** A write verb takes `--op <id>`; the same id again returns the recorded result and changes nothing, so a retry after a timeout is safe.
- **Nothing hidden.** Every state a tool writes can be read back with a verb. No silent fallback, no default that stands in for a missing input.
- **What an AI worker is handed is a whole brief.** A card is a complete child brief: the repository, the branch and base, the working directory, what it may not touch, the exact commands, the rules, what to write in its result and how it is reported. A one-line card makes a wandering child. The card lint holds every rule the coordinator gives a child, and `add` refuses a card that fails it.
- **One shape across the set.** The same flag means the same thing in every tool: `--json`, `--max` with its `MORE` line, `--redis` with the seat-first default, `--actor`, `--op`, `-h` with the exit table. A tool is its verbs plus one call into the shared skeleton (the `tool` package under `internal`, which lands with the tool-skeleton PR): verb dispatch, help, version, the banner, the refusal printer, the standard flags, the envelope encoder. A new tool inherits the whole shape.

### 2. General, never one fleet

The tools carry the concepts and none of the fleet: no host, machine, seat, person or friend name, no address, no home path, in any file. A fleet is one configuration in nova-config; identity, inventory, widths and ceilings come from it and are never kept a second time. Check: `TestGeneralityText` over every living text file, with a shrink-only ledger.

### 3. Correct: a model where there is a machine

Anything with states and transitions (a protocol, a session, a lease, a queue, a landing, a table, a tick) has a TLA+ module beside it: the actions are the verbs, the duties and the outside events; the invariants and liveness are the rules the design keeps. TLC runs on a bench, never on a working machine, and its records are kept fresh by hash (`TestTLCRecordsCoverCurrentModels`). What TLC can check is safety and liveness; reachability is shown by a reversed witness whose failing property is the named one; multi-step and statistical properties belong to the drive and the differential test against the reference model; a real run's log is a trace the model can be checked against.

### 4. Performant: a number in the gate

A requirement that is not a test is a hope. A design is not settled until each layer has its cost column: how many things and the bound per operation, with the structure chosen for that number before code is written. Every layer states its cost bound as a number (a tick under one second at a stated size; round trips per verb; a drive's floor), the gate prints and asserts it, and the report gives the measured number beside the bound. Redis is always batched: one round trip per verb, the connect is a handshake only. A row at a time is never done; the set is planned in one call. When a bound is missed, the profile names the decision and the structure changes; nothing is tuned around a wrong structure.

### 5. Minimal: less code is the best code

- A verb nobody runs is removed. Used means delivered: a dated log shows it ran; a verb, tool or module the record does not show is not kept because it might be wanted.
- Nothing lands that no shipped tool reaches. Reachability over the tools' roots (three operating systems, every build tag) is held at zero by a class test (lands with PR #4697 and the dead-code passes); a deletion declares its tests in the ledger.
- Duplicated function becomes one lower-level module the others depend on; a copy is a bug.
- The standard library or a well-known module is used before anything is written by hand: `exec.CommandContext` with a deadline and `WaitDelay` for every subprocess, `encoding/csv`, `text/tabwriter`, `slices`, `maps`, `cmp.Or`, `x/mod/semver`. Kept custom on purpose, with the reason beside it: a reader that must not evaluate, an escape that is a house rule, an encoder that must match a store byte for byte.
- Go, except where the host demands otherwise: Lua inside Redis, TLA+ modules, Lisp data, ansible and workflow YAML, unit files.
- Docs are present tense only: what the tools do now, never what they did.

### 6. Tests

Tests are the reason the code can change: without them a change breaks things unseen. They are table-driven: the code once, the cases as data, one function with named cases and `t.Run`. Shared rigs, fakes and harnesses are small test packages a test imports (a `testkit` tree under `internal`), never copies. Assertions are one line (testify). A test names what it pins. Overlap from layering is expected; a test is removed only when it covers and states nothing the others do not. Unit tests own no sockets; functional tests run in a container that cleans up every dependency. A test never asserts wall-clock time on a shared machine.

### 7. Landing

Every change is small, is read cold by a reader with no memory of the author's reasoning, with mutation probes where a rule is claimed, and lands only on that read. A class test that goes red is fixed at its cause, never weakened, never exempted without a written reason. A release ships the very best code of the moment, everything known fixed first, its docs written once from the finished binaries, its notes for humans and AIs.

### 8. The rhythm

Work expands: a stretch gives every idea code, and the tree grows wider than the working set. Then it contracts: the shape that ran is kept, the tree is pulled down to what it needs, and it is secured with the numbers above before the next expansion starts from less. A contraction is scheduled, never left to chance; its sign is a tree growing while the working set does not. The passes, each a measured number: scope down and release; delete the replaced; reachability to zero; the superseded against the delivery record; duplications into lower modules; tests into small shared packages; libraries over hand-rolled code; Go where the host does not demand otherwise; performance as the gate; the comfort pass by ratings; generality over every file; models where they pay.

| dir | purpose | guard | command |
| --- | --- | --- | --- |
| `.github/` | CI workflows and automation | `go test ./internal/ci` | `make test` |
| `assets/` | static assets and schemas | none | none |
| [cmd/](cmd/AGENTS.md) | 16 nova command-line tools | `nova-ci local` | `make build` |
| `deprecated/` | tools and modules not in use, kept for reference only; never built, tested or maintained | none | none |
| [docs/](docs/AGENTS.md) | specs, guides, and proposals | `go test ./internal/docs` | `go test ./internal/docs` |
| `fleet/` | fleet loop units and bench templates | none | none |
| `infra/` | runner images and scripts | none | none |
| [internal/](internal/AGENTS.md) | packages and libraries | `nova-ci local` | `make test` |
| `profiles/` | the darwin sandbox profile template internal/sandbox embeds, and its check script | `go test ./internal/sandbox` | `go test ./internal/sandbox` |
| `scripts/` | maintenance and operational scripts | none | none |
| `testdata/` | shared test fixtures and data | `go test ./internal/ci` | `make test` |
| `tla/` | the TLA+ models of the state machines and their runners | `go test ./internal/tlc` | `make tlc-test` |
| [tools/](tools/AGENTS.md) | developer and bench tools | `nova-ci local` | `make map` |
