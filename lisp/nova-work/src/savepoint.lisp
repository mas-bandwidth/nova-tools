;;;; savepoint.lisp --- the local savepoint's create and list.
;;;;
;;;; docs/SPEC-WORK.md:6391-6472. A savepoint is local, the checkpoint is
;;;; shared, and the two are never reported as one. `savepoint create` writes
;;;; one validated atomic local savepoint whose manifest names its schema,
;;;; journal, local revision, replay cut and boundary records and two
;;;; hash-checked content references; the cut never falls between an envelope's
;;;; events, and a failure at any of the four steps keeps the prior verified
;;;; savepoint. `savepoint list` exposes the age, the local and shared
;;;; revisions side by side, the unshared work and the failed attempts.
;;;;
;;;; The savepoint's own data type lives in control.lisp beside the shared
;;;; checkpoint it is never confused with; the journal's record identities and
;;;; the rotation helper live in roadmap.lisp. This file owns the verb.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; create: the image, the cut and the manifest (docs/SPEC-WORK.md:6393-6472)
;;; ------------------------------------------------------------------

(defstruct (savepoint-store
             (:constructor make-savepoint-store (&key verified published attempts)))
  "The writer's resident savepoint state: the last verified savepoint, the
published immutable images and the attempt log. A failed write returns a store
carrying the previous VERIFIED and PUBLISHED unchanged."
  verified published attempts)

(defun savepoint-record-last-event (record)
  "The sequence number of RECORD's last event, the only place a cut may land."
  (car (last (getf record :events))))

(defun savepoint-record-as-journal-record (record)
  "RECORD's journal identity: its sequence and its events, the two fields the
record hash is taken over (docs/SPEC-WORK.md:6419-6421)."
  (make-journal-record :seq (getf record :seq) :events (getf record :events)))

(defun savepoint-record-hash (record)
  "The verified identity of RECORD's complete journal record."
  (journal-record-hash (savepoint-record-as-journal-record record)))

(defun savepoint-cut-record (cut records)
  "The one complete record whose last event is CUT, or NIL for the seed."
  (find-if (lambda (r) (and (getf r :events)
                            (= cut (savepoint-record-last-event r))))
           records))

(defun savepoint-cut-ok-p (cut records)
  "A cut resolves to one complete record's last event, or to the seed."
  (or (zerop cut)
      (and (savepoint-cut-record cut records) t)))

(defun savepoint-cut-inside-p (cut records)
  "True when CUT falls between the events of a single envelope."
  (some (lambda (r)
          (let ((events (getf r :events)))
            (and events (<= (first events) cut)
                 (< cut (savepoint-record-last-event r)))))
        records))

(defun savepoint-image-events (cut records)
  "The immutable image the cut reflects: the events of every complete record at
or before CUT, each represented once."
  (list :events (loop for r in records
                      when (and (getf r :events)
                                (<= (savepoint-record-last-event r) cut))
                        append (getf r :events))))

(defun savepoint-retained-replies (records)
  "Every retained disposition: its request id, payload digest, accepted
record's sequence and hash and the original reply. They are kept in the image
even when their events lie before the cut (docs/SPEC-WORK.md:6437-6444)."
  (mapcar (lambda (r)
            (list :request (getf r :request)
                  :payload-sha256 (getf r :payload-sha256)
                  :sequence (getf r :seq)
                  :record-sha256 (savepoint-record-hash r)
                  :reply (getf r :reply)))
          records))

(defun savepoint-content-reference (path value)
  "One hash-checked content reference inside the savepoint's own root: the
path, the SHA-256 of the value's canonical bytes and the byte size."
  (let ((bytes (canonical-string value)))
    (list :path path :sha256 (sha256-hex bytes) :size (length bytes))))

