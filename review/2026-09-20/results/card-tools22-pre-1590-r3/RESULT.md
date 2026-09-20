RESULT tools22-pre-1590-r4 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1590 at head 7a977fdf0b15: nova-work #854 item 7, slice 5a: the `report` verb — a hand act recorded, and not one count mov
PREREAD 1590 claims=13 proven=13 unproven=0 defects=1 high=0

PR 1590, HEAD 7a977fdf0b1584dec634ab5b65e3861fc1a3bae1, BASE rowan/work-dep-verb, MERGE-BASE 9b129b293f14b9a060478f27a07c1558f50db2fd, BEHIND 0, FILES 7 production, 1 test, LINES +778 -8

1. A hand act is recorded and no tree state moves — no count, lease, holder, or transition changes. PROVEN-BY `lisp/nova-work/tests/replays-785-report.lisp:70` `report-records-and-changes-nothing` asserts open-count, closed-count, leases, holder, and state are unchanged after the report.

2. A `:report` event carries six payload fields in order: `:act`, `:subject`, `:what`, `:acted-at`, `:instead-of`, `:reason`. PROVEN-BY `lisp/nova-work/tests/replays-785-report.lisp:94` checks `(kind-fields :report)` equals the six-field list.

3. `:unmet` and `:need` are the session's own half, outside the payload digest — two serializers digest one report to one value with different session halves. PROVEN-BY `lisp/nova-work/tests/replays-785-report.lisp:126` `a-reports-session-half-is-outside-its-payload-digest` asserts equal digests despite different `:unmet`/`:need` values.

4. The dedup/replay answer is taken before any mutable state is read — an identical retry answers the original receipt with its original `unmet=1` even after the need has settled to 0. PROVEN-BY `lisp/nova-work/tests/replays-785-report.lisp:347` `a-report-replays-before-any-state-is-read-and-records-once` retransmits after state changes and asserts `check-string=` of the original line.

5. A report is never reversible: `undo` of a `:report` is refused `not reversible here (report is terminal)`. PROVEN-BY `lisp/nova-work/tests/replays-785-report.lisp:110` refutes `(submit k :verb :undo :of "rep-1" ...)` with `"not reversible here"`.

6. The whole mutation — validation, dedup, revision, node diagnostics, journal record, apply — happens inside the writer's one command thread. PROVEN-BY `lisp/nova-work/tests/replays-785-report.lisp:223` `a-report-is-one-command-in-the-writers-total-order` asserts unique monotone revisions under 8 concurrent threads, authoritative facts after a settle, and atomic failure (no event on refusal).

7. A report about a `:node` subject carries the unmet count, the first unmet need, and the holder. PROVEN-BY `lisp/nova-work/tests/replays-785-report.lisp:126` asserts `unmet=1`, `need=acme/work/n`, `holder=unowned` on the node-subject report.

8. A non-node subject prints `unmet=- need=- holder=-`. PROVEN-BY `lisp/nova-work/tests/replays-785-report.lisp:85` checks the REPORT OK line contains `"unmet=- need=- holder=-"`.

9. Refusals are exit 2 for invocation errors (missing flags, bad act, bad stamp, bad subject) and exit 1 for session-state refusals (no such node, unknown reporter, stale expect, clock ahead); neither writes an event. PROVEN-BY `lisp/nova-work/tests/replays-785-report.lisp:147` `report-refuses-by-name` tests every refusal class and asserts `(state-reports (kernel-state k))` is empty.

10. A different payload under the same request id is refused `reused with a different payload`. PROVEN-BY `lisp/nova-work/tests/replays-785-report.lisp:100` refutes `:what "something else"` under `"rep-1"`.

11. `--as` must name a registered friend of `friends`. PROVEN-BY `lisp/nova-work/tests/replays-785-report.lisp:191` refutes `"stranger"` with `"unknown reporter"`.

12. `state-reports` answers oldest first — the journal holds events in revision order. PROVEN-BY `lisp/nova-work/tests/replays-785-report.lisp:246` asserts `(sort revs #'<)` equals the original list.

13. A report replays from a durable file journal on reopen, carrying its act and the session half it was written with. PROVEN-BY `lisp/nova-work/tests/replays-785-report.lisp:378` closes, reopens, replays, and asserts the report comes back with `:act` and `:unmet` intact.

DEFECT low lisp/nova-work/src/undo.lisp:106 — `undo-plan` does not include `:report` in its explicit terminal-verb check, relying on the `%plan-rows` catch-all instead, which produces `"verb report is not reversible here"` while the undo-execution path (edit-undo.lisp:333) produces `"not reversible here (report is terminal)"`. Both paths refuse correctly, but the user-facing messages differ between plan and execute for the same verb. What would fix it: add `:report` to the member check at undo.lisp:106 alongside `:node-remove` and `:event-cancel`.

QUESTIONS
1. Is there a follow-up slice that registers the CLI verb (`nova-work report`) and adds `docs/CLI.md`? This slice wires only the mid-layer dispatch and tests — no CLI entry point or doc entry is present.
2. Does `%report-duration` need to handle negative seconds (which would indicate a bug upstream rather than a computed lag)? The `plusp` guard sends negative values to the raw-seconds format `"~Ds"`, but the clock-skew check should refuse acted-at ahead of the clock — is the `t` branch there only as a defensive fallback?
3. What is the intended behavior of `pushed=` on the REPORT OK line? It is hardcoded `"-"` in this slice, matching the `pushed=-` pattern of every other OK line in the kernel — is the report verb expected to never be a push target, or is that placeholder for a later slice?
4. Is the `:terminal t` flag in the `kernel-applied` entry used by any existing code or is it solely documentary? `undo-plan` and `%plan-rows` check `:verb`, not `:terminal`.

Left owed: nothing — all 8 files were read in full, and the 317-line new production file and 404-line new test file were both read and analyzed completely.

git status --short

git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1590-r4/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1590-r4	1	2026-09-20T19:28:46Z	2026-09-20T19:46:21Z	0	openrouter	deepseek/deepseek-v4-flash	227530	6133	0	1121536	17279	0.0177
