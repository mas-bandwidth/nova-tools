RESULT tools22-rule-review-7-L1159 sha=5298f6be12ea — does the code at this base do what docs/SPEC-REVIEW.md rule 7 says?
BLOCKED head=fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.
SPEC docs/SPEC-REVIEW.md:1159 rule 7
PKG internal/review
ASK unreachable: the tree at the pinned base is not present in this job, so rule 7 cannot be read or checked.
Evidence: `git rev-parse HEAD` in <JOBDIR>/repo fails with the fatal above; the job git repo has zero commits/objects (`git cat-file -t 5298f6be12eaa0f7e6622334d2b6a1eb427649e3` -> could not get object info); there is no `repo/` subdirectory; `docs/SPEC-REVIEW.md` does not exist; `git fetch /tmp/nova-tools-mirror.git master` -> "does not appear to be a git repository / Could not read from remote repository"; /tmp is `Operation not permitted` inside this sandbox.
Greps run: `git rev-parse HEAD`, `git cat-file -t <sha>`, `ls docs/`, `ls -d repo`, `git fetch /tmp/nova-tools-mirror.git`, none of which could surface any tree.
Left owed: the review itself — no code was reachable to compare against rule 7.