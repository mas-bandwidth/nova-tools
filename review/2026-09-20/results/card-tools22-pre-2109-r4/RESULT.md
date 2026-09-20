RESULT tools22-pre-2109-r4 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2109 at head fcbf9cb1dcbd: fill: the bench's SEAT comes from the registry, not swarm-<bench> (#2014)
ABSTAIN environment: STEP 1 impossible — no `repo` clone, no `/tmp/nova-tools-mirror.git`, no network; head fcbf9cb1dcbd992fd98654f0de768556da3d68ff unverifiable, no diff to read

What happened, exactly:

- The job root `/Users/glenn/rowan-working/tmp/922989e8-b78e-2d2d-0150-ed03870bcfc2-card-tools22-pre-2109-r4/jobs/card-tools22-pre-2109-r4` contains only `.git`, `.lease`,
  `.nova-sandbox-tmp`, `harness-output.log`, `opencode.json` — there is no `repo/` directory anywhere under the job tree.
- `git fetch -q /tmp/nova-tools-mirror.git refs/pull/2109/head:refs/tmp/pr2109` fails:
  `fatal: '/tmp/nova-tools-mirror.git' does not appear to be a git repository` (exit 128).
- The mirror path cannot even be listed: `/tmp` is a symlink to `/private/tmp`, and `ls /private/tmp` returns
  `Operation not permitted` from inside the sandbox (sandbox-exec, `net=nopromise`). Whether the mirror exists on
  the host or not, it is not reachable from this job.
- The job's own `.git` is a fresh, empty repository: `git rev-parse HEAD` fails with `unknown revision`, and
  `git rev-list --all --count` prints `0`. No objects, no refs, no remotes (`.git/config` has no `[remote]`).
- The opencode snapshot object store under `data/opencode/snapshot/...` holds only tree/blob objects (no commits),
  and a filesystem search for anything named `nova-tools` or `*mirror*` anywhere under the sandbox found nothing.
- No network is available (`net=nopromise`), so the PR head cannot be fetched from GitHub either.

Consequently:

- STEP 1's head check could not run, so I cannot confirm the head is still `fcbf9cb1dcbd992fd98654f0de768556da3d68ff`.
- STEP 2's diff, STEP 3's claims, STEP 4's defects, and STEP 5's questions all require the diff; none could be produced.
- Nothing in this card is a reading of the PR. No claims are asserted, no defects are alleged, no verdict is implied.
  If you want a real pre-read, re-run this card with the mirror present and readable (e.g. not behind the blocked
  `/private/tmp` mount) and the `repo` clone staged at base `5298f6be12eaa0f7e6622334d2b6a1eb427649e3`.

Left owed: the entire diff of mas-bandwidth/nova-tools#2109 — the environment never made it available.

git status --short:
(no output printed — status is clean: only untracked harness files and this RESULT.md)

git rev-parse HEAD:
fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.
(the job repo has zero commits; nothing was created, committed, or pushed)