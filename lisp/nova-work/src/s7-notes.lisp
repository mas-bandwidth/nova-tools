;;;; s7-notes.lisp --- slice 7: the delegation `applicable` note query.
;;;;
;;;; The cap (--max) bounds the prose rows, never the evaluation: a candidate
;;;; that a note denies is reported as excluded even when that note sorts last
;;;; and the cap would cut before it, and an unloadable notes index reports FAIL
;;;; rather than a shorter, silently eligible list (SPEC-WORK.md:5829).

(in-package #:nova-work)

(defun s7-ask-applicable (notes &key max candidate notes-index-unloadable)
  "`applicable --max <n> --candidate <role>/<model>`. NOTES is a list of plists,
  each (:id <id> :sort <n> :deny <candidate-or-nil>). Returns (values ROWS LINE):
  ROWS are the eligible proportions (the prose verdict rows, capped), and LINE
  carries the excluded deny and NOTES MORE so the cap never hides a deny."
  (when notes-index-unloadable
    (return-from s7-ask-applicable
      (values nil "APPLICABLE FAIL notes-index-unloadable")))
  (let* ((sorted (sort (copy-list notes) #'< :key (lambda (n) (getf n :sort))))
         (deny (find candidate sorted
                     :key (lambda (n) (getf n :deny))
                     :test (lambda (c d) (and d (stringp d) (string= c d)))))
         (eligible (remove-if (lambda (n)
                                (let ((d (getf n :deny)))
                                  (and d (stringp d) (string= candidate d))))
                              sorted))
         (line (with-output-to-string (s)
                 (when deny
                   (format s "excluded ~A" (getf deny :id)))
                 (when (and (or (null max) (> (length sorted) max)))
                   (format s " NOTES MORE"))))
         (rows (if max (subseq eligible 0 (min max (length eligible))) eligible)))
    (values rows line)))
