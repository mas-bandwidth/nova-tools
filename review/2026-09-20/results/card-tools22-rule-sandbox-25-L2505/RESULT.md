RESULT tools22-rule-sandbox-25-L2505 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SANDBOX.md rule 25 says?
CONFORMS profiles/darwin.sb.tmpl:69
SPEC docs/SPEC-SANDBOX.md:2505 rule 25
PKG internal/sandbox
ASK The sandbox must deny write/delete operations on any path not explicitly named in the task's --write set, so that `rm -rf` of the line's self path (which is never in --write) fails with EPERM and leaves the self directory unchanged.

Deciding lines:
- profiles/darwin.sb.tmpl:69 `(deny default)` — everything denied by default; only paths in @@WRITES@@ get `(allow file-read* file-write* (subpath ...))` grants.
- internal/sandbox/profile.go:73-81 — DarwinProfile grants `file-write*` only per `--write` path via WRITEn markers.
- internal/sandbox/landlock_linux.go:42-43,100-101,139-141 — Landlock handles `fsRemoveDir` and `fsRemoveFile` but grants them only for paths under `--write` directories (path_beneath rules in wrap_linux.go:328-332).
- internal/swarm/sandbox.go:94 — the dispatcher puts the line's self (SlotDir) in `--read` only, never `--write`. The write set is JobDir and DataHome, not the slot/self directory.
- cmd/nova-swarm/sandbox_seam_test.go:270-274 — `TestTheWorkerArgvIsTheDispatchersAndNotTheTasks` asserts the argv: `--read <slotDir> --write <jobDir> --write <dataHome>`.

GUARDED-BY
- cmd/nova-swarm/sandbox_seam_test.go:41 `TestAJobCannotWriteOutsideItsJobDir` — an end-to-end test: a worker cannot `touch` a file outside its job dir; the same task with `--no-sandbox` lands the file (control).
- cmd/nova-sandbox/main_test.go:384 `TestProbeProvesTheWall` — the probe asserts `write_outside expect=deny got=deny` before any worker starts.
- profiles/darwin-check.sh:181 — the sidecar shell check for #69's socket-outside denial (also tests the no-write-outside property).

UNGUARDED: There is no standalone test named "test 25" that runs `rm -rf` of the self path inside the wall and asserts EPERM + byte-identity. The behavior is covered by the wall's deny-default mechanism and by end-to-end tests of write-outside denial, but the specific `rm -rf` + byte-identical assertion of rule 25 has no dedicated guard.

Grep inputs:
- `grep -rn "EPERM" internal/sandbox/` — 1 match (comment in landlock_linux.go:245)
- `grep -rn "self.?path\|rm.?rf\|remove.?self" internal/sandbox/` — 0 matches
- `grep -rn "rule.?25" internal/sandbox/` — 0 matches
- `grep -rn "write_outside" cmd/nova-sandbox/` — found probe step and test assertions
- `sed -n '2485,2531p' docs/SPEC-SANDBOX.md` — read rule 25 and context
- `grep -rn "#69" --include='*.go' .` — tracked #69 references through the codebase
- `ls internal/sandbox/` — read all source files in package
- `grep -rn "SandboxArgv\|SandboxCommand" internal/swarm/sandbox.go` — found the write set construction

Left owed: none (read-only card; no code was changed or created).

git status --short