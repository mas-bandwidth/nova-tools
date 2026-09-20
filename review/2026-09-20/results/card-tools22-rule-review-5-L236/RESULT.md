RESULT tools22-rule-review-5-L236 sha=5298f6be12ea — does the code at this base do what docs/SPEC-REVIEW.md rule 5 says?
BLOCKED head=fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.
SPEC docs/SPEC-REVIEW.md:236 rule 5
PKG internal/review
ASK (not reached — tree absent)
Left owed. git status --short below prints the harness's own untracked files (.lease, harness-output.log, opencode.json, .nova-sandbox-tmp/); there is no checkout and no source to diff, so nothing of the repo is changed. Commands run: `git rev-parse HEAD` (exit 1, fatal: ambiguous argument 'HEAD'), `git log` (fatal: your current branch 'master' does not have any commits yet), `git show-ref` (empty), `ls` (no `repo/`, no `internal/`, no `docs/`), `git --git-dir=/tmp/nova-tools-mirror.git rev-parse HEAD` (Operation not permitted; mirror unreachable). Stopped per Step 1.

git status --short:
?? .lease
?? .nova-sandbox-tmp/
?? harness-output.log
?? opencode.json