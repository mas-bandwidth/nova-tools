;;;; dedup-root.lisp --- the dedup root a savepoint's boundary record names
;;;; (docs/SPEC-WORK.md:7155-7164, the replay at :6740-6744).
;;;;
;;;; SPEC-WORK.md:7160-7161 conditions the retirement of a retained reply on
;;;; the committed snapshot, its retained events and "the dedup root that
;;;; boundary record names" all being verified reachable. Neither the root nor
;;;; the boundary record existed at this head: the snapshot is absent, the
;;;; dedup lookup was a resident map (src/journal.lisp:444-448, which :2117
;;;; forbids) and SPEC-WORK.md:7156 forbids printing the boundary from the cut.
;;;;
;;;; This file builds only the narrowest precondition retirement cannot proceed
;;;; without: a REAL dedup-root object with a digest, written beside the
;;;; savepoint's other objects, and the digest carried INSIDE the manifest's
;;;; `:boundary` record. Retirement itself is a LATER row. It reuses
;;;; savepoint-create's one write-and-hash mechanism -- no new write, no new
;;;; hash -- and its reader goes through `read-restricted`, so evaluation
;;;; syntax is refused and never run.

(in-package #:nova-work)

(defun dedup-root-entries (scan)
  "One entry per accepted record of SCAN, ordered by :sequence so the bytes a
caller writes are deterministic: the request id, its payload digest, its
accepted record's sequence, its accepted record's hash and the revision it was
applied at. This is the dedup root's content (SPEC-WORK.md:7155-7164). The
revision is carried so a retry whose id the index holds names the revision in
its refusal (SPEC-WORK.md:366)."
  (loop for record in (sort (copy-list (journal-scan-records scan))
                            #'< :key (lambda (record) (getf record :sequence)))
        collect (list :request (getf record :request)
                      :payload-sha256 (getf record :payload-sha256)
                      :sequence (getf record :sequence)
                      :record-sha256 (getf record :record-sha256)
                      :rev (getf record :rev))))

(defun write-dedup-root (path entries)
  "Write ENTRIES to PATH as the restricted s-expressions they are, one entry
per line, synced through the savepoint's own object writer. Answers (values
DIGEST SIZE), the digest taken over the bytes ON DISK by the savepoint's own
content reference -- no new write and no new hash (SPEC-WORK.md:7155-7164)."
  (%savepoint-write-object path
                           (with-output-to-string (out)
                             (dolist (entry entries)
                               (write-string (canonical-string entry) out)
                               (write-char #\Newline out))))
  (let ((reference (%savepoint-object-reference "dedup-root" path)))
    (values (getf reference :sha256) (getf reference :size))))

(defun read-dedup-root (path)
  "The entries PATH holds, each line read through `read-restricted` so that
evaluation syntax is REFUSED and never run, and the digest of the bytes on
disk. A missing PATH signals `unsupported-input` naming it: an absent root must
never read as `nothing was ever applied` (SPEC-WORK.md:7155-7164)."
  (unless (probe-file path)
    (error 'unsupported-input :what (format nil "no dedup root at ~A" path)))
  (let ((entries '()))
    (with-open-file (in path :direction :input :element-type 'character
                             :external-format :utf-8)
      (loop for line = (read-line in nil :eof)
            until (eq line :eof)
            do (let ((trimmed (string-trim '(#\Space #\Tab #\Return) line)))
                 (unless (zerop (length trimmed))
                   (push (read-restricted trimmed) entries)))))
    (values (nreverse entries)
            (sha256-hex (%savepoint-read-object path)))))

(defun dedup-root-holds-p (path digest)
  "Rehash the root ON DISK and answer whether the bytes are DIGEST's, as the
savepoint's own content reference is checked (SPEC-WORK.md:7155-7164)."
  (and (probe-file path)
       (equal digest (sha256-hex (%savepoint-read-object path)))))

(defun dedup-root-lookup (entries request digest)
  "The two-part dedup test the dedup index makes over ENTRIES (SPEC-WORK.md:329-335,
365-368). Answers (values FOUND-P RESULT REV) where FOUND-P is T when REQUEST is
in the index, RESULT is :already-applied when the payload digest matches or
:conflict when it differs, and REV is the revision it was applied at (named in
the refusal so the requester re-reads). Where the digest differs, the requester
changed its payload under an id this session already applied and must re-read."
  (let ((entry (find request entries :key (lambda (e) (getf e :request)) :test #'equal)))
    (cond
      ((null entry) (values nil nil nil))
      ((string= digest (getf entry :payload-sha256))
       (values t :already-applied (getf entry :rev)))
      (t (values t :conflict nil)))))