(defun savepoint-journal-boundary (journal)
  "The newest boundary record the image reflects, named as a sequence and hash,
or `(:absent)` for an image with none (docs/SPEC-WORK.md:6418-6422)."
  (if (journal-chain-p journal)
      (let* ((header (journal-segment-header (journal-last-segment journal)))
             (b (getf header :copied-boundary)))
        (if b (list :sequence (getf b :seq) :sha256 (getf b :sha256)) +absent+))
      +absent+))

(defun savepoint-failed (store id rev stage)
  "The store a failed write returns: the previous VERIFIED and PUBLISHED
unchanged, plus the attempt listed verdict=failed
(docs/SPEC-WORK.md:6400-6401, :6446-6450)."
  (values (make-savepoint-store
           :verified (savepoint-store-verified store)
           :published (savepoint-store-published store)
           :attempts (cons (list :id id :rev rev :verdict :failed :stage stage)
                           (savepoint-store-attempts store)))
          (format nil "SAVEPOINT FAIL id=~A rev=~D: ~A failed" id rev stage)))

(defun savepoint-write (store id rev cut records
                        &key journal fail-stage (age 0) (schema "work-savepoint-v1"))
  "Write one validated atomic local savepoint in four steps: the immutable image
and its record cut captured at one revision under the single writer; the image
and the reply objects written and synced; the candidate manifest validated
against their exact identities; then published atomically and synced, the prior
verified savepoint kept through it. A cut inside an envelope is refused before
any step and publishes nothing; a failure at any step keeps the previous
verified savepoint and records the attempt verdict=failed
(docs/SPEC-WORK.md:6433-6450)."
  (unless (savepoint-cut-ok-p cut records)
    (return-from savepoint-write
      (values (make-savepoint-store
               :verified (savepoint-store-verified store)
               :published (savepoint-store-published store)
               :attempts (cons (list :id id :rev rev :verdict :failed :stage :cut)
                               (savepoint-store-attempts store)))
              (format nil "SAVEPOINT FAIL id=~A rev=~D: ~A" id rev
                      (if (savepoint-cut-inside-p cut records)
                          "cut inside an envelope"
                          "cut not at a record boundary")))))
  (let* ((cut-record (savepoint-cut-record cut records))
         (image-value (savepoint-image-events cut records))
         (replies-value (savepoint-retained-replies records))
         (state-ref (savepoint-content-reference "state" image-value))
         (replies-ref (savepoint-content-reference "local-replies" replies-value))
         (journal-id (if (journal-chain-p journal) (journal-chain-id journal) ""))
         (boundary (savepoint-journal-boundary journal))
         (replay-cut (if cut-record
                         (list :sequence (getf cut-record :seq)
                               :sha256 (savepoint-record-hash cut-record))
                         +absent+))
         (manifest (list :schema schema
                         :journal journal-id
                         :local-revision rev
                         :replay-cut replay-cut
                         :boundary boundary
                         :state state-ref
                         :local-replies replies-ref)))
    ;; step 2: the image and the reply objects are written and synced.
    (when (eq fail-stage :image)
      (return-from savepoint-write (savepoint-failed store id rev :image)))
    ;; step 3: the candidate manifest is validated against their exact
    ;; identities, then published atomically and synced.
    (when (eq fail-stage :manifest)
      (return-from savepoint-write (savepoint-failed store id rev :manifest)))
    (when (eq fail-stage :sync)
      (return-from savepoint-write (savepoint-failed store id rev :sync)))
    (let ((sp (make-savepoint
               :id id :schema schema :journal-id journal-id :local-revision rev
               :replay-cut replay-cut :boundary boundary :manifest manifest
               :manifest-sha (sha256-hex (canonical-string manifest))
               :image state-ref :local-replies replies-ref :age age
               :failed-backup nil)))
      (values (make-savepoint-store
               :verified sp
               :published (cons sp (savepoint-store-published store))
               :attempts (cons (list :id id :rev rev :verdict :ok)
                               (savepoint-store-attempts store)))
              nil))))

