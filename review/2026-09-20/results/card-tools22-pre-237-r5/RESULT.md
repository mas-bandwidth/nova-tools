RESULT tools22-pre-237-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#237 at head 4753e9c30ba8: nova-release: SPEC draft 1
PREREAD 237 claims=20 proven=0 unproven=20 defects=0 high=0

PR 237, HEAD 4753e9c30ba8bd4f181d2ed011a4b5533972f898, BASE dev, MERGE-BASE 8ba256bbbc6ef997f2bad05632aeb27a31f9e219, BEHIND 723, FILES 1 production, 0 test, LINES +1901 -0

1. `nova-release` is a new standalone binary at `cmd/nova-release/`, standard library only, reusing `internal/merge`'s record and lock code, using `internal/oneline`, `internal/bounded`, `internal/buildinfo` — UNPROVEN
2. `candidate` freeze: checks sha is reachable from lane's base on the wire, writes one immutable record file to `<lane>/releases/<tag>/`, commits and pushes through the compare-and-swap loop under checkout lock, out of `<lane>/outbox-release/` — UNPROVEN
3. `status` exits 0 with no candidate (`candidate=-`, `ready=false`); `verify` is keyed to the cut record, not the candidate — UNPROVEN
4. A re-freeze (second candidate at a different sha) voids the old delta, certify, notes and tree records; all four must be re-made at the new sha before `cut`, and the old files remain in the branch — UNPROVEN
5. `delta` walks first-parent landings between base (wire's `releases/latest` or `--since`) and candidate S; base must be an ancestor of S; refuses when any landing lacks a read — UNPROVEN
6. Two baseline modes recorded in the delta record (`latest`/`named`) and re-checked at `cut` time: mode `latest` moves if a release published between delta and cut; mode `named` is fixed to what the caller typed — UNPROVEN
7. Certification reads workflow runs at S with no status or event filter, paginated; `total_count` compared against runs read AND against the gate's one page of 100; deciding group is runs sharing the maximal `updated_at`, must be uniformly green — UNPROVEN
8. `bump` takes `--policy`, reads the manifest from `--dir`; plan-then-apply; path checks refuse symlinks and `..`; partial application prints `BUMP PARTIAL` lines, `applied=<n>` at exit 1, resume on re-run — UNPROVEN
9. `cut` creates tag and release in one API act with `target_commitish=S`; reads the tag back; releases checkout lock before delivering the cut record — UNPROVEN
10. Every writing verb (`candidate`, `delta`, `certify`, `notes`, `tree`, `cut`) has a `pushed=false` line at exit 1 naming the record's path, with re-run remedy — UNPROVEN
11. Notes require title not equal to the tag, body not generated (`## What's Changed` refused), a recorded read keyed to `(hash, S)` — UNPROVEN
12. `notes approve` requires `--author` != `--who` (self-read refused); `notes hold` allows the two equal — UNPROVEN
13. `verify` reads the cut record, checks tag peel, release body, asset set, checksum, and version probe; prints every `VERIFY FIELD` line even after the first false — UNPROVEN
14. Exit codes: 0 = verb ran and passed, 1 = verb ran and said NO, 2 = verb could not run; host answers split across rows — UNPROVEN
15. Output grammar: first token is verb or check name, second is `OK`/`FAIL`/`REFUSED` or informational; `OK` and informational to stdout, `FAIL` and `REFUSED` to stderr — UNPROVEN
16. Every `FAIL` line carries its verb's counts (`runs=`, `jobs=`, `fields=`, `ok=`, `assets=`, `probed=`, `sites=`, `done=`, `applied=`) — UNPROVEN
17. Records have specific JSON shapes under `<lane>/releases/<tag>/`, each carrying `file` and `who`; never edited or replaced; newest by `at` wins, ties fold toward the refusing verdict — UNPROVEN
18. Tool uses its own outbox `<lane>/outbox-release/` with a self-ignoring `.gitignore` written on first use — UNPROVEN
19. `internal/merge` gains additive fields: `OutboxDir` defaulting to `"outbox"`, configurable commit prefix, configurable lock-waiter tool name — UNPROVEN
20. One line cannot cut: `notes` refuses self-approve (rule 7), `tree` refuses approval by the line that recorded the candidate (rule 13), `cut` refuses with `tree=-` when no valid tree record exists (rule 14) — UNPROVEN

DEFECTS none

QUESTIONS:
1. The spec defers SPEC-MERGE.md changes (`releases/` tracked, `outbox-release/` untracked) to the code PR for work list item 2. Should this PR add that line to SPEC-MERGE.md now, or does the reviewer prefer to land the spec first and the spec edit alongside the code?
2. Rule 3's base derivation uses `releases/latest` by `created_at`. Has this repository ever had a `releases/latest` that pointed to a release on a maintenance branch, making the base not an ancestor of the default branch's candidate?
3. Work list item 11 cites release.yml line numbers from #252's head (`59b85fd6`). Have those files changed since then, making the line numbers stale?
4. The spec is 1901 lines and defines a complete tool design. Is the reviewer comfortable landing this spec before any code exists, or would they prefer to see the first work-list item (records.go) before accepting the spec?

Left owed: all 1901 lines of the diff were read. Nothing owed.

```
$ git status --short
$ git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
```===FILE=== card-tools22-pre-237-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-237-r1	1	2026-09-20T19:28:35Z	2026-09-20T19:41:11Z	0	openrouter	deepseek/deepseek-v4-flash	164378	14089	0	767232	8289	0.0129
