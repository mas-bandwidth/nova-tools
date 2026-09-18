# nova-tools, 2026-09-18 — three batches

Three batches instead of twenty pull requests. A batch is a handful of pre-tested
changes merged onto one tree, built and tested there, and opened as one entry that
names what is in it; the queue round lands the batch and the members are closed with
a pointer to it. What made it affordable is in the batches themselves: `nova-merge
simulate` names the poison entry before the queue finds it, lanes keep two cards out
of the same file, and four packages that were over the test-time budget are back
under it, so the round that proves a batch is a round a person will wait for.

Every line below is one member pull request and what it did. Nothing here claims a
saving that was not measured.

## Batch 1 — #1302: simulate, the slow packages, the spec slices

Ten members, built on `hulk` from `dev` at 327d26bb; `go build ./... && go vet ./...
&& go test ./...` clean and the nova-work acceptance suite 327/327 green on the
merged tree before the batch was pushed.

- **#1267** — **new verb `nova-merge simulate`**: squash-merges a queue's entries onto
  the base in order in a scratch worktree, runs every `--checks` command after each,
  and reports the growing batch's first failing step while skipping conflicts;
  exit 0 has no configured check failure, exit 2 is a check failure or invalid
  invocation, and exit 1 is a preparation or runtime refusal.
- **#1171** — **`nova-pulse cut` from a validated template**: `--issue`, `--rows` and
  `--branch-from` beside `--pool`, with five checks — branch, base, `STEP 1`, slot,
  result — run before a byte is written, each refusing on its own line.
- **#1076** — `nova-pulse fleet add` refuses a bench whose fleet-probe record is not
  all green, naming the runner that is not and the run it read (#875, red test first).
- **#1242** — slice 4 of `SPEC-DECIDE`, *Jev across the stack*: a merge classification
  labels a pull request and **never merges one**.
- **#1209** — `docs/SPEC-STATE.md` drafted: Redis for the live state (streams as the
  pull queue with leases, caps, presence and events) and Postgres for the durable
  record, with git keeping the bus and the journal.
- **#1208** — `docs/SPEC-FLEET-KUBE.md` drafted: Terraform declares the fleet,
  Kubernetes runs it, and `nova-sandbox` is the second wall.
- **#1286** — `cmd/nova-swarm` tests back inside the 60 s package budget, with no
  network and no wall-clock waits (the first `CI-SLOW` alert).
- **#1290** — `cmd/nova-merge` tests, the same.
- **#1291** — `internal/bus` tests, the same.
- **#1294** — `internal/pulse` tests, the same.

`#1262` (one helper for placing a built executable in a fixture) is listed in the
batch's own body but did **not** land with it: the pull request is still open, and
there is no such helper in the tree.

## Batch 2 — #1312: the deterministic idle watch, the sandbox guard, lanes

Five members, built on `hulk` from `dev` at 91091a1a, green on the merged tree the
same way.

- **#1314** — the swarm's idle watch is decoupled from host scheduler load: a snapshot
  interface the tests inject, a round-based fake sampler in place of a five-second
  CPU burn, and the OS process-tree sampler tested on its own. Closes the darwin
  flake class.
- **#1310** — **`nova-sandbox`'s probe parent guard compares the executable's identity,
  not its path spelling**: device and inode, one stat each, and a stat that fails is a
  refusal rather than a fallback. The old form compared two path strings that two
  syscalls spell differently (`/tmp/x` against `/private/tmp/x` on darwin) and bridged
  them with a symlink walk whose error it swallowed. The pid and the image are now
  read, then read again, so a parent that execs a different image in place — same pid,
  different binary — is refused (Johnny's read).
- **#1311** — 79 throwaway files removed from the tree and `scratch/` ignored, so the
  class cannot recur; card templates now put `TMPDIR` beside the clone rather than
  under it.
- **#1303** — **cards belong to lanes**: a `LANE: <name>` line in a card, a
  `queue/control/lanes.tsv` naming the lanes, and `nova-pulse fill` keeping one live
  card per lane and holding the rest in order. A lane the file does not name is
  refused with its remedy, not guessed.
- **#1313** — **`infra/image`, the card-runner container**: Ubuntu 24.04 with git,
  Go and SBCL fetched from the vendor and sha256-verified in the Containerfile, the
  harness blob pinned by sha too, and a non-root `card` user in `/work`. Nothing
  secret is in the image; credentials reach a run as named environment variables from
  `nova-secrets exec` on the host. The hardened run contract is part of the deliverable
  and every line of it was proven on `hulk`: `--read-only`, an exec tmpfs on `/tmp`, a
  0700 tmpfs over `$HOME`, `no-new-privileges`, `--cap-drop=ALL`, `--memory 8g`,
  `--pids-limit 512`, and one bind mount. `--userns=keep-id:uid=10001,gid=10001` is
  required, and the README says why. An egress allowlist is the next step and is not
  in this image.

## Batch 3 — #1308: the merge lane

Four members, stacked one onto the next in this order. **This batch has not landed
yet**, so the verbs below are not on `dev`; `docs/CLI.md` marks them where they are
documented.

- **#1244** — **`nova-merge rebase --once`** cuts and launches one rebase card for
  every `DIRTY` `rowan/*` pull request that has none, with `--markers` as the pass's
  whole memory so a pull request is carded once and not once per tick.
- **#1143** — **`nova-merge classify`** puts one failed merge-group run to a typed
  decision — flaky under load, the environment, or the pull request's own change —
  behind a 0.90 floor, and below the floor the kind is `unknown` and neither action is
  taken (#896, red test first).
- **#1272** — **`nova-work events`** publishes `card-done`, `pr-checks-done` and
  `dev-moved` from the `cards:done` stream and a forge poll, so the lane reacts to
  events instead of ticking; **`nova-merge react`** is the subscriber that enqueues,
  skips, holds and asks for rebases on them.
- **#1292** — `internal/merge` tests back inside the 60 s package budget, no network
  and no wall-clock waits.

## What a reader should do about it

Nothing, on batches 1 and 2 — they are on `dev`, and `go install ./cmd/...` is the
whole upgrade. Two things are worth knowing:

```sh
nova-merge simulate --repo . --base dev
nova-pulse fill --ready ./queue/ready --launched ./queue/launched --lanes ./queue/control/lanes.tsv --once
```

The first is the question to ask before a queue round, not after it. The second
refuses every card naming a lane until `lanes.tsv` exists, which is the file saying
it has not been written yet rather than a fill that serializes nothing.
