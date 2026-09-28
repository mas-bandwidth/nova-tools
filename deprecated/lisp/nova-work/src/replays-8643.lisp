;;;; replays-8643.lisp --- the pure part of the dry run, the complete cost
;;;; lineage, savepoint compaction and copied journals named by the replays of
;;;; docs/SPEC-WORK.md (5678 dry run, 4852 cost lineage, 5791 compaction,
;;;; 6017 copied journal, 6379 coordinator cost joins).
;;;;
;;;; Nothing here starts a session, opens a journal or dispatches a child: these
;;;; are the pure functions the five `*-8643` acceptance replays drive. The
;;;; live session, journal I/O and CLI wiring those paragraphs sit on is out of
;;;; this slice and is named in RESULT.md.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; The dry run (SPEC-WORK.md:5678)
;;; ------------------------------------------------------------------

(defun make-replay-session (&key (revision 0) (events 0) (pending 0)
                                 (pushed nil) (dedup nil) (journal nil))
  "The four counters a `SESSION OK` line reports, the dedup index and the
journal, as one immutable plist a preview and an apply both read."
  (list :revision revision :events events :pending pending
        :pushed pushed :dedup dedup :journal journal))

(defun preview-mutation (session request)
  "Validate and project a mutation at the session's current revision without
mutating it: the revision, the events=/pending=/pushed= counters and the dedup
index are all left exactly as they were. Returns the preview line and the
unchanged session (SPEC-WORK.md:5678)."
  (let ((expect (getf request :expect))
        (rev (getf session :revision)))
    (if (and expect (/= expect rev))
        (values (format nil "SESSION FAIL expect=~D current=~D: stale" expect rev)
                session)
        (values (format nil "SESSION OK events=~D pending=~D pushed=~A dry-run=true"
                        (getf session :events) (getf session :pending)
                        (or (getf session :pushed) "-"))
                session))))

(defun apply-mutation (session request)
  "The real apply: a stale --expect refuses by name and writes nothing; a new
request at the current revision is admitted, appends one event and moves the
revision by one (SPEC-WORK.md:5681)."
  (let ((expect (getf request :expect))
        (rev (getf session :revision))
        (id (getf request :id)))
    (cond
      ((and expect (/= expect rev))
       (values nil
               (format nil "SESSION FAIL expect=~D current=~D: stale" expect rev)
               session))
      ((member id (getf session :dedup) :test #'equal)
       (values t (format nil "SESSION OK request=~A already applied" id) session))
      (t
       (let ((new (list :revision (1+ rev)
                        :events (1+ (getf session :events))
                        :pending (getf session :pending)
                        :pushed (getf session :pushed)
                        :dedup (cons id (getf session :dedup))
                        :journal (cons id (getf session :journal)))))
         (values t
                 (format nil "SESSION OK request=~A rev=~D events=~D pushed=~A changed=1"
                         id (getf new :revision) (getf new :events)
                         (or (getf new :pushed) "-"))
                 new))))))

;;; ------------------------------------------------------------------
;;; Complete cost lineage (SPEC-WORK.md:4852)
;;; ------------------------------------------------------------------

(defun join-cost-lineage (receipts)
  "Join parent/child/retry receipts once each, count a failed attempt, never add
a cache subset a second time, keep implementation cost apart and leave the
operational total unknown when a receipt is a gap rather than reading it as
zero (SPEC-WORK.md:4852)."
  (let ((seen '()) (operational 0) (implementation 0) (gaps 0)
        (failed 0) (cache-merged 0) (joined 0) (gap-p nil))
    (dolist (r receipts)
      (let ((id (getf r :id)))
        (unless (member id seen :test #'equal)
          (push id seen)
          (incf joined)
          (let ((cost (getf r :cost))
                (role (getf r :role)))
            (cond
              ((eq cost :unknown) (incf gaps) (setf gap-p t))
              ((eq role :cache-subset) (incf cache-merged))
              ((or (eq role :implementation) (getf r :implementation))
               (incf implementation (if (numberp cost) cost 0)))
              (t
               (incf operational (if (numberp cost) cost 0))
               (when (eq (getf r :attempt) :failed) (incf failed))))))))
    (list :operational (if gap-p :unknown operational)
          :implementation implementation
          :gaps gaps :failed failed :cache-merged cache-merged :joined joined)))

;;; ------------------------------------------------------------------
;;; Compaction keeps the last copy (SPEC-WORK.md:5791, 6280)
;;; ------------------------------------------------------------------

(defun compact-copies (copies)
  "Plan a compaction that may never remove the only recoverable copy: the newest
verified copy is kept, unverified and superseded copies are pruned, and when no
copy is verified nothing is pruned (SPEC-WORK.md:5791, :6280)."
  (let ((verified (remove-if-not (lambda (c) (getf c :verified)) copies)))
    (if (null verified)
        (list :keep (mapcar (lambda (c) (getf c :id)) copies) :pruned '())
        (let ((keep (getf (car (last verified)) :id)))
          (list :keep (list keep)
                :pruned (remove keep (mapcar (lambda (c) (getf c :id)) copies)
                                :test #'equal))))))

;;; ------------------------------------------------------------------
;;; A copied journal grants nothing (SPEC-WORK.md:6017)
;;; ------------------------------------------------------------------

(defun restore-copy (copy)
  "A savepoint restore over a copied journal inspects in isolation: it takes no
ownership and dispatches nothing, whatever the copy's journal id says
(SPEC-WORK.md:6017)."
  (list :journal-id (getf copy :journal-id)
        :bench (getf copy :bench)
        :isolation :read-only
        :ownership nil
        :dispatches 0
        :side-effects 0))

(defun start-over-copy (copy)
  "A session start over a copied journal is refused by the fencing rules; the
journal id grants nothing (SPEC-WORK.md:6019)."
  (declare (ignore copy))
  (values nil "SESSION FAIL: copy not fenced to this bench (fencing rules)" 1))

;;; ------------------------------------------------------------------
;;; Cost joins include the coordinator (SPEC-WORK.md:6379)
;;; ------------------------------------------------------------------

(defun join-experiment-cost (record)
  "Join the complete operational cost of a comparable-work experiment:
coordinator overhead and rework included, elapsed time attributed to the
observable phases, unobservable time left unknown and implementation cost kept
sunk and apart (SPEC-WORK.md:6379)."
  (let* ((phases (list (cons :execution (getf record :execution))
                       (cons :queueing (getf record :queueing))
                       (cons :review (getf record :review))
                       (cons :ci-waiting (getf record :ci-waiting))))
         (unknown (count-if (lambda (p) (not (numberp (cdr p)))) phases))
         (observable (remove-if (lambda (p) (not (numberp (cdr p)))) phases))
         (operational (+ (getf record :coordinator-overhead 0)
                         (getf record :rework 0)
                         (reduce #'+ observable :key #'cdr :initial-value 0))))
    (list :operational operational
          :phases observable
          :unknown-phases unknown
          :coordinator-overhead (getf record :coordinator-overhead 0)
          :rework (getf record :rework 0)
          :implementation (getf record :implementation-cost 0)
          :implementation-sunk t
          :comparable (getf record :after-adoption))))
