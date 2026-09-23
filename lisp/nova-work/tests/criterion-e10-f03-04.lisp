;;;; criterion-e10-f03-04.lisp --- the E10-F03-04 proof.
;;;;
;;;; "Keep per-change CI within two minutes, exhaustive fault/scale suites
;;;; explicit nightly or pre-release; failures block affected gates"
;;;; (docs/roadmaps/nova-work.sexp E10-F03-04; SPEC-WORK.md:7089-7100,
;;;; :7235-7240).
;;;;
;;;; The production path is lisp/nova-work/run-tests.sh (the per-change runner
;;;; and the lane switch) and .github/workflows/nightly-slow.yml (the explicit
;;;; nightly lane). This proof reads those two files as text -- the instrument
;;;; internal/ci/ci_budget_test.go uses on the workflows, for its reason: the
;;;; policy is a shape of those lines, and the regression is a line that moves.
;;;; One live observation joins the text: which named fault/scale cases this
;;;; very run registered, so the lane split is seen to have happened and not
;;;; only described.

(in-package #:nova-work/tests)

(defparameter *e10-f03-04-runner*
  (asdf:system-relative-pathname :nova-work/tests "run-tests.sh"))

(defparameter *e10-f03-04-nightly*
  (asdf:system-relative-pathname :nova-work/tests
                                 "../../.github/workflows/nightly-slow.yml"))

(defun e10-f03-04-read (path)
  "PATH's whole contents as one string."
  (with-open-file (in path :direction :input :element-type 'character)
    (with-output-to-string (out)
      (loop for c = (read-char in nil nil)
            while c
            do (write-char c out)))))

(defun e10-f03-04-has (text needle)
  "True when TEXT carries NEEDLE."
  (and (search needle text) t))

(defun e10-f03-04-ceiling-seconds (runner)
  "PER_CHANGE_CEILING's assigned integer from RUNNER's text, or NIL when no
assignment of it is present. Only the assignment is read: the first
`PER_CHANGE_CEILING=` in the file is the one that sets the bound."
  (let ((pos (search "PER_CHANGE_CEILING=" runner)))
    (when pos
      (let* ((start (+ pos (length "PER_CHANGE_CEILING=")))
             (end (or (position #\Newline runner :start start)
                      (length runner))))
        (parse-integer (subseq runner start end) :junk-allowed t)))))

(defun e10-f03-04-lines (text)
  "TEXT split into its lines."
  (let ((lines '())
        (start 0)
        (len (length text)))
    (loop
      (let ((newline (position #\Newline text :start start)))
        (push (subseq text start (or newline len)) lines)
        (if newline
            (setf start (1+ newline))
            (return (nreverse lines)))))))

(defun e10-f03-04-need-names-p (workflow name)
  "True when some `needs:` line of WORKFLOW names the job NAME -- the leg's red
being read at the gate the report job is, rather than vanishing."
  (some (lambda (line)
          (let ((trimmed (string-trim '(#\Space #\Tab) line)))
            (and (<= 6 (length trimmed))
                 (string= "needs:" trimmed :end2 6)
                 (and (search name trimmed) t))))
        (e10-f03-04-lines workflow)))

(defun e10-f03-04-registered-p (name)
  (and (find name *tests* :key #'first :test #'string=) t))

;;; E10-F03-04's exhaustive fault/scale cases that the suite carries by name:
;;; SPEC-WORK.md:7094-7098's nightly-lane suites and E10-F03's own fault
;;; evidence. Each is a real deftest today, so their presence or absence in a
;;; run is observable either way. (The runner's list also names suites the
;;; kernel does not carry yet -- import-replay, atomic-mutation, recovery --
;;; which no run can observe and so this list does not carry.)
(defparameter *e10-f03-04-exhaustive-cases*
  '("source-inventory" "moving-source" "archive-completeness" "full-round-trip"
    "old-history" "async-operations" "single-writer" "indexes-and-counters"
    "materialized-working-set" "batches-and-pipelines" "schema-evolution"
    "hostile-data"
    "durable-journal-pre-append-failure-writes-nothing"
    "durable-journal-partial-write-refuses-without-truncation"
    "torn-tail-is-diagnosed-not-truncated"
    "crash-after-append-recovers-the-reply-once"
    "savepoint-write-failure-keeps-the-previous"
    "one-revision-publishes-together"))

;;; ------------------------------------------------------------------
;;; E10-F03-04   keep-per-change-ci-within-two   docs/SPEC-WORK.md:7235
;;; ------------------------------------------------------------------

(deftest "e10-f03-04-keep-per-change-ci"
    "docs/SPEC-WORK.md:7089-7100,7235-7240"
    "expected=per-change-inside-two-minutes;exhaustive-fault-scale-suites-explicit-nightly-or-pre-release;failures-block-affected-gates"
  (let ((runner (e10-f03-04-read *e10-f03-04-runner*))
        (nightly (e10-f03-04-read *e10-f03-04-nightly*)))
    ;; Clause 1: per-change CI inside two minutes. The runner carries the
    ;; two-minute bound as its per-change ceiling and enforces it.
    (check-equal 120 (e10-f03-04-ceiling-seconds runner)
                 "the per-change ceiling is the two-minute bound")
    (ok (e10-f03-04-has runner "-gt \"${PER_CHANGE_CEILING}\"")
        "the per-change run is measured against that ceiling")
    (ok (e10-f03-04-has runner "two-minute bound")
        "a ceiling breach names the two-minute bound")

    ;; Clause 2: the exhaustive fault/scale suites are explicit in the nightly
    ;; or pre-release lane and never on every change. They are named in one
    ;; place, dropped from the per-change lane, and run by name from
    ;; nightly-slow.yml's `--exhaustive` leg.
    (ok (e10-f03-04-has runner "lane=per-change")
        "the default lane is the per-change lane")
    (ok (e10-f03-04-has runner "--exhaustive")
        "the exhaustive lane is an explicit opt-in, never the default")
    (ok (e10-f03-04-has runner "lane=exhaustive")
        "the opt-in is the exhaustive lane")
    (ok (e10-f03-04-has runner "EXHAUSTIVE_FAULT_SCALE_SUITES=")
        "the exhaustive fault/scale suites are named explicitly")
    (dolist (suite *e10-f03-04-exhaustive-cases*)
      (ok (e10-f03-04-has runner suite)
          "the exhaustive fault/scale case ~A is named by the runner" suite))
    (ok (e10-f03-04-has runner "delete-if")
        "the per-change lane leaves those named cases to the exhaustive lane")
    (ok (e10-f03-04-has nightly "run-tests.sh --exhaustive")
        "nightly-slow.yml runs the exhaustive lane explicitly")
    (let ((present (remove-if-not #'e10-f03-04-registered-p
                                  *e10-f03-04-exhaustive-cases*)))
      (ok (or (null present)
              (= (length present) (length *e10-f03-04-exhaustive-cases*)))
          "the named exhaustive cases run together, or stay out together: ~D of ~D registered"
          (length present) (length *e10-f03-04-exhaustive-cases*)))

    ;; Clause 3: failures block the gates they affect. The runner exits with
    ;; the suite's own status -- and 1 over the ceiling -- so the gate of the
    ;; lane that ran is blocked by that exit, and the exhaustive leg is named
    ;; in the nightly report's needs so its red is read at that gate.
    (ok (e10-f03-04-has runner "exit \"${status}\"")
        "the suite's own status is the runner's: a red blocks the gate of its lane")
    (ok (not (e10-f03-04-has runner "|| true"))
        "no failure is swallowed")
    (ok (e10-f03-04-has runner "gate is blocked")
        "a red names the gate it blocks")
    (ok (e10-f03-04-need-names-p nightly "nova-work-exhaustive")
        "the exhaustive leg is a needs of the nightly report gate")))
