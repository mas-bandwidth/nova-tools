RESULT tools22-pre-1839-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1839 at head cf5cb1786236: toolwork T05: nova-pulse harvest runs accept before any push, and writes the typed OUTCOME (#16
PREREAD 1839 claims=12 proven=12 unproven=0 defects=3 high=0

PR 1839
HEAD cf5cb1786236103621aab2fb7432c0a1f0fa9371
BASE rowan/toolwork-t04-selftest
MERGE-BASE 594881168a420449bb9e3c32ac424016e3dc490c
BEHIND 0
FILES 5 production, 3 test
LINES +1103 -35

## CLAIMS

1. Harvest runs the accept gate for every done card whose KIND header names a gated kind, between the line-1 verify and any push. PROVEN-BY internal/pulse/harvest_accept_test.go:77 TestHarvestRunsAcceptBeforeAnyPush — calls Harvest with a reject gate, asserts the gate ran once and no push occurred.

2. Accept verdict "reject" pushes nothing, marks the card seen/rejected in seen.tsv, appends the reason token to retry.tsv as requeue evidence, and prints `rejected=<n>` on the HARVEST summary line. PROVEN-BY internal/pulse/harvest_accept_test.go:77 TestHarvestRunsAcceptBeforeAnyPush — asserts all three outputs (seen.tsv, retry.tsv, summary).

3. Accept verdict "ok" advances the card through classification and push; "no-change" and "already-fixed" skip the decider entirely and classify directly. PROVEN-BY-EXISTING internal/pulse/harvest_accept_test.go:185 TestNoDecideCallWhenAcceptDecided — counts decider calls and asserts they are zero when accept decided, plus asserts class/conf tokens on the row.

4. Accept verdict "abstain" pushes nothing, requeues nothing, and writes one line to `<root>/bench.tsv` unless the reason is "paused". PROVEN-BY internal/pulse/harvest_accept_test.go:231 TestHarvestAbstainWritesBenchTSVAndRequeuesNothing — asserts no push, no retry.tsv, and bench.tsv presence/absence based on reason.

5. Ungated kinds (including "read" and cards without typed headers) skip the gate, print `gate=none` on their row, and still proceed normally. PROVEN-BY-EXISTING internal/pulse/harvest_accept_test.go:105 TestHarvestRowSaysGateNoneForARead — adds a "read" kind card, asserts zero gate calls, `gate=none` on the row, and a successful push.

6. When accept returned a typed verdict ("ok" or "reject"), the decider is never called: "ok" maps to class=fixed, "reject" maps to class=rejected, both with conf=−. PROVEN-BY internal/pulse/harvest_accept_test.go:185 TestNoDecideCallWhenAcceptDecided — counting decider asserts d.n == 0 for both ok and reject cases, plus asserts class and conf fields.

7. A rejected card can never reach the push stage through any path, including when the classifying decider is active. PROVEN-BY internal/pulse/harvest_accept_test.go:217 TestAHarvestClassNeverPushesARejectedCard — rejects a card, enables Decide/floor/Decider, asserts no git push / gh pr create lines appear.

8. The acceptance gate resolves base refs to full SHAs in the job's own clone rather than passing bare refnames. PROVEN-BY internal/pulse/harvest_accept_test.go:285 TestHarvestPassesAcceptAFullBaseSHANeverARef — sets in.Base = "dev", checks that the gate call receives the resolved testBaseSHA instead of the string "dev".

9. If the base cannot be resolved, the harvest abstains (reason=toolchain) and does not push. PROVEN-BY internal/pulse/harvest_accept_test.go:301 TestHarvestAbstainsWhenTheBaseWillNotResolve — makes rev-parse return "dev" (not a SHA), asserts gate was never called, output says `gate=abstain`, no push happens.

10. A rejection with reason="secret" moves the job directory to `<root>/quarantine/<label>` (not copies, not deletes), writes a HUMAN line to `<queue>/HUMAN`, and does not push. PROVEN-BY internal/pulse/harvest_accept_test.go:331 TestSecretQuarantinesAndNeverDeletes — asserts quarantine dir exists, original job gone, RESULT.md present in quarantine, HUMAN file contains card name and reason.

11. Every outcome is appended as one JSON object per line to `<queue>/decide/outcomes.jsonl`. PROVEN-BY internal/pulse/harvest_accept_test.go:254 TestEveryOutcomeIsOneAppendedJSONLRow — reads the file, parses the single line as JSON, asserts required fields exist.

12. The push uses `sha:refs/heads/<branch>` so the pushed object is exactly what the gate judged; if HEAD moved under the gate, nothing is pushed and an abstain is recorded. PROVEN-BY internal/pulse/harvest_accept_test.go:382 TestHarvestPushesTheShaTheGateJudgedNeverTheBranchRef — gate returns OK, asserts push uses `full_sha:refs/heads/rowan/fix-1` and NOT `branch:branch`; and TestHarvestPushesNothingWhenTheHeadMovedUnderTheGate: gate returns OK with mismatched head, asserts no push and `gate=abstain` in output.

All 12 claims are proven (10 by new tests in this diff, 2 covered by tests already existing). No claim is UNPROVEN.

## DEFECTS

DEFECT medium internal/pulse/harvest_accept.go:285 appendOutcomeRow silently returns on every error path (MkdirAll failure, JSON marshal failure, file open failure) — callers never learn whether the outcomes log was written; if append fails silently, downstream metrics from outcomes.jsonl will lose rows without anyone noticing. Fix: return errors up to the caller (Harvest.runGate → Harvest.Harvest) so the HARVEST summary can flag append failures, or at minimum emit a HARVEST NOTE on stderr.

DEFECT medium internal/pulse/harvest_accept.go:307 recordRouteOutcome silently returns on every error path (log stat failure, command execute failure, nova-decide exit non-zero) — callers never learn whether the route recording succeeded; a failed outcome relay looks identical to a no-op. Fix: check cmd.Err and emit a HARVEST NOTE with detail on stderr when `nova-decide outcome` fails.

DEFECT medium internal/pulse/harvest_accept.go:321 appendBench silently returns on OpenFile/MkdirAll failure — a bench row that fails to write leaves no trace except the worker proceeding as normal; a person reading bench.tsv sees a gap. Fix: emit a HARVEST NOTE on stderr when bench.tsv write fails, so the operator knows some cards may lack bench entries.

## QUESTIONS FOR THE REVIEWER

1. `cmd/nova-pulse/main.go:478-479`: The --identity flag now delegates to `parseIdentities()` which is shared between `accept` and `harvest`. Is it intentional that both commands use the same validation (`Name <email>` format)? This was not previously documented as a shared rule.

2. `internal/pulse/harvest_accept.go:276-282` quarantine(): On HUMAN file write failure (os.OpenFile error inside queue check), the function continues and returns the dest path. Should a failed HUMAN write downgrade the result or at least warn, since the whole point of quarantine is to alert a human?

3. `internal/pulse/harvest_accept.go:307`: `recordRouteOutcome` checks if route.jsonl exists via os.Stat and returns immediately if it doesn't. Is this silence intentional — meaning harvest runs without Queue set or without a prior route entry produce no feedback? Should the operator see a note?

4. `internal/pulse/harvest_accept.go:330-344`: The quarantine function uses `safepath.RemoveUnder(dir, dest)` to clean the destination before renaming the job there. What happens if the rename succeeds but a subsequent operation on the quarantined job (by another worker instance) finds inconsistent state? Is there a race window where a second worker could also try to move the same card to quarantine?

Left owed
Read the entire diff (all 8 files, 1138 lines net). Did not read beyond the diff boundary: did not run go test, did not inspect other branches or PRs. Did not walk the full `internal/pulse/` package to verify all import dependencies and transitive behavior.

git status --short
 (empty — working tree clean)
git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
