RESULT: s17rb-971 sha=7c29513b0e8f

**BLOCKED**

What stopped me: the bench provisioned **neither** of the two inputs this card requires, so no part of the work (STEP 1..5) could even begin.

- `repo/` — the base clone that "is the clone in `repo/`" — does not exist. The very first orientation command `cd repo && git rev-parse HEAD` fails with "No such file or directory", and the bench's own initial probe in `harness-output.log` hit exactly the same error before I touched anything.
- The working-directory git repo is an empty `git init`: zero commits, empty `objects/`, no refs, no `FETCH_HEAD`, no packs.
- The head bundle `/tmp/s17-heads.bundle` and the base bundle `/tmp/schema14-ftf.bundle` are **absent** (`ls /tmp/*.bundle` → "No such file or directory"); `/tmp` (→ `private/tmp`) is also outside the sandbox read window, so they cannot be reached by any path I have.
- Exhaustive search of the accessible filesystem (`find` over the workspace tree and `/Users/nova/rowan-working` for `*.bundle`, `*.pack`, `repo`) found nothing but opencode's own workspace snapshot, which contains only `.lease`, `opencode.json`, `harness-output.log` — no repo content, no refs.
- The two commit shas are unrecoverable from the outside: GitHub commit search returns 0 hits for both `7c29513b0e8f42f457189d1bec23e1de475d4ed8` and `7729b196494f`, and repository search returns 0 hits for `schema14`, `refuse-is-total-prepass`, and `FIXED-FORM-ALGORITHM`. (curl/git can reach api.github.com, so the absence is the repo's, not the sandbox's — this is a synthetic offline bench whose content exists only in the two bundles that were never placed.)

Consequences: I cannot fetch the head, so I cannot compute the merge base, cannot read the three commits' messages, cannot produce the STEP-2 "what is this PR for" paragraph, cannot build the STEP-3 conflict inventory, and cannot resolve or rebase anything. There is no `rebase-work` branch and nothing to prove with a harness; `go` (go1.27.1) is present but there is no source tree to test.

This is the bench's fault (failed provisioning), not a defect in PR #971. The manager should re-provision the bundles/base clone and re-run the card.

Note on line 1: STEP 1 could not run, so I never printed a sha12; `7c29513b0e8f` is the base sha from the card header, which is what `git rev-parse HEAD` was required to print.