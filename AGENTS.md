# AGENTS.md — the one page to read before touching this repo

OpenCode and Codex read this file natively; Claude Code **2.1.277 and later**
reads it when the folder has no `CLAUDE.md` (Thariq, 2026-09-18 — the memory
docs page still says otherwise). On an older Claude Code nothing loads it, so
open the session with `read AGENTS.md first`. **There is no `CLAUDE.md`, no
pointer file and no symlink** — Glenn's ruling of 2026-09-18 is AGENTS.md
alone, and `internal/docs/agents_md_test.go` fails on one anywhere in the tree.

**Who this repo is for.** Nova Tools is machinery: command-line tools that AI
friends and people run **against** their own records, on their own machines,
with their own identities. The tools do not live in a self repo and never write
into one uninvited. Adoption is a choice — one tool is a fine number. Because a
tool ships instructions that execute, the bar here is higher than in a prose
repo: see [docs/CONTRIBUTING.md](docs/CONTRIBUTING.md).

## The first three commands

```
make build          # go build ./...
make test           # the fast tier, plus the per-package time budget
<tool> help         # e.g. nova-bus help — usage on stdout, exit 0, runnable examples
```

`make help` lists every target. `make check` is what CI runs: build, lint, test,
`test-e2e`, `test-lisp`. Go is the toolchain in `go.mod`.

## The ten rules never to break

Each line names the class test that enforces it. A class test reads this
repository's own text and refuses a SHAPE wherever it stands, so a rule lands
with its sweep of the tree or it does not land.

1. **Never `gh pr merge`, in any spelling, and never `--auto`** — nothing
   reaches the dev merge queue but a batch (`prmerge`).
2. **No test names a real network host** — unit tests test logic; mock with
   `httptest` or a local fake, and name `example.com`, `*.invalid` or `*.test`
   in fixtures (`net`).
3. **No fixed wall-clock wait on the CI path** — poll for the event up to
   `NOVA_TEST_WAIT`, or inject a fake clock (`waits`).
4. **No test asserts a bound under ten seconds** — a short bound asserts the
   machine's load, not the code (`wall clock`).
5. **No package over the per-package time budget** — per-change CI answers in
   one minute ideally, two at most (`slowtests`).
6. **The Makefile is the one entry for build, test and lint** — a workflow or a
   script calls a target, never its own `go test` line (`make`).
7. **Every command meets the onboarding standard** — `<tool> help` with runnable
   examples, a refusal that says what the input WANTS, a `### First run` section
   in [docs/CLI.md](docs/CLI.md) (`onboarding`).
8. **Every tool prints the one version line** — one shape, every binary
   (`version`).
9. **No `os.RemoveAll` of a computed path** — deletion is a verb over a
   validated path below a root (`removeall`).
10. **A test writes and lists only inside its own `t.TempDir()`** — never
    `os.TempDir()`, and every path a tool writes is named there
    (`testoutpath`, `sharedtemp`).

**The rest of the index, by name.** `templates`, `goenv`, `pathassert`, `busprogress`,
`outputs`, `windows-pr`, `windows-sizes`, `windows-table`, `one-windows-leg`,
`darwin-sizes`, `darwin-table`, `cache`, `pinned-actions`, `ci-ok`, `failed`,
`benchname`, `nightly-tags`, `selection`, `toolchainroots`, `hostseam`,
`kernel-components`. Every entry — the ten above too — is written out in
[docs/SPEC-CI.md](docs/SPEC-CI.md) under **The class tests** with its rule, the hurt
that bought it, its allowlist, its remedy line and its narrowings. Read the entry, not
the test. An allowlist only ever shrinks: a new row is a refusal, not a parking place.

**Two more that are not class tests.** A fake is **strict like the real tool** —
a lenient fake ships the real thing broken, so a fake refuses what the real one
refuses. And **tests run on the benches**: build and test on the bench the card
names before you call anything green.

## How work lands

1. Branch from `dev`. All merges go into `dev`; `main` is fast-forwarded from
   promoted `dev`.
2. Open a pull request into `dev`. Link the issue, say what changed and report
   the checks you ran.
3. A coordinator collects green pull requests into an **integration batch** on a
   `rowan/integration-*` branch and runs `nova-merge batch`, which builds, vets,
   tests and runs the lisp suite over the merged tree and prints a `BATCH OK`
   receipt for that sha.
4. **The one door** is `nova-merge land --repo <owner>/<name> --pr <n>
   --receipt-file <path>`. It is the only thing that admits anything to the
   queue, through `internal/merge.Enqueuer.Enqueue`, and it refuses a head that
   is not a batch's.

**You never merge your own pull request.** Not `gh pr merge`, not `--auto`, not
the web button. `--auto` does not queue here — it leaves a standing instruction
the forge executes later with no caller in the room, and that is how four red
pull requests landed on `dev` in one morning.

## Where the specs are

[docs/SPEC.md](docs/SPEC.md) is the umbrella: the **Conventions** every binary
keeps — exit codes (0 pass, 1 the check said NO, 2 could not run), **no guessed
paths** (`refusing to guess`, never a default directory), the one-line output
grammar, and the cap-and-count rule (`--fail-max`/`--max`, default 20, `0` means
all). Each tool then has its own normative spec: `docs/SPEC-BUS.md`,
`SPEC-MERGE.md`, `SPEC-SWARM.md`, `SPEC-WORK.md`, `SPEC-TOKENS.md` and the
rest under [docs/](docs/). A spec is normative — where the code and the spec
disagree, one of them has a bug and the tests decide which. **Read the spec
before the code.**

Also: [docs/ONBOARDING.md](docs/ONBOARDING.md) (the five-point standard every
command meets), [docs/CLI.md](docs/CLI.md) (the command reference and every
first run), [docs/TESTS.md](docs/TESTS.md) (the transcripts the tests execute),
[docs/TERMINOLOGY.md](docs/TERMINOLOGY.md).

## When a tool refuses

**The remedy on the line is the contract.** An unusable invocation costs one
line — `<tool>[ <verb>]: <what was wrong>; run: <tool> help` — and exits 2, and
where the guidance is a sentence of its own it follows on one further indented
line. Do that, rather than guessing. A class test's refusal names its
`remedy="…"`; do what it says instead of adding an allowlist row. A `FAIL` line
at exit 1 means the check ran and said NO — that is the check working.

**When the refusal is wrong, that is a gift.** Say three things, in this order:
what works, where it caught you with the exact sentence it printed, and the fix
you would make. Open an issue; do not work around it quietly.
