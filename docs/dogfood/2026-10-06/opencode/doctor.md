# Dogfood: nova-doctor, cold

One run, 2026-10-06, by an AI worker in opencode, at 0760eac79 (the tip of sprint/mechanical-2026-10-02,
from which v1.1.0 is cut). Read before use: `nova-doctor -h`, `nova-doctor help`,
`nova-doctor run -h`, `nova-doctor version -h`, `nova-doctor help run`, `nova-doctor help version`,
and docs/SPEC-DOCTOR.md. Nothing else about the tool was read.

The binary was built from the checkout (`go build -o <job>/tmp/nova-doctor ./cmd/nova-doctor`) and
run from a scratch directory outside the checkout; `run` was also run inside the checkout. Three
fake `nova-*` tools were written into the scratch directory's `fakebin` (two answering `v1.1.0`,
one recut to `v1.0.9` by overwriting its script) and put on `PATH` for the `self` runs. Long
absolute paths are elided as `…` in quoted output; the commands are as typed.

Coverage: every verb (`run` bare and spelled, `version`, `help`), every flag (`--check` with a
known name twice, with `--local` and `--strict` and `--json`, and with an unknown name), `--help`
spelled long, and the refusals: unknown verb, unknown flag, unknown check name, `help` for an
unknown verb, a file operand, a directory operand. The refusals were the best part: every one
printed one line, named what exists, and ended in `run:` with a command that runs. Exit codes
matched the help's table (0 ok, 2 fail or usage). No check produced a `warn` in these runs, so
exit 1 under `--strict` is untested by this record. Use took about half an hour.

## Findings

