# Nova Tools 1.2.14

Nova Tools 1.2.14 is a point release of fixes, one concern each, paired with
nova-sprint 1.2.14. It carries everything landed on dev after the 1.2.13 tag.
The largest change is the hook gate: every bus and friend verb now refuses an
identity that is not hooked in, and `nova-hook` is how an identity hooks in.
Around it are the canary adopt, a member that reports its ended cards side by
side and runs again a read whose stage failed, a friend daemon with a spend
cap and lane faults, a cut that reads cancelled runs honestly, and the tests,
models and contraction landed beside them. Every entry is in
[FIXES.md](../FIXES.md) under v1.2.14.

## Upgrading

Do these steps in order, on the machines they name. The order matters for the
hook gate: a gated tool upgraded before its identity is hooked in refuses
every verb until it is.

1. **Install `nova-hook`** on every machine that runs a friend daemon, a
   member, the sprint server or the seat.
2. **Hook each identity in**, the seat first and then every friend:
   `nova-hook hook --as <name> --role <role>`, answering each `HOOK CHALLENGE`
   line with the `nova-hook prove` line it carries. `nova-hook status --as <name>`
   shows who is hooked in. `prove` and `hook` take `--dry-run`, which plans on
   one read and writes nothing.
3. **Install the 1.2.14 binaries.** The fleet install play now runs one canary
   host first (`nova_adopt_canary`, by default the first host of the adopt
   group), waits on its member log for one finish ok line, rolls the canary
   back alone on failure, and only then installs every other host.
   `nova_adopt_canary=none` installs every host in one batch as before.
4. **Check the mirrors**: `nova-swarm mirror --check` names every bench missing
   a mirror as `MIRROR MISSING <bench> <repo>` and writes nothing; a pass
   without `--check` creates what is missing.
5. **Price every enabled route before `nova-config apply`**: apply now refuses
   an enabled route with no price sheet and names the `nova-config route set`
   command that sets it, or disable the route with `--enabled false`.

## The hook gate

Every `nova-bus` and `nova-friend` verb is gated on the hook record: a caller
whose pushes are not hooked in is refused with a breadcrumb that prints the
exact commands to hook in. The hook module (its own Go module under `hook/`,
Redis-backed: holder, lease, epoch, expiry, takeover) and `nova-hook` (`hook`,
`prove`, `unhook`, `status`) are new. The machine is `tla/Hook.tla`, with its
coordinator and friend instances and eight reversed witnesses.

## The member

- **Ended cards are reported side by side.** The member pass runs up to
  `pushWidth` reports at once instead of one after another, and takes for its
  free lanes before the reports return, so a lane freed by a report is filled by
  the next pass. What a report came to is read from the store's answer, never
  the exit code alone: a refused report whose claim moved is reaped, an accepted
  one is applied, an unanswered one keeps the run as failed for a person.
- **A read whose stage failed is run again,** never held as a ghost for the
  server's hour-long read deadline; a work card's stage failure is finished
  `--failed` at once.
- **The finish sends the child's harness output,** its last 40 lines, where it
  used to send the harness name; a child's exit code is reported with it.
- **One member implementation**: the member the fleet runs is the one the tests
  test (`docs/one-member-ownership.md`), held by a class rule.

## The friend daemon

- **A spend cap.** The daemon carries a spend cap per row and stops its lanes
  at a provider refusal, building on the lane-fault record below.
- **A provider refusal on a lane is a judgment.** The second identical refusal
  marks the lane faulted, reported as `lane-fault=<n>` on the beat and in the
  status, with one notice to the coordinator; a faulted lane takes no card until
  the daemon restarts or `nova-friend lanes --as <me> --clear-faults` runs.
- **A harness limit in any wording pauses the lanes.** The limit classifier
  strips ANSI colour codes before matching, so a coloured weekly-limit banner
  reads as a limit until the reset it names, and the lane keeps its card
  without counting an attempt.
- **A daemon row that names no mode runs as batch.**
- **Every AI's files live under one `ai/<name>` root:** inbox, lanes and
  launchd paths, the swarm templates and the doctor's friend-wall check read
  the one layout.
- **`nova-friend run` never writes its output after it returns,** and no two of
  its goroutines write at once; a data race the `-race` certification caught is
  gone.

## nova-config

- **The recorded sprint seat login is read** for the sprint store, and
  `status` shows it.
- **An enabled route with no price is refused at apply,** before anything is
  planned or written; the refusal names each route and the command that sets
  its price. A disabled route is never refused for its price.

## The fleet plays

- **The adopt starts with one canary member** (step 3 above).
- **Every bench carries every mirror a card can name**: `nova-swarm mirror`
  unions the running sprint's stream repositories with the fixed set every
  bench carries, over every fleet row, and `--check` is the converge check.
- **The `[toolchains]` play installs every language toolchain**: pinned Node,
  .NET, Dart, Elixir, Erlang/OTP, Rust, JDK, SBCL and GitHub CLI archives under
  each host login's `~/sdk`, SHA-256 verified, linked, the login shell
  configured and each tool's path and version written to
  `~/.config/nova/toolchains.json`. It is opt-in (`--tags toolchains`).

## The sandbox

- **A `--read-noexec` path under an execute-granting root is refused** at wall
  build time rather than silently executing: Landlock's rules are a union, so a
  no-exec rule under an execute root never held.

## The release cut and certification

- **Certification on dev never cancels on a later push**: each pushed sha gets
  its own run to completion.
- **The cut sets aside a cancelled suite superseded by a later green one** at
  the same sha, and names what it set aside in the tag annotation and the
  CHANGELOG section.
- **A cut ignores a previous tag's cancelled release run** on the same commit
  and refuses a waiver when only those set-aside runs are non-green.
- **The root module is tidy**: `make mod-tidy` is green again, with the three
  tool requirements in the direct require block.
- **`pkg/oneline` is its own module**, joined through `go.work`, its workspace
  checksum in `go.sum`.

## Also in this release

Tests, models and contraction, no behaviour change: the CardContract model
names `pkg/member` and splits its judgment into a report sent and collected
apart; the Linux sandbox wall, `pkg/seatcred` and the swarm install's units
loader are under unit test; a throwaway Postgres dies with its test binary;
seven tests that raced a loaded runner (the seatcred fallback, the inventory
timeout, the stalled store, the exec cover, the real-clock delay proxy, the
redis cover Close, the nova-friend first-run transcript) are deterministic;
the functional `internal/ci` package fits its bound; and the contraction pass
folded the ci allowlist rules, the ci and ghrelease run frames, the
case-folding probe, three twin-function pairs, the dial-guard, pipe-store,
first-run and version test helpers, and the repeated docs, tablemodel, links,
provider and cache test cases into shared helpers and tables.

## What is not in this release

Nothing is deferred from the 1.2.14 scope. The entries once labelled for
1.2.15 to 1.2.18 all landed on dev before this tag and ship here; the ledger
says so. Work that landed before the 1.2.13 tag but was still labelled 1.2.14
in the ledger (the push gate's refusal text, the acted receipt written again,
the lane beyond the cap, the no-result finish, the start scheduled before
STOP, the doctor's friend checks, the refused-verb raise, the secrets
remedies, the card lint's quoted code, the deadline report, the loops play's
version check, the newest claude model per tier) shipped in 1.2.12 and is
relabelled there.
