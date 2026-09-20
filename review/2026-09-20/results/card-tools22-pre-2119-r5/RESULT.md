RESULT tools22-pre-2119-r5 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2119 at head dee6a13e8593: fill: one poisoned registry row disables that bench, not the fleet (#2031)
ABSTAIN mirror unavailable — the head could not be verified, so no reading was produced
PR 2119 (unread — head never fetched)
HEAD unverified (card expects dee6a13e8593d5b182f8c27aaf9b092165d9e7fa)
MERGE-BASE unknown
BEHIND unknown
FILES unknown
LINES unknown

This card cannot be executed as cut. STEP 1 requires fetching refs/pull/2119/head from the
bench-local mirror at /tmp/nova-tools-mirror.git into a staged `repo`, but neither exists
inside this job's wall:

- `git fetch -q /tmp/nova-tools-mirror.git refs/pull/2119/head:refs/tmp/pr2119` fails:
  `fatal: '/tmp/nova-tools-mirror.git' does not appear to be a git repository`.
- `ls /tmp/` fails: `Operation not permitted`. The sandbox allow-list recorded in
  native-argv.log (the `--read`/`--write` paths passed to nova-sandbox) does not include
  `/tmp` or any mirror path, so the mirror is unreachable by design of this run.
- There is no `repo` subdirectory under the job root; the job root's own `.git` has zero
  objects and zero refs and no commits (`git rev-parse HEAD` → `fatal: ambiguous argument
  'HEAD': unknown revision`).
- No replacement mirror exists anywhere else in the sandbox-accessible tree (job dir,
  data/, tmp/, gomodcache were searched).

Because the PR head was never obtained, I could not perform STEP 2 (diff), STEP 3 (claims),
STEP 4 (defects) or STEP 5 (questions). I deliberately did not reconstruct the PR from
public knowledge of nova-tools: per the card, a reading of a head nobody in this wall can see
is worse than none. The useful output is this abstain, so the reviewer knows the run was not
staged and can re-cut the card.

Expected behaviour when the environment is healthy: fetch should print
dee6a13e8593d5b182f8c27aaf9b092165d9e7fa and the card should proceed with the merge-base
against 5298f6be12eaa0f7e6622334d2b6a1eb427649e3.

Left owed — everything: no head, no diff, no claims, no defects, no questions; the mirror
and staged repo were absent and the sandbox denies /tmp.

git status --short:
?? .lease
?? .nova-sandbox-tmp/
?? RESULT.md
?? harness-output.log
?? opencode.json

git rev-parse HEAD:
fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.
Use '--' to separate paths from revisions, like this:
'git <command> [<revision>...] -- [<file>...]'