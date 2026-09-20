RESULT work4s-E06-F01-03 sha=5298f6be12ea — nova-work E06-F01: does the contract say it? criterion E06-F01-03: Expose local/shared revision, age, unshared work and failed backup
BLOCKED head=fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.
CRITERION E06-F01-03 SPEC ABSENT
REPO mas-bandwidth/nova-tools
NO-BRANCH
grep -n "Expose local/shared revision" docs/SPEC-WORK.md | head -20
Noticed The repository was never staged. The card STEP 1 says the launcher already staged it into repo/ and printed STAGE OK; there is no repo/ directory in the job root (/Users/nova/rowan-working/tmp/1867fc68-2097-4d52-9052-08f8a5710e0f-card-work4s-E06-F01-03/jobs/card-work4s-E06-F01-03). The git repo in the job root is a bare-initialized repo with no commits (git status: "No commits yet"), no branches, no remotes, and an empty object database (find .git/objects -> no files, .git/packed-refs absent). base-repo /tmp/nova-tools-mirror.git does not exist (ls: No such file or directory), and /tmp is otherwise blocked by the sandbox (Operation not permitted). The harness backend declares net=nopromise, so cloning/fetching is not possible and the card forbids reaching github.com. git rev-parse HEAD exits 128 with "fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree." No SPEC-WORK.md or nova-work.sexp exists anywhere inside the job directory (find . -name SPEC-WORK.md -o -name '*.sexp' -> nothing). No grep over docs/SPEC-WORK.md could be run because the file is not present. git status --short is NOT empty, but only lists launcher/harness artifacts (.lease, .nova-sandbox-tmp/, harness-output.log, opencode.json) created by the sandbox harness before this session; none were created by this card. No searches of the contract, no sexp cross-check, and no verdict on the criterion were possible. This card changes nothing, as required; no files in the (empty) tree were edited.
git status --short (launcher artifacts only):
?? .lease
?? .nova-sandbox-tmp/
?? harness-output.log
?? opencode.json