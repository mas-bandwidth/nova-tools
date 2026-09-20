RESULT tools22-pre-1860-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1860 at head 358e07ff1573: decide: a mechanical kind's step-up rung is earned by a confirmed failure, not offered on a fir
PREREAD 1860 claims=5 proven=4 unproven=1 defects=3 high=0

PR 1860
HEAD 358e07ff15733927d110a523dfc2462c58b9cbc5
BASE dev
MERGE-BASE 0878987f9d44eb8c7a9ad01a2180b949d46476fe (older than dev@5298f6be; 5298 is a descendant of the merge base, so the branch forked before the base commit this card was cut on)
BEHIND 22
FILES 1 production, 5 test (plus 2 docs)
LINES +142 -9

CLAIMS
1. A mechanical kind with no CONFIRMED failure is offered its supported rung alone, so no decision is asked for it at all: the provider is never called, the rules' own rung stands, and the reason says "one eligible rung, so no decision to ask". PROVEN-BY internal/decide/mechanicaloffer_test.go:11 TestAMechanicalKindIsOfferedOneRungUntilSomethingFails (asserts fake.calls == 0, res.Rung == rules.Rung, and the reason phrase).
2. That decision boundary is a fact about the unit's attempts (no confirmed failure), never a proxy on len(offered): a mechanical no-failure unit whose supported height holds two eligible minds of different lineages still makes no call even though the offered set has two entries. PROVEN-BY internal/decide/mechanicaloffer_test.go:63 TestASameHeightMechanicalOfferWithTwoMindsMakesNoCall (asserts len(Offered) >= 2 AND fake.calls == 0 — Stella's #1860 witness shape).
3. A CONFIRMED failure restores the step-up offer: the provider is called again, two or more rungs are offered, and the provider's choice is the answer with Source=jev. PROVEN-BY internal/decide/mechanicaloffer_test.go:33 TestAConfirmedFailureRestoresTheStepUpOffer (asserts calls == 1, len(Offered) >= 2, res.Rung == Offered[1], Source == jev).
4. With the step-up offer restored, a confirmed-failure mechanical unit can still reach the top-of-ladder refusal, and the already-paid call survives it in the usage row and the log row. PROVEN-BY internal/decide/usage_test.go:141 TestUsageSurvivesARoutingRefusal and cmd/nova-decide/usage_witness_test.go:97 TestRouteRefusalStillPersistsTheCall (both re-anchored by this PR with a mid rung and a low:failed attempt; both assert Calls == 1 and the 937/12 spend survives the refusal).
5. The offer set itself for a mechanical no-failure unit is the supported rung ALONE (a single height, the step-up rung excluded). UNPROVEN — no test asserts the shape of the offered set in the no-failure case; test 1 and test 3 assert only the no-call and the reason string, and test 3's witness requires the offered set to hold TWO minds. If offer()'s bound=1 reverted to 2, every test in this diff would still pass, so the "alone" half of the claim is unwitnessed.

DEFECTS
DEFECT medium cmd/nova-decide/route.go:159 (and :175, :122-135) — for a mechanical kind with no confirmed failure the library now makes no provider call, but the CLI still prints "asking jev about unit ..." and "jev answered for unit ..." on stderr and still refuses the route without --usage/--log, all for a call that never happens — the operator-facing contract contradicts the new rule (SPEC-DECIDE.md's "no decision is asked for it at all" now sits beside "accounting ... required whenever jev is asked" with no carve-out) — detect the no-call case (mechanical kind, no confirmed failure) before the accounting gate and the stderr prints, or reword them to say no decision was asked.
DEFECT low internal/decide/tune1_test.go:109-122 — the four-confidence "measured band" loop in TestTheDefaultFloorSitsBelowTheMeasuredBand uses KindRowTest with no attempts, so under the new guard the provider is never consulted and every iteration returns the same rules answer; the loop asserts the same thing four times and can no longer catch a floor regression for mechanical kinds (only the "collapse" half was re-anchored with an attempt) — give the loop units a confirmed failure or drop the provider premise from the test's name and comment.
DEFECT low internal/decide/ladder.go:927 — a mechanical no-failure unit whose supported height holds two eligible minds returns a result whose Reason says "one eligible rung, so no decision to ask" while its Offered list carries both minds (test 3's own witness asserts len(Offered) >= 2 next to that phrase); the sentence was exact on the old len(offered) < 2 path but is now a claim about one HEIGHT that a reader holding the offered list can misread — reword the guard's reason (e.g. "the supported rung alone, so no decision to ask").

QUESTIONS
1. The gate is len(failedAt) == 0 ("no CONFIRMED failure anywhere"), but the commit title says "not offered on a first call". For a mechanical unit with prior attempts that SUCCEEDED (failedAt empty, attempts > 0) the guard also suppresses the provider call. Is "no confirmed failure" the intended boundary, or should it be "first call (no attempts)"?
2. The CLI accounting gate and the "asking jev"/"jev answered" stderr were left unchanged while the library stopped calling for mechanical first attempts. Was keeping the gate unconditional a deliberate choice, or is it an oversight this PR should close?
3. This removes the provider from first-attempt mechanical dispatch entirely: previously Jev chose between the two DeepSeek rungs (flash/pro) on a fresh rebase. What were the concrete #1513/#1860 failures that motivated suppressing even that supported-rung choice on the first call, and is the loss of first-call calibration (the tune1 "measured band" premise) acceptable?

Left owed — read the entire diff and, in full, the production file internal/decide/ladder.go, the new internal/decide/mechanicaloffer_test.go, and the changed test files (usage_test.go, tune1_test.go, usage_witness_test.go, accounting_test.go) and the changed docs sections. Not read in full: the rest of docs/CLI.md and docs/SPEC-DECIDE.md (only the changed paragraphs), and unchanged files (decisions.go, tune.go, routelog.go, registry.go were read only where the diff's symbols touch them). No test run was performed, as the card expects none.

git status --short:
(nothing printed)
git rev-parse HEAD:
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1860-r2/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1860-r2	1	2026-09-20T19:23:58Z	2026-09-20T19:35:07Z	0	opencode	deepseek-v4-flash	81437	36466	0	2801920	0	0.1001
