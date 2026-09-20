RESULT tools22-pre-1999-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1999 at head c5386798e7bb: hygiene: recognise the xai- provider key and the short sk- bound (#1899)
PREREAD 1999 claims=5 proven=4 unproven=1 defects=0 high=0

PR 1999, HEAD c5386798e7bb7969758651aa2ecffd03dda8234f, BASE dev, MERGE-BASE 3666f40487a97447c1632e4bebf2a40585077e4f, BEHIND 5, FILES 1 production, 1 test, LINES +45 -2

1. The hygiene gate recognises the `xai-` provider key prefix as a secret shape. — PROVEN-BY `hygiene_test.go:419-422` `TestHygieneRejectsAnXAIProviderKey`: asserts `has(fs, "secret") != nil` when an `xai-` key appears in an added line.
2. The hygiene gate never prints the matched key value in any finding field. — PROVEN-BY `hygiene_test.go:428-429` `TestHygieneRejectsAnXAIProviderKey`: asserts the key value is absent from the concatenation of all finding fields.
3. The hygiene gate reports the correct source location for an `xai-` key finding. — PROVEN-BY `hygiene_test.go:425` `TestHygieneRejectsAnXAIProviderKey`: asserts `f.At == "sign/sign.go:3"`.
4. A truncated `sk-` key (20 alphanumeric chars after prefix, below the old `{32,}` bound) is caught as a secret finding. — PROVEN-BY `hygiene_test.go:435-437` `TestHygieneRejectsAnXAIProviderKey`: asserts `has(check(...), "secret") != nil` for an `sk-` key with 20 chars after the prefix.
5. The two `keyshapes.txt` files (`internal/hygiene/` and `internal/keyshape/`) are kept in step where they overlap. — UNPROVEN: no test or CI step enforces byte-identity between the two embedded data files; only a comment says they should stay in sync.

DEFECTS none

1. Do xAI API keys contain hyphens in the key body (as `xai-[A-Za-z0-9-]{20,}` allows)? The `sk-` patterns use `[A-Za-z0-9]` without hyphens, so a mismatch would widen the false-positive surface.
2. The `internal/keyshape/keyshapes.txt` comment says "when that gate lands, one of the two loads the other" — is there a card to deduplicate the two embedded copies, or is keeping two files in step by hand the intended long-term state?
3. The short `sk-` test demonstrates the new `{20,}` bound catches a 20-char key, but does not also assert that the old `{32,}` bound would have missed it — is this gap accepted (e.g. because the base was never built against `{32,}`)?

Left owed: read all 2 files in full; nothing unread.

$ git status --short
$ git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970===FILE=== card-tools22-pre-1999-r2/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1999-r2	1	2026-09-20T19:26:49Z	2026-09-20T19:38:56Z	0	openrouter	deepseek/deepseek-v4-flash	183691	9420	0	672000	12306	0.0129
