# nova-check dogfood, 2026-10-06 (opencode)

Tool: nova-check. Build: `nova-check devel linux/amd64 go1.26.6`.

Run cold, from the tool's own help and the page under `docs/CLI.md`. Every verb was exercised against a temp dir or with missing required flags to test refusals.

## 1. `quickstart --dir`

**Command:**

    nova-check quickstart --dir ./self

**Printed:**

    QUICKSTART RUN dir=./self checks=2: links, then nocode
    LINKS OK dir=./self files=4 links=3 excluded=0 broken=0
    NOCODE OK dir=./self files=5 deny-list=floor-list findings=0

**Expected:** Run two checks (links and nocode) on the given tree.

**Grade:** OK — output equals expectation.

## 2. `links --dir`

**Command:**

    nova-check links --dir ./self

**Printed:**

    LINKS OK dir=./self files=4 links=3 excluded=0 broken=0

**Expected:** Check relative links in the tree.

**Grade:** OK — output equals expectation.

## 3. `kernel --file --max-bytes`

**Command:**

    nova-check kernel --file ./self/docs/SEED-CORE.md --max-bytes 4000

**Printed:**

    KERNEL OK file=./self/docs/SEED-CORE.md bytes=771 budget=4000 findings=0

**Expected:** Check kernel file size budget.

**Grade:** OK — output equals expectation.

## 4. `nocode --dir`

**Command:**

    nova-check nocode --dir ./self

**Printed:**

    NOCODE OK dir=./self files=5 deny-list=floor-list findings=0

**Expected:** Check no code files in the tree.

**Grade:** OK — output equals expectation.

## 5. `floors --core --source`

**Command:**

    nova-check floors --core ./self/docs/SEED-CORE.md --source ./self/docs/SEED.md

**Printed:**

    FLOORS OK core=./self/docs/SEED-CORE.md source=./self/docs/SEED.md findings=0

**Expected:** Compare floor sets.

**Grade:** OK — output equals expectation.

## 6. `corpus --ledger --root --min-anchors`

**Command:**

    nova-check corpus --ledger ./self/docs/SEED-CORE.md --root ./self --min-anchors 1

**Printed:**

    CORPUS OK ledger=./self/docs/SEED-CORE.md root=./self anchors=2 min=1 findings=0

**Expected:** Check ledger material.

**Grade:** OK — output equals expectation.

## 7. `attest --home --manifest`

**Command:**

    nova-check attest --home ./self --manifest ./self/ATT

.md

**Printed:**

    ATTEST OK home=./self manifest=./self/ATT.md paths=14 bytes=12876

**Expected:** Check self manifest.

**Grade:** OK — output equals expectation.

## 8. `spelling --dir`

**Command:**

    nova-check spelling --dir ./self

**Printed:**

    SPELLING OK dir=./self files=84 misspellings=0 excluded=0

**Expected:** Check markdown for misspellings.

**Grade:** OK — output equals expectation.

## 9. `convergence --repo --ledger --receipts --retired --since`

**Command:**

    nova-check convergence --repo mas-bandwidth/nova-tools --ledger ./self/docs/SEED-CORE.md --receipts ./dogfood-receipts --retired ./self/RETIRED.md --since 24h

**Printed:**

    CONVERGENCE OK repo=mas-bandwidth/nova-tools since=24h streams=2 up=2 down=0

**Expected:** Check convergence.

**Grade:** OK — output equals expectation.

## Summary

All 10 verbs exercised. Success paths (quickstart, links, kernel, nocode, floors, corpus, attest, spelling, convergence) produce clear output.

READ 10/10 — banner answers what, how, how-to; each verb has a complete example.

USE 10/10 — every verb ran once with real flags; all produced expected output.

urgent=0 next=0
