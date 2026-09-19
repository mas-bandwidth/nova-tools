;;;; replays-journal-frame-integrity.lisp --- the frame checks, exercised.
;;;;
;;;; `read-record-frame` (src/journal.lisp) makes four checks on every frame it
;;;; reads back: the SEQUENCE is the one expected, a RECORD is present, the
;;;; recorded LENGTH is the record's own length, and the recorded CHECKSUM is
;;;; the record's own SHA-256. Three of the four were reachable by no test.
;;;;
;;;; MEASURED, by mutation, against dev@11aa07a7 on the 335-case suite: delete
;;;; the sequence check, the length check or the checksum check -- one line each,
;;;; `(unless X ...)` -> `(unless t ...)` -- and the suite stays 335/335 green.
;;;;
;;;; The reason is that the one existing corruption case,
;;;; `durable-journal-corrupt-data-refuses-without-truncation`, appends a TORN
;;;; TAIL: `(:frame :seq 3 :len 120 :checksum "0000"` with no closing paren. That
;;;; never parses, so it is caught by the reader before any of these four checks
;;;; is reached. The header checks it also covers (`:magic`, `:initial-state`)
;;;; DO go red under mutation. What had no witness is the frame that PARSES
;;;; PERFECTLY WELL and is wrong -- a record altered in place, which is the one
;;;; thing a checksum is for.
;;;;
;;;; Nothing is fixed here. These are the cases the checks never had.

(in-package #:nova-work/tests)

(defun %frame-lines (path)
  "Every line of the journal file, in order."
  (with-open-file (in path :direction :input :element-type 'character
                           :external-format :utf-8)
    (loop for line = (read-line in nil nil)
          while line collect line)))

(defun %rewrite-last-frame (path fn)
  "Replace the last line of the journal at PATH with (funcall FN last-line).
Every other byte is left as it is, so the file is a journal with one frame
altered in place -- not a truncated one and not a torn one."
  (let* ((lines (%frame-lines path))
         (last (car (last lines)))
         (new (funcall fn last)))
    (with-open-file (out path :direction :output :if-exists :supersede
                              :element-type 'character :external-format :utf-8)
      (loop for rest on lines
            do (write-string (if (null (cdr rest)) new (car rest)) out)
               (terpri out)))
    (values last new)))

(defun %two-record-journal (name)
  "A real file journal with two ordinary records in it, closed. Answers its path
and the seed digest it was opened against."
  (let* ((path (test-journal-path name))
         (digest (root-digest (make-seed-state *seed*)))
         (j (open-file-journal path :initial-state-hash digest)))
    (let ((k (fresh :journal j)))
      (ok (submit k (close-request :node "acme/work/f1/t1" :request "jfi-1"))
          "the first record was refused")
      (ok (submit k (reopen-request :node "acme/work/f1/t1" :request "jfi-2"))
          "the second record was refused"))
    (close-file-journal j)
    (values path digest)))

(defun %reopen-must-refuse (path digest what)
  "Reopen and demand `journal-corrupt-data`, answering its reason. A journal
that opens over an altered frame has accepted a record nobody wrote."
  (let ((reason nil) (opened nil))
    (handler-case
        (let ((j (open-file-journal path :initial-state-hash digest)))
          (setf opened t)
          (ignore-errors (close-file-journal j)))
      (journal-corrupt-data (c) (setf reason (journal-corrupt-data-reason c)))
      (error (c) (fail "~A: unexpected error type: ~A" what c)))
    (ok (not opened) "~A: the journal OPENED over an altered frame" what)
    (ok reason "~A: no journal-corrupt-data reason was given" what)
    reason))

(deftest "a-journal-frame-that-parses-and-is-wrong-is-refused"
    "docs/SPEC-WORK.md:3348,3357"
    "expected=altered-record-refused-by-checksum;wrong-len-refused;wrong-seq-refused;no-truncation"
  ;; 1. THE CHECKSUM. One character changed INSIDE the record, in the author's
  ;; name, same length: `:len` still matches and the frame still parses. The
  ;; checksum is the only thing that can tell.
  (multiple-value-bind (path digest) (%two-record-journal "frame-checksum")
    (unwind-protect
         (progn
           (%rewrite-last-frame
            path (lambda (line)
                   (let ((at (search "\"rowan\"" line)))
                     (ok at "the frame does not carry the author to alter")
                     (concatenate 'string (subseq line 0 at) "\"rowaX\""
                                  (subseq line (+ at 7))))))
           (let ((bytes (file-byte-count path))
                 (reason (%reopen-must-refuse path digest "altered record")))
             (ok (search "checksum" reason)
                 "the refusal is not the checksum's: ~A" reason)
             ;; The file is evidence and is never repaired by being read.
             (check-equal bytes (file-byte-count path)
                          "the refused journal was truncated or rewritten")))
      (ignore-errors (delete-file path))))
  ;; 2. THE LENGTH, declared in the frame header and not in the record, so the
  ;; record itself is untouched and only `:len` disagrees with it.
  (multiple-value-bind (path digest) (%two-record-journal "frame-length")
    (unwind-protect
         (progn
           (%rewrite-last-frame
            path (lambda (line)
                   (let ((at (search ":len " line)))
                     (ok at "the frame carries no :len")
                     ;; one digit more: the declared length is now wrong
                     (concatenate 'string (subseq line 0 (+ at 5)) "9"
                                  (subseq line (+ at 5))))))
           (let ((reason (%reopen-must-refuse path digest "wrong :len")))
             (ok (search "length" reason)
                 "the refusal is not the length's: ~A" reason)))
      (ignore-errors (delete-file path))))
  ;; 3. THE SEQUENCE. The journal's order IS its sequence numbers
  ;; (SPEC-WORK.md:2603-2616), so a frame out of order is a journal that cannot
  ;; say what happened when.
  (multiple-value-bind (path digest) (%two-record-journal "frame-sequence")
    (unwind-protect
         (progn
           (%rewrite-last-frame
            path (lambda (line)
                   (let ((at (search ":seq 2" line)))
                     (ok at "the second frame is not :seq 2")
                     (concatenate 'string (subseq line 0 at) ":seq 7"
                                  (subseq line (+ at 6))))))
           (let ((reason (%reopen-must-refuse path digest "wrong :seq")))
             (ok (search "sequence" reason)
                 "the refusal is not the sequence's: ~A" reason)))
      (ignore-errors (delete-file path)))))
