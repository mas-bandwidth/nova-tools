RESULT work4s-E02-F07-03 sha=5298f6be12ea — nova-work E02-F07: does the contract say it? criterion E02-F07-03: Derive who, stale, expired and handoffs views from lease history; distinguish responsible from working-now
DONE
CRITERION E02-F07-03 SPEC STATED
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep "handoffs" docs/SPEC-WORK.md -> 15 hits
grep -in "working-now\|working now" docs/SPEC-WORK.md -> 2 hits
grep -in "stale" docs/SPEC-WORK.md -> 30 hits
grep -in "expired" docs/SPEC-WORK.md -> 17 hits
grep -in "lease history\|lease-hist\|lease_history\|derive" docs/SPEC-WORK.md -> 31 hits
grep -in "responsible" docs/SPEC-WORK.md -> 21 hits
grep -n "derive who" docs/SPEC-WORK.md -> 0 hits
grep -in "lease history\|lease-history\|history of the lease" docs/SPEC-WORK.md -> 0 hits
grep -n "held-not-worked" docs/SPEC-WORK.md -> 6 hits
grep -n "working-now" docs/SPEC-WORK.md -> 1 hit
grep -n "E02-F07" docs/roadmaps/nova-work.sexp -> 2 hits
grep -rln "no-shadow-lease-across-holders" lisp/nova-work/tests/ -> 1 hit (acceptance/slice-05-durable-journal.lisp)
grep -rln "accepted-creates-one-lease-or-binds" lisp/nova-work/tests/ -> 1 hit
grep -rln "branch-and-window-required" lisp/nova-work/tests/ -> 1 hit
grep -rln "delegated-to-a-sleeper-then-recovered" lisp/nova-work/tests/ -> 1 hit (replays-8663.lisp)
grep -rln "move-keeps-the-lease" lisp/nova-work/tests/ -> 1 hit
grep -rln "a-retry-does-not-overwrite-its-attempt" lisp/nova-work/tests/ -> 1 hit
grep -rln "correct-is-a-linked-segment" lisp/nova-work/tests/ -> 2 hits
grep -n "branch-and-window-required" lisp/nova-work/tests/acceptance/slice-05-durable-journal.lisp -> 2 hits
grep -rn "deftest \"correct-is-a-linked-segment\"" lisp/nova-work/tests/ -> 1 hit (replays-8640.lisp)
docs/SPEC-WORK.md:950: A task has no stored worker; who is working on it is answered from leases only; who is responsible is `:responsible`, inherited down the containment forest until overridden.
docs/SPEC-WORK.md:1359: is a count and never a finding; `:extend-once` is defined)*. A lease is an event; its current state is derived from its heartbeat, release and handoff events.
docs/SPEC-WORK.md:1372: A lease never expires into done. **Expiry is derived**: a lease whose deadline is behind the read's clock, with no release or handoff, reads as expired in every answer, the node reads as unowned for execution, `check` counts it (`expired=<n>`), and the responsibility on the node is untouched (Stella, point 4: *expiry ends the claim, not accountability or evidence*).
docs/SPEC-WORK.md:1383: because a tool that messaged a person here would be naming one house's bus. Working-now means a heartbeat inside `--window`; a live lease with no heartbeat in the window is *held, not worked*, and the answer says so.
docs/SPEC-WORK.md:1688: lease log stays whole for `handoffs` (replay `settle-releases-the-lease`).
docs/SPEC-WORK.md:1694: because *responsible*, *doing* and *working* are three facts and this document has kept them apart since its first draft. `--window` splits W the way `who` and `stale` already
docs/SPEC-WORK.md:1696: print it: worked-now (a heartbeat inside the window) and `held-not-worked=`. `|W| ≤ |O|` is rule 18's business below (replay `working-is-a-view`).
docs/SPEC-WORK.md:1786: `--branch root` **require `--from <stamp>` and `--to <stamp>`**, the time window over settle stamps, and `--branch open` refuses them at exit 2 — O is read in the present and `--at <revision>` is how it is read in the past. `who`, `stale` and `handoffs` refuse `--branch closed` and `--branch root` at exit 2 naming the flag, because a live lease is a fact of O alone (replay `branch-and-window-required`).
docs/SPEC-WORK.md:2095: `:lease`, `:heartbeat`, `:release` or `:handoff` event since the named revision, because a transition log is not a counting row. Every other ask prints the counting row.
docs/SPEC-WORK.md:2102: | `who --node X --branch open --window <dur>` | live leases on X and beneath it: holder, heartbeat age, deadline, default; then `held-not-worked` and `unowned` counts; and `responsible=` for X. This is *what is in flight*: its membership rule is `(working O)` and it prints `membership=working` |
docs/SPEC-WORK.md:2108: | `handoffs --since <revision>` | the lease transition log |
docs/SPEC-WORK.md:136: | a stored owner read as "working on it" while nothing moves | *responsible* and *working* are two facts: `:responsible` is durable accountability set by a person's word; *working-now* is a heartbeat inside a window (5654012267) |
:by-feature E02-F07 (docs/roadmaps/nova-work.sexp:80) tests = no-shadow-lease-across-holders; accepted-creates-one-lease-or-binds; branch-and-window-required; delegated-to-a-sleeper-then-recovered; move-keeps-the-lease; a-retry-does-not-overwrite-its-attempt; correct-is-a-linked-segment -> all seven are real deftests in the tree
git status --short = (empty)
Noticed: the criterion is fully covered: the contract derives `who` from leases only (950), derives the lease's current state, expiry and stale/expired counts from heartbeat/release/handoff events (1359, 1372-1374, 1357), derives `handoffs` from the lease transition log (1688, 1786, 2095, 2108), and separates `responsible` (durable, person's word) from working-now (a heartbeat inside `--window`, i.e. `held-not-worked` otherwise) at 136, 1383, 1693-1696, 2102. No drift: the spec names the same distinction with the same label the roadmap row uses.