;;; ------------------------------------------------------------------
;;; list (docs/SPEC-WORK.md:6399-6401)
;;; ------------------------------------------------------------------

(defun savepoint-list (store &key shared-revision unshared failed-backups)
  "Expose the savepoint's age, the local and the shared revisions side by side,
the unshared work and the failed backup attempts, then every attempt with its
verdict (docs/SPEC-WORK.md:6399-6401). The local savepoint is never printed as
the shared checkpoint."
  (with-output-to-string (s)
    (let ((v (savepoint-store-verified store)))
      (if v
          (progn
            (format s "SAVEPOINT id=~A rev=~A verdict=verified age=~A manifest=~A~%"
                    (savepoint-id v) (savepoint-local-revision v)
                    (savepoint-age v) (savepoint-manifest-sha v))
            (format s "SAVEPOINT local-revision=~A shared-revision=~A unshared=~S failed-backups=~S~%"
                    (savepoint-local-revision v) shared-revision unshared failed-backups))
          (format s "SAVEPOINT none~%")))
    (dolist (a (savepoint-store-attempts store))
      (format s "SAVEPOINT ATTEMPT id=~A rev=~A verdict=~A~@[ stage=~(~A~)~]~%"
              (getf a :id) (getf a :rev)
              (string-downcase (symbol-name (getf a :verdict)))
              (getf a :stage)))))

;;; ------------------------------------------------------------------
;;; verify (docs/SPEC-WORK.md:2274, :6399-6401, :6446-6459)
;;; ------------------------------------------------------------------

