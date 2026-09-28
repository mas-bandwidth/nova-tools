;;;; issue-2332.lisp --- dedup index refusal paths for reused requests.
;;;;
;;;; SPEC-WORK.md:329-335,365-368: a retry with a request id the dedup index
;;;; already holds is answered by a two-part test. Where the retry's payload
;;;; digest equals the digest the index recorded for that id, it is refused
;;;; `already applied`, naming the revision it was applied at. Where the digest
;;;; differs, it is refused at exit 1, `reused with a different payload`.
;;;;
;;;; nova-tools#2332: the dedup-root entries did not carry the revision, and no
;;;; lookup function existed for the two-part test.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; issue-2332  SPEC-WORK.md:329-335,365-368
;;; ------------------------------------------------------------------

(deftest "issue-2332" "docs/SPEC-WORK.md:329-335,365-368"
    "expected=same-id-different-payload-refuses-conflict;same-id-same-payload-refuses-already-applied-with-revision"
  ;; Build entries the dedup root carries: two accepted requests at revisions 1
  ;; and 2. The entries carry :rev so the refusal names the revision.
  (let* ((entries (list (list :request "req-1"
                              :payload-sha256 (sha256-hex "payload one")
                              :sequence 1
                              :record-sha256 (sha256-hex "record one")
                              :rev 1)
                        (list :request "req-2"
                              :payload-sha256 (sha256-hex "payload two")
                              :sequence 2
                              :record-sha256 (sha256-hex "record two")
                              :rev 2)))
         ;; The same id with a modified payload: the requester changed its
         ;; payload under an id this session already applied.
         (modified-digest (sha256-hex "MODIFIED payload one")))
    ;; Test 1: same id, different payload → :conflict
    ;; SPEC-WORK.md:334-335: "where the digest differs, it is refused at exit
    ;; 1, <MUTATION> FAIL request=<id>: reused with a different payload"
    (multiple-value-bind (found result rev)
        (nova-work::dedup-root-lookup entries "req-1" modified-digest)
      (check-equal t found "req-1 was not found in the dedup index")
      (check-equal :conflict result
                   "same id with different payload was not refused as conflict")
      (check-equal nil rev "conflict should name no revision"))
    ;; Test 2: same id, same payload → :already-applied with revision
    ;; SPEC-WORK.md:365-368: "a retry whose id the index holds is refused
    ;; `already applied`, naming the revision it was applied at"
    (multiple-value-bind (found result rev)
        (nova-work::dedup-root-lookup entries "req-1" (sha256-hex "payload one"))
      (check-equal t found "req-1 was not found in the dedup index")
      (check-equal :already-applied result
                   "same id with same payload was not refused as already-applied")
      (check-equal 1 rev "already-applied did not name the revision"))
    ;; Test 3: an id not in the index → not found
    (multiple-value-bind (found result rev)
        (nova-work::dedup-root-lookup entries "req-new" (sha256-hex "new payload"))
      (check-equal nil found "a new request id was found in the dedup index")
      (check-equal nil result "a new request id should have no result")
      (check-equal nil rev "a new request id should have no revision"))
    ;; Test 4: the second entry also works
    (multiple-value-bind (found result rev)
        (nova-work::dedup-root-lookup entries "req-2" (sha256-hex "payload two"))
      (check-equal t found "req-2 was not found in the dedup index")
      (check-equal :already-applied result
                   "req-2 with same payload was not refused as already-applied")
      (check-equal 2 rev "already-applied did not name req-2's revision"))))
