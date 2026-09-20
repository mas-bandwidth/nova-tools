RESULT tools22-pre-2142-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2142 at head e992d1662345: nova-merge simulate/batch: check children do not inherit parent PASSWORD/WEBHOOK secrets (#1836
PREREAD 2142 claims=5 proven=4 unproven=1 defects=2 high=0

* PR 2142
* HEAD e992d1662345a2b4701828f3da1701451fbcd381
* BASE dev
* MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730
* BEHIND 1
* FILES 1 production, 1 test
* LINES +95 -14

The merge base (a7611c8189) is the direct parent of the PR head and is OLDER than
the card's stated base 5298f6be12ea: dev moved one commit (5298f6be, #2144) after
this PR forked. `git log merge-base..pr` shows exactly one commit (the PR's), so no
foreign work is folded into the diff. Working tree sits on origin/main (which no
longer has simulate.go); every file below was read via `git show refs/tmp/pr2142:`.

CLAIMS

1. A check that `simulate` runs never sees a variable whose NAME contains PASSWORD that its parent process held.
   PROVEN-BY cmd/nova-merge/simulate_test.go:343 TestCheckChildrenDoNotInheritAParentSecret — the nil-env (simulate) path asserts the child printed CHILD_FAKE_PASSWORD_FOR_PROBE=unset; on the base commit the probe reached the child and the test is red.
2. A check that `batch` runs never sees a variable whose NAME contains PASSWORD that its parent held.
   PROVEN-BY cmd/nova-merge/simulate_test.go:351 — the ciTestEnv (batch) path of the same test asserts the same unset for the password probe; also red on base.
3. A check child never sees a parent variable whose NAME carries SECRET.
   PROVEN-BY cmd/nova-merge/simulate_test.go:347,355 — FAKE_SECRET_FOR_PROBE=unset is asserted on both paths; the behavior pre-exists in goenv.Clean but this is its first witness at the check-child boundary.
4. A check child never sees a parent variable whose NAME contains WEBHOOK.
   UNPROVEN — extraCheckSecret matches WEBHOOK (simulate.go:489) but no test in this diff or the tree ever sets a WEBHOOK-named variable; only the SECRET and PASSWORD probes are exercised. Deleting the WEBHOOK branch would leave every test green.
5. The drop decides by NAME and ignores the VALUE.
   PROVEN-BY cmd/nova-merge/simulate_test.go:334-335,347 — the dummies carry the innocuous value "dummy-not-a-credential" and are still dropped, and the child echoes only set/unset, never a value.

DEFECTS

DEFECT medium cmd/nova-merge/simulate.go:471-489 — the new PASSWORD/WEBHOOK drop is added in cmd/nova-merge's checkChildEnv instead of goenv.Clean, so goenv.go:41 ("Removed is the documented list of what Clean drops, and the only list") and goenv.go:126 ("Clean can be the one place a child's environment is built") are now false, and the same leak class stays open at internal/review/mutate.go:557 and internal/review/seed.go:356,395, whose children run the tree-under-test's own `go test` with only Clean/WithoutSecrets and so can still read SMTP_PASSWORD, BSKY_APP_PASSWORD or DISCORD_*_WEBHOOK — the PR closes the door for two of the three children its own package docstring (goenv.go:17) names in this class, silently — move PASSWORD/WEBHOOK into goenv.Clean (and keyshape.SecretName, the fleet's one predicate) so every child inherits it and the "only list" doc stays true.
DEFECT low cmd/nova-merge/simulate.go:487-490 — extraCheckSecret drops ANY variable whose name merely contains PASSWORD or WEBHOOK (e.g. PASSWORD_STORE_DIR, a non-credential config) with no allowlist and no message, so a custom `--checks` command or batch step that genuinely needs such a variable has it silently stripped — bounded blast radius (check children only) but silent and unfilterable — match the concrete names the tree holds (SMTP_PASSWORD, SMTP_PASSWORD_BACKUP, BSKY_APP_PASSWORD, DISCORD_*_WEBHOOK) or expose an explicit allowlist escape.

QUESTIONS

1. Why is the PASSWORD/WEBHOOK drop placed in cmd/nova-merge's checkChildEnv rather than in goenv.Clean/keyshape.SecretName, given goenv's "one place" doctrine and that internal/review/mutate.go:557 and seed.go:356,395 run the same class of tree-under-test child with only Clean/WithoutSecrets? Is simulate/batch the intended scope, or was extending goenv considered and rejected, and on what grounds?
2. Should a batch step be able to deliberately pass a PASSWORD/WEBHOOK-named variable to its own command (batchStep.env, batch.go:64, is now filtered too), or is the drop meant to be absolute for every check child with no opt-out?
3. The commit title promises WEBHOOK dropping too, but the new test sets only SECRET and PASSWORD probes — was a WEBHOOK probe considered and dropped, or is that branch deliberately left unwitnessed?

Left owed: simulate.go and simulate_test.go read in full at the PR head; goenv.go, keyshape.go (SecretName), checkproc_unix/other.go, CLI.md's simulate section, and the batch.go regions around ciTestEnv/withBin/step.env read for context. I did not read the whole of batch.go or internal/ci; I read only the parts touching runCheck/checkChildEnv. No test run was performed (none required); `go vet ./cmd/nova-merge/` at the PR head (exported via git archive) passed, confirming the tree compiles.

git status --short: (nothing)
git rev-parse HEAD: d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-2142-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-2142-r1	1	2026-09-20T19:28:13Z	2026-09-20T19:42:14Z	0	opencode	deepseek-v4-flash	48273	33937	0	2034176	0	0.0732
