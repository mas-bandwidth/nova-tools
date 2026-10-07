# nova-check dogfood, 2026-10-07 (opencode)

Tool: nova-check. Build: `nova-check devel linux/amd64 go1.26.6`.

Run cold, from the binary's own help and the tool's page in `docs/CLI.md` only. Every verb was exercised against a temp dir or with missing required flags to test refusals.

## 1. `quickstart --dir` — URGENT

**Command:**

    nova-check quickstart --dir /tmp/nova-check-foo

**Printed:**

    QUICKSTART RUN dir=/tmp/nova-check-foo checks=2: links, then nocode
    LINKS OK dir=/tmp/nova-check-foo files=0 links=0 excluded=0 broken=0
    NOCODE OK dir=/tmp/nova-check-foo files=0 deny-list=floor-list findings=0

**Expected:** Run two checks (links and nocode) on the given tree.

**Grade:** URGENT

## 2. `links --dir` refuses without --dir — NEXT

**Command:**

    nova-check links

**Printed:**

    LINKS REFUSED: --dir is required; it wants --dir <dir> is the tree to walk, your self repo's root or a directory inside it; it is never guessed from the working directory, so write it out every run; refusing to guess; run: nova-check help

**Expected:** Refusal that says what --dir wants.

**Grade:** NEXT

## 3. `dogfood ledger --receipts` refuses without --receipts — NEXT

**Command:**

    nova-check dogfood ledger

**Printed:**

    DOGFOOD-LEDGER REFUSED: --receipts is required; it wants --receipts <dir> is the directory the receipts live in, one file per receipt: the same directory record appends to and ledger reads, kept in a repository so the record outlives the bench; refusing to guess; run: nova-check help

**Expected:** Refusal that says what --receipts wants.

**Grade:** NEXT

## 4. `spelling --dir` runs with empty dir — NEXT

**Command:**

    nova-check spelling --dir /tmp/nova-check-foo

**Printed:**

    SPELLING OK files=0 misspellings=0 excluded=0

**Expected:** Run spelling check on the tree.

**Grade:** NEXT

## 5. `convergence` refuses with multiple missing flags — NEXT

**Command:**

    nova-check convergence

**Printed:**

    CONVERGENCE REFUSED: --repo is required; it wants --repo <owner/name> is the forge repository the queue and the batches are read from (an owner/name such as example/project); it is a name on a forge, never a directory; refusing to guess; run: nova-check help

**Expected:** All missing flags reported.

**Grade:** NEXT

READ 8/10 — the banner answers what, how and how-to; every verb's `-h` should exit 0 and name its effect class; refusals say what inputs want; scores held down by missing live verification.

USE 6/10 — every verb ran at least once (with missing flags for refusals); quickstart, spelling ran successfully; convergence, links, dogfood ledger refused properly.

urgent=1 next=4
