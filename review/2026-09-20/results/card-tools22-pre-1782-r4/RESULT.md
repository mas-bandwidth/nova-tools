RESULT tools22-pre-1782-r5 sha=5298f6be12ea
PREREAD 1782 claims=4 proven=4 unproven=0 defects=0 high=0

PR 1782, HEAD 114041e197993a74595f6eb0adba27f9ea8c419f, BASE dev, MERGE-BASE c732de087df4370c62baa5708f5e81c9971e3b55, BEHIND 26, FILES 1 production, 1 test, LINES +100 -0

## CLAIMS

1. The darwin standard check "spotlight-off" provisions a bench only when Spotlight indexing is disabled for both `$HOME` and `$TMPDIR`. — PROVEN-BY `internal/pulse/issue1432_spotlight_test.go:59-86` — e2e test with fake mdutil: "Indexing enabled." produces a DRIFT line, "Indexing and searching disabled." produces an OK line.

2. The check is darwin-only; linux and windows benches never carry it. — PROVEN-BY `internal/pulse/issue1432_spotlight_test.go:44-48` — test asserts `find(FleetStandardChecks(goos, ...), "spotlight-off")` returns false for both "linux" and "windows".

3. The check uses MatchEquals, wants "off", and carries no toolchain Root. — PROVEN-BY `internal/pulse/issue1432_spotlight_test.go:35-42` — test asserts `c.OS`, `c.Match`, `c.Want`, `c.Root` against the returned check.

4. The probe names mdutil, $HOME, and $TMPDIR. — PROVEN-BY `internal/pulse/issue1432_spotlight_test.go:43` — test asserts `strings.Contains(c.Probe, want)` for each of "mdutil", "HOME", "TMPDIR".

## DEFECTS

none

## QUESTIONS

1. When a real macOS bench runs this check with Spotlight enabled and drifts, is there automation that disables Spotlight on the bench, or is it a manual step documented in the provisioning procedure?

2. The check sits between "runner-path" and the toolchain-root comment block in the `all` slice. Does placement matter beyond print order (e.g. does any consumer iterate the slice by index)?

3. The probe falls back to `/tmp` when `TMPDIR` is unset. On macOS `/tmp` is a symlink to `/private/tmp` on the root volume, while the bench's real `TMPDIR` is typically per-user under `/var/folders/`. Is the fallback path intentionally checking the root volume's Spotlight status, or would a Mac bench always have `TMPDIR` set?

## Left owed

Read both files in full (100 lines total). No test file is wider than the diff; no production file beyond the insertion. Nothing owed.

---

git status --short: (nothing to commit, working tree clean)
git rev-parse HEAD: 5298f6be12eaa0f7e6622334d2b6a1eb427649e3===FILE=== card-tools22-pre-1782-r5/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1782-r5	1	2026-09-20T20:26:42Z	2026-09-20T20:33:14Z	0	openrouter	deepseek/deepseek-v4-flash	61980	4901	0	448768	7170	0.0063
