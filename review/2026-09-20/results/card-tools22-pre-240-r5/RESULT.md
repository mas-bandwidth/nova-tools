RESULT tools22-pre-240-r1 sha=5298f6be12ea — a pre-read of mas-bandwidth/nova-tools#240 at head 269e56d289b1: nova-tokens: attempts joined to work, hot spots, friction records, adoption matrix (SPEC amendm
PREREAD 240 claims=10 proven=1 unproven=9 defects=1 high=0

PR 240
HEAD 269e56d289b1a7728e55b755c6ce3a2115fd290f
BASE dev
MERGE-BASE 8ba256bbbc6ef997f2bad05632aeb27a31f9e219
BEHIND 723
FILES 2 production, 0 test
LINES +2104 -45

The head matches the card's fingerprint (printed 269e56d2…). The merge base
8ba256bb is one of the card's own ancestors and is *older* than the card's
stated base dev@5298f6be12ea, so the reading is of a head whose diff was taken
against an ancestor of the base the card names. BEHIND 723: the head is
seven hundred and twenty-three commits behind origin/dev, so dev has moved
far past this branch since it was cut. All of these caveats are noted; the
diff read is the one the card commands.

This PR changes two documentation files and nothing else: docs/SPEC-TOKENS.md
(+2091/−45, now 4310 lines) and docs/SPEC-UPDATE.md (+13). It is a pure SPEC
amendment introducing rules 32–41 (usage receipts joined to work, cost,
hot, diff, friction, adoption) plus amendments threaded into rules 1–31. The
shipped binary — cmd/nova-tokens/main.go, unchanged by this PR — has verb
cases only for fold, report, sum, check, sources, version: it implements none
of publish, receipt, cost, hot, diff, friction, frictions or the new --rates,
--resume, --until, --top flags. The tree's only demanded-test file,
cmd/nova-tokens/demanded_test.go, contains tests for rules 1 through 21 and
none for rules 22–41. Consequently nearly every behaviour this amendment
claims is a forward spec with no code and no test anywhere in the diff or the
tree.

CLAIMS

1. A usage receipt joins one session's spend to one work node and one stage,
   written under <out>/receipts/ with the caller's `:attempt` pointer
   `note:usage:<receipt-id>`. — UNPROVEN. No `receipt` verb exists in
   cmd/nova-tokens/main.go (`case` list at main.go:128–141), and demanded test
   32 is not in demanded_test.go. A test would go red the day it existed; none
   does.
2. `receipt --resume` counts the messages no earlier receipt of the session
   counted, selecting by retained message id and never by a stamp. —
   UNPROVEN. Same verb and flag are absent from the binary and demanded test
   39 from the tree.
3. An interrupted multi-day receipt completes from its retained `.ids`
   record under the stored id, reading no source, writing the absent days,
   `state=completed`; a stored row it cannot reproduce is `reason=differs` and
   reads no source. — UNPROVEN. demanded test 41 is absent.
4. `cost --node <id>…` reads <out>/receipts/*.tsv, sums every stage, and
   prints per stage/model/receipt with `usd=` and `per_mtok=` when `--rates` is
   given and dashes when it is not. — UNPROVEN. No `cost` verb or `--rates`
   flag in the binary; demanded test 33 absent.
5. `check` gates the receipts files: eighteen columns, `ids_sha256` on every
   row, `.ids` present and hashing, and `DUPLICATE`, `SPLIT`, `EXCEEDS`,
   `ORPHAN` findings. — UNPROVEN. demanded test 34 absent; the shipped
   `check` (`main.go` check output at 1102–1133) has none of it.
6. `hot --session --top N` ranks a session's repeated reads and largest step
   outputs and emits one `HOT NOTE` paragraph. — UNPROVEN. No `hot` verb;
   demanded test 35 absent.
7. `diff` prints rows added, removed and changed between two day files or two
   sessions, largest movement first, capped. — UNPROVEN. No `diff` verb;
   demanded test 36 absent.
8. `friction add` / `frictions` record and sum a stumble per gap, and
   `publish --frictions` is a third typed contribution kind with an append
   transition, `state=appended`. — UNPROVEN. No friction verb and no publish
   verb in the binary; demanded test 37 absent.
9. The adoption matrix lives on `nova-update adoption`, each cell decided from
   bus evidence alone, `--tokens-out` deleted. — UNPROVEN. The verb belongs to
   nova-update (this PR's SPEC-UPDATE note is only the pointer), and its
   demanded test is written for SPEC-UPDATE's list, which carries no tests in
   this PR.
10. The read side is unchanged: fold, report, sum, check and sources run no
    `git`, open no network (rules 16, 19, 27 are only narrowed, not widened,
    for the read verbs). — PROVEN-BY-EXISTING. demanded_test.go covers the
    read verbs' no-git/no-net contract (rules 16, 19) and the run on the fake
    git records nothing; this is intact in the tree and not altered by the
    amendment.

DEFECTS

DEFECT medium docs/SPEC-TOKENS.md:761,772,784 vs cmd/nova-tokens/main.go:666,1038,1133 — the amendment writes new output fields on shipped read-verb lines (TOKENS DAY gains `receipts=`, SUM PAIR/MODEL/TOTAL gain `usd= per_mtok= priced_tokens= unpriced_tokens=`, CHECK OK/FAIL gain `receipts= dup= split= exceeds= orphan=`), but the shipped binary emits none of them and no demanded test in the diff or tree asserts the new grammar — the spec now describes lines the binary does not print and nothing would go red if the claim were false. — it contradicts the spec's own test-decides norm against the very verbs that are shipped, and breaks any tool that parses these machine-scannable lines as written. — land the demanded test (or the code) with the grammar change, or mark the added fields as forward-spec in the syntax so a reader and a parser know they are not yet emitted.

DEFECTS none further claimed; a manufactured defect is worse than a silence.

QUESTIONS

1. Is this SPEC amendment intentionally spec-ahead-of-code — rules 22–41
   describe verbs and flags to be built later, and the existing demanded
   tests 1–21 are the only working ones — or is a companion code PR expected?
   The diff gives no code and no test, so I cannot tell intent from it.
2. The merge base is a real ancestor of the card's stated base but seven
   hundred commits older; does the reviewer want the reading repeated against
   dev@5298f6be12ea, which would show whatever landed on dev between the two?
3. The grammar changes on shipped lines (TOKENS DAY, SUM, CHECK) are written
   without a witness test; is the house convention that grammar text is
   normative even before its test lands, or is an unasserted grammar line an
   error here?
4. Rule 2 (one reader per format) is cited as the reason the adoption cell
   reads the bus alone and `--tokens-out` is deleted; is that the decision
   that binds this amendment, and does it close the earlier drafts' cross-tool
   reads for good?

Left owed — I read docs/SPEC-TOKENS.md at the head in full (all 4310 lines:
the rules, the verbs and their synopses, exit codes, the output grammar, the
day file, the four sources, receipts file, the adoption amendment note, what
it deliberately does not do, and the demanded-tests index and work list), and
the SPEC-UPDATE.md amendment note. I did not read docs/SPEC.md, docs/SPEC.md's
Conventions file, SPEC-WORK, SPEC-BUS-DELIVERY, PROPOSAL-SCHEDULING-COST or the
nova-update SPEC beyond the one appended paragraph, all of which this
amendment reaches into; I judged the claims on the tree's demanded_test.go and
shipped main.go coverage only. No test was run (none expected); no file was
written except this RESULT.md.

git status --short
---
git rev-parse HEAD
d576bf6bbabb39068096a97b4560de9b5e245970
