RESULT tools22-pre-2140-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2140 at head 5bd40e026cbe: nova-sandbox: prove the WSL2 resolv.conf symlink grant on Darwin (#1737)
PREREAD 2140 claims=4 proven=3 unproven=1 defects=0 high=0
PR 2140, HEAD 5bd40e026cbe4d60601b349dca462c5c42298aff, BASE dev, MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730, BEHIND 1, FILES 2 production, 1 test, LINES +71 -7
1. resolverConfigDirectory returns the directory of the resolved symlink target (not empty) — PROVEN-BY resolv_test.go:38-42 TestWSL2ResolverConfigSymlinkDirectoryIsGranted: asserts dir == want (the resolved target directory).
2. resolverConfigDirectory returns a directory outside the symlink's own parent (the grant is a new directory, not /etc) — PROVEN-BY resolv_test.go:43-44 TestWSL2ResolverConfigSymlinkDirectoryIsGranted: asserts dir != filepath.Dir(link).
3. The path logic is testable on Darwin because resolv.go has no linux build tag — PROVEN-BY-EXISTING: no build tag on either resolv.go or resolv_test.go; compilation on darwin proves portability.
4. The empty/error return cases of resolverConfigDirectory (missing path, unresolvable path, resolves to root) — UNPROVEN: no test exercises resolverConfigDirectory with a missing file, an unresolvable path, or a path that resolves to "/".
DEFECTS none
1. The test creates a symlink-only WSL2 fixture. On filesystems that do not support symlinks the test skips silently — is that acceptable on all CI platforms that build the darwin binary?
2. The existing linux-only TestLinuxWallGrantsTheResolvedResolverConfigDirectory (systemreads_test.go:27-57) already covers linuxRoots as a whole via the resolvConfPath seam — was a second, darwin-capable, isolated path unit test needed beyond that coverage, or is the main value the structural refactor?
3. The empty/error returns of resolverConfigDirectory are documented but untested — is there a reason these edge cases are intentionally omitted (e.g. they are unreachable in practice or covered by other tests)?
Left owed: none

```
git status --short

git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
```===FILE=== card-tools22-pre-2140-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-2140-r1	1	2026-09-20T19:28:12Z	2026-09-20T19:39:25Z	0	openrouter	deepseek/deepseek-v4-flash	35305	4092	0	612096	7081	0.0064
