RESULT tools22-rule-review-6-L2157 sha=5298f6be12ea — does the code at this base do what docs/SPEC-REVIEW.md rule 6 says?
BLOCKED head=fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.; use '--' to separate paths from revisions, like this: 'git <command> [<revision>...] -- [<file>...]'
SPEC docs/SPEC-REVIEW.md:2157 rule 6
PKG internal/review
ASK An implementation would need a `cmd/nova-review/main.go` binary implementing verbs including `policy`, flag refusals for `--readers`/`--policy` exclusion, `--base` for a `base:` row, `--reuse` with id check, reserved `answer`/`policy` names, the output grammar, `--max` and `--max-bytes`, the exit table, and `help`/`version` commands, with tests covering every refusal sentence and every exit code.
GREPS_RUN:
  ls -la /Users/glenn/rowan-working/tmp/2e8b6a25-21cc-a1c5-9442-ea31ae8d4f5e-card-tools22-rule-review-6-L2157/jobs/card-tools22-rule-review-6-L2157/repo (empty dir)
  find /Users/glenn/rowan-working/tmp -name ".git" -type d 2>/dev/null (no matches found)
  find /tmp -maxdepth 3 -name "nova*" 2>/dev/null (no matches found)
  git clone attempts to github.com/anomalyco/nova-tools.git (network unreachable/permission denied)
  ls /tmp/nova-tools-mirror.git (does not exist)
NOTE: Repository was not available at the expected location (/jobs/.../repo), the specified mirror (/tmp/nova-tools-mirror.git) does not exist, and network access to clone the source repository is blocked. No code could be examined.
Left owed
git status --short: (empty — no files tracked or untracked in repo/)
