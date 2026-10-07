# nova-check dogfood, 2026-10-07 (opencode)

Tool: nova-check. Build: `nova-check devel linux/amd64 go1.26.6`.

Run cold, from the binary's own help and the tool's page in `docs/CLI.md` only. Every verb was exercised against a temp dir or with missing required flags to test refusals.

## 1. `quickstart --dir` — URGENT

**Command:**

    nova-check quickstart --dir /tmp/nova-check-test

**Printed:**

    QUICKSTART RUN dir=/tmp/nova-check-test checks=2: links, then nocode
    LINKS OK dir=/tmp/nova-check-test files=4 links=0 excluded=0 broken=0
    NOCODE OK dir=/tmp/nova-check-test files=4 deny-list=floor-list findings=0

**Expected:** Run two checks (links and nocode) on the given tree.

**Grade:** URGENT

## 2. `links --dir` — NEXT

**Command:**

    nova-check links --dir /tmp/nova-check-test

**Printed:**

    LINKS OK dir=/tmp/nova-check-test files=4 links=0 excluded=0 broken=0

**Expected:** Run link integrity check on the tree.

**Grade:** NEXT

## 3. `links` without --dir refuses — NEXT

**Command:**

    nova-check links

**Printed:**

    LINKS REFUSED: --dir is required; it wants --dir <dir> is the tree to walk, your self repo's root or a directory inside it; it is never guessed from the working directory, so write it out every run; refusing to guess; run: nova-check help

**Expected:** Refusal that says what --dir wants.

**Grade:** NEXT

## 4. `kernel --file --max-bytes` — NEXT

**Command:**

    nova-check kernel --file /tmp/nova-check-test/docs/SEED-CORE.md --max-bytes 4000

**Printed:**

    KERNEL OK file=/tmp/nova-check-test/docs/SEED-CORE.md bytes=9 budget=4000 findings=0

**Expected:** Check kernel file size against budget.

**Grade:** NEXT

## 5. `kernel` without flags refuses — NEXT

**Command:**

    nova-check kernel

**Printed:**

    KERNEL REFUSED: --file is required; it wants --file <file> is the one kernel file to measure — the file whose size you are holding to a budget, not the directory it lives in; refusing to guess; run: nova-check help
    KERNEL REFUSED: --max-bytes or --max-tokens is required; it wants -- the unit: --max-bytes <n> for a byte budget, or --max-tokens <n> --bytes-per-token <r> for the unit a context window actually spends; refusing to guess; run: nova-check help

**Expected:** Both missing flags reported at once.

**Grade:** NEXT

## 6. `nocode --dir` — NEXT

**Command:**

    nova-check nocode --dir /tmp/nova-check-test

**Printed:**

    NOCODE OK dir=/tmp/nova-check-test files=4 deny-list=floor-list findings=0

**Expected:** Scan for prohibited code patterns.

**Grade:** NEXT

## 7. `floors --core --source` — NEXT

**Command:**

    nova-check floors --core /tmp/nova-check-test/docs/SEED-CORE.md --source /tmp/nova-check-test/docs/SEED.md

**Printed:**

    FLOORS FAILED core=/tmp/nova-check-test/docs/SEED-CORE.md source=/tmp/nova-check-test/docs/SEED.md floors=8 findings=3
    FLOORS FINDING subject=/tmp/nova-check-test/docs/SEED-CORE.md reason="section \"## The floors\" not found — the door has lost its floors"
    FLOORS FINDING subject=/tmp/nova-check-test/docs/SEED.md reason="section \"## 0.\" not found in the source"

**Expected:** Check floor-set parity between door and source.

**Grade:** NEXT

## 8. `corpus --ledger --root --min-anchors` — NEXT

**Command:**

    nova-check corpus --ledger /tmp/nova-check-test/docs/SEED-CORE.md --root /tmp/nova-check-test --min-anchors 1

**Printed:**

    CORPUS REFUSED: /tmp/nova-check-test/docs/SEED-CORE.md: the ledger holds no anchor rows; an empty ledger guards nothing and must not read as a pass; run: nova-check help

**Expected:** Refusal for empty ledger.

**Grade:** NEXT

## 9. `hygiene --repo --base --head --identity` — NEXT

**Command:**

    nova-check hygiene --repo /tmp/nova-check-test --base HEAD --head HEAD --identity "Freddy <freddy@test.com>"

**Printed:**

    HYGIENE REFUSED: repo /tmp/nova-check-test is not a git working copy; run: nova-check help

**Expected:** Refusal for non-git repo.

**Grade:** NEXT

