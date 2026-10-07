# nova-check dogfood, 2026-10-06 (friend)

Tool: nova-check. Build: `nova-check devel linux/amd64 go1.26.6`, from the staged checkout. Read as a stranger from the tool's own help (`nova-check`, `nova-check -h`, `nova-check help`, `nova-check help <verb>`, `nova-check <verb> -h`) and its docs/CLI.md page. Every verb was run at least once against example fixtures or scratch dirs; refusals were exercised. No code was changed.

## Findings

1. `dogfood ledger --tools` expects built binaries, not a directory of example markdown files.

   **Command:**
   ```
   nova-check dogfood ledger --tools example-dogfood-dir --receipts receipts-dir
   ```

   **Printed** (exit 2):
   ```
   DOGFOOD-LEDGER REFUSED: the sources named declare no verbs at all...
   ```

   **Expected:** help says `--tools <dir> directory of built nova-* binaries, each asked for its own verbs`; when passed an example data directory it should declare what's missing (no binaries found) rather than "no verbs declared". Grade: NEXT.

2. Example receipts in example-dogfood/receipts declare `nova-example` tool which is not in CLI.md's verb list.

   **Command:**
   ```
   nova-check dogfood ledger --cli docs/CLI.md --receipts cmd/nova-check/testdata/example-dogfood/receipts
   ```

   **Printed** (exit 0, at end):
   ```
   DOGFOOD NOTE stranded ...: tool=nova-example verb=links ... (nothing close enough to suggest)
   DOGFOOD NOTE stranded ...: tool=nova-example verb=nocode ... (nothing close enough to suggest)
   DOGFOOD NOTE stranded=2 shown=2...
   ```

   **Expected:** either example-dogfood/CLI.md should declare nova-example verbs, or the receipts should be removed/renamed. Grade: NEXT.

3. `hygiene` verb requires a live git repository; example checkout has a worktree .git that points to unavailable mirror.

   **Command:**
   ```
   nova-check hygiene --repo . --base HEAD --head HEAD --identity "Test <test@test.com>"
   ```

   **Printed** (exit 2):
   ```
   HYGIENE REFUSED: base HEAD names no commit in this repo
   ```

   **Expected:** help doesn't warn that the --repo directory must be a working git checkout, not a worktree with broken refs. Grade: NEXT.

4. `floors` check needs both SEED-CORE.md (door) and SEED.md (source); example-self/docs has only SEED-CORE.md.

   **Command:**
   ```
   nova-check floors --core docs/SEED-CORE.md --source docs/SEED.md
   ```

   **Printed** (exit 1):
   ```
   FLOORS FAILED ... findings=3 ...
   ```

   **Expected:** refusal when source file is missing, before attempting comparison. Grade: NEXT.

## Gate

```
go test -count=1 -timeout 600s ./internal/docs -run TestDocsTreeIsConsistent
ok  	github.com/mas-bandwidth/nova-tools/internal/docs	(cached)
go test -count=1 -timeout 600s ./internal/docs ./internal/ci
ok  	github.com/mas-bandwidth/nova-tools/internal/docs	2.524s
ok  	github.com/mas-bandwidth/nova-tools/internal/ci	14.718s
```

The card's named test `TestDocsTreeIsConsistent` exists and passes. The two packages named in STEP 4 are green.

READ 8/10 -- the banner answers what it does, how it works, and how to use it; every verb's `-h` is available; refusals name what the flag wants. Held down by `hygiene` not warning about worktree requirements and `floors` not refusing early on missing source.

USE 7/10 -- most verbs ran against example fixtures and produced actionable output; `quickstart`, `links`, `kernel`, `corpus`, `attest`, `nocode`, `spelling`, `dogfood ledger/gate` all work. `hygiene` needs a real repo; `floors` needs paired seed files; `dogfood --tools` expects binaries not data dirs.

urgent=0 next=4
