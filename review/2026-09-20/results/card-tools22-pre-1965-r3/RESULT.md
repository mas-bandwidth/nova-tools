RESULT tools22-pre-1965-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1965 at head 108ce8bfc170: decide: a committed rule answers its kind with no call and is still logged (#1622)
PREREAD 1965 claims=7 proven=5 unproven=2 defects=2 high=0

PR 1965
HEAD 108ce8bfc170db5b826e909587d528e75f8b8aaa
BASE dev
MERGE-BASE 23d9698b0b862641a7569dd268c6f1ed8c4d57b0
BEHIND 9
FILES 2 production, 1 test
LINES +155 -2

The merge base (23d9698b) is OLDER than the card's BASE 5298f6be12ea (it is its
ancestor, nine commits back); the head is 9 commits behind origin/dev. The
diff is read against the merge base as the card requires. The changed files
were read in full; the default embedded registry carries no `rules` member, so
the default ladder is unchanged.

CLAIMS

1. A committed registry rule answers its kind: a unit of a ruled kind with no
   counter-evidence is routed to the rule's rung.
   PROVEN-BY internal/decide/rule_test.go:36-41 TestARuledKindMakesNoCallAndIsStillLogged — asserts the dogfood unit answers Source "rule" with rung "pro".
2. The rule's answer makes no provider call.
   PROVEN-BY internal/decide/rule_test.go:33-35 — asserts the fakeDecider was called 0 times.
3. The rule's answer is still logged: a decision row with source=rule and calls=0.
   PROVEN-BY internal/decide/rule_test.go:42-44 — asserts EntryFor yields Source "rule" and Calls 0.
4. A confirmed failure steps the rule aside and the ladder asks the provider as today.
   PROVEN-BY internal/decide/rule_test.go:55-60 — a unit with a failed flash attempt is re-routed, the fake is called, Source is not "rule".
5. A security touch, a fresh-take flag, or a size above the smallest bucket also steps the rule aside.
   UNPROVEN — no test exercises any of the three; the behaviour rests on the bare conditionals in ruled() (internal/decide/ladder.go:746, 749) with no witness.
6. A rule with no `by` is refused at load, because promotion is a person's commit.
   PROVEN-BY internal/decide/rule_test.go:63-67 — a rule naming only kind+rung is refused.
7. The registry refuses a malformed rule at load: no kind, an unknown kind, no rung, a rung the registry does not hold, or a kind ruled twice.
   UNPROVEN — the five validation branches in ParseRegistry (internal/decide/registry.go:224-236) have no test; only the no-`by` case is covered.

DEFECTS

DEFECT low internal/decide/ladder.go:746 — ruled() re-checks u.Security(), but routeRules answered every security unit at ladder.go:497 before ruled() is ever reached, so the condition is dead — a reader is told a security touch steps the rule aside when it is actually the earlier security branch that answers, and the check would silently mislead a future caller of ruled() from another path — drop the u.Security() term or move the ruled step-aside before the security branch and test it.

DEFECT low internal/decide/registry.go:225 — Rule.From is documented as "the log summary date it came from" (registry.go:120) but is only TrimSpace'd, never validated as a date, so a mistyped from is accepted silently — a rule row cannot tell a person which log summary it came from, which is the field's only purpose — validate From as an RFC3339 date (or a YYYY-MM-DD) at load, refusing a value that is not one.

QUESTIONS

1. Spec H1 (SPEC-AHEAD #1622, docs/SPEC-DECIDE.md:1331-1345) also mandates the 1-in-20 sampling of ruled kinds (RULE-STALE) and the RULE-CANDIDATE lines in `nova-decide log --summary`; this PR lands only the answering half. Is the sampling and the candidate printer deliberately deferred to a follow-up, and is a rule without it authorable in practice (the default registry has no rules and nothing computes candidates yet)?
2. A rule steps aside only above the smallest size bucket (files/packages/lanes > 3, ladder.go:749), while supportedHeight raises a rung already at packages >= 3 or lanes >= 2 (ladder.go:765-766) — so a ruled kind with exactly 3 packages or 2-3 lanes is answered by the rule with confidence 1.00 and no call, even though the ladder's own size evidence would raise it. Is that boundary intended, or should the rule step aside wherever the ladder's evidence rules would raise?
3. An odd platform or a sub-10-minute deadline does not step a rule aside (they are not in ruled()'s list), so a ruled kind on, say, windows is still answered the rule's rung, confidence 1.00, no call. Deliberate — the rule is a person's constant that outweighs first-attempt risk — or an oversight?
4. The registry validation checks only that a rule's rung names a mind the registry holds, not that it is usable: a rule naming an asleep or reserved mind loads fine and would route a ruled kind to it. Should load refuse such a rule, or is the person's constant meant to beat availability?

Left owed — cmd/nova-decide/main.go and the other decide test files (ladder_test.go, registry_test.go, hold_test.go, public_test.go, tune1_test.go, routelog_test.go) were not read in full: the diff does not touch them, and the changed functions' consumers (Summarize, LastDecision, RouteCard, nova-work's src= line) were traced through grep to confirm none switches on the log's source value beyond outcome-vs-decision. The rest of docs/SPEC-DECIDE.md beyond the registry and H1-H4 sections was not read. go build and go vet of internal/decide, internal/swarm and cmd/nova-decide pass.

git status --short: (clean; nothing printed)
git rev-parse HEAD: 108ce8bfc170db5b826e909587d528e75f8b8aaa===FILE=== card-tools22-pre-1965-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1965-r1	1	2026-09-20T19:26:22Z	2026-09-20T19:34:57Z	0	opencode	deepseek-v4-flash	65703	28614	0	1157888	0	0.0496
