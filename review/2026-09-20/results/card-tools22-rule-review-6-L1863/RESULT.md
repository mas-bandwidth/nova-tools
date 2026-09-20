RESULT tools22-rule-review-6-L1863 sha=5298f6be12ea — does the code at this base do what docs/SPEC-REVIEW.md rule 6 says?
BLOCKED head=fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree
SPEC docs/SPEC-REVIEW.md:1863 rule 6
PKG internal/review
STEP 1 gate failed: `<JOBDIR>/repo` does not exist. The job directory's own `.git` has no
commits (no refs, no objects), so `git rev-parse HEAD` returns exit 128 with the fatal above
instead of `5298f6be12eaa0f7e6622334d2b6a1eb427649e3`. The pinned base tree, docs/SPEC-REVIEW.md,
and internal/review were never checked out; a recursive find of the workspace root for
`SPEC-REVIEW.md`, `review` dirs, or any `*.go` file returned nothing. The `base-repo`
`/tmp/nova-tools-mirror.git` is not accessible (Operation not permitted) and no `nova-*` binary
may be run. Nothing can be read, so no verdict beyond this gate is possible. Stop per STEP 1.

git status --short (job dir, printed below; only harness bookkeeping files, no source tree):
 .lease
 .nova-sandbox-tmp/
 harness-output.log
 opencode.json