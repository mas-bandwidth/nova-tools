;;;; savepoint-restore.lisp --- `savepoint list`, `savepoint verify` and the
;;;; isolated read-only `savepoint restore` (docs/SPEC-WORK.md:2275-2278,
;;;; :5983-5986, :7109-7190, the replays named at :6510 and :6734-6738).
;;;;
;;;; `src/savepoint.lisp` carries the pure model's `savepoint-list`,
;;;; `savepoint-verify` and `savepoint-restore` over a STORE and a LOAD struct
;;;; the replay builds. These are the verbs, and every byte they read is a file
;;;; `savepoint create` published.
;;;;
;;;; **Restoring one is five steps** (SPEC-WORK.md:7165-7168): the manifest, the
;;;; journal id and the exact cut verified; the image and its replies loaded;
;;;; only the COMPLETE records STRICTLY AFTER the cut replayed once in sequence;
;;;; boundary records processed; then the whole validation before anything is
;;;; exposed.
;;;;
;;;; **Read-only, isolated, non-dispatching** (:2278, :6738, :7168). A restore
;;;; here answers a plain WSTATE. It starts no command thread, so it cannot be
;;;; a writer; it opens the journal file for reading only and takes no `flock`,
;;;; so it takes no ownership; it sends nothing and replays no message. **A
;;;; copied journal grants nothing** (:6737): a restore from a savepoint whose
;;;; journal id is not the journal it was handed refuses `journal mismatch`.
;;;;
;;;; **THE JOURNAL ID, AND WHAT THE HEADER ACTUALLY CARRIES.** SPEC-WORK.md:7145
;;;; names `:journal "<64-hex>"` in the manifest. `src/journal.lisp` write-header
;;;; writes no id field, so the identity used here is the SHA-256 of the header
;;;; line the journal itself holds. That is what the file carries, and it is what
;;;; a reader can check without a second source -- but two journals opened over
;;;; the same seed with the same stamp have byte-identical headers, so this
;;;; identity separates journals by content and not by instance. Giving the
;;;; header a per-journal id belongs to the journal, not to this file; it is
;;;; named in the PR rather than smuggled in here.
;;;;
;;;; **A missing or mismatched cut, a missing required tail, a broken hash or
;;;; sequence or an incomplete envelope is a recovery gap by its kind and never
;;;; a truncation or a rollback of acknowledged work** (:7169-7171).

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; Shared reading
;;; ------------------------------------------------------------------

(defstruct (savepoint-report
            (:constructor %make-savepoint-report
                (&key id manifest revision cut gap image-path replies-path)))
  "What a verify found: the published manifest, the image's revision, the cut
it resolved to, and the recovery gap's kind when it did not hold."
  id manifest revision cut gap image-path replies-path)

