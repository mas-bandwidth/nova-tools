RESULT work4m-E11-F05-72 sha=5298f6be12ea — nova-work E11-F05 criterion E11-F05-72 (sexp id E11-F05-03): no-receipt-of-receipt: a worker returns one structured result; a verdict is keyed (reader, sha) and a gate (base, head, integration) in one durable home; an independent review is not re-routed through the coordinator; a receipt of a receipt is refused as a duplicate
DONE
CRITERION E11-F05-03 STATE verified
BRANCH rowan/work4m-E11-F05-72
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/src/receipt-admission.lisp lisp/nova-work/tests/replays-8660.lisp
SPEC docs/SPEC-WORK.md:4650 "| no-receipt-of-receipt | A worker returns one structured result; a verdict is keyed (reader, sha) and a gate (base, head, integration) in one durable home; an independent review is not re-routed through the coordinator; a receipt of a receipt is refused as a duplicate. |"
TEST TestE11F05NoReceiptOfReceiptA
BASELINE NOVA-WORK SLICE1 total=419 pass=411 fail=8
RED TEST TestE11F05NoReceiptOfReceiptA FAIL spec=docs/SPEC-WORK.md:4650 expected=worker-returns-one-structured-result;receipt-of-a-receipt-refused-as-a-duplicate: a receipt of a receipt was admitted, not refused: expected NIL got T
GREEN NOVA-WORK SLICE1 total=420 pass=412 fail=8
CONTROL NOVA-WORK SLICE1 total=420 pass=411 fail=9 — TEST TestE11F05NoReceiptOfReceiptA FAIL spec=docs/SPEC-WORK.md:4650 expected=worker-returns-one-structured-result;receipt-of-a-receipt-refused-as-a-duplicate: a receipt of a receipt was admitted, not refused: expected NIL got T
STATUS M  lisp/nova-work/src/receipt-admission.lisp
STATUS M  lisp/nova-work/tests/replays-8660.lisp
head 7109378f9cc920cd0dd6957b05eb430c0e8feefa
Noticed The E11-F05 (decision packets) feature is broader than this one criterion; the other two subfeatures (decision-packet-per-item-revision, packet-is-smallest-sufficient) are separately owed and not addressed here. The nine acceptance replays at SPEC-WORK.md:4657 still record E11-F05 state as missing; the roadmap sexp row is not updated (not part of this card's sources).
Left owed The remaining E11-F05 subfeatures (decision-packet-per-item-revision, packet-is-smallest-sufficient) and the E11-F06 subfeatures, plus a roadmaps/nova-work.sexp state update for E11-F05 from missing to verified.
