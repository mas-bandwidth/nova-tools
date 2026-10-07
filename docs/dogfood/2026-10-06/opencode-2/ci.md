# Dogfood: nova-ci — 2026-10-06, opencode-2

One friend, one tool, cold. I read only `nova-ci -h`, `nova-ci help`, every
verb's `-h`, and its page under docs/ (`docs/CLI.md` §nova-ci, `docs/TESTS.md`
§nova-ci, `docs/SPEC-CI.md`), then used every verb at least once with its real
flags: `slowtests` over the built-in `--example` stream, over a real
`go test -json` pipe from a scratch module (`example.com/sleepmod`), and over
hand-truncated event streams; `functional`; `local` for real (green);
`new-rule` and `new-verb` both `--dry-run` and really written into a scratch
checkout; `bench run` really on a Linux bench (copies, `--with-git`,
a failing command, `--fallback`); and `github receipt` under `--dry-run` and
against a closed port. No code changed; a finding is recorded and never fixed
here. Built from the checkout at `abb9bfecc729` as
`nova-ci v1.0.1-0.20261007153756-abb9bfecc729 darwin/arm64 go1.26.6`.

## Findings

1. `cat scratch/events.jsonl | nova-ci slowtests --package-budget 0.1 --allowlist scratch/allow-full.txt --load 4 --cpus 16`

   with `scratch/allow-full.txt` holding
   `example.com/sleepmod/beta<TAB>TestSlowThing<TAB>0.3<TAB>0.18s@run1` — the
   exact `package=` the tool's own `CI-SLOW` line printed for that test:

       nova-ci slowtests REFUSED: --allowlist scratch/allow-full.txt: line 1: package "example.com/sleepmod/beta" must be the full module-relative path; run: nova-ci slowtests -h
       exit=2

   The module-relative spelling `beta` is refused identically, and `--sleeps`
   refuses a `beta<TAB>TestSleepy<TAB>run1` row the same way. Expected both
   accepted: the banner says "slowtests and functional work in any Go module"
   and lists `--allowlist` and `--sleeps` under that heading, and the value
   refused is the one the tool itself prints as `package=`. Only a row whose
   package starts with `cmd/`, `internal/` or `tools/` is accepted (a row
   `internal/pkg<TAB>TestA<TAB>4.5<TAB>3s@run1` loads, but matches no package
   in a foreign module), a shape the help names nowhere except its one example
   row. In a module without those three prefixes both flags are dead, and the
   refusal names the wrong want and offers no shape that works. Grade: URGENT.

2. `nova-ci new-rule --root scratch/foreign --dry-run probe`

   where `scratch/foreign` is a bare Go module (`go.mod`, `module example.com/foreign`) with no nova-tools layout:

       would write internal/ci/<rule>_class_test.go
       would write internal/ci/testdata/<rule>/fixture.txt
       would write make/rule_<rule>.mk
       exit=0

   (the rule name `probe` stands in for `<rule>` in that listing)

   Expected the refusal the same flag gives a directory with no `go.mod`
   ("is not a nova-tools checkout (no go.mod)"): the verb's help says it "needs
   a nova-tools checkout", and a foreign module's `go.mod` is not one. A real
   run would write this repository's rule skeleton into somebody else's
   module. Grade: URGENT.

3. `printf '' | nova-ci slowtests --load 4 --cpus 16`

       CI-SLOW FAILED packages=0 slowest=none: looked at nothing; run: nova-ci slowtests --allow-empty
       CI-LOAD load=4.00 cpus=16 per-cpu=0.25: measured, not a verdict
       exit=1

   Expected what the page under docs/ promises: `docs/TESTS.md` §nova-ci says
   "with an empty stream it reads zero packages and prints `CI-SLOW OK packages=0 slowest=none`", and `docs/SPEC-CI.md`'s red test 3 says "An empty
   stream is `CI-SLOW OK packages=0 slowest=none`, exit 0." The tool's own
   banner is truthful (exit 1 without `--allow-empty`), but the page a stranger
   reads disagrees with the binary on the status word and on the exit code —
   the one thing a CI path reads. Grade: URGENT.