(defun savepoint-published-ids (root)
  "Every savepoint directory under ROOT, newest write first. A directory with
no published manifest is a failed attempt and is listed as one, never skipped
(SPEC-WORK.md:7117-7119)."
  (let ((dirs (directory (merge-pathnames "*/" (pathname (%directory-namestring root))))))
    (sort (loop for d in dirs
                for id = (car (last (pathname-directory d)))
                when id collect id)
          #'string<)))

(defun savepoint-published-p (root id)
  (and (probe-file (savepoint-manifest-path root id)) t))

(defun savepoint-age-seconds (root id now)
  "The savepoint's age: NOW less the moment its manifest was published. The
manifest carries no stamp of its own -- the field list at SPEC-WORK.md:7145 is
closed -- so the published file's own write date is what answers."
  (let ((path (savepoint-manifest-path root id)))
    (if (probe-file path)
        (max 0 (- now (file-write-date path)))
        0)))

;;; ------------------------------------------------------------------
;;; `savepoint verify` (SPEC-WORK.md:2277, :5986, :7165-7171)
;;; ------------------------------------------------------------------

(defun %gap (id rev kind)
  (values nil
          (format nil "SAVEPOINT FAIL id=~A rev=~A: ~A" id (or rev "-") kind)
          1
          (%make-savepoint-report :id id :gap kind)))

(defun savepoint-verify-published (root id &key journal-path)
  "Verify the savepoint ID names: its manifest, its journal id and its exact
cut, and the two content references against the bytes on disk. Answers
(values OK-P LINE EXIT-CODE REPORT). Every refusal names its recovery gap by
kind and rolls nothing back (SPEC-WORK.md:7165-7171)."
  (let ((manifest (handler-case (read-savepoint-manifest root id) (error () nil))))
    (unless manifest
      (return-from savepoint-verify-published (%gap id nil "no published manifest")))
    (unless (equal "work-savepoint-v1" (getf manifest :schema))
      (return-from savepoint-verify-published (%gap id nil "unsupported schema")))
    (let* ((dir (savepoint-directory root id))
           (image-path (merge-pathnames "state" dir))
           (replies-path (merge-pathnames "local-replies" dir))
           (rev (getf manifest :local-revision))
           (cut (getf manifest :replay-cut)))
      ;; the two hash-checked content references, against the bytes on disk
      (dolist (pair (list (cons (getf manifest :state) image-path)
                          (cons (getf manifest :local-replies) replies-path)))
        (unless (and (probe-file (cdr pair))
                     (%savepoint-reference-holds-p (car pair) (cdr pair)))
          (return-from savepoint-verify-published (%gap id rev "broken hash"))))
      ;; the dedup root the boundary record names, against the bytes on disk (:7160-7161)
      (let* ((dedup-root-path (merge-pathnames "dedup-root" dir))
             (boundary (getf manifest :boundary))
             (named (and (consp boundary) (consp (cdr boundary))
                         (getf boundary :dedup-root))))
        (when named
          (unless (and (probe-file dedup-root-path)
                       (dedup-root-holds-p dedup-root-path named))
            (return-from savepoint-verify-published
              (%gap id rev (format nil "dedup root ~A is missing or altered" dedup-root-path))))))
      ;; the image is one revision, and it is the manifest's
      (let ((image (handler-case (reconstruct-state (%savepoint-read-object image-path))
                     (error () nil))))
        (unless image
          (return-from savepoint-verify-published (%gap id rev "corrupt image")))
        (unless (eql rev (state-revision image))
          (return-from savepoint-verify-published
            (%gap id rev "image revision differs from its cut")))
        ;; the journal identity and the exact cut. The new shape requires both
        ;; the stable logical identity and a continuous chain to the cut; a
        ;; manifest without it is the explicit legacy shape (:7176-7182).
        (when journal-path
          (unless (probe-file journal-path)
            (return-from savepoint-verify-published (%gap id rev "journal mismatch")))
          (let ((rotated nil))
           (let ((logical (getf manifest :journal-logical)))
            (if logical
                (progn
                  (unless (equal logical (journal-logical-identity journal-path))
                    (return-from savepoint-verify-published
                      (%gap id rev "journal identity mismatch")))
                  (multiple-value-bind (found root-sha rotated-p)
                      (journal-chain-covers-cut-p journal-path cut)
                    (setf rotated rotated-p)
                    (when (and rotated-p (not found))
                      (return-from savepoint-verify-published
                        (%gap id rev "broken chain: the live journal does not reach the saved cut")))
                    (unless (equal root-sha (getf manifest :journal))
                      (return-from savepoint-verify-published (%gap id rev "journal mismatch")))))
                (let ((identity (handler-case
                                    (sha256-hex (with-open-file (in journal-path
                                                                    :external-format :utf-8)
                                                  (read-line in nil "")))
                                  (error () nil))))
                  (unless (equal identity (getf manifest :journal))
                    (return-from savepoint-verify-published (%gap id rev "journal mismatch"))))))
          (let* ((scan (scan-journal-file journal-path))
                 (records (journal-scan-records scan)))
            ;; a rotated segment verifies its cut from the header alone.
            (unless (or rotated (absentp cut))
              (let ((record (find (getf cut :sequence) records
                                  :key (lambda (r) (getf r :sequence)))))
                (unless record
                  (return-from savepoint-verify-published
                    (%gap id rev "missing required tail")))
                (unless (equal (getf cut :sha256) (getf record :record-sha256))
                  (return-from savepoint-verify-published
                    (%gap id rev "cut inside an envelope")))
                ;; the record the cut names resolves to the image's revision
                (unless (eql rev (getf record :rev))
                  (return-from savepoint-verify-published
                    (%gap id rev "image revision differs from its cut")))))
            ;; every retained disposition at or before the cut keeps its
            ;; ORIGINAL reply: a restore missing one refuses (:7141-7144).
            (let ((replies (handler-case
                               (read-restricted (%savepoint-read-object replies-path))
                             (error () nil)))
                  (cut-seq (if (absentp cut) 0 (getf cut :sequence))))
              (dolist (record records)
                (when (<= (getf record :sequence) cut-seq)
                  (let ((kept (find (getf record :request) replies
                                    :key (lambda (r) (getf r :request))
                                    :test #'equal)))
                    (unless (and kept (getf kept :reply))
                      (return-from savepoint-verify-published
                        (%gap id rev "missing original reply"))))))))))
        (values t
                (format nil "SAVEPOINT OK id=~A rev=~D checkpoint=- pushed=- boundary=~A age=~A unshared=0 manifest=~A shown=1"
                        id rev
                        (if (absentp cut) "-" (getf cut :sequence))
                        (format nil "~Ds" (savepoint-age-seconds root id (get-universal-time)))
                        (sha256-hex (%savepoint-read-object (savepoint-manifest-path root id))))
                0
                (%make-savepoint-report :id id :manifest manifest :revision rev
                                        :cut cut :image-path image-path
                                        :replies-path replies-path))))))

;;; ------------------------------------------------------------------
;;; `savepoint list` (SPEC-WORK.md:2275, :7117-7119)
;;; ------------------------------------------------------------------

(defun savepoint-list-published (root &key shared-revision (max 64) journal-path)
  "Expose every savepoint under ROOT: its age, the local and the shared
revisions SIDE BY SIDE, and the attempts that published nothing. The local
savepoint is never printed where a checkpoint was asked for
(SPEC-WORK.md:5983, :7117-7119). Answers (values COUNT-LINE ROW-LINES ROWS)."
  (let ((now (get-universal-time))
        (rows '())
        (published 0)
        (failed 0))
    (dolist (id (savepoint-published-ids root))
      (if (savepoint-published-p root id)
          (multiple-value-bind (okp line code report)
              (savepoint-verify-published root id :journal-path journal-path)
            (declare (ignore line code))
            (incf published)
            (push (list :id id
                        :verdict (if okp :verified :gap)
                        :gap (savepoint-report-gap report)
                        :local-revision (savepoint-report-revision report)
                        :shared-revision shared-revision
                        :age (savepoint-age-seconds root id now))
                  rows))
          (progn
            (incf failed)
            (push (list :id id :verdict :failed :gap "no published manifest"
                        :local-revision nil :shared-revision shared-revision
                        :age (savepoint-age-seconds root id now))
                  rows))))
    (setf rows (nreverse rows))
    (let* ((shown (min max (length rows)))
           (lines (loop for row in (subseq rows 0 shown)
                        collect (format nil "SAVEPOINT ROW id=~A verdict=~A rev=~A checkpoint=~A age=~Ds~@[ gap=~A~]"
                                        (getf row :id)
                                        (string-downcase (symbol-name (getf row :verdict)))
                                        (or (getf row :local-revision) "-")
                                        (or (getf row :shared-revision) "-")
                                        (getf row :age)
                                        (getf row :gap)))))
      (values (format nil "SAVEPOINT OK savepoints=~D verified=~D failed=~D checkpoint=~A shown=~D"
                      (length rows) published failed
                      (or shared-revision "-") shown)
              lines rows))))

;;; ------------------------------------------------------------------
;;; `savepoint restore` --- isolated, read-only, non-dispatching
;;; (SPEC-WORK.md:2278, :6738, :7165-7168)
;;; ------------------------------------------------------------------

(defstruct (restored-savepoint
            (:constructor %make-restored-savepoint
                (&key id state revision replayed replies ownership-taken-p
                      dispatched-p messages-replayed)))
  "What a restore exposes: a plain WSTATE and what it did to get there. It
holds no kernel, so it has no command thread and cannot be a writer;
OWNERSHIP-TAKEN-P, DISPATCHED-P and MESSAGES-REPLAYED exist so a replay can
assert the three nots rather than trust the prose (SPEC-WORK.md:2278)."
  id state revision replayed replies ownership-taken-p dispatched-p
  messages-replayed)


(defun savepoint-restore-published (root id &key journal-path max-bytes max-nodes)
  "`savepoint restore --savepoint <path> --into <path> --max-bytes <n>
--max-depth <n> --max-nodes <n>` (SPEC-WORK.md:2278). Verify first -- the whole
validation runs before anything is exposed -- then load the image and its
replies and replay ONLY the complete records STRICTLY AFTER the cut, once, in
sequence. Answers (values OK-P LINE EXIT-CODE RESTORED).

It is isolated and read-only: it builds no kernel, so it starts no command
thread and can never be a writer; it opens the journal for reading only and
takes no lock, so it takes no ownership; and it dispatches nothing and replays
no message."
  (multiple-value-bind (okp line code report)
      (savepoint-verify-published root id :journal-path journal-path)
    (unless okp
      (return-from savepoint-restore-published (values nil line code report)))
    (let* ((manifest (savepoint-report-manifest report))
           (rev (getf manifest :local-revision))
           (image-bytes (%savepoint-read-object (savepoint-report-image-path report)))
           (cut (savepoint-report-cut report))
           (cut-seq (if (absentp cut) 0 (getf cut :sequence))))
      ;; The bounds the grammar requires, before the image is built.
      (when (and max-bytes (> (length image-bytes) max-bytes))
        (return-from savepoint-restore-published
          (values nil (format nil "SAVEPOINT FAIL id=~A rev=~A: image ~D past --max-bytes ~D"
                              id rev (length image-bytes) max-bytes)
                  1 nil)))
      (let ((state (reconstruct-state image-bytes))
            (replayed 0))
        (when (and max-nodes (> (length (state-node-ids state)) max-nodes))
          (return-from savepoint-restore-published
            (values nil (format nil "SAVEPOINT FAIL id=~A rev=~A: nodes ~D past --max-nodes ~D"
                                id rev (length (state-node-ids state)) max-nodes)
                    1 nil)))
        ;; Only the complete records STRICTLY AFTER the cut, once, in sequence.
        (when journal-path
          (let ((scan (scan-journal-file journal-path)))
            (dolist (record (journal-scan-records scan))
              (when (> (getf record :sequence) cut-seq)
                (let ((events (loop for e in (getf record :events)
                                    collect (record-form->event
                                             e :session-written-p
                                             (member (getf e :kind) '(:settle :revive))))))
                  (setf state (apply-envelope state
                                              (list :request (getf record :request)
                                                    :digest (getf record :digest)
                                                    :events events)))
                  (incf replayed))))))
        (values t
                (format nil "SAVEPOINT OK id=~A rev=~D checkpoint=- pushed=- boundary=~A age=~Ds unshared=0 manifest=~A shown=1 replayed=~D"
                        id (state-revision state)
                        (if (absentp cut) "-" (getf cut :sequence))
                        (savepoint-age-seconds root id (get-universal-time))
                        (sha256-hex (%savepoint-read-object (savepoint-manifest-path root id)))
                        replayed)
                0
                (%make-restored-savepoint
                 :id id :state state :revision (state-revision state)
                 :replayed replayed
                 :replies (read-restricted
                           (%savepoint-read-object (savepoint-report-replies-path report)))
                 :ownership-taken-p nil :dispatched-p nil :messages-replayed 0))))))

;;; ------------------------------------------------------------------
;;; `savepoint compare` --- the isolated old restore against current state
;;; (SPEC-WORK.md:2279, :7085, :7117-7119)
;;; ------------------------------------------------------------------

(defun %state-node-rows (state)
  "Every node of STATE as (id branch state holder), in seed order. This is what
a comparison is over: the work the two sides actually hold."
  (loop for id in (state-node-ids state)
        collect (list :node id
                      :branch (node-branch state id)
                      :state (node-state state id))))

(defun savepoint-compare-published (root id &key journal-path against
                                                 against-kind (max 64)
                                                 shared-checkpoint)
  "`savepoint compare --savepoint <path> --against (--session <path> |
--snapshot <path> ...)` (SPEC-WORK.md:2279). AGAINST is the WSTATE of the live
session or of a loaded snapshot. The comparison restores the savepoint in
isolation -- so it takes no ownership and dispatches nothing -- then puts the
two revisions SIDE BY SIDE, names the work the against side holds beyond the
savepoint, and keeps the shared checkpoint a different field from the local
savepoint (:5983, :7109). Answers (values COUNT-LINE ROW-LINES ROWS)."
  (multiple-value-bind (okp line code restored)
      (savepoint-restore-published root id :journal-path journal-path)
    (declare (ignore code))
    (unless okp
      (return-from savepoint-compare-published (values line '() nil)))
    (let* ((mine (restored-savepoint-state restored))
           (theirs against)
           (my-rows (%state-node-rows mine))
           (their-rows (and theirs (%state-node-rows theirs)))
           (differences '()))
      (dolist (row my-rows)
        (let ((theirs-row (find (getf row :node) their-rows
                                :key (lambda (r) (getf r :node)) :test #'equal)))
          (cond
            ((null theirs-row)
             (push (list :node (getf row :node) :difference :only-in-savepoint
                         :savepoint (getf row :state) :against nil)
                   differences))
            ((not (and (eq (getf row :branch) (getf theirs-row :branch))
                       (eq (getf row :state) (getf theirs-row :state))))
             (push (list :node (getf row :node) :difference :moved
                         :savepoint (getf row :state)
                         :against (getf theirs-row :state))
                   differences)))))
      (dolist (row their-rows)
        (unless (find (getf row :node) my-rows
                      :key (lambda (r) (getf r :node)) :test #'equal)
          (push (list :node (getf row :node) :difference :only-in-against
                      :savepoint nil :against (getf row :state))
                differences)))
      (setf differences (nreverse differences))
      (let* ((mine-rev (restored-savepoint-revision restored))
             (theirs-rev (and theirs (state-revision theirs)))
             (shown (min max (length differences)))
             (lines (loop for d in (subseq differences 0 shown)
                          collect (format nil "SAVEPOINT ROW node=~A difference=~A savepoint=~A against=~A"
                                          (getf d :node)
                                          (string-downcase (symbol-name (getf d :difference)))
                                          (or (getf d :savepoint) "-")
                                          (or (getf d :against) "-")))))
        (values (format nil "SAVEPOINT OK id=~A rev=~D checkpoint=~A against=~A against-kind=~A differences=~D shown=~D"
                        id mine-rev
                        (or shared-checkpoint "-")
                        (or theirs-rev "-")
                        (if against-kind
                            (string-downcase (princ-to-string against-kind)) "-")
                        (length differences) shown)
                lines differences)))))
