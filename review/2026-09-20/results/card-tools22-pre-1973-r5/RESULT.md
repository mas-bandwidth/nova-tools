RESULT tools22-pre-1973-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1973 at head 7b57ce98c810: nova-work kernel: add the promised dep structure verb (#1673)
PREREAD 1973 claims=19 proven=13 unproven=6 defects=3 high=0

PR 1973, HEAD 7b57ce98c810af23ebcc5899b00041f2cac33549, BASE dev, MERGE-BASE 23d9698b0b862641a7569dd268c6f1ed8c4d57b0, BEHIND 9, FILES 3 production, 3 test, LINES +427 -2

CLAIMS

1. `dep --add` adds a `:deps` edge from `--node` to `--add`, visible in `node-deps` and `node-dependents`. PROVEN-BY replays-dep-verb.lisp:48 `the-dep-verb-edits-the-deps-edge-spec-work-md-987` — asserts forward edge and reverse edge are populated.
2. `dep --remove` removes a `:deps` edge. PROVEN-BY replays-dep-verb.lisp:61 same test — asserts removed edge is gone.
3. A self-loop edge (node needs itself) is refused with exit 1 and "rule 3". PROVEN-BY replays-dep-verb.lisp:82 `a-dep-edge-refuses-a-cycle-and-a-dangling-id` — asserts exit 1 and "rule 3" in message.
4. An edge that closes a two-node cycle is refused with exit 1 and "rule 3". PROVEN-BY replays-dep-verb.lisp:84 same test — asserts refusal and "rule 3".
5. A `dep --add` whose target names no existing node is refused with exit 1 and "rule 2: dangling". PROVEN-BY replays-dep-verb.lisp:87 same test — asserts exit 1 and "rule 2: dangling".
6. After any refusal, the node's `:deps` edges are unchanged. PROVEN-BY replays-dep-verb.lisp:94 same test — asserts `before == after`.
7. A `dep` edit is journaled and the edge survives a restart via file journal replay, including the reverse edge. PROVEN-BY replays-dep-verb.lisp:97 `a-dep-edit-is-journaled-and-survives-a-restart` — asserts forward and reverse edges after replay.
8. A retransmitted `dep` request with the same id and identical payload answers the original receipt (dedup replay, exit 0). PROVEN-BY replays-dep-verb.lisp:115 `a-dep-retry-answers-its-original-receipt-once` — asserts same receipt string and exit 0.
9. A retransmitted `dep` request with the same id and a different payload is refused (dedup conflict, exit 1). PROVEN-BY replays-dep-verb.lisp:126 same test — asserts refusal with "different payload".
10. `*kind-field-order*` in `event.lisp` has a `:structure` row. PROVEN-BY work_kernel_dep_test.go:29 `TestDepVerbReachesTheKernelAsAStructureEvent` — asserts presence via `hasKindFieldOrderRow`.
11. The `:structure` row's fields are `:verb :add :remove :reason`. PROVEN-BY work_kernel_dep_test.go:31 same test — asserts the literal pattern.
12. `state.lisp`'s `apply-event` has a `:structure` branch that references `wnode-deps`. PROVEN-BY work_kernel_dep_test.go:34 same test — asserts `(:structure` and `wnode-deps` patterns.
13. The kernel's `%submit` dispatches the `:dep` verb to `%dep-submit`. PROVEN-BY work_kernel_dep_test.go:38 same test via `dispatchesDep` — asserts `%dep-submit` appears in source.
14. `dep --remove` of a non-existent edge is refused. UNPROVEN
15. `dep --add` of an already-present edge is a no-op (changed=0, no new event). UNPROVEN
16. `--add` and `--remove` are mutually exclusive; providing both is refused. UNPROVEN
17. `--reason`, `--as`, `--request` are required; missing any causes exit 2. UNPROVEN
18. Removing a need clears `needs-broken` when all remaining needs are terminal. UNPROVEN
19. Adding a need never raises `needs-broken`. UNPROVEN

DEFECTS

DEFECT medium dep-verb.lisp:64 — `%dep-line` output does not match the wire schema's grammar line at nova-work-wire-v1.json:1269. The format includes `change=` and `need=` that the grammar line does not list, and omits `emitted=<bytes>` that the grammar line does list. — A parser built from the schema grammar template would not find `change` or `need` fields, and would expect `emitted`. — Either update the grammar line to include `change` and `need` and drop `emitted`, or change `%dep-line` to match the existing grammar.

DEFECT low work_kernel_dep_test.go:15 — Comment says "the way lispkernel_class_test.go reads its ASDF system" but no file by that name exists in the repository. — A reader cannot cross-reference the claimed pattern. — Either add the referenced file or correct the comment to name an existing test.

DEFECT low dep-verb.lisp:63 — `%dep-line` always outputs `pushed=-`, but the wire schema grammar line says `pushed=<rev|->`. The push status is never populated. — Consistent with other initial implementations, but the schema promises conditional output. — Either pass the real push status or note in the grammar that `-` is the current seat.

QUESTIONS

1. SPEC-WORK.md:2322 documents `nova-work dep --session <path> ...` as a CLI command, but this PR adds only the Lisp kernel verb — the Go CLI in `cmd/nova-work/main.go` has no `dep` subcommand. Is the Go CLI handler coming in a separate PR, or is `dep` intended to be reachable only through the Lisp `dep-edit` / `%submit` path?

2. The reversible-verbs table in `operations.lisp` and `edit-undo.lisp` already lists `(:dep . :dep)`. Does undo for `dep` edits work through the existing `%undo-submit` framework, or is there a gap between the table entry and the actual undo implementation?

3. The `needs-broken` recomputation in `state.lisp:609` runs only when `(wnode-needs-broken node)` is already true — so a `dep --add` to a node that was not broken can never raise `needs-broken`. Is this one-sided logic (clear-only, never set) intended for the slice-1 design, or should every `:structure` dep change recompute `needs-broken` unconditionally?

Left owed: read the full diff and all files; nothing left unread.

git status --short: (nothing printed)
git rev-parse HEAD: d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1973-r2/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1973-r2	1	2026-09-20T19:26:30Z	2026-09-20T19:42:26Z	0	openrouter	deepseek/deepseek-v4-flash	253998	16228	0	1294080	16485	0.0206
