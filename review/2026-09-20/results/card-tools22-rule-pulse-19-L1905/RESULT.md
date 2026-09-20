RESULT tools22-rule-pulse-19 sha=5298f6be12ea — does the code at this base do what docs/SPEC-PULSE.md rule 19 says?
ABSENT
SPEC docs/SPEC-PULSE.md:1905 rule 19
PKG internal/pulse
ASK To satisfy the rule the `width` verb must, when the pool and the queue are both empty, count the in-flight cards and print `PULSE POOL EMPTY in-flight=3` (exit 0), and `harvest` at the same state must print that same line as its last line with no `batch`, padding nothing and inventing no candidate.

Verdict is ABSENT: the `width` verb is unshipped and prints a refusal, and the only "POOL EMPTY" line in the tree is harvest's relaunch, which hardcodes `in-flight=0` and never counts in-flight cards.

Deciding lines:
- cmd/nova-pulse/main.go:305-307 — `case "width":` prints `"nova-pulse %s: not implemented in this card\n"` and returns 2. There is no `func cmdWidth` anywhere (grep `func cmdWidth` -> no files).
- cmd/nova-pulse/main_test.go:43 — `unshipped := map[string]bool{"width": true}`.
- internal/pulse/relaunch.go:31-33 — harvest's relaunch prints `PULSE POOL EMPTY in-flight=0` (literal 0, not the in-flight count the rule demands): `fmt.Fprintf(in.Stdout, "PULSE POOL EMPTY in-flight=0\n")`. It returns 0 before `runLaunchSubprocess`, so it runs no batch, but the count is wrong/hardcoded.

Where I looked (greps run):
- `grep -rn "POOL EMPTY" --include='*.go'` → only relaunch.go:32 and review1818_test.go.
- `grep -rn 'in-flight' --include='*.go'` → only relaunch.go:32 (`in-flight=0`), no `in-flight=3` or in-flight counting.
- `grep -rn '"width"' --include='*.go'` → cmd/nova-pulse/main.go:305 (verb stub), main_test.go:43 (unshipped), status.go:113 (unrelated width cap counter).
- `grep -rn 'func cmdWidth' --include='*.go'` → no files.
- `grep -rn 'padding\|invented' --include='*.go' internal/pulse/` → only an unrelated CSS `padding:4px` in statushtmlpage.go:62; no invented candidate anywhere in the pool/write path.

Left owed: the `width` verb and the in-flight count that fills `in-flight=n`.
