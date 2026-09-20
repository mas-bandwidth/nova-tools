"RESULT tools22-rule-pulse-17-L716 sha=5298f6be12ea — does the code at this base do what docs/SPEC-PULSE.md rule 17 says?
BLOCKED head=<none: repo dir absent, git HEAD undefined>
SPEC docs/SPEC-PULSE.md:716 rule 17
PKG internal/pulse
ASK (cannot be formed: tree not present) An implementation would have to ensure a symlink inside `_diag` that targets a file outside the runner directory is left in place with its link target intact, while aggregate delete accounting (a prune byte cap) proves the prune actually removed something.
Left owed (blocked pre-analysis — cannot read docs/SPEC-PULSE.md or internal/pulse).

why blocked: launch.out records `WALL REFUSED denied /tmp/ task=card-tools22-rule-pulse-17-L716 step=-`. The pinned base-repo is `/tmp/nova-tools-mirror.git`, denied by the sandbox, so `<JOBDIR>/repo` was never cloned. `cd repo` fails (`No such file or directory`), and the only `.git` in the job dir is an empty initializer (`fatal: your current branch 'master' does not have any commits yet`). No `HEAD`, no working tree, no source to read; rule 17 cannot be evaluated.
