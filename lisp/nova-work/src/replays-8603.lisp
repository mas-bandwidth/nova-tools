;;;; replays-8603.lisp --- the acceptance replays the closed history, the clip
;;;; bound and the six new verbs promise (docs/SPEC-WORK.md:5100-5560,5766-5775).
;;;;
;;;; These are the smallest engine functions the three replays of card 8603
;;;; call: a state-as-of ask that refuses an unreadable day partition, the
;;;; candidate gate that refuses one indivisible record before it is journaled,
;;;; and a clip that names every number it would write and the one remedy that
;;;; moves the overflowing part. The session, the CLI, the paged index and the
;;;; six new verbs' own kinds are outside slice 1 (README.md "What is out");
;;;; what is here is the refusal each paragraph makes true of this build.

(in-package #:nova-work)

(defun replay-word (mutation)
  "The `<MUTATION>` word of the mutation line (SPEC-WORK.md:5295)."
  (ecase mutation
    ((:state-to-done :state-to-doing) "STATE")
    (:event-reopen "EVENT")
    (:node "NODE")
    (:event "EVENT")
    (:note "NOTES")))

;;; ------------------------------------------------------------------
;;; as-of-refuses-unavailable-partition (SPEC-WORK.md:759,5135,5586)
;;; ------------------------------------------------------------------

(defun as-of-partition (as-of)
  "The `yyyy-mm-dd` day partition an `as-of` stamp names."
  (subseq as-of 0 10))

(defun ask-state-as-of (kernel &key ask as-of partitions)
  "Answer a `state-as-of` ask over the day partitions that can be read.
Return (values OK-P LINE EXIT-CODE). A partition the ask needs that is not
among PARTITIONS refuses at exit 1, naming the one partition it would need,
and is never answered from a newer row; a readable partition answers."
  (let* ((partition (as-of-partition as-of))
         (kind (string-downcase (symbol-name ask))))
    (if (member partition partitions :test #'string=)
        (values t
                (format nil "QUERY OK ask=~A as-of=~A partition=~A scope=~D"
                        kind as-of partition
                        (state-revision (kernel-state kernel)))
                0)
        (values nil
                (format nil "QUERY FAIL ask=~A as-of=~A partition=~A: historical window unavailable"
                        kind as-of partition)
                1))))

;;; ------------------------------------------------------------------
;;; indivisible-record-refused-before-ack (SPEC-WORK.md:794,5543,5994)
;;; ------------------------------------------------------------------

(defun admit-record (kernel &key mutation request key bytes page-bytes max-bytes)
  "The candidate gate for a record that may not fit a page. A single key with
its one locator that would pass --page-bytes on a page of its own is
indivisible: exit 2, `indivisible`, nothing journaled and nothing acknowledged.
A journal record the reader's bounds could not read back is refused by the same
gate, naming --max-bytes (SPEC-WORK.md:803,5295)."
  (declare (ignore kernel))
  (let ((word (replay-word mutation)))
    (cond
      ((and page-bytes (> bytes page-bytes))
       (values nil
               (format nil "~A FAIL request=~A key=~A bytes=~D past --page-bytes=~D: indivisible"
                       word request (string-downcase (symbol-name key)) bytes page-bytes)
               2))
      ((and max-bytes (> bytes max-bytes))
       (values nil
               (format nil "~A FAIL request=~A key=~A bytes=~D past --max-bytes=~D: indivisible"
                       word request (string-downcase (symbol-name key)) bytes max-bytes)
               2))
      (t
       (values t (format nil "~A OK request=~A key=~A bytes=~D"
                         word request (string-downcase (symbol-name key)) bytes)
               0)))))

;;; ------------------------------------------------------------------
;;; clip-names-the-index-that-overflowed (SPEC-WORK.md:803,5102,5553)
;;; ------------------------------------------------------------------

(defun clip (kernel &key snapshot retained index closed-index max-bytes)
  "A clip whose snapshot would exceed --max-bytes refuses, printing all four
numbers it would write beside the bound and naming the one remedy that moves
the overflowing part. The remedy does not branch: every part is bounded by
--retain, so it is always `lower --retain or raise --max-bytes`."
  (declare (ignore kernel))
  (if (and max-bytes (> snapshot max-bytes))
      (values nil
              (format nil "CLIP FAIL snapshot=~D retained=~D index=~D closed-index=~D past --max-bytes=~D, lower --retain or raise --max-bytes"
                      snapshot retained index closed-index max-bytes)
              1)
      (values t
              (format nil "CLIP OK snapshot=~D retained=~D index=~D closed-index=~D"
                      snapshot retained index closed-index)
              0)))

(defun split-index-page (bytes page-bytes)
  "An index page that would pass --page-bytes is split into a further page by
the clip that writes it and is never refused for growing (SPEC-WORK.md:803).
Return (values PAGES REFUSED-P); REFUSED-P is always NIL."
  (values (ceiling bytes page-bytes) nil))
