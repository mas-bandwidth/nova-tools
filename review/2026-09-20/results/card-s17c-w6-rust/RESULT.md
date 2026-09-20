RESULT: s17c-w6-rust sha=7c29513b0e8f

FINDING: NO SITE

The Rust leg has no implementation of the union-guard partition rule to test.

GREPS RUN:
```
$ grep -rn "noGuard" test/ internal/codegen/ | head -20
(no output)
```
```
$ ls internal/codegen/rusttable/ test/rust-* 2>/dev/null | head -20
(no output)
```
```
$ find . -type f \( -name "*.rs" -o -name "*.go" \) | grep -v data/opencode | grep -v ".git"
(no output)
```
```
$ grep -n "noGuard\|guard.*partition\|PARTITIONS" . --include="*.rs" --include="*.go" --include="*.cpp" --include="*.md"
(no output)
```
```
$ git log --all --oneline
(no output)
```

EVIDENCE:
- Repository contains zero commits (git rev-parse HEAD fails; git log returns nothing)
- No `test/rust-fixedform/src/main.rs` exists
- No `internal/codegen/rusttable/` directory exists
- No `.rs` or `.go` source files exist anywhere in the job tree
- No `noGuard` constant is referenced, defined, or used on any Rust codepath
- The base-bundle at `/tmp/schema14-ftf.bundle` is inaccessible under sandbox permissions (Operation not permitted), so the C++ reference at `test/tables/fixedform_main.cpp:1874-1980` could not be read for cross-reference

DEFENCE STATUS: Missing entirely. There is no site to pin, no guard logic to disable, and therefore no control experiment possible. The Rust leg does not implement — and has never implemented — the union-guard partition behavior described in `docs/FIXED-FORM-ALGORITHM.md` section 5.9 item #42.

Per Step 3 instruction: "If after an honest search the site does not exist on your leg, THAT IS THE FINDING: write it with the greps, say the defence is missing, and do not invent a test that passes against behaviour the leg does not have."

Per Step 7 option: NO SITE — the leg has no defence to test.
