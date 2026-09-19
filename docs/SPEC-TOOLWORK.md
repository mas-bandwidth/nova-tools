# Mechanical tool work — specification (draft 1, 2026-09-19)

Glenn, 2026-09-19: *"I want to upgrade our tools so we can push more work to swarms
mechanically."*

This document specifies the seven enablers that let a swarm do work **on these tools
themselves** with no person and no model standing between a card's result and the
decision to keep it. It is an **amendment document**: every section names the existing
rule it builds on by `file:line` at `dev@31e35195` (the cited files are byte-identical at `11aa07a7`, where the triage this builds on was cut), says what that rule does not yet
hold, and adds the rules that close the gap. It is one new file on purpose
([WORKER-CARDS.md](WORKER-CARDS.md) practice 26, `docs/WORKER-CARDS.md:370`: parallel
amendments that touch one file all conflict; 54 of 114 open pull requests conflicted with
`dev` on the day this was written). Each amended spec carries a short pointer here at
the amended rule; the implementation card for a section moves the normative text home
into that spec when it lands, and this file shrinks to the index.

This spec is normative. Where the code and this document disagree, one of them has a bug
and the tests decide which. [SPEC.md](SPEC.md)'s **Conventions** — exit codes, no guessed
paths, the one-line grammar, the cap-and-count rule — apply unchanged and are not
restated. **No code lands with this document**; the work list at the end is the order.

**What "mechanical" means here, and what it does not.** A decision is mechanical when it
is made from exit codes, git objects and typed lines, by a verb that makes **no model
call** and reads **no prose as an instruction**. Mechanical acceptance decides whether a
card's commit becomes a pull request in the read queue. It never lands anything: the
read condition (`docs/SPEC-MERGE.md:808-827`) and the one entry to the merge queue
(`docs/SPEC-MERGE.md:1540-1561`, *"swarms produce branches, never queue entries"*) stand
exactly as written, and §6 makes the lane hold them harder, not softer.

## The failures it closes

