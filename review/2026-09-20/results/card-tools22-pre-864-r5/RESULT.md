RESULT tools22-pre-864-r5 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#864 at head d727576bd90f: nova-bus: wake, verdict rows and close --older-than (class K of #828)
ABSTAIN precondition failed: repo/ directory does not exist and /tmp/nova-tools-mirror.git is not present. The harness did not stage the repository at the pinned base as required by the card. Without the mirror and repo, no fetch or diff is possible. The card cannot proceed.

```
> git status --short
?? .lease
?? .nova-sandbox-tmp/
?? RESULT.md
?? harness-output.log
?? opencode.json

> git rev-parse HEAD
fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.
Use '--' to separate paths from revisions, like this:
'git <command> [<revision>...] -- [<file>...]'
HEAD
```