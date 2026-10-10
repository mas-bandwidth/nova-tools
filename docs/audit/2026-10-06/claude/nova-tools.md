# nova-tools — cold audit, claude, 2026-10-06

Base: `sprint/mechanical-2026-10-02` at `1b559077e0cb9ee9e14efe2910743cc4cff45ab9`.
Scope: nova-tools without the sprint — every command and internal package, the fleet
plays, the docs tree and their TLA+ models. The code was read as a stranger with the
specs beside it; nothing was run against a live store or server.

---

1. **`tla/BusCursor.tla:3-4` and `tla/README.md:33` —
   the model cites pkg/bus files that were deleted three days before the model landed.**
   The header says it models `pkg/bus (cursor.go:1-25, :401-417 WriteCursor, :611-612 ReadOpen; lock.go:1-30; conflict.go:41-55; protocol.go; git.go)`; commit `84d517dfb` (2026-10-04), whose subject names that bus's removal, deleted the commit-cursor bus and all those files, and `docs/SPEC-BUS.md:8` records that nova-bus took its name when that bus was removed.
   *Evidence:* `ls pkg/bus` holds no `cursor.go`, `lock.go`, `conflict.go`, `protocol.go` or `git.go`; `git show --stat 84d517dfb` is the removal; `tla/RUNS.tsv:440-443` still records the `MCBusCursor*` TLC runs as PASS on 2026-10-07; `tla/README.md:33` describes the model in the present tense.
   *Grade:* **NEXT** — a live, TLC-registered model of removed code is dead weight and a false claim about what `pkg/bus` contains.
   *Fix:* retire it (delete `tla/BusCursor.tla`, the `tla/MCBusCursor*.cfg`/`.tla` files and the README/RUNS/COVERAGE rows) or rewrite it against the Redis-stream bus that replaced the removed one.

2. **`tla/Bus2.tla:1-2` —
   the delivery model's header names a spec and package that do not exist.**
   Line 2 reads `nova-bus2's delivery machine (…; …bus2.go: Send, Recv, AckEntry and Ack; …)` and gives the spec as `SPEC-BUS2.md` and the package as `bus2`; neither is in the tree, because the bus was renamed to `pkg/bus` and specced by `docs/SPEC-BUS.md` on 2026-10-04 (`docs/SPEC-BUS.md:7-8`), and the same model's README row already points at `docs/SPEC-BUS.md, pkg/bus` (`tla/README.md:32`).
   *Evidence:* `grep -rn 'SPEC-BUS2'` returns only `tla/Bus2.tla:2`; the spec file `SPEC-BUS2.md` and the package directory `bus2` do not exist anywhere in the tree, while `pkg/bus` is the live package.
   *Grade:* **NEXT** — the index and the model disagree about what the model describes.
   *Fix:* retarget the header to `docs/SPEC-BUS.md` and `pkg/bus` (or retire the model with its `MCBus2*` wrappers).

3. **`pkg/secrets/model_invariants_test.go:3` —
   the test says the TLA+ module is "beside this package", but it is only on an unmerged branch.**
   The comment reads "The TLA+ module `tla/SecretsSeat.tla` beside this package states the design as state…", and the test replays `MCSecretsSeatBrokenReceiptByValue.cfg`, `MCSecretsSeatBrokenSeatAddReplaces.cfg` and other witnesses (`:230`, `:301-305`, `:368`, `:414`, `:505`), but no `tla/SecretsSeat.tla` and no `tla/MCSecretsSeat*.cfg` exist at this base. `pkg/secrets/gate.go:227-228,305` and `docs/SPEC-SECRETS.md:426` name the branch `sprint/md-secrets-h.w1.g1.e15`; the test comment does not.
   *Evidence:* `find . -name 'SecretsSeat*'` at the base → nothing; `git ls-tree -r --name-only origin/sprint/md-secrets-h.w1.g1.e15 | grep SecretsSeat` lists `tla/SecretsSeat.tla` and the eight `MCSecretsSeat*` files; `git merge-base --is-ancestor origin/sprint/md-secrets-h.w1.g1.e15 HEAD` is false (merge base `08ff859c4`).
   *Grade:* **NEXT** — the test's premise is false on the branch it ships on; the model and its witnesses must be landed or the comment must say where they are.
   *Fix:* name the branch in the comment (as `gate.go` and `SPEC-SECRETS.md` do), or land `tla/SecretsSeat.tla` and its MC configs with the package.

4. **`pkg/friend/stage.go:676` —
   the comment cites `tla/JobWorktrees.tla`, but the model is at `pkg/friend/tla/JobWorktrees.tla`.**
   Line 676 reads `witness "async" of tla/JobWorktrees.tla)`, while the same file's line 39 correctly says `pkg/friend/tla/JobWorktrees.tla`.
   *Evidence:* `ls tla/JobWorktrees.tla` → no such file; `ls pkg/friend/tla/JobWorktrees.tla` → present.
   *Grade:* **NEXT** — a dead path in a citation a reader cannot follow.
   *Fix:* change the line to `pkg/friend/tla/JobWorktrees.tla`.

---

urgent=0 next=4
