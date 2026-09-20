RESULT tools22-pre-1857-r2 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#1857 at head 217b26357233: SPEC-WAKE: the four awake world-refusals carry the door too (#1451, from the #1753 read)
PREREAD 1857 claims=4 proven=4 unproven=0 defects=1 high=0

PR 1857
HEAD 217b26357233bc0437733ef938a820fd95439a97
BASE dev
MERGE-BASE 0878987f9d44eb8c7a9ad01a2180b949d46476fe
BEHIND 22
FILES 1 production, 1 test
LINES +88 -1

CLAIMS

1. "Every one of the four awake world-refusals — no `--bus`, a `--bus` that is not a git repository, a `--window` that is not positive, a negative `--max` — ends in the door `; run: nova-wake help`, and SPEC-WAKE now says so."
   PROVEN-BY internal/docs/specwake_awake_door_test.go:41 TestSpecWakeSaysTheAwakeWorldRefusalsCarryTheDoor — reads the door literal out of `func awakeRefused`, the one printer every AWAKE REFUSED exit goes through, and requires the SPEC-WAKE paragraph to carry it; red if the code or the prose loses the door. Also PROVEN-BY-EXISTING cmd/nova-wake/issue1451_test.go:62 TestIssue1451EveryRefusalNamesTheDoor — runs bare `awake` (the no--bus refusal) and requires every refusal line to end with the door.
2. "The awake grammar line `AWAKE REFUSED <reason>` is unchanged — the door sits inside `<reason>` rather than in the grammar."
   PROVEN-BY internal/docs/specwake_awake_door_test.go:52 — asserts SPEC-WAKE still carries the exact line `AWAKE REFUSED <reason>`.
3. "The new prose claims the door only for the awake shape, not for watch's `WAKE REFUSED`, which `refused()` still prints without a door."
   PROVEN-BY internal/docs/specwake_awake_door_test.go:46 — the control strips `AWAKE REFUSED` from the paragraph and fails if a `WAKE REFUSED` claim remains.
4. "The pin follows the code: the door literal is read out of `func awakeRefused` in the source, not copied."
   PROVEN-BY internal/docs/specwake_awake_door_test.go:64-83 awakeDoorLiteral — extracts `; run: nova-wake ` from the function body up to the format string's `\n`, so a wording change in the code moves the pin with it.

DEFECTS

DEFECT low docs/SPEC-WAKE.md:541 — The inserted sentence joins its clauses with ASCII double hyphens (`--`) where the rest of the spec uses the em-dash `—` (552 occurrences in this file). — In a document that is otherwise punctuation-consistent, the hyphen pair reads like the `--flag` prefix used throughout the grammar, so the sentence is easy to misparse. — Write `—` in place of the two `--` pairs (e.g. `the door — `; run: nova-wake help` —`).

QUESTIONS

1. The door behaviour was already on dev at the merge base: `awakeRefused` has carried `; run: nova-wake help` since #1451, and issue1451_test.go pins it end-to-end. This PR touches only SPEC-WAKE prose and adds a spec pin. Is #1857 intended purely as the docs/test follow-up from the #1753 read, with the behaviour itself out of scope?
2. SPEC-WAKE enumerates exactly four awake world-refusals, but `cmdAwake` has two more AWAKE REFUSED exits — the "not a directory" stat failure and the laneNames error (cmd/nova-wake/main.go:368 and :376) — which also end in the door. Is the four-item enumeration deliberately canonical, and should the door sentence have said "every refusal" instead of "those four"?
3. `awakeDoorLiteral` (internal/docs/specwake_awake_door_test.go:74) takes the first `; run: nova-wake ` between `func awakeRefused(` and the first `\n}\n`. If the function ever grows an earlier occurrence (a comment, a nested block), the pin follows the wrong literal — in the worst case passing while the real format string lost its door. Is that brittleness acceptable for a spec pin, or should the pin anchor the literal after the `AWAKE REFUSED %s` verb?

Left owed

Both changed files read in full. Context read: the SPEC-WAKE.md awake grammar block and `awakeRefused`/`refuse`/`refused` plus all six awakeRefused call sites in cmd/nova-wake/main.go (the door lives in the single shared function, so per-site reading was not needed); the existing pins issue1451_test.go and awake_test.go:120-140 that witness the behaviour. The rest of main.go (1817 lines) was not read. I ran `go test ./internal/docs/ -run TestSpecWakeSaysTheAwakeWorldRefusalsCarryTheDoor` against the PR head: green.

```
$ git status --short
(empty)
$ git rev-parse HEAD
217b26357233bc0437733ef938a820fd95439a97
```===FILE=== card-tools22-pre-1857-r2/usage.tsv
job	attempt	started	ended	rc	provider	model	tokens_in	tokens_out	cache_write	cache_read	reasoning	usd
card-tools22-pre-1857-r2	1	2026-09-20T19:23:52Z	2026-09-20T19:27:57Z	0	opencode	deepseek-v4-flash	27007	26229	0	613888	0	0.0283
