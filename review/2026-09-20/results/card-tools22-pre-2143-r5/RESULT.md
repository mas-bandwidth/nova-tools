RESULT tools22-pre-2143-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2143 at head 8b8cdc18ddf7: release adopt: do not trust a partial release dir (#1981)
PREREAD 2143 claims=4 proven=2 unproven=2 defects=0 high=0

PR 2143
HEAD 8b8cdc18ddf73d39b9acff3f424333ce1ace473a
BASE dev
MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730
BEHIND 1
FILES 8 production, 1 test
LINES +262 -25

CLAIMS
1. adopt no longer trusts a partial release directory (one with missing or corrupt artifacts) as complete; it re-streams and verifies via checksum before promoting.
2. "Already holds" messages now print the verified artifact count (e.g. 22/22) instead of just checking for the presence of SHA256SUMS.
3. Streams now land in a `<version>.partial/` staging directory and are only renamed into place after the bench verifies every artifact.
4. adopt does not ask remote machines to compute digests; `sha256sum -c` runs locally on the bench against a copy, not against remote files.

DEFECTS none

QUESTIONS
1. Why is `clearReleaseFilesArgv` needed before the `mv` — would `mv` fail or nest if a leftover final dir exists from a previous failed transfer?
2. Is there any consideration for machines where both `sha256sum` and `shasum` are absent, or is this assumed to be covered by the OS/platform support matrix?
3. The `countVerified` function parses output lines ending with `: OK` — does this hold for localized tool output or machines with unusual locale settings?

Left owed
- No production files were fully read; I relied on the diff to understand behavior.
- `internal/release/adopt.go` and `internal/release/release_test.go` were examined via diff but not read in full.

git status --short
git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
