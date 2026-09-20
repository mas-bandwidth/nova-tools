RESULT work4r3-E11-F05-72 sha=5298f6be12ea — nova-work E11-F05 criterion E11-F05-72 (sexp id E11-F05-03): no-receipt-of-receipt: a worker returns one structured result; a verdict is keyed (reader, sha) and a gate (base, head, integration) in one durable home; an independent review is not re-routed through the coordinator; a receipt of a receipt is refused as a duplicate
DONE
CRITERION E11-F05-03 STATE verified
BRANCH rowan/work4r3-E11-F05-72
REPO mas-bandwidth/nova-tools
PATHS lisp/nova-work/src/replays-verdict-state.lisp lisp/nova-work/src/package.lisp lisp/nova-work/tests/replays-8660.lisp
SPEC docs/SPEC-WORK.md:4557-4560 — "One writer and one durable home per fact: a verdict is keyed (reader, sha), a gate (base, head, integration), ownership on the node; a bus note carries questions, findings and handoffs only, and there is no receipt-of-receipt — a worker returns one structured result, and an independent review does not route through the coordinator to be counted."
SPEC replay docs/SPEC-WORK.md:4650 — "no-receipt-of-receipt | A worker returns one structured result; a verdict is keyed (reader, sha) and a gate (base, head, integration) in one durable home; an independent review is not re-routed through the coordinator; a receipt of a receipt is refused as a duplicate."
TEST TestE11F05NoReceiptOfReceiptA
BASELINE NOVA-WORK SLICE1 total=419 pass=411 fail=8
RED TEST TestE11F05NoReceiptOfReceiptA FAIL spec=docs/SPEC-WORK.md:4557-4560,4650 expected=worker-returns-one-structured-result;verdict-keyed-reader-sha-and-gated-base-head-integration-in-one-durable-home;independent-review-not-rerouted-through-coordinator;receipt-of-receipt-refused-as-a-duplicate: The function NOVA-WORK/TESTS::MAKE-WORKER-RESULT is undefined.
GREEN NOVA-WORK SLICE1 total=420 pass=412 fail=8
GREEN (second run, after negative control revert/pop) NOVA-WORK SLICE1 total=420 pass=412 fail=8
CONTROL NOVA-WORK SLICE1 total=420 pass=411 fail=9
CONTROL FAIL TEST TestE11F05NoReceiptOfReceiptA FAIL spec=docs/SPEC-WORK.md:4557-4560,4650 expected=worker-returns-one-structured-result;verdict-keyed-reader-sha-and-gated-base-head-integration-in-one-durable-home;independent-review-not-rerouted-through-coordinator;receipt-of-receipt-refused-as-a-duplicate: The function NOVA-WORK/TESTS::MAKE-WORKER-RESULT is undefined.
GITSTATUS  M lisp/nova-work/src/package.lisp
  M lisp/nova-work/src/replays-verdict-state.lisp
  M lisp/nova-work/tests/replays-8660.lisp
head 173f7e90bb9a63991d32f347ad9964aa9921f282
Noticed The reply-of-receipt dedup already present in src/receipt-admission.lisp (journal dedup by request id) is a separate, lower layer; this card's criterion is the review/decision-packet layer, represented here as a pure model because, like the other E11-F05/Duty-tier replays, "nothing here is built" (resident review session is outside this slice). The verdict extends rather than replaces the existing verdict-row (replays-verdict-state.lisp) and book-receipt/review-binds-p (assignment.lisp).
Left owed decision-packet-per-item-revision and packet-is-smallest-sufficient (E11-F05-01/02) remain unmeasured; the resident review session that would wire this pure model to a durable store on the node is a later slice.
