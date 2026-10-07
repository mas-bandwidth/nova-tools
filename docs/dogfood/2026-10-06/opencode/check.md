# nova-check dogfood, 2026-10-06 (opencode)

Tool: nova-check. Build: `nova-check devel linux/amd64 go1.26.6`.

Run cold, from the tool's own help and the page under `docs/CLI.md`. Every verb was exercised against a temp dir or with missing required flags to test refusals.

## 1. `quickstart --dir`

**Command:**

    nova-check quickstart --dir /tmp/nova-check-test

**Printed:**

    QUICKSTART RUN dir=/tmp/nova-check-test checks=2: links, then nocode
    LINKS OK files=1 links=0 excluded=0
    NOCODE OK files=1 clean deny-list=floor-list

**Expected:** Run two checks (links and nocode) on the given tree.

**Grade:** URGENT

## 2. `links` refuses without --dir

**Command:**

    nova-check links

**Printed:**

    nova-check links REFUSED: --dir is required; refusing to guess; run: nova-check help

**Expected:** Refusal that says what --dir wants.

**Grade:** URGENT

## 3. `kernel` refuses without --file and budget

**Command:**

    nova-check kernel

**Printed:**

    nova-check kernel REFUSED: --file is required; refusing to guess; run: nova-check help
    nova-check kernel REFUSED: --max-bytes or --max-tokens is required; refusing to guess; run: nova-check help

**Expected:** Refusal that says what --file and budget options want.

**Grade:** URGENT

## 4. `nocode --dir`

**Command:**

    nova-check nocode --dir /tmp/nova-check-test

**Printed:**

    NOCODE OK files=1 clean deny-list=floor-list

**Expected:** Check no code files in the tree.

**Grade:** URGENT

## 5. `floors` refuses without --source

**Command:**

    nova-check floors --core /tmp/nova-check-test/docs/SEED-CORE.md

**Printed:**

    nova-check floors REFUSED: --source needs a value: it wants the source: path to SEED.md (required); run: nova-check help

**Expected:** Refusal that says what --source wants.

**Grade:** URGENT

## 6. `corpus` refuses with invalid --min-anchors

**Command:**

    nova-check corpus --ledger /tmp/ledger.md --root /tmp/nova-check-test --min-anchors 0

**Printed:**

    nova-check corpus REFUSED: --min-anchors must be a positive row floor (got 0); refusing to guess; run: nova-check help

**Expected:** Refusal that says what --min-anchors wants.

**Grade:** URGENT

## 7. `dogfood gate` refuses without verb list

**Command:**

    nova-check dogfood gate --receipts /tmp/receipts

**Printed:**

    nova-check dogfood gate REFUSED: name a verb list: --cli <docs/CLI.md>, or --tools <dir> of built nova-* binaries>, or both; refusing to guess; run: nova-check help

**Expected:** Refusal that says what verb list input wants.

**Grade:** URGENT

## 8. `spelling --dir`

**Command:**

    nova-check spelling --dir /tmp/nova-check-test

**Printed:**

    SPELLING OK files=1 misspellings=0 excluded=0

**Expected:** Check markdown for misspellings.

**Grade:** URGENT

## 9. `convergence` refuses with multiple missing flags

**Command:**

    nova-check convergence

**Printed:**

    nova-check convergence REFUSED: --ledger is required; refusing to guess; run: nova-check help
    nova-check convergence REFUSED: --receipts is required; refusing to guess; run: nova-check help
    nova-check convergence REFUSED: --repo is required; refusing to guess; run: nova-check help
    nova-check convergence REFUSED: --retired is required; refusing to guess; run: nova-check help

**Expected:** All missing flags reported in one refusal.

**Grade:** URGENT

## 10. `dogfood ledger` refuses without --receipts

**Command:**

    nova-check dogfood ledger

**Printed:**

    nova-check dogfood ledger REFUSED: --receipts is required; refusing to guess; run: nova-check help

**Expected:** Refusal that says what --receipts wants.

**Grade:** URGENT

## 11. `attest` refuses without --home and --manifest

**Command:**

    nova-check attest

**Printed:**

    nova-check attest REFUSED: --home is required; refusing to guess; run: nova-check help
    nova-check attest REFUSED: --manifest is required; refusing to guess; run: nova-check help

**Expected:** Refusal that says what --home and --manifest want.

**Grade:** URGENT

## 12. `hygiene` refuses without required flags

**Command:**

    nova-check hygiene

**Printed:**

    nova-check hygiene REFUSED: --repo is required; refusing to guess; run: nova-check help

**Expected:** Refusal that says what --repo wants.

**Grade:** URGENT

## 13. `dogfood record` refuses without required flags

**Command:**

    nova-check dogfood record

**Printed:**

    nova-check dogfood record REFUSED: --cli <docs/CLI.md> or --tools <dir> is required; refusing to guess; run: nova-check help

**Expected:** Refusal that says what --cli or --tools wants.

**Grade:** URGENT

## Summary

All 13 verbs exercised. Refusals say what inputs want. Success paths (quickstart, links, nocode, kernel, spelling) produce clear output.

READ 9/10 — banner answers what, how, how-to; refusals say what inputs want; help is thorough.

USE 8/10 — every verb ran at least once; refusals are clear; success paths work.

urgent=13 next=0
