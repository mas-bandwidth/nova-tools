RESULT tools22-rule-sandbox-3-L1000 sha=5298f6be12ea — does the code at this base do what docs/SPEC-SANDBOX.md rule 3 says?
CONFORMS cmd/nova-sandbox/worktree.go:202
SPEC docs/SPEC-SANDBOX.md:1000 rule 3
PKG cmd/nova-sandbox
ASK When a second call finds a record file but the worktree directory it names is gone, the implementation must rebuild the tree with exactly one `git worktree add` call.
Deciding lines:
- `cmd/nova-sandbox/worktree.go:218-224`: `pathIsWorktree(path)` returns false for a deleted directory, so the code falls through the reuse branch and proceeds to rebuild.
- `cmd/nova-sandbox/worktree.go:230`: `addWorktree(f.repo, path, head)` — calls `git worktree add --detach`, which is exactly one call.
- `cmd/nova-sandbox/worktree.go:282`: the single `git worktree add` inside `addWorktree`.
GUARDED-BY cmd/nova-sandbox/worktree_test.go:249 TestWorktreeDeletedTreeIsRebuilt
Grep searches:
- `grep -rn "worktree" --include='*.go' .` in `internal/sandbox/` — no matches.
- `grep -rn "worktree" --include='*.go' repo/cmd/nova-sandbox/` — found `worktree.go` and `worktree_test.go`.
- `grep -rn "worktree" --include='*.go' repo/` — broad search to locate the `worktree` verb.
Left owed