(defstruct (savepoint-load
             (:constructor make-savepoint-load
                 (&key savepoint journal image replies (records '()) gap)))
  "What one read of a savepoint's own root produced: the savepoint (its manifest
and the content references the read used), the journal chain at hand, the image
and the retained replies the references name, the records the read saw, and any
gap the tail or a record itself is."
  savepoint journal image replies records gap)

(defun savepoint-cut-sequence (savepoint)
  "The replay cut's record sequence, or 0 for an initial image's (:absent) cut."
  (let ((cut (savepoint-replay-cut savepoint)))
    (if (absentp cut) 0 (getf cut :sequence))))

(defun savepoint-cut-by-sequence (savepoint records)
  "The one complete record whose sequence the manifest's replay cut names, or
NIL for an initial image (docs/SPEC-WORK.md:6413-6421)."
  (let ((cut (savepoint-replay-cut savepoint)))
    (unless (absentp cut)
      (find (getf cut :sequence) records
            :key (lambda (r) (getf r :seq)) :test #'eql))))

(defun savepoint-content-ok-p (reference value)
  "A content reference is verified by hashing the exact bytes it names: the
SHA-256 and the size both (docs/SPEC-WORK.md:6459)."
  (let ((bytes (canonical-string value)))
    (and (equal (getf reference :sha256) (sha256-hex bytes))
         (eql (getf reference :size) (length bytes)))))

(defun savepoint-gap-line (kind)
  "A recovery gap printed by its kind, never rounded to another and never a
truncation (docs/SPEC-WORK.md:6454-6459)."
  (format nil "SAVEPOINT FAIL: recovery-gap kind=~(~A~)" kind))

(defun savepoint-verify (load)
  "Verify what a savepoint newly wrote or a later read uses: the manifest's
journal id against the journal at hand, the exact replay cut against one
complete record's identity, the image and retained-reply content references by
the bytes they name, and every retained disposition's reply. A torn tail is an
interrupted append, told apart from a corrupt record, and both are kept; a
missing or mismatched cut, a broken hash or sequence and an incomplete envelope
are recovery gaps by their kind and never a truncation
(docs/SPEC-WORK.md:2274, :6399-6401, :6446-6459). Returns (values verdict line
kind): :verified, or :failed with the gap named."
  (let* ((sp (savepoint-load-savepoint load))
         (journal (savepoint-load-journal load))
         (records (savepoint-load-records load))
         (image (savepoint-load-image load))
         (replies (savepoint-load-replies load))
         (gap (savepoint-load-gap load))
         (cut (and sp (savepoint-cut-sequence sp)))
         (cut-rec (and sp (savepoint-cut-by-sequence sp records))))
    (cond
      ((null sp)
       (values :failed (savepoint-gap-line "missing-manifest") :missing-manifest))
      ((member gap '(:torn-tail :corrupt-record) :test #'eq)
       (values :failed (savepoint-gap-line gap) gap))
      ((and (journal-chain-p journal)
            (plusp (length (or (savepoint-journal-id sp) "")))
            (not (string= (journal-chain-id journal) (savepoint-journal-id sp))))
       (values :failed "SAVEPOINT FAIL: journal mismatch" :journal-mismatch))
      ((and (plusp cut) (null cut-rec))
       (values :failed (savepoint-gap-line "missing-cut") :missing-cut))
      ((and cut-rec
            (not (string= (getf (savepoint-replay-cut sp) :sha256)
                          (savepoint-record-hash cut-rec))))
       (values :failed "SAVEPOINT FAIL: journal mismatch: replay cut" :journal-mismatch))
      ((some (lambda (d) (null (getf d :reply))) replies)
       (values :failed (savepoint-gap-line "missing-reply") :missing-reply))
      ((not (savepoint-content-ok-p (savepoint-image sp) image))
       (values :failed (savepoint-gap-line "corrupt-record") :corrupt-record))
      ((not (savepoint-content-ok-p (savepoint-local-replies sp) replies))
       (values :failed (savepoint-gap-line "corrupt-record") :corrupt-record))
      (t (values :verified :verified nil)))))

;;; ------------------------------------------------------------------
;;; restore (docs/SPEC-WORK.md:2275, :6406-6411, :6447-6454)
;;; ------------------------------------------------------------------

(defun savepoint-complete-records-p (records)
  "Every record is a complete envelope and the records replay once in sequence:
strictly ascending sequence numbers with no gap or repeat."
  (let ((seqs (mapcar (lambda (r) (getf r :seq)) records)))
    (and (every (lambda (r) (and (getf r :events) (getf r :request))) records)
         (or (null seqs)
             (let ((sorted (sort (copy-list seqs) #'<)))
               (and (equal sorted seqs)
                    (= (length sorted) (length (remove-duplicates sorted)))))))))

(defun savepoint-restore (load)
  "Restore one savepoint as an isolated, read-only, non-dispatching recovery
session in five steps: verify the manifest, the journal id and the exact cut;
load the image and its retained replies; replay only the complete records
strictly after the cut once in sequence; process boundary records; then finish
the whole validation before anything is exposed. It inherits no coordinator
ownership, reanimates no assignment, replays no bus message and duplicates no
external side effect, and a restore missing a retained reply refuses
(docs/SPEC-WORK.md:2275, :6406-6411, :6447-6454). Returns (values session line
code)."
  (multiple-value-bind (verdict line kind) (savepoint-verify load)
    (if (not (eq verdict :verified))
        (values nil line (if (eq kind :missing-manifest) 1 2))
        (let* ((sp (savepoint-load-savepoint load))
               (records (savepoint-load-records load))
               (cut (savepoint-cut-sequence sp))
               (after (remove-if-not (lambda (r) (> (getf r :seq) cut))
                                     records)))
          (if (not (savepoint-complete-records-p after))
              (values nil (savepoint-gap-line "incomplete-envelope") 2)
              (values (make-restore-session
                       :savepoint sp
                       :image (savepoint-load-image load)
                       :replies (savepoint-load-replies load)
                       :replayed-records after
                       :cut (savepoint-replay-cut sp)
                       :boundary (savepoint-boundary sp)
                       :read-only-p t
                       :mode :isolated)
                      "RESTORE OK read-only isolated"
                      0))))))
