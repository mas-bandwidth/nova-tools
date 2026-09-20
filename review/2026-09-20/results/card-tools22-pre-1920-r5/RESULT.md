RESULT tools22-pre-1920-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1920 at head 0d984668bde5: nova-tokens: the help examples run as pasted (#1455)
PREREAD 1920 claims=3 proven=3 unproven=0 defects=0 high=0

PR 1920, HEAD 0d984668bde5ef5b66a10a88068d97e2bd29edbf, BASE dev, MERGE-BASE 485050e30543e816f4adcc6328fe717bcd1f1248, BEHIND 16, FILES 1 production, 1 test, LINES +182 -0

1. The usage banner carries a fixture setup line that a stranger pasting the example block would execute first. PROVEN-BY examplelines_test.go:46 — fixtureSetupLine returns non-empty in TestHelpExampleLinesRunAsPrinted.
2. The fixture setup line exits 0 when run from a checkout root. PROVEN-BY examplelines_test.go:60 — TestHelpExampleLinesRunAsPrinted asserts exit == 0 for the setup line.
3. Every command in the usage banner's example blocks, as printed, exits 0 when pasted after the setup line. PROVEN-BY examplelines_test.go:67 — TestHelpExampleLinesRunAsPrinted loop asserts each example exit == 0.

DEFECTS none

1. The session example runs after fold writes to ./out/2026-09-11.tokens and also writes to ./out — does the session merge into the day file the fold just wrote, or overwrite it? The test only checks exit 0, not the content of ./out.
2. The fixture window.jsonl carries `msg_a1` twice (a duplicate by id) — was this intentional, and would a reader who pastes the setup line see a duplicate-count line in fold's output?
3. The setup line includes `cp ./transcripts/window.jsonl ./session.jsonl` — is `session.jsonl` the canonical filename a real user would have, or only a test convenience?

Left owed: nothing — the diff is 2 files, both read in full.

git status --short: (no output — clean)
git rev-parse HEAD: d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1920-r1/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1920-r1	1	2026-09-20T19:25:16Z	2026-09-20T19:34:50Z	0	openrouter	deepseek/deepseek-v4-flash	96898	4144	0	814336	13742	0.0105