4. `nova-ci bench run --host <bench> --dir / -- go version`

       CI BENCH host=<bench> run=nova-bench/runs/run.EF5bsd82 exit=- removed=yes
       BENCH-RUN REFUSED: <bench>: copying / to nova-bench/runs/run.EF5bsd82/repo: open /.file: permission denied ; run: nova-ci bench run -h
       exit=2

   Expected the refusal `--root /` and `--root ~` get at once, before any bench
   is reached: `docs/SPEC-CI.md` describes `TestBenchRunRefusesUsage` as
   refusing "a `~`, `..`, home or root path, and a missing tree ... before any
   bench is reached". `--dir /` instead opened ssh, made a run directory on the
   bench and began copying the machine's root; `--dir ..` was likewise accepted
   and began a 1.6 GB copy before I stopped it. Only `--dir ~` and a missing
   tree are refused locally. Grade: NEXT.

5. `nova-ci functional ./scratch/mod/gamma --json`

       nova-ci functional REFUSED: unknown flag "--json" (functional takes no flags, only package directories such as ./cmd/nova-table or ./internal/...); run: nova-ci functional -h
       exit=2

   `version`, `local`, `new-rule`, `new-verb`, `bench run` and `github receipt`
   refuse `--json` too; only `slowtests` accepts it and only its help names the
   flag. Expected the standard's one shape across the set (AGENTS.md section 2:
   "Every verb accepts `--json`"). Grade: NEXT.

6. `cat scratch/events.jsonl | nova-ci slowtests --budget 60 --load 4 --cpus 16`

       CI-SLEEPS test=TestSleepy package=example.com/sleepmod/beta: skipped for a wall-clock wait and not on the\x20SLEEPS\x20ledger\x20(no\x20--sleeps\x20given); inject a clock or tag it //go:build functional
       CI-LOAD load=4.00 cpus=16 per-cpu=0.25: measured, not a verdict
       exit=1

   Expected plain spaces: the line's reason and remedy are prose, and every
   other rendered field is readable. The default ledger sentence is passed
   through a field escaper that leaves literal `\x20` runs in the middle of the
   prose. Grade: NEXT.

7. `cat scratch/events.jsonl | nova-ci slowtests --test-budget 0.1 --json --load 4 --cpus 16`

       {"result":...,"items":[...,{"kind":"sleeps","fields":{"test":"TestSleepy","package":"example.com/sleepmod/beta"}}]}
       exit=1

   Expected the one-value-two-renderings contract: the reason and remedy prose
   the line rendering prints survives as `text` in the JSON item. The sleeps
   item carries only `test` and `package`, so a JSON reader learns the count
   but not what to do, while the line reader gets the remedy. Grade: NEXT.

8. `printf '{"Action":"start","Package":"example.com/x"}\n' | nova-ci slowtests --load 4 --cpus 16`

       truncated: example.com/x started and never ended
       CI-LOAD load=4.00 cpus=16 per-cpu=0.25: measured, not a verdict
       exit=1

   Expected a line leading with the verb's status word, as `CI-SLOW` and
   `CI-SLEEPS` do, and a remedy or next command; the truncated run is a real
   refusal but the line is unprefixed free text a `CI-` grep cannot find and
   gives the reader nothing to run. Grade: NEXT.

9. `nova-ci bench -h`

       usage: nova-ci bench <run> [flags]
         nova-ci bench run --host <h> [--fallback <h>] --dir <tree> [--root <dir>] [--cache <dir>] [--with-git] -- <go command>
       `nova-ci bench <verb> -h` lists a verb's flags.
       exit codes: nova-ci: test-time budgets over go test -json output, and this repository's own CI steps

   Expected the group's own exit table, the way `local -h` and `github receipt -h` open theirs with `exit codes: 0 done, ...`. The `exit codes:` label is
   glued to the top-level banner's first line, and the whole top-level banner
   then follows the group's three usage lines. Grade: NEXT.

