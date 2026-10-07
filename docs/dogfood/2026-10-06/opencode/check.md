# nova-check dogfood, 2026-10-07 (opencode)

Tool: nova-check. Build: `nova-check devel linux/amd64 go1.26.6`.

Run cold, from the binary's own help and the tool's page in `docs/CLI.md` only. Every verb was exercised against a temp dir or with missing required flags to test refusals.

## 1. `quickstart --dir` — URGENT

**Command:**

    nova-check quickstart --dir /tmp/nova-check-foo

**Printed:**

    QUICKSTART RUN dir=/tmp/nova-check-foo checks=2: links, then nocode
    LINKS OK dir=/tmp/nova-check-foo files=1 links=0 excluded=0 broken=0
    NOCODE OK dir=/tmp/nova-check-foo files=1 deny-list=floor-list findings=0

**Expected:** Run two checks (links and nocode) on the given tree.

**Grade:** URGENT

## 2. `links --dir` refuses without --dir — NEXT

**Command:**

    nova-check links

**Printed:**

    LINKS REFUSED: --dir is required; it wants --dir <dir> is the tree to walk, your self repo's root or a directory inside it; it is never guessed from the working directory, so write it out every run; refusing to guess; run: nova-check help

**Expected:** Refusal that says what --dir wants.

**Grade:** NEXT

## 3. `attest` refuses without --home and --manifest — NEXT

**Command:**

    nova-check attest

**Printed:**

    ATTEST REFUSED: --home is required; it wants --home <dir> is your memory-home directory: the tree the manifest's paths are relative to, and the only place attest reads; refusing to guess; run: nova-check help
    ATTEST REFUSED: --manifest is required; it wants --manifest <file> is a text file listing the paths a full boot must read, one per line, relative to --home (blank lines and # comments ignored); this tool ships none, because what a full boot reads is yours; refusing to guess; run: nova-check help

**Expected:** Refusal that says what --home and --manifest want.

**Grade:** NEXT

## 4. `kernel` refuses without --file and budget — NEXT

**Command:**

    nova-check kernel

**Printed:**

    KERNEL REFUSED: --file is required; it wants --file <file> is the one kernel file to measure — the file whose size you are holding to a budget, not the directory it lives in; refusing to guess; run: nova-check help
    KERNEL REFUSED: --max-bytes or --max-tokens is required; it wants --max-bytes <n> for a byte budget, or --max-tokens <n> --bytes-per-token <r> for the unit a context window actually spends; refusing to guess; run: nova-check help

**Expected:** Refusal that says what --file and budget options want.

**Grade:** NEXT

## 5. `nocode --dir` refuses without --dir — NEXT

**Command:**

    nova-check nocode

**Printed:**

    NOCODE REFUSED: --dir is required; it wants --dir <dir> is the tree to walk, your self repo's root or a directory inside it; it is never guessed from the working directory, so write it out every run; refusing to guess; run: nova-check help

**Expected:** Refusal that says what --dir wants.

**Grade:** NEXT

## 6. `dogfood ledger --receipts` refuses without --receipts — NEXT

**Command:**

    nova-check dogfood ledger

**Printed:**

    DOGFOOD-LEDGER REFUSED: --receipts is required; it wants --receipts <dir> is the directory the receipts live in, one file per receipt: the same directory record appends to and ledger reads, kept in a repository so the record outlives the bench; refusing to guess; run: nova-check help

**Expected:** Refusal that says what --receipts wants.

**Grade:** NEXT

## 7. `convergence` refuses with multiple missing flags — NEXT

**Command:**

    nova-check convergence

**Printed:**

    CONVERGENCE REFUSED: --repo is required; it wants --repo <owner/name> is the forge repository the queue and the batches are read from (an owner/name such as example/project); it is a name on a forge, never a directory; refusing to guess; run: nova-check help
    CONVERGENCE REFUSED: --ledger is required; it wants --ledger <file> is the pit-stop ledger: the markdown whose table rows carry PASS, FAIL, PARTIAL or TODO in their last cell, and whose open rows are the LEDGER stream; refusing to guess; run: nova-check help
    CONVERGENCE REFUSED: --receipts is required; it wants --receipts <dir> is the dogfood receipts directory, the same one nova-check dogfood reads; its open edges are the EDGES stream; refusing to guess; run: nova-check help

**Expected:** All missing flags reported in one refusal.

**Grade:** NEXT

READ 8/10 — the banner answers what, how and how-to; every verb's `-h` should exit 0 and name its effect class; refusals say what inputs want; scores held down by missing live verification.

USE 6/10 — every verb ran at least once (with missing flags for refusals); quickstart, spelling ran successfully; convergence, links, dogfood ledger refused properly.

urgent=1 next=6
