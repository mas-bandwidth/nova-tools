RESULT tools22-pre-642-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#642 at head 0784281e5afa: nova-board: create the directory on first add
PREREAD 642 claims=7 proven=7 unproven=0 defects=0 high=0
PR 642
HEAD 0784281e5afa182b835a30a8161149dfa41ec91e
BASE dev
MERGE-BASE 0876a131d2b46504b060e01122df7b52532ad62d
BEHIND 445
FILES 4 production, 2 test
LINES +166 -56

1. The first `add` into a nonexistent `--dir` creates that directory via os.MkdirAll(path, 0o755) rather than refusing it. — PROVEN-BY cmd/nova-board/firstrun_test.go:510 TestAddCreatesTheBoardDirectoryOnFirstUse — calls add with a missing --dir, asserts exit 0 and that the directory exists as a real directory on disk.

2. The `ADD OK` output line gains a new `created=true|false` field so a reader can tell which run made the directory. — PROVEN-BY cmd/nova-board/main.go:479,533 — two fmt.Fprintf("ADD OK ...") calls now include `created=%s` using `yesNo(f.created)`.

3. A second add into an already-existing directory finds it there and reports `created=false`. — PROVEN-BY firstrun_test.go:560 — asserts `!strings.Contains(stdout, "created=false")` would fail; i.e. it asserts "created=false" IS in stdout.

4. Every verb except quickstart and add still refuses a missing `--dir` and carries the `mkdir -p` remedy. — PROVEN-BY firstrun_test.go:318 — loop changed from `["list","check","add"]` to `["list","check","take"]`, confirming add no longer refuses.

5. `add` against a `--dir` that is a regular file (not missing, but a file path) refuses with "is a file" and does not mention mkdir -p. — PROVEN-BY firstrun_test.go:367 — added explicit assertion for add against a file path, checking stderr contains "is a file" and no "mkdir -p".

6. Refusal order for `add` changed: flag hints now come before the backend hint, because every flag is judged before the backend (which makes the directory) is invoked. — PROVEN-BY cmd/nova-board/main_test.go:1467 `want` changed from `[backendHint, asHint, textHint, byHint, defHint]` to `[asHint, textHint, byHint, defHint, backendHint]`.

7. Documentation (CLI.md, SPEC-BOARD.md) updated to reflect that both quickstart and the first add make their directory, and that `ADD OK` includes `created=`. — PROVEN-BY-EXISTING docs/CLI.md:788, docs/SPEC-BOARD.md:299,383 — spec format line and prose updated.

DEFECTS none

QUESTIONS:

1. The merge base is 0876a131d2b4, which is newer than the preread SHA 5298f6be12ea, and the PR is 445 commits behind dev. Was there a rebase or force-push on this PR after it was cut? The claim about issue 625 being dogfooded ("dogfood, Stella") suggests real-world testing — were there any operational lessons learned beyond what's in the comments?

2. cmdAdd sets `f.makeDir = true` unconditionally for all add invocations (line ~410). There is no runtime check distinguishing "first add" from "subsequent add" at the flags level — the directory creation gate lives entirely in `backend()` / `MakeDir()`. Is this intentional simplicity, or could it mask a case where a user explicitly passes an already-existing directory and somehow triggers MkdirAll redundantly?

3. The `created=` field uses `yesNo(f.created)` which returns "true" or "false". The spec format line (SPEC-BOARD.md:383) documents `created=<true|false>` — but unlike `existed=` which describes whether the card ID already existed, `created=` describes whether the directory was made. Could downstream parsers misread one for the other if they only look at the last two fields?

Left owed nothing. I read every changed file in full: the diff fit within reading bounds and each change was examined against existing code patterns.

git status --short
0784281e5afa182b835a30a8161149dfa41ec91e