1. `PATH=fakebin:$(dirname $(which go)) nova-doctor run --check gosdk` (in the checkout, GOCACHE
   the shared build cache, GOFLAGS=-mod=readonly)
   printed (first 3 lines; it is one line):
   ```
   DOCTOR gosdk fail GOCACHE …/go-build is not writable fix: export GOCACHE to a directory this user writes, e.g. export GOCACHE=$HOME/.cache/go-build (docs/SETUP.md, dep-go-sdk-b.w2)
   ```
   expected: the verdict on the directory. It is writable — a probe file created and removed by
   hand says so — and the same state with the machine's usual `PATH` prints `… GOCACHE … writable …`
   and exits 0; putting only `/usr/bin:/bin` back on `PATH` flips the verdict back to `ok`. The
   writability probe depends on a utility found on `PATH`, not on the directory, so a bench whose
   `PATH` is just its tool directory plus go's directory is told its writable cache is not
   writable, and the fix line sends it to change the environment that was never wrong.
   grade: URGENT (a wrong result: evidence is the tool's whole product)

2. `nova-doctor run <checkout>/go.mod` (typed from the scratch directory; `nova-doctor
   <checkout>` behaves the same)
   printed (first 3 lines; they are two):
   ```
   DOCTOR gosdk fail no go.mod here: the bench's toolchain is not named fix: run nova-doctor in a checkout of this repository, whose go.mod names the bench's toolchain (docs/SETUP.md, dep-go-sdk-b.w2)
   DOCTOR self fail no nova-* tool is on PATH fix: nova-update apply --file <manifest> <tool>, or put the directory holding the nova tools on PATH
   ```
   expected: either the checks run against the checkout the operand names (its `gosdk` would read
   that `go.mod`) or a refusal saying what a file operand is for. The refusal grammar names the
   form as accepted — `nova-doctor badverb` answers `"badverb" is no verb and no file; … a file is
   given by its path (./badverb)` — so an operand is taken and then ignored without a word: with
   the checkout's own `go.mod` named, the answer is still `no go.mod here`, about the working
   directory, with nothing printed about the discarded operand.
   grade: URGENT (a silent ignore answered about the wrong directory)

3. `nova-doctor run --local` (in the checkout)
   printed (first 3 lines; they are two):
   ```
   DOCTOR gosdk ok go 1.26.6, go.mod toolchain 1.26.6, GOCACHE …/go-build writable, GOFLAGS -mod=readonly
   DOCTOR self fail no nova-* tool is on PATH fix: nova-update apply --file <manifest> <tool>, or put the directory holding the nova tools on PATH
   ```
   expected: the line the spec promises, `DOCTOR local skipped=<name,name> (...)`, or at least one
   line saying nothing was skipped. `--local` printed nothing of its own, so a reader cannot tell
   the flag was seen; on this tree it is a silent no-op, because both checks are local.
   grade: NEXT (a silent no-op where the spec promises a line)

4. `nova-doctor run --json` (in the checkout)
   printed (first line, cut):
   ```
   {"exit":2,"results":[{"check":"gosdk","dependency":"the Go toolchain","status":"fail","evidence":"no go.mod here: the bench's toolchain is not named",…
   ```
   expected: the shape docs/SPEC-DOCTOR.md shows, `{"exit":N,"results":[…],"skipped":[…]}`; the
   `skipped` list never prints, empty or not, so the documented object and the printed object
   disagree.
   grade: NEXT (the spec's JSON shape is not the printed one)

5. `nova-doctor run -h`
   printed (first 3 lines):
   ```
   usage: nova-doctor run [flags]
   checks: gosdk, self
   example: nova-doctor --local
   ```
   expected: docs/SPEC-DOCTOR.md, the tool's page, to name `gosdk` and what it reads (`go.mod`'s
   toolchain line, GOCACHE, GOFLAGS) the way it names `self` with its dependency. The page's
   "The checks" holds only `self`, so `--check gosdk` was run blind from the help's name alone.
   grade: NEXT (the page under docs/ lags the binary by a whole check)

6. `nova-doctor -h`
   printed (first 3 lines, then the line in question):
   ```
   nova-doctor: says what is missing for the nova tools to work, and the one line that fixes each

   how it works: each check covers one dependency: ok, warn or fail, with evidence and a fix line.
   ```
   and, later in the same banner: `Every verb but run takes --json: run prints one
   `DOCTOR <check> ok|warn|fail <evidence> [fix: <line>]` line per check; with --json it prints
   the same results as one object.`
   expected: a true sentence. `run` takes `--json`: `run -h` lists it and `nova-doctor run --json`
   prints the object; the sentence contradicts its own second half and the flag list.
   grade: NEXT (the banner's own sentence is false)

7. `nova-doctor run -h`
   printed (line 5):
   ```
   --check <help run>  run only this check (repeatable); the names are in help run
   ```
   expected: `--check <name>`, the same placeholder the top-level usage line uses; `<help run>`
   reads as a typo and cost one turn to decode as "the command `nova-doctor help run`", which does
   print the check names.
   grade: NEXT (a garbled placeholder in the main verb's help)

## Every invocation, with its exit

- `nova-doctor -h`, `nova-doctor --help`, `nova-doctor help`, `nova-doctor help run`,
  `nova-doctor help version`, `nova-doctor run -h`, `nova-doctor run --help`,
  `nova-doctor version -h` — 0.
- `nova-doctor`, `nova-doctor run` — 2 (both checks fail off a checkout).
- `nova-doctor run --local`, `nova-doctor run --strict` — 2 (no skip line; no warn to escalate).
- `nova-doctor run --json`, `nova-doctor run --check self`,
  `nova-doctor run --check gosdk --check self` — 2.
- `nova-doctor run --check gosdk` (in the checkout) — 0.
- `nova-doctor version`, `nova-doctor version --json` — 0.
- `nova-doctor run --check nosuch`, `nova-doctor help nosuchverb`, `nova-doctor badverb`,
  `nova-doctor --badflag`, `nova-doctor run --strict --badflag` — 2, each one refusal line with a
  `run:` remedy.
- `nova-doctor ./scratchfile.txt`, `nova-doctor run ./scratchfile.txt`,
  `nova-doctor <checkout>`, `nova-doctor run <checkout>/go.mod` — 2, the operand unmentioned.
- `PATH=fakebin … run --check self` — 2 (the mute tool named: `nova-mute did not answer version
  with a version line`, fix names it); after removing it — 0 (`2 tools on PATH, all v1.1.0`);
  after recutting one to `v1.0.9` — 2 (`the tools are not one release: nova-beta=v1.0.9 differ
  from v1.1.0 (1 tools)`, fix `nova-update apply --file <manifest> nova-beta --version v1.1.0`).
  The `self` check's evidence and fix are exact.

READ 7/10 — the banner answers what, how and how-to-use and every help door opens at exit 0, but
the page under docs/ misses a whole shipped check and the `--json` `skipped` key, and the help
carries a garbled placeholder and a sentence about `--json` that is false.
USE 6/10 — every verb ran, every refusal carried a remedy and a next command, and the fix lines
were real commands; but two runs answered wrong (a writable GOCACHE judged by `PATH`, a file
operand judged by the working directory), and a doctor whose evidence lies twice in half an hour
is not yet one to depend on.

urgent=2 next=5
