RESULT: s17pyf-w14 sha=7c29513b0e8f

**BLOCKED** — base repository was never provisioned into this bench.

What stopped me: the card's base-repo `/tmp/schema14-ftf.bundle` does not exist in this sandbox,
and `/private/tmp` (the real target of the `/tmp` symlink) is not readable by the sandbox at all.
The job git repo was initialized empty — no commits, no objects — so STEP 1's
`git rev-parse HEAD` cannot print `7c29513b...`. Consequently the law doc
(`docs/FIXED-FORM-ALGORITHM.md`, STEP 2) and the C++ oracle files
(`test/tables/fixedform_main.cpp`, `test/tables/fixedform_properties.cpp`, `build/fixedform-corpus`,
STEP 3) do not exist on this machine, so row W14 cannot be ported. This is the bench's fault, not
the row's: python3 IS present (`/opt/homebrew/bin/python3`, Python 3.14.7), so it is not
`BLOCKED-TOOLCHAIN`.

Evidence, all verbatim:

```
$ git rev-parse HEAD
fatal: ambiguous argument 'HEAD': unknown revision or path not in the working tree.
Use '--' to separate paths from revisions, like this:
'git <command> [<revision>...] -- [<file>...]'

$ git count-objects -v
count: 0
size: 0
in-pack: 0
packs: 0

$ git bundle list-heads /tmp/schema14-ftf.bundle
error: could not open '/tmp/schema14-ftf.bundle'

$ ls /tmp
/tmp          # empty — no schema14-ftf.bundle

$ ls /private/tmp
ls: /private/tmp: Operation not permitted

$ ls python/ test/py-* 2>/dev/null   # no output: neither tree exists
$ grep -rn "python\|py3\|\.py\b" make/ Makefile .github/workflows/ci-fast.yml 2>/dev/null
$ command -v python3 && python3 -VV
/opt/homebrew/bin/python3
Python 3.14.7 (main, Aug  5 2026, 10:29:49) [Clang 21.0.0 (clang-2100.1.1.101)]

# no FIXED-FORM / fixedform file anywhere readable:
$ find /Users/glenn/sdk ... -iname "FIXED-FORM*" -o -iname "fixedform*"   # nothing
$ find ... -maxdepth 4 -name ".git" -type d
/Users/glenn/rowan-working/tmp/.../jobs/card-s17pyf-w14/.git   # the only one, and it is empty
```

Sandbox read whitelist (from native-argv.log) grants reads only of the job parent dir,
`/Users/Shared/nova-swarm-stella-20260913/harness-v1.18.20`, `/opt/homebrew`, and `/Users/glenn/sdk` —
none of which contains the schema repo or the bundle.

Nothing was committed: there are no files under `test/py-fixedform/` or `python/` to commit, and per
STEP 6 a commit may only contain those two trees. This RESULT.md is left as the finding for the
manager.