| what happened | what closes it |
|---|---|
| `harvest` pushes and opens a PR for every card whose `RESULT.md` line 1 matches, on the worker's own line-2 word; nothing executes anything (`docs/SPEC-PULSE.md:194-218`) | §1: the accept gate runs before any push |
| card-13 pasted a red row and thirteen green rows; an independent reproduction still failed both families (`docs/WORKER-CARDS.md:87-101`) | §1 rules 3-4: the red is reproduced by the gate, never read from the report |
| `nova-review mutate` is specified as *"the harvest runs it before any reader is spawned"* (`docs/SPEC-REVIEW.md:659`) and no file under `internal/pulse`, `cmd/nova-pulse`, `internal/swarm` or `cmd/nova-swarm` calls it | §1 rule 4 and work item T3 |
| a suite that was green with a rule's guard removed: 335 cases, one red, and that one incidental (nova-work mutation M01, 2026-09-19) | §1 rules 6-8: the gate's own negative control; §5 kind `mutation-kill` |
| `NATIVE OK rc=0 harness=ok` printed over cards whose gates never compiled anything (#1465, #1463, #912) | §2: a result is trusted only from a certified bench |
| every C and C++ invocation, and every `make` target, fails inside the wall on macOS (#1557); dotnet cannot run inside the wall on any Linux bench (#1495) | §2 rules 1-3 |
| no rule anywhere says whose name a card's commit carries, what files it may add, or that its diff stays inside the card's named files (searched `SPEC-PULSE.md`, `SPEC-SWARM.md`, `WORKER-CARDS.md` for author, identity, stray, secret at `31e35195`: none) | §3 |
| a held head reached `dev` through a green gate at 2026-09-19T02:43Z (#1572) | §6 |
| 8 of 22 `docs/TESTS.md` transcripts were executed by no test at the triage (7 after #1602); 9 more are compared as a set of shapes, so an abridged or reordered block passes; nova-post's quickstart documents a channel the tool refuses | §7 |

## The pit-stop rule: what a swarm never modifies

**A swarm card never changes the machinery that contains, judges, records or lands swarm
work.** Those paths are changed only by an Opus child of the coordinator, red test
first, and land only with a friend's read at the exact head. The protected set, by path,
at `dev@31e35195`:

| what | paths |
|---|---|
| the kernel and its journal | `lisp/nova-work/**`, `internal/workclient/**`, `cmd/nova-work/**` |
| leases | `internal/swarm/lease*.go`, `internal/swarm/slot*.go`, every file that calls `TakeSlotLeases`, `StartJobLease` or their release halves |
| secrets | `cmd/nova-secrets/**`, `internal/secrets/**` |
| the sandbox | `cmd/nova-sandbox/**`, `internal/sandbox/**`, `profiles/**` |
| the merge queue and the landing lane | `cmd/nova-merge/**`, `internal/merge/**` |
| nova-swarm itself | `cmd/nova-swarm/**`, `internal/swarm/**` |
| the specs | `docs/SPEC*.md`, `docs/spec-pulse/**`, `AGENTS.md`, `docs/WORKER-CARDS.md`, this file |
| CI and the class tests | `.github/**`, `internal/ci/**` |
| **the gate that judges the swarm** *(an addition to Glenn's list; open question Q1)* | the `accept` verb and its fixtures (§1), `cmd/nova-review/mutate*.go`, the hygiene checks of §3, the certification verb of §2 |

1. **The set is data, in one file, and shrink-proof.** `internal/ci/testdata/pitstop_paths.txt`
   holds the globs above, one per line. A class test asserts every glob matches at least
   one file (a rule over nothing passes by checking nothing) and that a line leaves the
   file only in the change that removes every file it matched.
2. **Admission refuses, before any worker starts.** A card whose `PATHS:` line (§5)
   intersects the set is refused by `cut` and by `nova-swarm lint --card` with the rule
   token `pit-stop` and the remedy *"this path is changed by an Opus child with a
   friend's read; see docs/SPEC-TOOLWORK.md, the pit-stop rule"*.
3. **The gate rejects, after.** A card's diff that touches the set is `reason=pit-stop`
   at §3's bounded-diff check, whatever its `PATHS:` said. Admission is the cheap
   refusal; the gate is the one that cannot be talked past.
4. **Reading is never refused.** A swarm may read, probe and report on every protected
   path; the kinds whose gate is `none` (§5: `read`, `probe`) are admitted over it.
   What it finds is an issue, and the fix is somebody else's card.

**The name.** [PIT-STOP.md](PIT-STOP.md) already names the coordinator's decision to
stop widening and fix the faults every card pays for. This rule is that page's other
half — *what the crew does not hand to the cars* — and it takes the name on Glenn's
word. If the collision costs a reader anything, the rename is one line (open question
Q7).

## 1. The accept gate — mechanical accept or reject, with a negative control

**Builds on.** `harvest` is gated on the verdict and never on mergeability
(`docs/SPEC-PULSE.md:194-199`); line 1 must match or nothing is pushed
(`docs/SPEC-PULSE.md:200-218`); `gather` scores a card `done` on line 1 alone, whatever
the exit code (`docs/SPEC-SWARM.md:1470`), which is why line 1 never carries the answer
(`docs/WORKER-CARDS.md:310-321`); a fix card names its reproducing test and the manager
refuses a fix PR that carries neither the `red:` line nor the test file
(`docs/WORKER-CARDS.md:323-340`); `nova-review mutate` reverts the change, keeps the
tests and demands they go red (`docs/SPEC-REVIEW.md:643-660`, grammar `:818-824`, exits
`:787-789`); the card is a three-call pipeline whose tools the harness runs
(`docs/SPEC-SWARM.md:771-838`).

**What those rules do not hold.** Every one of them reads what the worker **said**. The
contract line proves which card this is. Line 2 is the worker's own verdict. The `red:`
line is a pasted string. Between the worker's word and an open pull request there is no
step that executes the claim, so the first execution of a swarm's claim is a friend's
read — the most expensive reader in the house doing the most mechanical check in it.

**The verb.** `nova-pulse gate` is taken (it is the dev-red STOP gate,
`docs/SPEC-PULSE.md:1443-1446`), so this is `accept`:

```
nova-pulse accept --job <dir> --card <path> --base <ref> --bench <name> --cert <path> [--timeout <seconds>] [--max <n>]
nova-pulse accept --selftest --fixtures <dir> --bench <name> --cert <path> [--timeout <seconds>]
```

```
ACCEPT OK      label=<label> kind=<kind> head=<sha12> base=<sha12> tests=<n> red_without=<n> edits=<n|-> control=<id> bench=<name> cert=<id> took=<d>
ACCEPT REJECT  label=<label> kind=<kind> head=<sha12|-> reason=<token> at=<path[:line]|test|-> control=<id> bench=<name> cert=<id> took=<d>
ACCEPT ABSTAIN label=<label> kind=<kind> reason=<bench-uncertified|toolchain|base-red|control-stale|control-red|timeout> bench=<name> took=<d>
ACCEPT SELFTEST control=<id> accepted=<n>/<n> rejected=<n>/<n> edits=1 build=<build identity> fixtures=<sha12> bench=<name> <PASS|FAIL>
ACCEPT SEED    name=<seed> edits=<n> want=<token> got=<token|ACCEPT> <ok|WRONG>
ACCEPT REFUSED: <reason> (<remedy>)
```

Exit 0 is `ACCEPT OK` or a passing selftest; 1 is the verb saying NO (`REJECT`, a
failing selftest); 2 is could-not-run, which includes `ABSTAIN` and `REFUSED`.

1. **The gate is `harvest`'s default step, before any push.** For every `done` card
   whose kind declares a gate (§5), `harvest` runs `accept` after rule 12's line-1
   verify and **before** the push of rule 12 and the read card of rule 13. `ACCEPT OK`
   pushes and opens the PR as today, with the `ACCEPT OK` line as the first line of the
   PR body. `REJECT` pushes nothing, writes the card's row in `seen.tsv` as `rejected`,
   counts `rejected=<n>` on `HARVEST OK`, and sends the card down rule 14's requeue-once
   path with the reason token as the requeue's evidence. `ABSTAIN` pushes nothing and
   requeues nothing: it is the bench's fault, and it is one line in `<root>/bench.tsv`
   for a person. There is no `--no-gate`. A kind whose declared gate is `none` (reads,
   probes) skips the step and its `HARVEST` row says `gate=none`, so a green row never
   claims a check that did not run.
2. **The gate reads the card and the commit, never the report.** Its inputs are the
   card file `cut` wrote (whose typed header lines are the card writer's), the job's git
   objects, and `--base`. `RESULT.md` is data and is not opened by `accept` at all (its
   line 1 is `verify`'s, one step earlier). This keeps `docs/SPEC-SWARM.md:2470` true — *"`nova-swarm` does not
   read a worker's `RESULT.md` and act on it"* — and means a worker cannot name its own
   gate, widen its own paths or supply its own seed. The gate's commands come from the
   kind's declaration in the tool, parameterised only by the card's `TEST:` and
   `PATHS:` lines, each of which is validated as a test name or a repo-relative glob
   before use and never passed through a shell.
3. **The gate runs in its own tree, inside the wall, on a certified bench.** `accept`
   makes a throwaway worktree of the job's head under `--job`'s slot, runs every
   command through `nova-sandbox` with the read and write lists of
   `docs/SPEC-SWARM.md:2245-2275`, and removes the tree on every path. It never runs in
   the worker's own working copy: an untracked file the worker left behind must not be
   able to turn a test green. `--cert` names the bench's certification record (§2); a
   missing, stale or failing record is `ACCEPT ABSTAIN reason=bench-uncertified`.
4. **The order, and the first failure decides.** (a) §3's hygiene checks: `identity`,
   `stray-file`, `secret`, `out-of-path`, `pit-stop`. (b) The kind's shape check: the
   named test exists at head (`named-test-missing`); a code-changing kind changed at
   least one test file (`no-test`). (c) Positive: build, vet, and the packages of every
   changed file test green at head (`build`, `vet`, `red-at-head`, each with the first
   failing line that is not a notice). (d) **Negative control for the card**:
   `nova-review mutate --repo <tree> --base <base> --head <head>` must print `PASS`.
   A `MUTATE GREEN` line is `reason=vacuous-test at=<test>`: a test that is green
   without the change it claims to cover proves nothing
   (`docs/SPEC-REVIEW.md:653-654`), and that is now a rejection and not a reader's
   finding. The card's named `TEST:` must be among the tests that went red
   (`named-test-not-red`). (e) The kind's own control, where mutate's revert is not the
   right defect (§5: `transcript-test`, `mutation-kill`, `sweep`).
5. **A red test is a finding, never a rerun.** `accept` runs each command once. A test
   red at head is `REJECT reason=red-at-head`; a second run to see whether it goes
   green is forbidden, because a gate that reruns until green accepts every flaky fix.
   A test red at head that the card neither changed nor named is run once **at the base**:
   red there too is `ABSTAIN reason=base-red`, the base's fault and not the card's (a
   pre-existing failure is named by its failure set against the pinned base,
   `docs/WORKER-CARDS.md:103-118`).
   A red whose first non-notice line names a missing toolchain, a refused path
   (`SANDBOX DENIED`, `WALL`) or a full disk is the bench's:
   `ABSTAIN reason=toolchain`, and the certification record is marked stale (§2 rule 6).
6. **The gate's own negative control: it must be seen red before its green counts.**
   `accept --selftest` runs the gate over a fixture repository shipped in
   `cmd/nova-pulse/testdata/accept/`: one known-good fix commit, which must be
   `ACCEPT OK`, and one seeded defect per reject token, each of which must be
   `ACCEPT REJECT` **with that token and no other**:

   | seed | the one edit | want |
   |---|---|---|
   | `fix-reverted` | the fix's one changed line put back | `red-at-head` |
   | `vacuous` | the new test's assertion replaced by one that holds without the fix | `vacuous-test` |
   | `no-test` | the test hunk dropped | `no-test` |
   | `wrong-author` | the commit re-authored | `identity` |
   | `stray` | one untracked-then-added file outside `PATHS:` | `stray-file` |
   | `wide` | one line changed in a file outside `PATHS:` | `out-of-path` |
   | `secret` | one line carrying a key-shaped fixture string | `secret` |
   | `protected` | one line changed under a pit-stop path | `pit-stop` |

   `ACCEPT SELFTEST … PASS` requires every row right. One wrong row is `FAIL`, exit 1,
   with its `ACCEPT SEED … WRONG` line.
7. **Every seed is one edit, and the count is asserted.** A seed is applied by
   `nova-review mutate --seed` (rule 9) and is refused unless it changes **exactly one
   line** — one `-` and one `+`, or one added line, or one removed line, or one line
   moved (the same text removed in one place and added in another); for the
   whole-object seeds exactly one object: one commit (`wrong-author`), one file (`stray`),
   one hunk (`no-test`). The
   count is printed as `edits=1` on the `ACCEPT SEED` line and asserted by the verb
   itself: a seed that changed nothing proves the gate red on nothing, and a seed that
   changed two things does not say which one the gate caught. `edits=0` and `edits>1`
   are `ACCEPT REFUSED: seed <name> made <n> edits, want exactly 1`, exit 2.
8. **The control is an id, and a green without it does not count.** `control=<id>` is
   the first twelve hex of the SHA-256 of the gate binary's build identity, the fixture
   tree's digest and the bench certification id. `accept` refuses to print `ACCEPT OK`
   unless a passing `ACCEPT SELFTEST` for the **same id** is on file under
   `<root>/accept/control/<id>`: it runs the selftest itself when none is
   (`control-red` if it fails). A new build of the tool, a changed fixture or a
   re-certified bench is a new id and a new selftest. `harvest` copies `control=` onto
   the PR body; `nova-merge batch` (§6) and a reader can ask for it; an `ACCEPT OK`
   with no control on file is `control-stale` and is treated as `ABSTAIN`.
9. **`nova-review mutate` grows the seed form, and says how much it reverted.**

   ```
   nova-review mutate --repo <dir> --head <ref> --seed <patch file> --tests <package>[,<package>...] [--timeout <seconds>]

   MUTATE <head8> seed=<sha8> edits=1 red=<n> green=<n> <PASS|FAIL>
   MUTATE REFUSED: seed makes <n> edits, want exactly 1
   ```

   The seed is a unified diff applied in the throwaway worktree with `git apply`; the
   verb counts changed lines from the patch it applied, never from the patch file's own
   header. The range form gains `reverted=<n>` (hunks put back) on its verdict line, so
   *"every non-test hunk"* (`docs/SPEC-REVIEW.md:647`) is a number a caller can gate on.
   `mutate` still records nothing and writes nothing into the repo it is pointed at.
10. **The gate makes no model call and no network call.** It reads no forge, asks no
    provider and never consults `--decide`: Jev's harvest classification (§4) runs
    **after** the gate, over its typed line, and can only route a result — it can never
    turn a `REJECT` into a push. (Floors: `docs/SPEC-DECIDE.md` rule 5, *"a decision
    below the floor is a suggestion"*; a decision above the floor is still not an
    acceptance.)

**Red tests this section demands**, each seen red first, fakes for the bench and the
clock, no network: `accept-runs-before-any-push` (the git fixture's argv log holds no
push for a rejected card); `accept-never-opens-result-md` (a `RESULT.md` that is a FIFO
does not hang the gate); `accept-rejects-a-vacuous-test`; `accept-rejects-when-named-test-stays-green`;
`accept-never-reruns-a-red`; `accept-abstains-on-a-bench-red` (a `WALL` line is the
bench's); `selftest-every-seed-is-one-edit` (a two-line seed is refused by count);
`selftest-wrong-token-is-a-fail`; `ok-without-a-control-on-file-is-refused`;
`control-id-changes-with-the-build`; `mutate-seed-refuses-two-edits`;
`mutate-prints-reverted-count`; `harvest-row-says-gate-none-for-a-read`.

## 2. A wall that can build this repository, and a bench that is certified to

**Builds on.** Every job runs inside `nova-sandbox`, argv built by the dispatcher and
never from the task text (`docs/SPEC-SWARM.md:2245-2275`); `--go` puts `GOROOT` and
`GOMODCACHE` in the read set as `go env` reports them (`docs/SPEC-SANDBOX.md:597-612`);
a toolchain under a user directory is named with `--read`, and *"a command that runs
outside the wall and dies inside it is missing a `--read`"*
(`docs/SPEC-SANDBOX.md:1195-1210`), which already names the `xcode_select_link` shim;
`tools/bench-standard.sh` checks a Linux bench's `go version` and `sbcl` on `PATH`
(`docs/SPEC-PULSE.md:384-392`); every launcher takes a bench slot lease
(`docs/SPEC-SWARM.md:2103-2147`).

**What those rules do not hold.** They say how to let a toolchain through the wall; none
says **which toolchains this repository's own gate needs**, none proves a bench can run
that gate **inside** the wall, and nothing ties a card's result to a bench that was ever
shown able to produce one. The receipts: `/usr/bin/cc` and `/usr/bin/c++` fail inside
the wall on macOS because the profile denies `/var/db/xcode_select_link`, which also
kills every `make` target at parse time (#1557); `native` shares the Go caches but never
reads the Go toolchain, so a Go card printed `rc=0 harness=ok` having compiled nothing
(#1465); the harness fence ignores `read_roots` (#1463); on Linux the wall refuses
`/tmp`, which dotnet hard-codes (#1495), `policy` prints the darwin profile under
`backend=landlock` (#1469), and the nova-sandbox and nova-swarm transcripts cannot be
reproduced on a Linux bench at all (#1509). **nova-sandbox on Linux is not clean, and
until rule 7's list is closed no Linux bench can be certified for a wall-dependent
leg.**

1. **The repository declares its legs, as data.** `tools/legs.tsv` — `leg`, `probe
   command`, `wants` — one row per toolchain this repository's gate uses:

   | leg | probe (run inside the wall, in a scratch clone) | wants |
   |---|---|---|
   | `go` | `go build ./... && go vet ./...` | exit 0; `go version` equals `go.mod`'s |
   | `go-cross` | `GOOS=windows go vet ./...` | exit 0 |
   | `cc` | compile and run a ten-line C file with `cc` | exit 0, the file's one line |
   | `make` | `make -n test` | exit 0 (parses; #1557's parse-time `$(CC)`) |
   | `sbcl` | `lisp/nova-work/run-tests.sh` on a fresh isolated ASDF cache | its `total=<n> pass=<n> fail=0` line |
   | `sqlite3` | open, write and read one row in the write set | the row |
   | `git` | clone from the bench mirror, commit, `git worktree add` | exit 0 |
   | `tmp` | `mkdtemp()` under `$TMPDIR` **and** under `/tmp` | both succeed inside the write set (#1495) |

   A leg is added by adding a row, and a class test asserts every toolchain `ci.yml`
   installs has a row.
2. **`nova-sandbox` grows a named toolchain set, not a wider wall.** `--toolchain
   <leg>[,<leg>...]` adds, per leg and per platform, the narrowest roots that leg was
   measured to need, asked of the toolchain the way `--go` asks `go env` and never
   guessed: for `cc` on darwin, `DEVELOPER_DIR` set for the child to what
   `xcode-select -p` prints **outside** the wall, plus read on that directory — the
   narrower of #1557's two fixes, and the one the cargo workaround on that issue already
   proves; for `sbcl`, `SBCL_HOME` and the core; for `sqlite3`, nothing but the binary.
   Each resolved root is printed on the `SANDBOX OK` line's `toolchain=` field so the
   wall a card ran behind is a fact on its record. An unknown leg is `SANDBOX REFUSED
   reason=bad_toolchain`. This is a sandbox change: pit-stop rule, Opus child, a
   friend's read.
3. **`native` and `accept` pass the card's legs to the wall.** A card's `LEGS:` line
   (§5) names what its gate needs; the dispatcher turns it into `--toolchain`, and the
   harness fence is given the same roots (#1463) so the two walls agree. A card with no
   `LEGS:` line gets `go`, which closes #1465 as the default rather than as a flag
   somebody remembers.
4. **`certify` proves a bench, leg by leg, inside the wall.**

   ```
   nova-pulse certify --bench <name> --legs <tools/legs.tsv> --root <dir> --out <cert file> [--timeout <seconds>]

   CERTIFY LEG  bench=<name> leg=<leg> <ok|FAIL|absent> wall=<backend> took=<d> [first=<first line that is not a notice>]
   CERTIFY OK   bench=<name> cert=<id> legs=<ok list> failed=<list|none> absent=<list|none> wall=<backend> build=<build identity> at=<stamp> until=<stamp>
   ```

   Every probe runs through `nova-sandbox --toolchain <leg>` exactly as a card's gate
   will, so a leg is certified **inside the wall or not at all**. The record is one file,
   `cert=<id>` the first twelve hex of its SHA-256; it names the tool build, the wall
   backend, the host, and the legs that passed. `absent` (the toolchain is not
   installed) is not a failure of the bench, only a leg it cannot be routed.
5. **A result is trusted only from a bench certified for the card's legs.** The result
   that is trusted is the **gate's**, never the worker's (§1 rule 2), so the record that
   decides is the one for the bench `accept` runs on: `accept` refuses to gate, and
   `harvest` refuses to count, a card whose `LEGS:` are not all in the `legs=` list of a
   live certification for that bench — `ACCEPT ABSTAIN reason=bench-uncertified`.
   Routing reads the same records for the worker's side: a card is never sent to a bench
   not certified for its legs, because a worker that cannot run its own test comes back
   `BLOCKED` and the card was paid for nothing (the route's `outcome` records the
   refusal). *Measure a toolchain before routing a card there* becomes a file the router
   reads.
6. **A certification expires, and a bench red kills it.** `until=` is 24 hours. It is
   also void the moment the tool build changes (`nova-update adopt`), the host's
   toolchain versions change (`go version`, `sbcl --version`, `cc --version` are in the
   record), or any `accept` on that bench abstains with `reason=toolchain`. A void
   record is re-run, never edited.
7. **What Linux needs before any Linux bench is certified for a walled leg** — each an
   existing issue, in this order: #1495 (a private writable `/tmp` inside the Landlock
   policy, with a class test that `mkdtemp()`s under `/tmp` inside the wall); #1469
   (`policy` prints the Landlock rule set it will apply, not the darwin template);
   #1509 (the transcripts name their platform, §7 rule 5); then rule 2's `--toolchain`
   on Landlock, where read roots are inherited across `fork(2)` and cannot be widened
   after the wrap. Until then a Linux bench certifies with `wall=none` legs only, its
   record says so, and `accept` treats `wall=none` as uncertified for any card that
   changes code. UDP is unrestricted under Landlock at every ABI
   (`docs/SPEC-SANDBOX.md:1559`); `certify` prints that as a note and it is not a leg.

**Red tests:** `certify-runs-every-probe-inside-the-wall` (the sandbox fake's argv log
holds one wrap per leg); `certify-absent-is-not-failed`; `cert-voids-on-build-change`;
`accept-abstains-on-an-uncertified-leg`; `route-refuses-a-bench-without-the-leg`;
`toolchain-cc-sets-developer-dir-and-reads-nothing-wider` (darwin);
`legs-tsv-covers-every-toolchain-ci-installs`; `native-defaults-to-the-go-leg`.

## 3. Identity and hygiene, at staging and at harvest

**Builds on.** The card ends at the commit and publication is the launcher's
(`docs/WORKER-CARDS.md:129-135`); `harvest` pushes by explicit refspec, never bare and
never to `main` (`docs/SPEC-PULSE.md:203-206`), with a lease from `ls-remote` and a
refusal of any branch off `rowan/*` (`docs/SPEC-PULSE.md:2049-2051`); the key is in
neither of the wall's lists (`docs/SPEC-SWARM.md:2268`); the work is anchored to named
files (`files-named`, `docs/WORKER-CARDS.md:28`).

**What those rules do not hold.** Nothing says whose name a card's commit carries, so
it carries whatever the bench's git config held; nothing looks at **what** is in the
commit beyond its branch name; and `files-named` is checked on the card's text at lint
and never on the diff that comes back.

**Staging** is what the launcher puts in a job directory before the worker starts.
**Harvest** is what `accept` checks before anything leaves it. Both halves are
mechanical, and the harvest half never trusts that the staging half ran.

1. **Staging sets the identity; the worker never does.** The launcher writes the job
   clone's **local** git config — `user.name`, `user.email`, `commit.gpgsign=false`,
   `core.hooksPath=/dev/null` — from the pool's `identity.tsv` (`owner`, `name`,
   `email`), and exports `GIT_CONFIG_GLOBAL=/dev/null` and `GIT_CONFIG_NOSYSTEM=1` so a
   bench's own config cannot leak in. A pool with no identity row is refused at launch.
2. **Staging leaves no way out of the job root.** No absolute symlink and no symlink
   resolving outside the job root exists in a staged tree (#1557's `repo/dist ->
   /Users/…` cost four legs a toolchain); toolchains are real directories or
   copy-on-write clones. The launcher checks this before the first worker starts and
   refuses by path.
3. **`identity`.** Every commit in `<base>..<head>` has author **and** committer equal
   to the pool's identity row, and none is a merge commit. Anything else is
   `reason=identity at=<sha12>`.
4. **`out-of-path`: the diff is bounded to the card's declared paths.** The card's
   `PATHS:` line (§5) is a list of repo-relative globs, validated at `cut`: no `..`, no
   absolute path, no bare `**`, at most 8 entries. Every path in `git diff --name-only
   <base>..<head>` matches one. A rename counts on both sides. A path under the
   pit-stop set is `pit-stop` first.
5. **`stray-file`.** No added file matches the stray list — `RESULT.md`, `PROMPT.md`,
   `scratch/**`, `*.log`, `*.orig`, `*.rej`, `*.test`, `*.out`, `.DS_Store`, editor
   swap files, anything over 1 MiB, any file whose mode is not `100644` or `100755`,
   any symlink, any submodule — and the worktree after the gate's checkout holds no
   conflict marker in a changed file (`git diff --check`; card-16 left `<<<<<<< HEAD`
   in a fenced block, `docs/WORKER-CARDS.md:112-113`). The list is one data file, and
   an allowlisted exception names the card kind it is for.
6. **`secret`.** Every **added line** is matched against a list of key SHAPES held as one
   data file — PEM private-key headers, `AGE-SECRET-KEY-1`, the forge's token prefixes,
   the provider key prefixes this fleet holds keys for — and never against a key's
   value: the gate holds no key and reads none (no shape matcher exists in the tree at
   `31e35195`; `internal/swarm/key.go:118` redacts an error's text and is the nearest
   thing). A match is `reason=secret at=<path>:<line>` and **the matched
   text is never printed**, only its path, line and shape name. The job directory is
   then quarantined — moved to `<root>/quarantine/<label>`, not harvested, not deleted
   — and one `HUMAN` line is written, because a key in a worker's diff means a key
   reached a worker. The fixture strings the selftest seeds are generated at test time
   and are not valid keys for any provider.
7. **The checks are one package with one entry point**, `internal/hygiene.Check(repo,
   base, head, paths, identity) []Finding`, called by `accept`, by `nova-merge batch`
   on each member (§6 rule 6) and by a `nova-check hygiene` verb a person can run on a
   branch before asking for a read. One implementation, three callers, so the lane and
   the harvest cannot disagree about what clean means. `identity` is a set and
   `paths` may be absent: at harvest the set is the pool's one row and `paths` is the
   card's; at `batch` the set is the lane's `identities.tsv` — every friend who commits
   to this repository — and a member that is not a swarm's has no `PATHS:`, so
   `out-of-path` is skipped for it and says so (`paths=-`), while `identity`,
   `stray-file`, `secret` and the conflict-marker check run on every member.

**Red tests:** `launch-refuses-a-pool-with-no-identity`;
`staged-clone-ignores-the-bench-gitconfig`; `stage-refuses-a-symlink-out-of-the-job`;
`hygiene-rejects-a-foreign-committer`; `hygiene-rejects-a-merge-commit`;
`hygiene-counts-a-rename-on-both-sides`; `hygiene-rejects-result-md-in-the-diff`;
`hygiene-never-prints-the-secret` (the output is searched for the fixture string);
`secret-quarantines-and-never-deletes`; `paths-line-refuses-dotdot-and-bare-doublestar`.

## 4. Typed results, and Jev's harvest classification

**Builds on.** Line 2 is one of `DONE`, `ABSTAIN <why>`, `BLOCKED <why>`
(`docs/spec-pulse/10-the-card-as-cut-writes-it.md:8`); every abstain names one reason
token so the packet is the whole read (`docs/SPEC-SWARM.md:1480-1503`); the working
layout's five harvest classes are *"the typed decision behind the floor"*
(`docs/SPEC-PULSE.md:2045`); SPEC-DECIDE's **nova-pulse harvest class** asks one
`choice` over {fixed, already-fixed, no-change, failed, off-branch} over bounded public
state (`docs/SPEC-DECIDE.md:407-417`).

**The Jev lane owns the SPEC-DECIDE amendment** (the 2026-09-19 Jev integration lane,
umbrella #896). This section does not restate or change it. It fixes only the
**boundary**: what is typed by the gate and therefore never asked of Jev, and what Jev
is handed.

1. **The result of a card is one typed line, written by the machinery.** After
   `accept`, `harvest` writes `<job>/OUTCOME` — one line, and the only thing any later
   step reads about the card:

   ```
   OUTCOME label=<label> kind=<kind> gather=<done|reason token> accept=<ok|reject|abstain|none> reason=<token|-> class=<class|-> conf=<0-1|-> head=<sha12|-> base=<sha12> bench=<name> cert=<id|-> control=<id|-> pr=<n|-> took=<ms>
   ```

   `gather=` is SPEC-SWARM's token, `accept=`/`reason=` are §1's, `class=`/`conf=` are
   rule 3's. The worker writes none of it.
2. **What the gate decides is never asked of a model.** `accept=ok` is `class=fixed`
   and `accept=reject` is `class=failed`, with `conf=-`, and no decision call is made:
   a typed judgment over evidence a verb already settled is a paid coin-flip over a
   known answer. `no-change`, `already-fixed` and `off-branch` stay the mechanical git
   reads they are in the working layout (`docs/SPEC-PULSE.md:2045-2049`).
3. **Jev classifies what is left: the cards with no gate verdict.** `accept=abstain`,
   `accept=none` with line 2 `BLOCKED` or `ABSTAIN`, and every `gather` abstain whose
   token does not already name its remedy. The question, its option set, the state sent
   and the floor are the Jev lane's amendment; this spec requires only that (a) the
   state is built from `OUTCOME` and the reason token's bounded field, never from
   `RESULT.md` prose, which also keeps SPEC-DECIDE rule 4 (public or synthetic state
   only) true by construction; (b) the answer lands in `class=`/`conf=` on `OUTCOME`
   and on the route log's `outcome`; (c) below the floor `class=unknown` and rule 14's
   requeue-once path runs as today.
4. **A classification routes; it never accepts.** No `class`, at any confidence, turns
   `accept=reject` or `accept=abstain` into a push, lifts a HOLD, or skips a read. The
   red test is SPEC-DECIDE's own shape, `a-merge-classification-never-merges`
   (`docs/SPEC-DECIDE.md:510`), restated for harvest:
   `a-harvest-class-never-pushes-a-rejected-card`.
5. **Every `OUTCOME` is a row for tuning.** `harvest` appends it to
   `<queue>/decide/outcomes.jsonl` beside the route log, so the Jev lane measures
   agreement between `class=` and what a person later did, from rows and not from a
   feeling (`docs/SPEC-PULSE.md`, rule 10's `ROUTES.log` sentence).

**Red tests:** `outcome-is-written-by-harvest-and-never-by-the-card` (a job that ships
its own `OUTCOME` is `stray-file`); `no-decide-call-when-accept-decided` (the provider
fake sees zero requests); `decide-state-is-built-from-outcome-only`;
`a-harvest-class-never-pushes-a-rejected-card`; `below-floor-is-unknown-and-requeues-once`.

## 5. Card kinds for tool work, each with its declared gate and control

**Builds on.** `cut` renders six typed templates — `read`, `fix`, `text`, `replay`,
`drift`, `tone` — and refuses a rendered card that breaks the practice-17 shape
(`docs/spec-pulse/02-the-rules-numbered.md:33-51`); text-only templates forbid the
build (`:52-55`); the contract line's `sha12` binds everything below line 1
(`docs/spec-pulse/10-the-card-as-cut-writes-it.md:12-14`); the fix-card shape is three
model calls (`docs/SPEC-SWARM.md:822-830`) and a fourth without `MODE: explore` is
refused at admission (`:808-813`); `nova-swarm lint --card` checks twelve rule tokens
(`docs/WORKER-CARDS.md:23-36`); *"a template is text and nothing else"*
(`docs/SPEC-SWARM.md:2470`).

**What those rules do not hold.** A template fixes what a card **says**. Nothing fixes
what **accepting** that kind of card means, so every kind is accepted the same way — by
its line 1. A kind is a template **plus** the gate that judges it and the control that
proves the gate: the first half is text for the worker, the second is code in the tool
the worker never sees and cannot edit.

1. **Five typed header lines, under the contract line and inside its hash.**

   ```
   KIND: <kind>
   PATHS: <glob>[, <glob>...]
   TEST: <package> <TestName>          (or `TEST: none` where the kind allows it)
   LEGS: <leg>[,<leg>...]
   SOURCE: <owner>/<repo>#<n> | <file:line at the pinned head>
   ```

   They are written by `cut` from the pool row, never by a model, and because they sit
   below line 1 the contract hash covers them: a card whose header was altered after
   admission is `line1-mismatch` at `gather`. `cut` refuses a gated kind missing any of
   them (`CUT REFUSED kind=<kind>: no <LINE>`), and `lint --card` gains the tokens
   `kind-declared`, `paths-declared`, `pit-stop` (the pit-stop rule, 2) and
   `test-named`.
2. **The kinds.** `gate` is the step list of §1 rule 4; `control` is what must be seen
   red before the gate's green counts for this card.

   | kind | the card does | `PATHS:` may hold | gate | control (seeded defect, `edits=1`) | kind-specific reject tokens |
   |---|---|---|---|---|---|
   | `fix-red` | fixes one defect, red test first (WORKER-CARDS 4 and 23; SPEC-SWARM P7) | the named source and test files | hygiene, shape, positive, mutate | the change reverted: `nova-review mutate` range form `PASS`, and `TEST:` is among the tests that went red | `no-test`, `vacuous-test`, `named-test-not-red` |
   | `transcript-test` | makes one `docs/TESTS.md` section executed line for line (§7) | `cmd/<tool>/firstrun_test.go`, `cmd/<tool>/testdata/**` — **never `docs/TESTS.md`** | hygiene, shape, positive | three seeds applied to a **copy** of the tool's section, each one edit: one line dropped, one value altered, one line moved; the new test goes red on each | `doc-edited`, `transcript-not-read` (a seed stayed green: the test does not read the document) |
   | `rebase` | replays one of our own open PRs onto the base, changing nothing | the PR's own changed files, computed by `cut` from the PR at its pinned head | hygiene, positive, and **range equality**: `git range-diff` pairs every commit, and each pair's patch-id is equal except in files git reported conflicted during the replay | one line changed in a file that did **not** conflict: the gate rejects `rebase-drift` | `rebase-drift`, `commit-dropped`, `commit-added` |
   | `sweep` | applies one mechanical class fix at every site the class test names | up to 8 globs, plus `FILES: <n>`, the most files the diff may touch | hygiene, shape, positive, mutate | **two** seeds, each one edit: the first and the last changed site (path order) reverted alone; the class test in `TEST:` goes red **naming that site** — a class test that samples is found out | `site-not-seen`, `over-files` |
   | `mutation-kill` | writes the test that kills one surviving mutant | test files only | hygiene, shape, positive, and the diff is test-only | the card's own `SEED:` patch (the mutant, written by the card writer into the card, `edits=1` asserted at `cut`) applied with `mutate --seed`: the new test is red with it and green without | `mutant-survives`, `non-test-change` |
   | `read`, `probe`, `text`, `tone` | read and report | none: `PATHS: none` | `none` | none; the `HARVEST` row says `gate=none` | — |

3. **A kind's gate is declared in the tool, in one table, and printed.**
   `internal/pulse/kinds.go` holds the table above as data; `nova-pulse accept --kinds`
   prints it, one line per kind, and a class test asserts this section's table and
   that output name the same kinds, steps and tokens. A kind the table does not hold is
   refused by `cut` and abstained by `accept`; there is no default kind.
4. **What makes a card of a kind eligible for a swarm.** All of: its `PATHS:` miss the
   pit-stop set; its `SOURCE:` names an issue or a `file:line` the card writer opened at
   the pinned head (WORKER-CARDS 3); its `TEST:` either exists at the pinned head
   (`transcript-test`'s target section, `sweep`'s class test) or is a name the card
   fixes in advance; its `LEGS:` are certified on at least one bench (§2 rule 5); and it
   fits the three-call pipeline or says `MODE: explore` with a `TURNS:` budget. `cut`
   checks the first, third and fourth; the second is the card writer's, by practice 3.
5. **One card of a new template runs alone before the batch widens.** `launch` refuses
   a batch wider than one for a `(kind, template sha12)` pair with no `ACCEPT OK` on
   file in `<root>/accept/pilots.tsv`: `PULSE REFUSED: no accepted pilot for kind=<kind>
   template=<sha12> (launch one card first)`. One wrong template line goes out on every
   card; the pilot is how it goes out on one. A pilot that is `REJECT` or `ABSTAIN`
   leaves the pair unpiloted.
6. **A kind never widens itself.** The templates stay text (`docs/SPEC-SWARM.md:2470`);
   the gate is chosen by `KIND:` from the tool's table and by nothing the worker wrote.
   A `RESULT.md` that names a different kind, a different test or more paths is not
   read (§1 rule 2), so it changes nothing.

**Red tests:** `cut-refuses-a-gated-kind-without-paths`; `header-lines-are-inside-the-contract-hash`;
`kinds-table-matches-the-spec`; `transcript-test-rejects-an-edit-to-the-document`;
`rebase-rejects-one-changed-line-outside-a-conflict`; `sweep-control-names-the-reverted-site`;
`mutation-kill-rejects-a-test-the-mutant-survives`; `launch-refuses-a-wide-batch-with-no-pilot`;
`accept-abstains-on-an-unknown-kind`.

## 6. Lanes and landing that refuse a held member, mechanically

**Builds on.** *"A hold blocks, and nothing outvotes it"*; per `(who, head)` the newest
`at` wins and a tie folds hold-last (`docs/SPEC-MERGE.md:820-827`); a read is recorded
by the reader, with the verb, for the head the reader read, and a verdict whose head is
not the entry's current `oid` is kept, counted `stale` and authorizes nothing
(`docs/SPEC-MERGE.md:286-304`); an approve from the author is not a read, and `who` is
*"a line at a keyboard"*, never a host login (`docs/SPEC-MERGE.md:828-837`); nothing
reaches the merge queue but a batch, and `land` re-reads the PR from the forge
(`docs/SPEC-MERGE.md:1540-1561`); `batch` drops a member with no green `ci-ok` before
the merge (`docs/CLI.md:1213-1258`).

**What those rules do not hold — #1572.** The read condition is held by `run`, over the
lane's **records**. `batch` and `land` — the only road to `dev` since 2026-09-18 — read
`MERGEABLE` and `ci-ok` and **no disposition at all**, and on this repository a friend's
disposition is a pull-request **comment**, posted through one shared login, in prose
(`Stella … Exact head f3e2426d… **HOLD: live-owner exclusion is still lost.**`, PR
#1430). At 2026-09-19T02:34:25Z a scoped HOLD was posted on #1551 two minutes after
`BATCH OK`; at 02:43:03Z the held head was on `dev`, through a gate that checked
everything it was specified to check. Three more were one command from the same end on
the day of the triage: #1430 and #1588 (green, MERGEABLE, HOLD at the exact head) and
#1479 (green, MERGEABLE, approved, and does not compile on `dev`).

1. **One fold of dispositions, from two sources, keyed by reviewer and head.** The fold
   takes (a) the lane's records — `nova-merge read` and `nova-review verdict` — and (b)
   the forge's reviews and comments on the pull request. Its key is `(who, head)`; per
   key the newest `at` wins and a tie folds **hold-last**, exactly as
   `docs/SPEC-MERGE.md:820-827` already folds records. `who` is a name in the lane's
   `reviewers.tsv` (`who`, `logins`, `may-hold`), never a bare login: several friends
   write through one login, and a login is evidence about an account.
2. **A forge comment is a disposition when it says so on a typed line.**

   ```
   DISPOSITION who=<name> head=<sha40> verdict=<HOLD|APPROVE|ABSTAIN> [scope="<text>"]
   ```

   one whole line, anywhere in the comment, from a login listed for that `who`. A forge
   review counts by its state and its `commit_id`: `CHANGES_REQUESTED` is a HOLD,
   `APPROVED` an APPROVE. `nova-review verdict` and `nova-merge read` print the typed
   line for the reader to paste, so the record and the comment are one act.
3. **Fail closed on a comment that might be a hold.** A comment with no typed line,
   from a reviewer login that is **not** the pull request's author login, posted after
   the current head was pushed, whose text holds the word `HOLD` as a heading word or
   inside bold, is `hold-unparsed`: the member is dropped and the line names the
   comment's URL. The author's own comments are never scanned — this lane quotes the
   word while reporting on holds, and a substring match over all comments would have
   dropped several innocent members on the night of #1572. A false drop costs one
   typed line from the reviewer; a false admit costs a held head on `dev`.
4. **What blocks.** A member is **held** when, for any reviewer with `may-hold`: that
   reviewer's newest disposition **at the current head** is HOLD; **or** that reviewer
   has no disposition at the current head and their newest disposition at any earlier
   head is HOLD (`hold-carried`: a push does not lift a hold, only its holder does — the
   mirror of *a stale approve authorizes nothing*); **or** rule 3. A HOLD is lifted only
   by the same `who` recording APPROVE at the current head — a scoped APPROVE counts, a
   prose *"clear"* does not — and by nothing the author or the lane can type. There is
   no `--ignore-hold` in this draft (open question Q3).
5. **Read twice: at admission and again at the door.** `batch` folds each member's
   dispositions at the point it reads that member's `ci-ok`, from the wire, and drops a
   held member before the merge:

   ```
   BATCH DROP #<n> reason="held by <who> at <stamp> head=<sha12> (<hold|hold-carried|hold-unparsed>) <url>"
   ```

   `BATCH OK` gains `holds=<n>` (members dropped as held) and `dispositions=<stamp>`
   (when the fold was read). `land` reads the receipt's `members=` list and folds every
   member **again**, from the wire, immediately before `Enqueuer.Enqueue`; one held
   member refuses the whole landing, exit 1:

   ```
   LAND REFUSED pr=<n> member=#<m> held by <who> at <stamp> (posted after BATCH OK at <stamp>); rebuild the batch without it
   ```

   The 02:34Z hold arrived between the two reads; only the second one sees it. `queue
   sweep` and `react` fold the same way and never enqueue a held pull request
   (`held=<n>` on `QUEUE SWEEP`), and `nova-pulse status` counts held PRs so a lane
   standing behind a hold does not read as a lane with nothing to do.
6. **A swarm's member carries its gate's line and passes hygiene again.** A member
   whose head branch was pushed by `harvest` is admitted to a batch only if its PR
   body's first line is an `ACCEPT OK` whose `head=` is the member's current head and
   whose `control=` is on file; otherwise `BATCH DROP #<n> reason="no ACCEPT OK for head
   <sha12>"`. `batch` runs `internal/hygiene.Check` (§3 rule 7) over every member
   whatever its origin. **The merged tree must compile**: #1479 merged clean and left
   `undefined: useFakeForge`; `batch`'s `build` step already catches that for the batch
   — the rule added here is that the failing member is **named** by bisecting the
   members once (`BATCH DROP #<n> reason="build red with this member merged: <first
   line>"`) instead of failing the batch whole.
7. **A mechanical accept is never a read.** `ACCEPT OK` satisfies nothing in the read
   condition; `needs_read` stands for every swarm PR that changes code, and the reader
   *"judges spec fit and nothing else"* (`docs/SPEC-REVIEW.md:659-660`) because the
   gate already did the rest.

**Red tests**, the forge a fake in every one: `batch-drops-a-member-held-at-its-head`;
`batch-drops-a-carried-hold-after-a-push`; `a-newer-approve-by-the-same-who-lifts-it`;
`another-reviewers-approve-lifts-nothing`; `the-authors-quoted-hold-drops-nothing`;
`an-untyped-hold-from-a-reviewer-fails-closed`; `two-names-one-login-fold-separately`;
`land-refuses-a-hold-posted-after-batch-ok` (the fake forge grows a comment between the
two reads; the receipt is #1572's timeline); `sweep-never-enqueues-a-held-pr`;
`batch-names-the-member-that-breaks-the-build`; `swarm-member-without-accept-ok-is-dropped`.

## 7. Tests that execute documents

**Builds on.** Every command carries a `### First run` transcript in its `## <tool>`
section of `docs/TESTS.md`, asserted for every directory under `cmd/`
(`docs/SPEC-CI.md:1138-1163`); each `docs/TESTS.md` heading is written once, because
only the first is read (`docs/SPEC-CI.md:1200-1227`); `docs/TESTS.md:1-3` says every `$`
line *"is run by a test … and what the tool prints is compared with what is written
here by SHAPE"*; `onboarding.FirstRun`, `Transcript` and `Shape`
(`internal/onboarding/onboarding.go:71,153,93`).

**What those rules do not hold.** The class test asserts the section **exists**, not
that anything **runs** it. Measured at `11aa07a7` by the 2026-09-19 triage: 8 of 22
sections were executed by no test — nova-ci (its `firstrun_test.go` never opens the
document), nova-decide, nova-play, nova-pulse, nova-review, nova-secrets (no
`firstrun_test.go` at all), nova-post (asserts only that the section is non-empty) and
nova-sandbox (one line pinned by substring) — 7 at `31e35195`, after #1602 gave
nova-play the first line-for-line test. Nine more —
`cmd/nova-{board,bus,cairn,check,fuse,merge,self-talk,wake,work}/firstrun_test.go` —
collect what was printed into a `printed map[string]bool` and ask whether each
documented line is in it, so an abridged or reordered block passes. nova-post's section
runs `--channel fake` six times and the shipped tool refuses it
(`internal/post/post.go:111-118`). Every drift found that week was found by a person.

1. **Every transcript is executed, line for line, in order.** For every `## <tool>`
   section, a test in `cmd/<tool>` runs every `$` line of every fenced block under
   `### First run` and compares each command's **whole** output with the block under
   it: same number of lines, same lines, same order — #1602's shape, in-process
   `run()`, one temp directory for the sitting.
2. **One comparator, in one place.** `onboarding.CompareTranscript(doc, got, volatile)`
   is the only comparison a `firstrun_test.go` may make. Values are compared **as
   written**; the only values matched by shape are the fields in one shared table of
   run-owned values (`at=`, `took=`, `created=`, a temp path, a fresh sha) —
   `onboarding.Volatile` — and a test may name a field from that table and may not
   invent one. The set-of-shapes helper and every `printed map[string]bool` are
   deleted.
3. **The class test asserts execution, not existence.**
   `TestEveryTranscriptIsExecutedLineForLine` (`internal/ci`) walks `docs/TESTS.md`'s
   sections and fails for any tool whose package has no test calling
   `onboarding.CompareTranscript` on that tool's section, and for any
   `firstrun_test.go` that compares any other way. Its allowlist is the sections not
   yet converted, by name with the issue that owes each, **shrink-only in both
   directions** — the repository's own pattern
   (`internal/ci/testdata/prmerge_allowlist.txt`).
4. **The comparator is seen red three ways, and so is every test that uses it.** The
   comparator's own tests carry the three one-edit seeds — a line dropped, a value
   altered, a line moved — each red. Per tool, the `transcript-test` kind's control
   (§5) applies the same three seeds to a copy of that tool's **real** section and
   demands red, which proves the test reads the document and not a fixture of its own.
5. **A section that one platform cannot reproduce says which.** A `Platform:
   <goos>[,<goos>]` line under the `## <tool>` heading; elsewhere the test is a named
   skip, and the class test requires every platform named to be a leg `ci.yml` runs, so
   a skipped transcript is still executed somewhere (#1509: `backend=`, `hosts=`,
   `gpu=`, `used=`, `ancestors=`).
6. **A false transcript is a finding, never an edit to make a test pass.** A
   `transcript-test` card may not touch `docs/TESTS.md` (§5). When the document and the
   tool disagree the card's line 2 is `BLOCKED drift <file:line>` with the two lines,
   and which of the two is wrong is a person's decision and a `fix-red` or a `text`
   card afterwards — nova-post's `fake` channel is the first such decision (Q5).
7. **The other documents a stranger pastes from are counted, and the count only
   shrinks.** Every `$ ` line in a fenced block of `README.md`, `docs/USAGE.md`,
   `docs/CLI.md`'s `### First run` sections and `docs/nova-swarm-quickstart.md`, and
   every `example:` line of every `help` (#1455: 28 of 61 exit 2 when pasted), is
   either executed by a test through the same comparator or listed in
   `internal/ci/testdata/unexecuted_examples.txt` with its reason. The list is
   shrink-only; a new unexecuted example fails the class test on the PR that adds it.

**Red tests:** `compare-rejects-a-dropped-line`; `compare-rejects-a-moved-line`;
`compare-rejects-an-altered-value`; `volatile-field-outside-the-table-is-refused`;
`every-transcript-is-executed-line-for-line` (fails today, naming the seven and the
nine); `platform-line-must-name-a-ci-leg`; `unexecuted-examples-only-shrink`.

## The work list

Ordered. **T1-T6 are the gate, and until they land no swarm card changes this
repository's code.** T7 onward is ordered so that the first things a swarm is handed
are the narrowest, most mechanical kinds with the strongest controls. Each item is one
card's worth; `S` is SWARM-ELIGIBLE (once T1-T6 are on `dev`), `O` is OPUS+FRIENDS
(an Opus child, red first, a friend's read at the exact head — the pit-stop rule).

| # | who | item | section |
|---|---|---|---|
| T1 | O | `nova-review mutate`: `reverted=<n>` on the verdict line, and the `--seed` form with `edits=1` asserted | §1 rule 9 |
| T2 | O | `internal/hygiene.Check` and its data files (stray list, key shapes), plus `nova-check hygiene` | §3 rules 3-7 |
| T3 | O | `nova-pulse accept`: the worktree, the step order, the reject and abstain tokens, never opens `RESULT.md`, never reruns a red | §1 rules 2-5 |
| T4 | O | `accept --selftest`: the fixture repository, the eight one-edit seeds, `control=<id>`, and OK refused without a control on file | §1 rules 6-8 |
| T5 | O | `harvest` runs `accept` by default before any push; `rejected=` on `HARVEST OK`; the `OUTCOME` line; no decide call where the gate decided | §1 rule 1, §4 rules 1-2 |
| T6 | O | the five header lines in `cut`, the `lint --card` tokens, `pitstop_paths.txt` with its class test, admission refusal, the kinds table with `fix-red` and `transcript-test`, the pilot rule | pit-stop rule, §5 rules 1-5 |
| T7 | O | `onboarding.CompareTranscript`, `onboarding.Volatile`, the three seeded reds, and `TestEveryTranscriptIsExecutedLineForLine` with its shrink-only allowlist | §7 rules 2-4 |
| T8 | S | `transcript-test`, one card per tool, the sections no test executes whose tool is outside the pit-stop set: nova-ci, nova-decide, nova-pulse, nova-review, and nova-post after Q5 | §7 rule 1 |
| T9 | S | `transcript-test`, one card per tool, the set-of-shapes tests outside the pit-stop set moved onto the comparator: nova-board, nova-bus, nova-cairn, nova-check, nova-fuse, nova-self-talk, nova-wake | §7 rule 2 |
| T10 | S | `fix-red` cards over the triage's defects whose paths miss the pit-stop set (the first: nova-tokens #1472 #154 #155, nova-check #1400, nova-review's packet #417 #418 #449 #476), one issue per card | §5 `fix-red` |
| T11 | S | `sweep`: the help examples that exit 2 when pasted (#1455), one card per tool outside the pit-stop set, the class test first (needs T13) | §5 `sweep`, §7 rule 7 |
| T12 | O | the transcript tests of the four protected tools — nova-sandbox and nova-secrets (unexecuted), nova-merge and nova-work (set of shapes) — and then the set helper is deleted | §7 rules 1-2, pit-stop rule |
| T13 | O | kinds `rebase`, `sweep` and `mutation-kill` in the table, each with its control and its selftest seed | §5 rule 2 |
| T14 | S | `rebase`: the conflicting open PRs that are ours and miss the pit-stop set, oldest first, one per card (54 conflicted at the triage) | §5 `rebase` |
| T15 | S | `mutation-kill`: one card per surviving mutant in the Go tools outside the pit-stop set, from a mutation pass an Opus child runs and files (Q8) | §5 `mutation-kill` |
| T16 | O | #1572: the disposition fold, the typed `DISPOSITION` line printed by `read` and `verdict`, `batch` drops a held member, `land` reads again at the door, `sweep` and `react` fold too | §6 rules 1-5 |
| T17 | O | `batch` admits a swarm member only with its `ACCEPT OK`, runs hygiene on every member, names the member that breaks the build | §6 rule 6 |
| T18 | O | `nova-sandbox --toolchain` on darwin: `cc`, `make`, `sbcl`, `sqlite3` (#1557), and `native` defaults to the `go` leg and hands the fence the same roots (#1465, #1463) | §2 rules 1-3 |
| T19 | O | `tools/legs.tsv`, `nova-pulse certify`, the record, its expiry; `accept` and the router refuse an uncertified leg | §2 rules 4-6 |
| T20 | O | Linux: #1495, #1469, then `--toolchain` on Landlock; only then is a Linux bench certified for a walled leg | §2 rule 7 |
| T21 | O | staging: the pool's `identity.tsv`, the clone's local git config, no symlink out of the job root | §3 rules 1-2 |
| T22 | O | the Jev boundary: the state built from `OUTCOME`, `class=`/`conf=` written back, `outcomes.jsonl` — with the Jev lane's SPEC-DECIDE amendment, not before it | §4 rules 3-5 |
| T23 | O | `Platform:` lines and their class test (#1509); the unexecuted-examples count and its shrink-only list | §7 rules 5, 7 |
| T24 | O | move each section's normative text home into its own spec and leave this file as the index | preamble |

T7 is `O` although `internal/onboarding` is outside the pit-stop set: it is the
comparator every T8 and T9 card is judged against, and the thing that judges a swarm's
work is not written by one. T16 does not wait for the gate — it closes a road to `dev`
that is open today — and is listed where it is only because the list is ordered for
swarm hand-off; it may start first, and so may T18. Two things outside this list gate T8 onward as
well: PR #1478 (#1463, #1464, #1465), without which a Go card cannot run its own test
inside the wall on any bench — `accept` names the Go roots itself, the way `--go` does
(`docs/SPEC-SANDBOX.md:597-612`), so the gate is sound before it lands, but every card would
come back `BLOCKED` — and, until T19, §2 rule 5 held by hand: T8-T11 cards are `LEGS: go`
and run only where a person has measured that leg inside the wall.

## Open questions, each with the default this draft stands on

- **Q1. Is the gate itself inside the pit-stop set?** Glenn's list is kernel, journal,
  leases, secrets, sandbox, merge queue, specs, nova-swarm. Default here: **yes** — a
  swarm never edits `accept`, `mutate`, `hygiene`, `certify`, `internal/ci` or the
  comparator, because a worker that can change its judge can pass it. This makes all of
  `nova-pulse`'s harvest path Opus work. Glenn's call.
- **Q2. Whose HOLD counts.** Default: every name in the lane's `reviewers.tsv` with
  `may-hold`, seeded with Glenn and every friend who reads for this repository. #1572
  asks the same question. Stella's or Glenn's call.
- **Q3. What lifts a HOLD, and is there an escape.** Default: only the holder's own
  APPROVE at the current head; a hold carries across a push; no `--ignore-hold`. The
  cost is a reviewer who is asleep (Emma was unwakeable on both seats, #1519) holding a
  lane. A friend's judgment, then Glenn's.
- **Q4. Will friends write the typed `DISPOSITION` line?** The fold fails closed on an
  untyped hold, so nothing unsafe happens if they do not, but every untyped hold is then
  a dropped member somebody has to clear by hand. It needs each friend's yes.
- **Q5. nova-post's quickstart documents `--channel fake`, which the tool refuses.**
  Ship the channel, or re-cut the block? Until decided, nova-post's T8 card is blocked.
- **Q6. Certification lifetime.** Default 24 hours and void on any tool or toolchain
  change. A shorter life costs a certify run per bench per day (eight probes, each under
  the two-minute law).
- **Q7. The name.** "Pit-stop rule" collides with [PIT-STOP.md](PIT-STOP.md)'s pit
  stop. Default: keep Glenn's word and cross-link.
- **Q8. `mutation-kill` needs a mutant source.** `nova-review mutate` reverts a change;
  it does not generate mutants. Default: an Opus child runs the pass by hand-written
  one-line seeds, as the nova-work hardening lane did on 2026-09-19, and files one issue
  per survivor. A generator is not specified here.

## What this draft does not do

It lands no code. It does not let a swarm land anything, lift a hold, skip a read or
touch the merge queue. It does not change the read condition. It does not specify Jev's
question, options, state or floor — that is the Jev lane's SPEC-DECIDE amendment. It
does not widen the wall: §2 names narrower roots per leg and refuses an unknown one. It
does not make a mutant generator (Q8). It does not certify any Linux bench for a walled
leg until §2 rule 7's list is closed.
