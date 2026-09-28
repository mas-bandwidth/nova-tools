;;;; replays-8649.lisp --- the pure kernel the five replays of card 8649 need:
;;;; the roadmap proof (docs/SPEC-WORK.md:6244), one-journal rotation (:5998),
;;;; rule 2's unavailable partition (:5024/:5633), the savepoint's cut
;;;; (:5989/:6312) and its write-failure retention (:6014/:6326).
;;;;
;;;; As with replays-applicable-delegation.lisp, this is the pure part: no
;;;; session, no CLI, no socket, no filesystem. The live wiring a later slice
;;;; owes is named in RESULT.md.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; roadmap-proof (docs/SPEC-WORK.md:6244)
;;; ------------------------------------------------------------------

(defstruct (roadmap-row
             (:constructor make-roadmap-row (&key id axis kind state evidence status)))
  id axis kind state evidence status)

(defstruct (roadmap
             (:constructor make-roadmap
                 (&key id axes rows shared-prerequisites discovered closed)))
  id axes rows shared-prerequisites discovered closed)

(defun roadmap-name (x)
  "A canonical lowercase name: keywords print downcased, strings verbatim."
  (if (symbolp x) (string-downcase (symbol-name x)) (princ-to-string x)))

(defun roadmap-row-line (row)
  (format nil "row id=~A axis=~A kind=~A state=~A evidence=~A status=~A"
          (roadmap-row-id row)
          (roadmap-name (roadmap-row-axis row))
          (roadmap-name (roadmap-row-kind row))
          (roadmap-name (roadmap-row-state row))
          (roadmap-name (roadmap-row-evidence row))
          (roadmap-name (roadmap-row-status row))))

