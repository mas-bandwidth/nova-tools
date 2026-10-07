# Dogfood Report: nova-version

Worker: Freddy (inception/mercury-2.5)
Date: 2026-10-06

## Summary
Record of findings from using nova-version as a stranger would (help and docs only).

## Findings

### 1. nova-version (base command)
**Command:** nova-version -h
**Output:** (first 3 lines)
```
nova-version: which version of each tool is installed, recorded and compared

how it works: report reads each tool's installed version; snapshot records a directory's binaries;
```
**Expected:** Clear overview of what the tool does
**Grade:** READ 8/10, USE 7/10 - Help is concise and explains the main verbs well.

### 2. nova-version example
**Command:** nova-version example --out /tmp/versions.tsv
**Output:** (first 3 lines)
```
EXAMPLE OK wrote=/tmp/versions.tsv entries=1 unchanged=false
EXAMPLE NOTE next: nova-version report --file /tmp/versions.tsv
```
**Expected:** Creates an example manifest file
**Grade:** READ 9/10, USE 9/10 - Good onboarding step, tells you what to do next.

### 3. nova-version report
**Command:** nova-version report --file /tmp/versions.tsv
**Output:** (first 3 lines)
```
REPORT OK checked=1 known=1 unknown=0 changed=- sent=- took=23ms file=/tmp/versions.tsv host=- as=- entries=1 kinds=tool at=2026-10-07T00:06:45Z timeout=5s budget=1m0s max=20 snapshot=-
REPORT TOOL name=go kind=tool version=1.27.1 raw=go\x20version\x20go1.27.1\x20darwin/arm64 path=/opt/homebrew/bin/go
```
**Expected:** Reports tool versions from manifest
**Grade:** READ 8/10, USE 8/10 - Works well, output is verbose but informative.

### 4. nova-version snapshot
**Command:** nova-version snapshot --bin /tmp/nova-bin --out /tmp/snapshot.tsv
**Output:** (first 3 lines)
```
SNAPSHOT OK bin=/tmp/nova-bin out=/tmp/snapshot.tsv tools=1 stamp=v1.2.0-dev.7a1152a
SNAPSHOT ROW name=nova-version stamp=v1.2.0-dev.7a1152a revision=- platform=darwin/arm64
```
**Expected:** Captures binaries in a directory
**Grade:** READ 9/10, USE 9/10 - Works as documented.

### 5. nova-version snapshot (failure case)
**Command:** nova-version snapshot --bin /tmp --out /tmp/snapshot.tsv
**Output:**
```
SNAPSHOT REFUSED: cannot read nova-bus.md version (not_found) (repair the build there: go build ./cmd/nova-bus.md); run: nova-version help
```
**Expected:** Either succeed or give clearer error about why it needs nova-bus.md
**Grade:** READ 6/10, USE 5/10 - The error message is cryptic and doesn't explain why /tmp can't be used.

### 6. nova-version diff
**Command:** nova-version diff --from /tmp/snapshot1.tsv --to /tmp/snapshot.tsv
**Output:** (first 3 lines)
```
DIFF OK from=/tmp/snapshot1.tsv to=/tmp/snapshot.tsv tools=1 changed=0
```
**Expected:** Compares two snapshots
**Grade:** READ 9/10, USE 9/10 - Simple and works well.

### 7. nova-version moved
**Command:** nova-version moved --from HEAD~1 --to HEAD --repo <repo> --out /tmp/moved.tsv
**Output:** (first 3 lines)
```
MOVED OK from=fdee3091571a039598fde076c77c718b3a1921a9 to=3e680dfd4d8d029aa3d1d63674d479cc553f3bb1 added=0 deleted=0 renamed=0 verbs=303 file=/tmp/moved.tsv
```
**Expected:** Shows binary changes between commits
**Grade:** READ 8/10, USE 8/10 - Useful output, could use better formatting.

### 8. nova-version send (failure case)
**Command:** nova-version send --file /tmp/versions.tsv --as freddy --to tester
**Output:**
```
SEND FAILED checked=1 known=1 unknown=0 changed=- sent=uncertain took=34ms
...
SEND NOTE send not confirmed: exit 2; the bus said: SEND REFUSED: --redis is required: NOVA_BUS_REDIS is unset...
```
**Expected:** Either work or give clearer guidance on setup
**Grade:** READ 7/10, USE 6/10 - Error message is long and contains lots of implementation detail.

## Summary

READ 8/10 - Help is clear and well-structured overall. Some errors could be more user-friendly.

USE 7/10 - Core functionality works well, but failure modes (snapshot refusing without explanation, send requiring hidden env vars) could be more transparent.

urgent=0 next=2
