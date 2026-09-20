RESULT: s17pyf-w1 sha=7c29513b0e8f

**BLOCKED** — the base repo was never materialized into the working tree.

What stopped me: the working tree is empty and the source bundle is unreadable, so the row
cannot be ported. This is the bench's fault, not the row's.

STEP 1 (orient) already fails:
- `git rev-parse HEAD` → `fatal: ambiguous argument 'HEAD': unknown revision or path in the
  working tree` (exit 128). The repo has **no commits**: `git count-objects -v` reports
  `count: 0`, `in-pack: 0`, `packs: 0`, no refs under `.git/refs/`, no `packed-refs`.
- The sha12 above is taken from the card header (`base-sha 7c29513b0e8f…`), not from `git
  rev-parse HEAD`, because the latter cannot print any sha in this empty repo.
- `ls python/ test/py-*` → nothing (both trees absent, `test/` does not exist).
- `grep -rn "python\|py3\|\.py\b" make/ Makefile .github/workflows/ci-fast.yml` → nothing
  (none of those paths exist).
- The bundle that should provide the base repo:
  `stat /tmp/schema14-ftf.bundle` → `Operation not permitted` (and `/private/tmp` is denied
  the same way by this sandbox). So I cannot fetch the base commit `7c29513b0e8f`.

STEP 2 (the law for row W1): impossible. `docs/FIXED-FORM-ALGORITHM.md` does not exist in the
working tree, so there is no rule sentence to quote and no file:line. I cannot determine
whether the doc states the "write slack is template zeros" rule.

STEP 3 (the C++ oracle): impossible. `test/tables/fixedform_main.cpp`,
`test/tables/fixedform_properties.cpp`, and `build/fixedform-corpus` do not exist (the whole
`test/` tree is absent). I cannot quote the C++ reference's line range for row W1.

STEP 5 (red-first case): cannot be written, since the case must be authored against the
algorithm law and byte oracle that are missing. No scaffolding was written under
`test/py-fixedform/` or `python/`; nothing was committed (the repo has no commits).

Toolchain note (so the row is not blamed for the environment): `python3` is present
(`Python 3.14.7`), but `python3 -m pytest` is not installed on this bench, so the acceptable
`python3 <case file>` fallback would have been used. That is not the blocker.

Greps run:
```
$ ls python/ test/py-* 2>/dev/null ; ls test/ 2>/dev/null
(no output — neither tree exists)
$ grep -rn "python\|py3\|\.py\b" make/ Makefile .github/workflows/ci-fast.yml 2>/dev/null
(no output — paths do not exist)
$ stat /tmp/schema14-ftf.bundle
stat: /tmp/schema14-ftf.bundle: stat: Operation not permitted
$ git rev-parse HEAD
fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.
$ git count-objects -v
count: 0   size: 0   in-pack: 0   packs: 0
```

Verdict: **BLOCKED** (bench/environment fault: base repo missing and `/tmp/schema14-ftf.bundle`
unreadable). Re-run this card on a harness that materializes the base commit `7c29513b0e8f`
into the working tree first.