(defun roadmap-render (roadmap &optional (style :chat))
  "One canonical render of the whole table. STYLE names the surface the render
is written to and changes no byte, so the chat and file renders are identical
(docs/SPEC-WORK.md:6244)."
  (declare (ignore style))
  (with-output-to-string (s)
    (format s "ROADMAP id=~A~%" (roadmap-id roadmap))
    (dolist (axis (roadmap-axes roadmap))
      (format s "axis=~A~%" (roadmap-name axis)))
    (dolist (row (roadmap-rows roadmap))
      (write-string (roadmap-row-line row) s)
      (write-char #\Newline s))
    (dolist (p (roadmap-shared-prerequisites roadmap))
      (format s "shared-prerequisite ~A~%" p))
    (dolist (d (roadmap-discovered roadmap))
      (format s "discovered ~A~%" d))
    (dolist (c (roadmap-closed roadmap))
      (format s "closed ~A~%" c))))

(defun count-occurrences (needle haystack)
  (if (or (null needle) (string= "" needle))
      0
      (loop with n = 0
            with start = 0
            for pos = (search needle haystack :start2 start)
            while pos
            do (incf n)
               (setf start (+ pos (length needle)))
            finally (return n))))

(defun roadmap-edit-marker (text marker replacement)
  "Replace the single occurrence of MARKER and preserve every unrelated byte; an
absent or ambiguous marker refuses and publishes nothing
(docs/SPEC-WORK.md:6244)."
  (let ((n (count-occurrences marker text)))
    (cond
      ((zerop n) (values nil (format nil "marker not found: ~S" marker)))
      ((> n 1) (values nil (format nil "ambiguous marker: ~S appears ~D times" marker n)))
      (t (let ((pos (search marker text)))
           (values (concatenate 'string
                                (subseq text 0 pos)
                                replacement
                                (subseq text (+ pos (length marker))))
                   nil))))))

;;; ------------------------------------------------------------------
;;; rotation-keeps-one-journal (docs/SPEC-WORK.md:5998, :6312, :6338)
;;; ------------------------------------------------------------------

(defstruct (journal-record (:constructor make-journal-record (&key (seq 1) (events '()))))
  seq events)

(defun journal-record-hash (record)
  "The verified identity of one journal record: the SHA-256 of its canonical
bytes (docs/SPEC-WORK.md:6296)."
  (sha256-hex (canonical-string (list :seq (journal-record-seq record)
                                      :events (journal-record-events record)))))

(defstruct (journal-segment
             (:constructor make-journal-segment (&key (path "") (header '()) (records '()))))
  path header records)

(defstruct (journal-chain
             (:constructor make-journal-chain (&key (id "") (segments '()))))
  id segments)

(defun journal-last-segment (chain)
  (car (last (journal-chain-segments chain))))

(defun rotate-journal (chain)
  "Rotate CHAIN: the new segment's header names the same journal id and copies
the boundary record -- its sequence and hash -- so the chain and the savepoint
cut stay reachable without scanning an old segment (docs/SPEC-WORK.md:5998,
:6338)."
  (let* ((segments (journal-chain-segments chain))
         (old (car (last segments)))
         (boundary (car (last (journal-segment-records old))))
         (number (1+ (length segments)))
         (header (list :journal-id (journal-chain-id chain)
                       :segment number
                       :copied-from (journal-segment-path old)
                       :copied-boundary (and boundary
                                             (list :seq (journal-record-seq boundary)
                                                   :sha256 (journal-record-hash boundary)))))
         (new (make-journal-segment
               :path (format nil "~A.~D" (journal-chain-id chain) number)
               :header header
               :records (if boundary (list boundary) '()))))
    (make-journal-chain :id (journal-chain-id chain)
                        :segments (append segments (list new)))))

(defun journal-append-record (chain record)
  "Append RECORD to the last segment, continuing the chain's sequence from the
copied boundary."
  (let* ((segments (journal-chain-segments chain))
         (last-seg (car (last segments)))
         (last-rec (car (last (journal-segment-records last-seg))))
         (next-seq (if last-rec (1+ (journal-record-seq last-rec)) 1))
         (rec (make-journal-record :seq next-seq :events (journal-record-events record)))
         (new (make-journal-segment :path (journal-segment-path last-seg)
                                    :header (journal-segment-header last-seg)
                                    :records (append (journal-segment-records last-seg)
                                                     (list rec)))))
    (make-journal-chain :id (journal-chain-id chain)
                        :segments (append (butlast segments) (list new)))))

(defun savepoint-cut-reachable-p (chain cut)
  "The cut is reachable from the last segment's copied boundary record alone,
with no scan of an old segment (docs/SPEC-WORK.md:5998, :6338)."
  (let* ((header (journal-segment-header (journal-last-segment chain)))
         (b (getf header :copied-boundary)))
    (and b (integerp cut) (<= 0 cut (getf b :seq)))))

(defun export-journal-bundle (chain &key from)
  "The offline bundle. FROM names the file it is written from and changes no
byte, so either file writes the same bundle (docs/SPEC-WORK.md:6001)."
  (declare (ignore from))
  (let* ((header (journal-segment-header (journal-last-segment chain)))
         (b (getf header :copied-boundary)))
    (canonical-string (list :journal-bundle
                            :journal-id (journal-chain-id chain)
                            :boundary (if b
                                          (list :seq (getf b :seq) :sha256 (getf b :sha256))
                                          +absent+)))))

;;; ------------------------------------------------------------------
;;; rule-2-unavailable-is-not-green (docs/SPEC-WORK.md:5019, :5033, :5633)
;;; ------------------------------------------------------------------

(defun rule-2-check (reference &key partition-readable-p)
  "Rule 2 (:5011) resolves a reference against O's nodes or C's closed index.
Where the page that lookup needs cannot be read the rule reports that it could
not check and never that the reference is broken, and green is never printed
over history it could not read (docs/SPEC-WORK.md:5019-5033). REFERENCE's own
:resolved-p says the name resolved without a partition read."
  (let ((id (getf reference :id))
        (partition (getf reference :partition))
        (resolved-p (getf reference :resolved-p)))
    (cond
      (resolved-p (values :green nil))
      ((not partition-readable-p)
       (values :unavailable
               (format nil "WORK FAIL ~A: rule 2: unavailable partition=~A" id partition)))
      (t (values :dangling
                 (format nil "WORK FAIL ~A: rule 2: dangling" id))))))

(defun validate-report (verdicts)
  "One validation over the verdicts: exit 1 with a `WORK FAIL` count line on any
finding, exit 0 with `WORK OK` only when every rule was green
(docs/SPEC-WORK.md:5001-5008, :5633)."
  (let ((findings (count-if (lambda (v) (not (eq v :green))) verdicts)))
    (if (plusp findings)
        (values 1 (format nil "WORK FAIL findings=~D" findings))
        (values 0 "WORK OK"))))

;;; ------------------------------------------------------------------
;;; savepoint cut and write failure (docs/SPEC-WORK.md:5989, :6014, :6312)
;;; ------------------------------------------------------------------

(defstruct (savepoint-store
             (:constructor make-savepoint-store (&key verified published attempts)))
  verified published attempts)

(defun savepoint-record-last-event (record)
  (car (last (getf record :events))))

(defun savepoint-cut-ok-p (cut records)
  "A cut resolves to one complete record's last event, or to the seed."
  (or (zerop cut)
      (some (lambda (r) (= cut (savepoint-record-last-event r))) records)))

(defun savepoint-cut-inside-p (cut records)
  "True when the cut falls between the events of a single envelope."
  (some (lambda (r)
          (let ((events (getf r :events)))
            (and events (<= (first events) cut) (< cut (savepoint-record-last-event r)))))
        records))

(defun savepoint-image (cut records)
  "The events and retained replies of every complete record at or before CUT,
each represented once."
  (let ((kept (remove-if-not (lambda (r) (<= (savepoint-record-last-event r) cut))
                             records)))
    (list :events (loop for r in kept append (getf r :events))
          :replies (loop for r in kept collect (cons (getf r :request) (getf r :reply))))))

(defun savepoint-write (store id rev cut records &key fail-stage)
  "Write one savepoint in three steps -- the image, the manifest publication and
the sync. A cut inside an envelope is refused before any step and publishes
nothing; a failure at any step keeps the previous verified savepoint and records
the attempt verdict=failed (docs/SPEC-WORK.md:5989, :6014, :6312-6329)."
  (unless (savepoint-cut-ok-p cut records)
    (return-from savepoint-write
      (values store
              (format nil "SAVEPOINT FAIL id=~A rev=~D: ~A" id rev
                      (if (savepoint-cut-inside-p cut records)
                          "cut inside an envelope"
                          "cut not at a record boundary")))))
  (let* ((image (list :id id :rev rev :cut cut :image (savepoint-image cut records)))
         (previous (savepoint-store-verified store)))
    (dolist (stage '(:image :manifest :sync))
      (when (eq stage fail-stage)
        (return-from savepoint-write
          (values (make-savepoint-store
                   :verified previous
                   :published (savepoint-store-published store)
                   :attempts (cons (list :id id :rev rev :verdict :failed :stage stage)
                                   (savepoint-store-attempts store)))
                  (format nil "SAVEPOINT FAIL id=~A rev=~D: ~A failed" id rev stage)))))
    (values (make-savepoint-store
             :verified image
             :published (cons (getf image :image) (savepoint-store-published store))
             :attempts (cons (list :id id :rev rev :verdict :ok)
                             (savepoint-store-attempts store)))
            nil)))

(defun savepoint-list (store)
  "Expose the verified savepoint and every attempt, the failed ones marked
verdict=failed (docs/SPEC-WORK.md:6014-6016)."
  (with-output-to-string (s)
    (let ((v (savepoint-store-verified store)))
      (if v
          (format s "SAVEPOINT id=~A rev=~A verdict=verified~%" (getf v :id) (getf v :rev))
          (format s "SAVEPOINT none~%")))
    (dolist (a (savepoint-store-attempts store))
      (format s "SAVEPOINT ATTEMPT id=~A rev=~A verdict=~A~@[ stage=~A~]~%"
              (getf a :id) (getf a :rev)
              (string-downcase (symbol-name (getf a :verdict)))
              (getf a :stage)))))