10. `nova-ci bench run --host <bench> --root / --dir scratch/mod -- go version`

        BENCH-RUN REFUSED: --root "" is the home or the root itself; run: nova-ci bench run -h
        exit=2

    Expected the value the caller typed, `--root "/"`; the refusal prints an
    empty quoted value, so the reader cannot tell which guard was hit. Grade:
    NEXT.

11. `nova-ci github receipt --from-runner --redis 127.0.0.1:6399 --repo mas-bandwidth/nova-tools --sha <40hex> --run-id 7 --workflow CI --conclusion success`

        redis: 2026/10/07 11:58:27 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:6399: connect: connection refused
        redis: 2026/10/07 11:58:28 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:6399: connect: connection refused
        redis: 2026/10/07 11:58:28 pool.go:762: redis: connection pool: failed to dial after 5 attempts: dial tcp 127.0.0.1:6399: connect: connection refused
        exit=1

    Four dated `redis:` client log lines leak to stderr before the tool's one
    correct `nova-ci github receipt FAILED: ...` line, and they say "failed to
    dial after 5 attempts" next to the banner's "A refused write is tried once,
    not retried". The verdict and exit are right; the noise is not. Grade: NEXT.

12. `cat scratch/events.jsonl | nova-ci slowtests --budget 60 --package-budget 0.1 --load 4 --cpus 16`

        CI-SLOW package=example.com/sleepmod/beta seconds=0.2s budget=0.1s slowest=TestSlowThing:0.2s,TestSleepy:0.1s
        CI-SLOW package=example.com/sleepmod/alpha seconds=0.1s budget=0.1s slowest=TestMiddle:0.1s
        exit=1

    Expected a refusal or a note saying which budget won: the usage line prints
    the two as alternatives (`[--budget <seconds> | --package-budget <s>]`),
    and the tool silently applied `--package-budget 0.1` and ignored
    `--budget 60`. Grade: NEXT.

## What the tool got right

- `slowtests --example` runs from the binary alone and reproduces `docs/TESTS.md`
  line for line; `--max` prints the first `--max` findings plus the
  `CI-SLOW MORE shown= total=` line naming the flag, and `--max 0` prints every
  finding; `--enforce` is the only thing that makes a `CI-SLOW` line fail.
- The refusals are one line that names every independent problem at once with a
  remedy: `functional` names the flag *and* the accepted arguments, `github receipt` names every bad field and the missing store in one line, `new-rule`
  and `new-verb` refuse a bad name, a missing `func main` and an existing file
  with what to do next, and `bench run` refuses a host that ssh could read as an
  option, `~`, `..`, home or root `--root`/`--cache` before any dial.
- `bench run` did what its help says on a real bench: it copied the tree,
  streamed the command, left `.git` out unless `--with-git`, removed exactly the
  run directory it made (`removed=yes`) for a passing command, a failing command
  (its own exit status propagated) and a refused copy, and passed over a
  non-answering host with one `CI BENCH PASSED ... next=<h>` line.
- `local --dry-run` printed the merge base, the packages and the exact `make`
  line, and the real `local` ran the tier green (`packages=2 seconds=25.7s red=0 make-exit=0`, exit 0).

READ 7/10 — the banner answers what the tool does, how it works and how to
start, every verb states its effect and its exit table, one line carries every
refusal and a remedy; the docs page that lies about the empty-stream exit code,
the glued banner on `bench -h`, and a `--allowlist`/`--sleeps` row shape the
help never defines a working form for keep it from a 9.

USE 7/10 — every verb ran for real and did what its help says on a first read,
including the bench and a green `local`; the allowlist ledger is unusable in any
module without a `cmd/`, `internal/` or `tools/` tree, `--root` accepts a
foreign `go.mod` as a nova-tools checkout, and `--dir` skips the path guards the
other two path flags get, so three real paths stumble.

urgent=3 next=9
