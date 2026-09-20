RESULT tools22-pre-2108-r3 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2108 at head fc4866097bf6: accept: a card-printed wall marker is not ABSTAIN toolchain (#1806)
PREREAD 2108 ABSTAIN — required infrastructure missing

PR 2108, HEAD fc4866097bf67dda96a01ccaf0230bf7c3b3a9b9 (unreachable), BASE dev, MERGE-BASE unknown, BEHIND unknown, FILES 0 production, 0 test, LINES +0 -0

## ABSTAIN — environment not set up

The job directory contains no `repo/` subdirectory and `/tmp/nova-tools-mirror.git` does not exist. Every `cd repo` command in STEP 1–2 fails with "No such file or directory". Without the repository clone and the bench-local mirror, no part of this reading can be performed: no head verification, no diff, no claims, no defect analysis.

The card mandates that the entire product is RESULT.md and that the result is correct and useful even when the head cannot be fetched. This is the correct outcome for a card whose staging has not been completed.

git status --short prints:
?? .lease
?? .nova-sandbox-tmp/
?? RESULT.md
?? harness-output.log
?? opencode.json

git rev-parse HEAD prints: fatal: your current branch 'master' does not have any commits yet