RESULT tools22-pre-2137-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#2137 at head c01e9da5daaf: memindex: guard the bounded heap top-k selector (#2025)
PREREAD 2137 claims=2 proven=2 unproven=0 defects=0 high=0

PR 2137, HEAD c01e9da5daaff5d862e608b2ce3d35ac3e8e9396, BASE dev, MERGE-BASE a7611c8189d33979923064f4d10eb07fb9957730, BEHIND 1, FILES 0 production, 1 test, LINES +28 -0

1. channels.go imports "container/heap" and defines/uses scoreHeap with heap.Init and heap.Fix. PROVEN-BY internal/memindex/topk_test.go:56-71 TestTopKSelectsWithABoundedHeap — asserts the source text contains each of these strings.
2. channels.go does not collect every candidate before selecting k (no naive full-sort pattern). PROVEN-BY internal/memindex/topk_test.go:72-75 TestTopKSelectsWithABoundedHeap — asserts the forbidden `make([]Scored, 0, len(scores))` pattern is absent from the source.

DEFECTS none

1. The test reads `"channels.go"` by relative path; `go test ./internal/memindex/` makes this work, but a test run from a different working directory would fail. Is the relative-path `os.ReadFile` intentional for this guard, or should it use `runtime.Caller` to resolve the path?
2. What commit is `2f3c6e0e` (named in the test comment as the point where the heap selector was lost), and did it ship to a release? This tells the reviewer whether the guard covers a real regression or a speculative one.
3. The test checks for the literal type name `"type scoreHeap"` — if someone renamed the heap type while keeping a heap-based selector, the test would pass the `"container/heap"`, `"heap.Init"`, and `"heap.Fix"` checks but fail the `"type scoreHeap"` check, producing a misleading error. Is that acceptable, or should the non-type-name checks be the sole gate and the type-name check be a soft warning?

Left owed: read the full diff (28 lines of one test file — read in full). No production files changed.

```
$ git status --short
(no output)
$ git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
```===FILE=== card-tools22-pre-2137-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-2137-r1	1	2026-09-20T19:28:11Z	2026-09-20T19:34:49Z	0	openrouter	deepseek/deepseek-v4-flash	22736	3308	0	200192	2881	0.0027