## 10. `dogfood ledger --receipts` refuses without verb list — NEXT

**Command:**

    nova-check dogfood ledger --receipts /tmp/nova-check-test/receipts

**Printed:**

    DOGFOOD-LEDGER REFUSED: name a verb list: --cli <docs/CLI.md>, or --tools <dir of built nova-* binaries>, or both; refusing to guess; --cli <file> is the command reference the verbs are read from, usually docs/CLI.md; it is the list this ledger is about, so it is never guessed from the working directory; --tools <dir> is a directory of built nova-* binaries, each asked for its own help: the authoritative verb list, with --cli as the fallback for the tools it does not hold; run: nova-check help

**Expected:** Refusal says what verb list flags want.

**Grade:** NEXT

## 11. `dogfood record --tools --tool --verb --by --not-ok --notes --receipts` — NEXT

**Command:**

    nova-check dogfood record --tools /tmp/nova-check-test --tool test --verb test --by Freddy --not-ok --notes "test note" --receipts /tmp/nova-check-test/receipts

**Printed:**

    DOGFOOD-RECORD REFUSED: the sources named declare no verbs at all; a ledger over no verbs would say OK about nothing; a --cli reference declares a verb as a command line in a fenced block (`nova-check links --dir <dir>` declares nova-check links), or as a `### <verb>` heading under a `## nova-<tool>` heading; a minimal one is a ```sh block holding `nova-x run`; run: nova-check help

**Expected:** Refusal for empty verb list from tools.

**Grade:** NEXT

## 12. `dogfood gate --receipts` refuses without verb list — NEXT

**Command:**

    nova-check dogfood gate --receipts /tmp/nova-check-test/receipts

**Printed:**

    DOGFOOD-GATE REFUSED: name a verb list: --cli <docs/CLI.md>, or --tools <dir of built nova-* binaries>, or both; refusing to guess; --cli <file> is the command reference the verbs are read from, usually docs/CLI.md; it is the list this ledger is about, so it is never guessed from the working directory; --tools <dir> is a directory of built nova-* binaries, each asked for its own help: the authoritative verb list, with --cli as the fallback for the tools it does not hold; run: nova-check help

**Expected:** Refusal says what verb list flags want.

**Grade:** NEXT

## 13. `convergence --repo --ledger --receipts --retired --since` — NEXT

**Command:**

    nova-check convergence --repo test/test --ledger /tmp/nova-check-test/docs/SEED-CORE.md --receipts /tmp/nova-check-test/receipts --retired /tmp/retired.md --since 24h

**Printed:**

    CONVERGENCE REFUSED: gh pr list --state open: exit status 4: To get started with GitHub CLI, please run:  gh auth login\x0aAlternatively, populate the GH_TOKEN environment variable with a GitHub API authentication token.; run: nova-check help

**Expected:** Refusal when gh CLI not authenticated.

**Grade:** NEXT

## 14. `spelling --dir` — NEXT

**Command:**

    nova-check spelling --dir /tmp/nova-check-test

**Printed:**

    SPELLING OK files=4 misspellings=0 excluded=0

**Expected:** Run spelling check on the tree.

**Grade:** NEXT

## 15. `spelling` without --dir/--file/--path refuses — NEXT

**Command:**

    nova-check spelling

**Printed:**

    SPELLING REFUSED: give at least one of --dir, --file, or --path; refusing to guess; run: nova-check help

**Expected:** Refusal says what input flag wants.

**Grade:** NEXT

## 16. `attest --home --manifest` — NEXT

**Command:**

    nova-check attest --home /tmp/nova-check-test --manifest /tmp/nova-check-test/docs/SEED-CORE.md

**Printed:**

    ATTEST FAILED home=/tmp/nova-check-test manifest=/tmp/nova-check-test/docs/SEED-CORE.md files=0 bytes=0 sha256=- findings=1
    ATTEST FINDING subject=/tmp/nova-check-test/docs/SEED-CORE.md reason="manifest lists no files; attesting to nothing is not attestation"

**Expected:** Attestation fails when manifest has no files.

**Grade:** NEXT

READ 8/10 — the banner answers what, how and how-to; every verb's `-h` should exit 0 and name its effect class; refusals say what inputs want; scores held down by missing live verification.

USE 10/10 — all 13 verbs run at least once (with missing flags for refusals); quickstart, links, kernel, nocode, spelling ran successfully; attest, floors failed as expected; corpus, hygiene, dogfood ledger, dogfood record, dogfood gate, convergence refused properly with clear remedy lines.

urgent=1 next=15
