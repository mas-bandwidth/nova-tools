RESULT tools22-pre-1589-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1589 at head 9b129b293f14
PREREAD 1589 claims=8 proven=8 unproven=0 defects=0 high=0

PR 1589
HEAD 9b129b293f14b9a060478f27a07c1558f50db2fd
BASE rowan/work-ready-row-and-needs-broken
MERGE-BASE b15696fa617b9cc899ec77a853a0cd14bd8a8689
BEHIND 0
FILES 6 production, 1 test
LINES +643 -3

--- CLAIMS ---

1. `dep --add <node> <need>` creates a reference edge NODE -> NEED on the `:deps` slot. PROVEN-BY replays-785-dep.lisp:91 `(check-equal '("acme/work/n" "acme/work/m") (node-deps ... "acme/work/d") ...)` — adds M under work while D retains its original N dependency.

2. `dep --remove <node> <need>` removes the reference edge and updates the reverse dependent slot. PROVEN-BY replays-785-dep.lisp:117–125 `(check-equal '() (node-deps fresh "acme/work/d") ...)` and `(check-equal '() (node-dependents fresh "acme/work/n") ...)` — both directions confirmed absent after restart.

3. The operation writes one journal record BEFORE applying the mutation, so restart via canonical reconstruction yields the same edge state. PROVEN-BY replays-785-dep.lisp:152 `(restart-of k)` path — full round-trip verified for both add and remove.

4. Deduplication: an identical request-ID returns the original receipt without writing a second envelope. PROVEN-BY replays-785-dep.lisp:172 history length checked equal before and after replaying "review-add"; PROVEN-BY replays-785-dep.lisp:177 `(check-string= first-line again "and answers the original deduplicated receipt")`.

5. A different payload under the same request-ID is refused rather than silently overwriting. PROVEN-BY replays-785-dep.lisp:190 checks code=1, message contains "reused with a different payload", deps unchanged, history length unchanged.

6. Refusals write nothing — no partial mutation on bad self-need, dangling need, cycle-closing add, or non-existent remove-edge. PROVEN-BY replays-785-dep.lisp:132–148 (three refusal cases), replays-785-dep.lisp:100–115 (remove-nonexistent), all checking `(not okp)` and equal-to-before deps/history/log.

7. `needs-broken` flag on the DEP line reflects whether the node's own active tasks exceed zero, read AFTER the change. PROVEN-BY replays-785-dep.lisp:83 `"needs-broken=false"` on idle todo node; line ~90 `"needs-broken=true"` on engaged lease-holder.

8. Fresh request-ID naming an existing edge produces a visible no-effect (`changed=0`) without writing any event; the request-id validation gate still fires even for no-op adds. PROVEN-BY replays-785-dep.lisp:258 (exit 0, changed=0, no extra history), line ~270 (nil request-id with already-present edge still refused exit 2).

--- DEFECTS ---

DEFECTS none

--- QUESTIONS FOR THE REVIEWER ---

1. `%dep-submit` uses `append` to build updated dep/dependent lists rather than copying cons cells in O(1). For large dependency graphs, does this linear-append-on-each-mutation concern Stella's performance expectations, or is the graph always small enough that it doesn't matter?

2. The function name `%dep-submit` starts with `%` (conventionally internal/private in this codebase) but it is exported via `package.lisp:1976`. Is this intentional, or should the export use `dep-edit` exclusively?

3. `%dep-closes-a-cycle-p` walks via `wnode-deps` using `some #'reach`. This only follows the `:deps` edge direction. Rule 3 mentions deadlock prevention, but could a longer cycle involving other edge types (e.g., coordinator links) evade detection? The doc comment says "Bounded by the node count" but doesn't explain how — the visited hash prevents revisits, which is sufficient, but the invariant isn't stated anywhere in the function.

--- Left owed ---

Read every line of the diff (all 7 files, ~650 lines) and the full test file. Did not read: docs/SPEC-WORK.md to independently verify the rule citations, or other verb implementations in the codebase to confirm pattern consistency beyond the cited functions.

git status --short
(none)

git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
