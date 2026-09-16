;;;; s3-lease.lisp --- the lease: take, heartbeat, release, and the lease log.
;;;;
;;;; docs/SPEC-WORK.md:1348-1384 (*The lease*) and :2408-2432 (*What a mutation
;;;; does* for the lease verbs) are the source. This slice is pure: the lease
;;;; log is a record here, the verbs are functions over it, and the wiring into
;;;; the command thread / kernel is a later card's work.
;;;;
;;;; A lease is an event. Its current state is derived from its :lease,
;;;; :heartbeat, :release and :handoff events (SPEC-WORK.md:1350-1351). Expiry is
;;;; derived and never stored (SPEC-WORK.md:1348-1350); a live lease per node is
;;;; the rule, a second take names the holder.

(in-package #:nova-work)

;;; ------------------------------------------------------------------
;;; Stamps and durations. The only arithmetic the lease needs: compare two
;;; ISO-8601 stamps and add a duration ("30m", "2h", "1d", "90s") to one. The
;;; stamps this slice sees are the fixed-width "YYYY-MM-DDTHH:MM:SSZ", so they
;;; are comparable byte-for-byte, but the deadline arithmetic uses a seconds
;;; form so a duration lands on a real stamp.
;;; ------------------------------------------------------------------

(defun stamp< (a b) (string< a b))
(defun stamp> (a b) (string> a b))
(defun stamp>= (a b) (not (string< a b)))
(defun stamp<= (a b) (not (string> a b)))

(defun days-from-civil (y m d)
  "Days since 1970-01-01, civil calendar (Howard Hinnant's algorithm)."
  (let ((y (if (<= m 2) (1- y) y)))
    (let* ((era (floor y 400))
           (yoe (- y (* era 400)))
           (mp (if (> m 2) (- m 3) (+ m 9)))
           (doy (+ (floor (+ (* 153 mp) 2) 5) d -1))
           (doe (+ (* yoe 365) (floor yoe 4) (- (floor yoe 100)) doy)))
      (+ (- (* era 146097) 719468) doe))))

(defun civil-from-days (z)
  "Inverse of DAYS-FROM-CIVIL: returns (values y m d)."
  (let ((z (+ z 719468)))
    (let* ((era (if (>= z 0) (floor z 146097) (floor (- z 146096) 146097)))
           (doe (- z (* era 146097)))
           (yoe (floor (+ doe (- (floor doe 1460)) (floor doe 36524)
                            (- (floor doe 146096)))
                       365))
           (y (+ yoe (* era 400)))
           (doy (- doe (+ (* yoe 365) (floor yoe 4) (- (floor yoe 100)))))
           (mp (floor (+ (* 5 doy) 2) 153))
           (d (+ (- doy (floor (+ (* 153 mp) 2) 5)) 1))
           (m (+ mp (if (< mp 10) 3 -9))))
      (values (+ y (if (<= m 2) 1 0)) m d))))

(defun stamp-fields (s)
  ;; "YYYY-MM-DDTHH:MM:SSZ" -> (list y mo d h mi sec)
  (list (parse-integer s :start 0 :end 4)
        (parse-integer s :start 5 :end 7)
        (parse-integer s :start 8 :end 10)
        (parse-integer s :start 11 :end 13)
        (parse-integer s :start 14 :end 16)
        (parse-integer s :start 17 :end 19)))

(defun format-stamp-fields (f)
  (destructuring-bind (y mo d h mi sec) f
    (format nil "~4,'0D-~2,'0D-~2,'0DT~2,'0D:~2,'0D:~2,'0DZ"
            y mo d h mi sec)))

(defun stamp-seconds (s)
  (destructuring-bind (y mo d h mi sec) (stamp-fields s)
    (+ (* 86400 (days-from-civil y mo d)) (* 3600 h) (* 60 mi) sec)))

(defun seconds-stamp (n)
  (let* ((d (floor n 86400))
         (rem (- n (* d 86400)))
         (h (floor rem 3600))
         (rem (- rem (* h 3600)))
         (mi (floor rem 60))
         (sec (- rem (* mi 60))))
    (multiple-value-bind (y mo dd) (civil-from-days d)
      (format-stamp-fields (list y mo dd h mi sec)))))

;;; parse-duration is defined once by the kernel (src/session.lisp) and reused
;;; here: it is a superset of the duration words this slice needs.

(defun stamp-word-p (s)
  "A `--by` value is either a duration word or an absolute stamp (has a T)."
  (and (stringp s) (find #\T s)))

(defun resolve-deadline (by now)
  (if (stamp-word-p by)
      by
      (seconds-stamp (+ (stamp-seconds now) (parse-duration by)))))

(defun format-distance (seconds)
  "A human age, kept coarse: the tests and rows need a stable word, not a clock."
  (cond ((< seconds 60) (format nil "~Ds" seconds))
        ((< seconds 3600) (format nil "~Dm" (floor seconds 60)))
        ((< seconds 86400) (format nil "~Dh" (floor seconds 3600)))
        (t (format nil "~Dd" (floor seconds 86400)))))

;;; ------------------------------------------------------------------
;;; The lease event and the lease log.
;;; ------------------------------------------------------------------

(defstruct (lease-event (:constructor make-lease-event
                     (&key kind node id by stamp clock deadline default evidence to rev)))
  kind      ; :lease | :heartbeat | :release | :handoff
  node      ; the node id the lease is on
  id        ; the lease id this event names (the lease's own id on :lease)
  by        ; the author / holder making the event
  stamp     ; the stamp (an ISO string)
  clock     ; :tool | :given (kept for the record, not read here)
  deadline  ; on :lease and :handoff
  default   ; on :lease and :handoff: :release | :extend-once | (:escalate "<name>")
  evidence  ; on :heartbeat: the pointer
  to        ; on :handoff: the new holder
  rev)      ; this event's revision (its identity within the log)

(defstruct (lease-log (:constructor %make-lease-log (events next-rev)))
  events    ; lease events, oldest first
  next-rev) ; one past the newest revision

(defun make-lease-log ()
  (%make-lease-log '() 1))

(defun lease-log-append (log event)
  (%make-lease-log (append (lease-log-events log) (list event))
                   (1+ (lease-event-rev event))))

(defun fresh-lease-id ()
  (format nil "l-~8,'0X" (random #x100000000)))

;;; ------------------------------------------------------------------
;;; Derived reads. None of these visit a node of O; they read the lease log
;;; alone, which is what makes W read with zero visits into O.
;;; ------------------------------------------------------------------

(defun active-lease (log node)
  "The newest :lease/:handoff on NODE with no later :release/:handoff, or NIL.
  Expiry is NOT applied here: expiry is a read-time derived fact, never stored."
  (let ((active nil))
    (dolist (e (lease-log-events log))
      (when (and (equal node (lease-event-node e))
                 (member (lease-event-kind e) '(:lease :handoff :release)))
        (case (lease-event-kind e)
          ((:lease :handoff) (setf active e))
          (:release (setf active nil)))))
    active))

(defun lease-holder-name (active)
  "The holder of an active lease event: its author, or its :to after a handoff."
  (if (eq (lease-event-kind active) :handoff)
      (lease-event-to active)
      (lease-event-by active)))

(defun lease-live-p (log node now)
  "A live lease: an active lease whose (possibly once-extended) deadline is at or
  ahead of NOW. Expired is the complement, and it is never stored."
  (let ((active (active-lease log node)))
    (when active
      (let ((dl (lease-event-deadline active)))
        (cond ((stamp>= dl now) t)
              ((eq (lease-event-default active) :extend-once)
               ;; Extend once by the original length. (The second-expiry half of
               ;; extend-once is left to the wiring card; see notes.)
               (let ((orig (- (stamp-seconds dl) (stamp-seconds (lease-event-stamp active)))))
                 (stamp< now (seconds-stamp (+ (stamp-seconds dl) orig)))))
              (t nil))))))

(defun lease-holder (log node &key now)
  "The current holder as a string, or NIL when the node reads unowned (no live
  lease, or an expired one)."
  (let ((active (active-lease log node)))
    (when (and active (lease-live-p log node now))
      (lease-holder-name active))))

(defun latest-heartbeat (log node)
  (let ((h nil))
    (dolist (e (lease-log-events log))
      (when (and (eq (lease-event-kind e) :heartbeat) (equal node (lease-event-node e)))
        (setf h e)))
    h))

(defun heartbeat-age (log node now)
  (let ((h (latest-heartbeat log node)))
    (if h
        (format-distance (- (stamp-seconds now) (stamp-seconds (lease-event-stamp h))))
        "none")))

(defun worked-now-p (log node now window)
  "Working-now means a heartbeat inside --window (SPEC-WORK.md:1375-1376)."
  (let ((h (latest-heartbeat log node)))
    (and h
         (stamp>= (lease-event-stamp h)
                  (seconds-stamp (- (stamp-seconds now) (parse-duration window)))))))

(defun held-not-worked-p (log node now window)
  (and (lease-live-p log node now)
       (not (worked-now-p log node now window))))

(defun lease-escalated-to (log node now)
  "At expiry an (:escalate \"<name>\") default names the escalation. NIL otherwise."
  (let ((active (active-lease log node)))
    (when (and active (not (lease-live-p log node now)))
      (let ((d (lease-event-default active)))
        (when (and (consp d) (eq (car d) :escalate))
          (second d))))))

(defun working-lease-p (log node now)
  (lease-live-p log node now))

;;; ------------------------------------------------------------------
;;; The three verbs. Each is pure: (values OK-P LINE NEW-LOG). On a refusal OK-P
;;; is NIL, LINE is the FAIL line, and NEW-LOG is the un-appended log.
;;; ------------------------------------------------------------------

(defun normalize-default (d)
  "The wire `escalate:<name>` arrives already as (:escalate \"<name>\") here; the
  keyword forms arrive as :release / :extend-once."
  d)

(defun default-wire (d)
  (cond ((null d) "-")
        ((eq d :release) "release")
        ((eq d :extend-once) "extend-once")
        ((and (consp d) (eq (car d) :escalate))
         (format nil "escalate:~A" (second d)))
        (t (princ-to-string d))))

(defun lease-ok-line (word id node rev holder deadline default live)
  (format nil "~A OK id=~A request=- node=~A rev=~D pushed=- changed=1 holder=~A deadline=~A default=~A live=~D"
          word id node rev holder deadline default live))

(defun lease-take (log &key node as by default now (id nil idp) request)
  "One live lease per node. A second take is refused naming the holder."
  (declare (ignore request))
  (unless by
    (return-from lease-take
      (values nil (format nil "LEASE FAIL node=~A: no --by" node) log)))
  (unless default
    (return-from lease-take
      (values nil (format nil "LEASE FAIL node=~A: no --default" node) log)))
  (let ((live (lease-live-p log node now)))
    (when live
      (let ((active (active-lease log node)))
        (return-from lease-take
          (values nil
                  (format nil "LEASE FAIL node=~A holder=~A since=~A deadline=~A live=1: held"
                          node (lease-holder-name active)
                          (lease-event-stamp active) (lease-event-deadline active))
                  log)))))
  (let* ((lease-id (if idp id (fresh-lease-id)))
         (deadline (resolve-deadline by now))
         (event (make-lease-event :kind :lease :node node :id lease-id :by as
                             :stamp now :clock :tool
                             :deadline deadline :default (normalize-default default)
                             :rev (lease-log-next-rev log))))
    (let ((new-log (lease-log-append log event)))
      (values t (lease-ok-line "LEASE" lease-id node (lease-event-rev event)
                               as deadline (default-wire default) 1)
              new-log))))

(defun heartbeat-lease (log &key node as evidence now request)
  "A heartbeat is the holder's alone. A third party's is refused naming the holder."
  (declare (ignore request))
  (let ((active (active-lease log node)))
    (unless active
      (return-from heartbeat-lease
        (values nil (format nil "HEARTBEAT FAIL node=~A holder=- since=- deadline=- live=0: unheld"
                            node)
                log)))
    (let ((holder (lease-holder-name active)))
      (unless (string= holder as)
        (return-from heartbeat-lease
          (values nil (format nil "HEARTBEAT FAIL node=~A holder=~A since=~A deadline=~A live=~D: held"
                              node holder (lease-event-stamp active) (lease-event-deadline active)
                              (if (lease-live-p log node now) 1 0))
                  log))))
    (let ((event (make-lease-event :kind :heartbeat :node node :id (lease-event-id active)
                              :by as :stamp now :clock :tool :evidence evidence
                              :rev (lease-log-next-rev log))))
      (let ((new-log (lease-log-append log event)))
        (values t (format nil "HEARTBEAT OK id=~A request=- node=~A rev=~D pushed=- changed=1 lease=~A"
                          (lease-event-id active) node (lease-event-rev event) (lease-event-id active))
                new-log)))))

(defun lease-release (log &key node as handed by default now (id nil idp) request)
  "A release ends the holder's claim. `--handed` passes it: a :handoff event naming
  the new holder with a fresh deadline and default. A release whose --as is not the
  holder is refused naming the holder (SPEC-WORK.md:1378-1383)."
  (declare (ignore request))
  (let ((active (active-lease log node)))
    (unless active
      (return-from lease-release
        (values nil (format nil "LEASE FAIL node=~A holder=- since=- deadline=- live=0: unheld"
                            node)
                log)))
    (let ((holder (lease-holder-name active)))
      (unless (string= holder as)
        (return-from lease-release
          (values nil (format nil "LEASE FAIL node=~A holder=~A since=~A deadline=~A live=~D: held"
                              node holder (lease-event-stamp active) (lease-event-deadline active)
                              (if (lease-live-p log node now) 1 0))
                  log))))
    (if handed
        (progn
          (unless (and by default)
            (return-from lease-release
              (values nil (format nil "RELEASE FAIL node=~A: --handed needs --by and --default" node)
                      log)))
          (let* ((lease-id (if idp id (fresh-lease-id)))
                 (deadline (resolve-deadline by now))
                 (event (make-lease-event :kind :handoff :node node :id lease-id :by as
                                     :stamp now :clock :tool
                                     :deadline deadline :default (normalize-default default)
                                     :to handed :rev (lease-log-next-rev log))))
            (let ((new-log (lease-log-append log event)))
              (values t (format nil "RELEASE OK id=~A request=- node=~A rev=~D pushed=- changed=1 to=~A holder=~A deadline=~A default=~A live=1"
                                lease-id node (lease-event-rev event) handed handed deadline
                                (default-wire default))
                      new-log))))
        (let ((event (make-lease-event :kind :release :node node :id (lease-event-id active)
                                  :by as :stamp now :clock :tool
                                  :rev (lease-log-next-rev log))))
          (let ((new-log (lease-log-append log event)))
            (values t (format nil "RELEASE OK id=~A request=- node=~A rev=~D pushed=- changed=1"
                              (lease-event-id active) node (lease-event-rev event))
                    new-log))))))

;;; ------------------------------------------------------------------
;;; The settle cascade's lease half, and the removal refusal. A wiring card calls
;;; these; neither touches the kernel here.
;;; ------------------------------------------------------------------

(defun lease-release-on-settle (log &key node as now)
  "The settle envelope carries a :release for a live lease, written by the settling
  author and naming the holder (SPEC-WORK.md:1674-1680). The lease log stays whole
  for `handoffs`; the node reads holder=unowned afterwards."
  (let ((active (active-lease log node)))
    (if active
        (multiple-value-bind (ok line new-log)
            (lease-release log :node node :as as :now now)
          (declare (ignore ok line))
          new-log)
        log)))

(defun leased-node-refusal (log &key node now)
  "`node remove` refuses a node holding a live lease and names the holder. Returns
  (values HELD-P HOLDER). NIL and NIL when the node is unleased or expired."
  (let ((active (active-lease log node)))
    (when (and active (lease-live-p log node now))
      (values t (lease-holder-name active)))))

;;; ------------------------------------------------------------------
;;; The query rows themselves (who / stale / handoffs). Rows are plists; the OK
;;; line is assembled by the caller over the returned counts.
;;; ------------------------------------------------------------------

(defun holder-word (log node now)
  (let ((h (lease-holder log node :now now)))
    (or h "unowned")))

(defun who-row-form (log node active &key now window)
  (list :id node
        :lease (lease-event-id active)
        :holder (holder-word log node now)
        :heartbeat (heartbeat-age log node now)
        :deadline (lease-event-deadline active)
        :default (default-wire (lease-event-default active))
        :escalated-to (or (lease-escalated-to log node now) "-")
        :responsible "-"))

(defun who-rows (log &key now window)
  "The nodes of W: the open ids holding a live lease, in lease-log order."
  (let ((rows '()))
    (dolist (e (lease-log-events log))
      ;; Emit one row per node that holds a live lease, at its newest active lease.
      (when (and (member (lease-event-kind e) '(:lease :handoff))
                 (eq e (active-lease log (lease-event-node e)))
                 (lease-live-p log (lease-event-node e) now))
        (pushnew (lease-event-node e) rows :test #'string=)))
    (mapcar (lambda (node)
              (who-row-form log node (active-lease log node) :now now :window window))
            (nreverse rows))))

(defun stale-rows (log &key now window)
  "The requeue list: live leases that are held but not worked (no heartbeat inside
  --window), in lease-log order."
  (let ((rows '()))
    (dolist (e (lease-log-events log))
      (when (and (member (lease-event-kind e) '(:lease :handoff))
                 (eq e (active-lease log (lease-event-node e)))
                 (held-not-worked-p log (lease-event-node e) now window))
        (pushnew (lease-event-node e) rows :test #'string=)))
    (mapcar (lambda (node)
              (who-row-form log node (active-lease log node) :now now :window window))
            (nreverse rows))))

(defun stale-by-holder (log &key now window)
  "Group the stale (held-not-worked) nodes by holder: an alist of (holder . node-ids)."
  (let ((groups '()))
    (dolist (row (stale-rows log :now now :window window))
      (let ((holder (getf row :holder)))
        (let ((entry (assoc holder groups :test #'string=)))
          (if entry
              (push (getf row :id) (cdr entry))
              (push (list holder (getf row :id)) groups)))))
    (mapcar (lambda (g) (cons (car g) (nreverse (cdr g)))) (nreverse groups))))

(defun handoff-row-form (e)
  (list :lease (lease-event-id e)
        :node (lease-event-node e)
        :kind (lease-event-kind e)
        :rev (lease-event-rev e)
        :at (lease-event-stamp e)
        :from (lease-event-by e)
        :to (or (lease-event-to e) "-")
        :deadline (or (lease-event-deadline e) "-")
        :default (default-wire (lease-event-default e))))

(defun handoff-rows (log &key (since 0) (after nil))
  "The lease log, as transition rows. SINCE is the start revision; AFTER is a
  continuation cursor (the last printed row's rev), so a later page reads only the
  rows after it and never drifts across a settle that landed in between."
  (let ((lo (if after (max since after) since)))
    (sort (loop for e in (lease-log-events log)
                when (> (lease-event-rev e) lo)
                  collect (handoff-row-form e))
          #'< :key (lambda (r) (getf r :rev)))))
