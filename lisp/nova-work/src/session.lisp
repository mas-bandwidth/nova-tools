;;;; session.lisp --- resident session W1 kernel slice: ownership and lifecycle.
;;;;
;;;; Docs/SPEC-WORK.md:202-260:
;;;; - Ownership record (`OWNER`): coordinator name, generation, token, stamp, until, successor.
;;;; - Session start: evaluation of ownership claim (vacant take, expired take, resume, handoff, fenced).
;;;; - Admission check per request: compare clock to until; status and export admit exit 0 when fenced;
;;;;   all mutation and other queries refuse exit 1 fenced.
;;;; - Reconfirm: check tip == base, generation & token unchanged, completion before until.

(in-package #:nova-work)

;;; ----------------------------------------------------------------------
;;; Time and duration helpers
;;; ----------------------------------------------------------------------

(defun parse-duration (dur)
  "Parse a duration specifier like '30s', '5m', '1h', '500ms', or integer seconds."
  (typecase dur
    (integer dur)
    (number (round dur))
    (string
     (let ((len (length dur)))
       (cond
         ((zerop len) 0)
         ((and (> len 2) (string-equal (subseq dur (- len 2)) "ms"))
          (/ (parse-integer (subseq dur 0 (- len 2))) 1000))
         ((char-equal (char dur (1- len)) #\s)
          (parse-integer (subseq dur 0 (1- len))))
         ((char-equal (char dur (1- len)) #\m)
          (* 60 (parse-integer (subseq dur 0 (1- len)))))
         ((char-equal (char dur (1- len)) #\h)
          (* 3600 (parse-integer (subseq dur 0 (1- len)))))
         ((char-equal (char dur (1- len)) #\d)
          (* 86400 (parse-integer (subseq dur 0 (1- len)))))
         (t (parse-integer dur)))))
    (t 0)))

(defun parse-rfc3339 (str)
  "Parse RFC3339 timestamp string YYYY-MM-DDTHH:MM:SSZ into universal time integer."
  (unless (and (stringp str) (>= (length str) 19))
    (error "Invalid RFC3339 timestamp: ~S" str))
  (let ((year (parse-integer str :start 0 :end 4))
        (month (parse-integer str :start 5 :end 7))
        (day (parse-integer str :start 8 :end 10))
        (hour (parse-integer str :start 11 :end 13))
        (min (parse-integer str :start 14 :end 16))
        (sec (parse-integer str :start 17 :end 19)))
    (encode-universal-time sec min hour day month year 0)))

(defun format-rfc3339 (ut)
  "Format universal time integer into RFC3339 UTC timestamp string."
  (multiple-value-bind (sec min hour day month year)
      (decode-universal-time ut 0)
    (format nil "~4,'0D-~2,'0D-~2,'0DT~2,'0D:~2,'0D:~2,'0DZ"
            year month day hour min sec)))

;;; ----------------------------------------------------------------------
;;; Ownership record
;;; ----------------------------------------------------------------------

(defstruct (ownership-record (:conc-name owner-))
  (owner "" :type string)
  (generation 1 :type integer)
  (token "" :type string)
  (stamp "" :type string)
  (until "" :type string)
  (bench "" :type string)
  (successor nil))

(defun format-ownership-record (record)
  "Format an OWNERSHIP-RECORD into string."
  (format nil "owner=~A~%generation=~D~%token=~A~%stamp=~A~%until=~A~%bench=~A~@[~%successor=~A~]~%"
          (owner-owner record)
          (owner-generation record)
          (owner-token record)
          (owner-stamp record)
          (owner-until record)
          (owner-bench record)
          (owner-successor record)))

(defun parse-ownership-record (str)
  "Parse an OWNERSHIP-RECORD from string."
  (let ((rec (make-ownership-record)))
    (with-input-from-string (s str)
      (loop for line = (read-line s nil nil)
            while line
            do (let* ((trimmed (string-trim '(#\Space #\Tab #\Return #\Newline) line))
                      (eq-pos (position #\= trimmed))
                      (colon-pos (position #\: trimmed))
                      (sp-pos (position #\Space trimmed))
                      (sep-pos (or eq-pos colon-pos sp-pos)))
                 (when (and sep-pos (> (length trimmed) 0) (char/= (char trimmed 0) #\#))
                   (let ((key (string-downcase (string-trim '(#\Space #\Tab) (subseq trimmed 0 sep-pos))))
                         (val (string-trim '(#\Space #\Tab) (subseq trimmed (1+ sep-pos)))))
                     (cond
                       ((string= key "owner") (setf (owner-owner rec) val))
                       ((string= key "generation") (setf (owner-generation rec) (parse-integer val)))
                       ((string= key "token") (setf (owner-token rec) val))
                       ((string= key "stamp") (setf (owner-stamp rec) val))
                       ((string= key "until") (setf (owner-until rec) val))
                       ((string= key "bench") (setf (owner-bench rec) val))
                       ((string= key "successor") (setf (owner-successor rec) val))))))))
    rec))

(defun evaluate-ownership-claim (current as-owner &key now (every "30s") (skew "5s")
                                                     token journal-token
                                                     (my-bench "") (lock-held t))
  "Evaluate an ownership claim by AS-OWNER against CURRENT ownership record.
Returns (values action record line exit-code), where action is :take, :resume, or :fenced.
The resume predicate (SPEC-WORK.md:198-204) is: the record names me, my journal
holds its token, my bench wrote that journal (MY-BENCH equals the record's bench),
and I hold the journal's lock (LOCK-HELD). So a copied journal -- same owner and
token on a different bench -- or a journal whose lock is not held is refused
rather than resumed."
  (let* ((now-str (or now (format-rfc3339 (get-universal-time))))
         (now-ut (parse-rfc3339 now-str))
         (every-sec (parse-duration every))
         (skew-sec (parse-duration skew))
         (tok (or token (format nil "tok-~A" (random 100000000))))
         (until-ut (+ now-ut (* 2 every-sec)))
         (until-str (format-rfc3339 until-ut)))
    (cond
      ;; 1. Vacant record: taking generation 1
      ((null current)
       (let ((rec (make-ownership-record
                   :owner as-owner
                   :generation 1
                   :token tok
                   :stamp now-str
                   :until until-str
                   :bench my-bench)))
         (values :take rec
                 (format nil "SESSION OK owner=~A generation=1 until=~A" as-owner until-str)
                 0)))

      ;; 2. Successor handoff: wait neither until nor skew, take next generation
      ((and (owner-successor current)
            (string= (owner-successor current) as-owner))
       (let* ((next-gen (1+ (owner-generation current)))
              (rec (make-ownership-record
                    :owner as-owner
                    :generation next-gen
                    :token tok
                    :stamp now-str
                    :until until-str
                    :bench my-bench)))
         (values :take rec
                 (format nil "SESSION OK owner=~A generation=~D until=~A" as-owner next-gen until-str)
                 0)))

      ;; 3. Resume: same owner, matching token in journal. The full predicate
      ;;    also demands the bench match and the journal's lock be held.
      ((and (string= (owner-owner current) as-owner)
            (or (and journal-token (string= (owner-token current) journal-token))
                (and (not journal-token) token (string= (owner-token current) token))))
       (cond
         ;; 3a. A copied journal: the bench that wrote this record is not mine.
         ((and (plusp (length (owner-bench current)))
               (not (string= (owner-bench current) my-bench)))
          (values :fenced current
                  (format nil "SESSION FAIL owner=~A generation=~D bench=~A: copied journal, not resumed"
                          (owner-owner current)
                          (owner-generation current)
                          (owner-bench current))
                  1))
         ;; 3b. The journal's lock is held by someone else (or not at all).
         ((not lock-held)
          (values :fenced current
                  (format nil "SESSION FAIL owner=~A generation=~D: journal lock not held"
                          (owner-owner current)
                          (owner-generation current))
                  1))
         ;; 3c. The whole predicate holds: resume, keeping generation and token.
         (t
          (let* ((gen (owner-generation current))
                 (rec-tok (owner-token current))
                 (rec (make-ownership-record
                       :owner as-owner
                       :generation gen
                       :token rec-tok
                       :stamp now-str
                       :until until-str
                       :bench (owner-bench current))))
            (values :resume rec
                    (format nil "SESSION OK owner=~A generation=~D until=~A" as-owner gen until-str)
                    0)))))

      ;; 4. Expired: until + skew < now
      ((< (+ (parse-rfc3339 (owner-until current)) skew-sec) now-ut)
       (let* ((next-gen (1+ (owner-generation current)))
              (rec (make-ownership-record
                    :owner as-owner
                    :generation next-gen
                    :token tok
                    :stamp now-str
                    :until until-str
                    :bench my-bench)))
         (values :take rec
                 (format nil "SESSION OK owner=~A generation=~D until=~A" as-owner next-gen until-str)
                 0)))

      ;; 5. Competing live owner: fenced
      (t
       (values :fenced current
               (format nil "SESSION FAIL owner=~A generation=~D until=~A held"
                       (owner-owner current)
                       (owner-generation current)
                       (owner-until current))
               1)))))

;;; ----------------------------------------------------------------------
;;; Session struct and operations
;;; ----------------------------------------------------------------------

(defstruct session
  (path "" :type string)
  (owner "" :type string)
  (generation 1 :type integer)
  (token "" :type string)
  (until "" :type string)
  (state :live :type keyword)  ; :live, :fenced, :stopped
  (kernel nil)
  (file "" :type string)
  (base "" :type string)
  (journal "" :type string)
  (every "30s")
  (skew "5s")
  (max-bytes 104857600 :type integer)
  (max-depth 64 :type integer)
  (max-nodes 100000 :type integer)
  (index-cache 256 :type integer)
  (page-bytes 4096 :type integer)
  (page-records 128 :type integer)
  (closed-window "14d")
  (clip-cadence "5m")
  ;; The remaining SESSION OK fields of the output grammar
  ;; (SPEC-WORK.md:5346): the clip cadence, the retention window and the
  ;; counters the resident session reads rather than remembers.
  (clip-every "5m")
  (clip-after 0 :type integer)
  (retain "14d")
  (pushed "-")
  (boundary 0 :type integer)
  (pending 0 :type integer)
  (findings 0 :type integer)
  (parses 0 :type integer)
  (replays 0 :type integer)
  (emitted 0 :type integer)
  (repair nil)
  (cache "" :type string)
  (verification nil))

(defun session-check-admission (sess verb &key now)
  "Check admission for a request on SESS with VERB at timestamp NOW.
Returns (values admitted-p reason exit-code)."
  (let ((now-ut (if now (parse-rfc3339 now) (get-universal-time)))
        (until-ut (parse-rfc3339 (session-until sess))))
    ;; If clock is past until, self-fence immediately
    (when (> now-ut until-ut)
      (setf (session-state sess) :fenced))
    (cond
      ;; Status and export verbs are admitted even when fenced, and exit 0
      ((member verb '(:status :export "status" "export"))
       (values t nil 0))
      ;; When fenced, all other verbs refuse with exit 1
      ((eq (session-state sess) :fenced)
       (values nil
               (format nil "SESSION FAIL fenced generation=~D until=~A"
                       (session-generation sess)
                       (session-until sess))
               1))
      ;; Admitted
      (t
       (values t nil 0)))))

(defun session-status (sess)
  "Report status of SESS. Exits 0 whether live or fenced."
  (let* ((owner (session-owner sess))
         (gen (session-generation sess))
         (st (session-state sess))
         (until (session-until sess))
         (base (session-base sess))
         (token (session-token sess))
         (line (format nil "SESSION OK owner=~A generation=~D token=~A state=~(~A~) until=~A base=~A every=~A skew=~A max-bytes=~D max-depth=~D max-nodes=~D index-cache=~D page-bytes=~D page-records=~D closed-window=~A"
                       owner gen token st until (if (or (null base) (zerop (length base))) "nil" base)
                       (session-every sess) (session-skew sess)
                       (session-max-bytes sess) (session-max-depth sess) (session-max-nodes sess)
                       (session-index-cache sess) (session-page-bytes sess) (session-page-records sess)
                       (session-closed-window sess))))
    (values t line 0)))

(defun session-submit (sess request &key now)
  "Submit REQUEST to SESS, checking admission first. Under `session start
--repair` a mutation goes through the repair gate before it reaches the
kernel (SPEC-WORK.md:2430-2440)."
  (let ((verb (getf request :verb)))
    (multiple-value-bind (admitted-p reason exit-code)
        (session-check-admission sess verb :now now)
      (unless admitted-p
        (return-from session-submit (values nil reason exit-code)))
      (if (and (session-repair sess)
               (not (member verb '(:status :export))))
          (%session-repair-submit sess request)
          (submit (session-kernel sess) request)))))

(defun %session-dry-run (kernel request)
  "Apply REQUEST to a private copy of KERNEL's O through a private kernel's own
writer and answer (values OK-P LINE EXIT-CODE CANDIDATE-STATE). The resident O,
its journal and its revision are untouched: the validate-on-a-private-copy,
then-publish shape of `atomic-batch-run` (src/transport.lisp)."
  (let ((clone (make-kernel :state (copy-state (kernel-state kernel))
                            :rev-base (kernel-next-rev kernel))))
    (unwind-protect
         (multiple-value-bind (okp line code) (submit clone request)
           (values okp line code (kernel-state clone)))
      ;; Stop the private kernel's command thread; it served one command.
      (sb-thread:with-mutex ((kernel-q-lock clone))
        (setf (kernel-closed-p clone) t)
        (sb-thread:condition-broadcast (kernel-q-cvar clone))))))

(defun %session-repair-submit (sess request)
  "The repair session's mutation path (SPEC-WORK.md:2430-2440): the candidate
is the resident O with the event applied, built on a private copy; it is
published through the kernel's one writer only when `session-repair-gate`
admits it, and refused at exit 1 with the `no repair` line otherwise, changing
nothing. An admitted event updates the session's finding count, and at zero
the session leaves repair and takes the ordinary gate thereafter."
  (let ((kernel (session-kernel sess)))
    (multiple-value-bind (okp line code candidate) (%session-dry-run kernel request)
      (unless okp
        (return-from %session-repair-submit (values nil line code)))
      (multiple-value-bind (admitted rline rcode)
          (session-repair-gate sess candidate :node (getf request :node))
        (unless admitted
          (return-from %session-repair-submit (values nil rline rcode))))
      (multiple-value-bind (okp line code envelope) (submit kernel request)
        (when okp
          (setf (session-findings sess)
                (length (cow-load-findings (kernel-state kernel))))
          (when (zerop (session-findings sess))
            (setf (session-repair sess) nil)))
        (values okp line code envelope)))))

(defun session-reconfirm (sess tip-sha &key now owner-record)
  "Reconfirm SESS against TIP-SHA and OWNER-RECORD on the branch.
Checks completion before until, tip == base, and OWNER generation/token."
  (let ((now-ut (if now (parse-rfc3339 now) (get-universal-time)))
        (until-ut (parse-rfc3339 (session-until sess))))
    ;; 1. Check completion deadline: must complete strictly before until.
    ;; An owner unable to reconfirm before until is fenced at the exact
    ;; boundary, so now == until already refuses.
    (when (>= now-ut until-ut)
      (setf (session-state sess) :fenced)
      (return-from session-reconfirm
        (values nil
                (format nil "SESSION FAIL reconfirm completed after expiry until=~A" (session-until sess))
                1)))
    ;; 2. First check tip == base
    (when (and (session-base sess)
               (not (string= tip-sha (session-base sess))))
      (setf (session-state sess) :fenced)
      (return-from session-reconfirm
        (values nil
                (format nil "SESSION RACED expected=~A found=~A"
                        (session-base sess) tip-sha)
                1)))
    ;; 3. Check OWNER on tip still carries its generation and token
    (when owner-record
      (unless (and (= (owner-generation owner-record) (session-generation sess))
                   (string= (owner-token owner-record) (session-token sess)))
        (setf (session-state sess) :fenced)
        (return-from session-reconfirm
          (values nil
                  (format nil "SESSION FAIL owner changed on tip generation=~D"
                          (owner-generation owner-record))
                  1))))
    ;; 4. Success: advance until = now + 2 * every
    (let* ((every-sec (parse-duration (session-every sess)))
           (new-until-ut (+ now-ut (* 2 every-sec)))
           (new-until (format-rfc3339 new-until-ut)))
      (setf (session-until sess) new-until)
      (values t
              (format nil "SESSION OK owner=~A generation=~D until=~A"
                      (session-owner sess)
                      (session-generation sess)
                      new-until)
              0))))

(defun session-start (&key path (owner "emma") (state-seed nil) (journal nil)
                           (base "tip") (every "30s") (skew "5s") (token nil)
                           (journal-token nil) (owner-record nil) (now nil)
                           (my-bench "") (lock-held t)
                           (socket-path nil) (serve nil) (foreground t)
                           (repair nil)
                           (cache nil) (resolvers nil))
  "Start or resume a session. With SERVE the session becomes the resident
process: it binds the local listener at SOCKET-PATH and serves reads, and with
FOREGROUND NIL the caller is the launcher, which gets the SESSION OK line and
returns while the daemon stays up (SPEC-WORK.md:268-310, :2256-2267)."
  (multiple-value-bind (action record line exit-code)
      (evaluate-ownership-claim owner-record owner
                               :now (or now (format-rfc3339 (get-universal-time)))
                               :every every
                               :skew skew
                               :token token
                               :journal-token journal-token
                               :my-bench my-bench
                               :lock-held lock-held)
    (if (eq action :fenced)
        (values nil line exit-code)
        (let* ((k (make-kernel :state (if (typep state-seed 'wstate)
                                          state-seed
                                          (make-seed-state state-seed))
                               :journal (or journal (make-ordering-journal))))
               (cache-path (or cache ""))
               (verification (session-verification-from-cache cache-path resolvers))
                (sess (make-session :path (or path "")
                                    :owner (owner-owner record)
                                    :generation (owner-generation record)
                                    :token (owner-token record)
                                    :until (owner-until record)
                                    :state :live
                                    :kernel k
                                    :base base
                                    :every every
                                    :skew skew
                                    :repair repair
                                    :cache cache-path
                                    :verification verification)))
           (when repair
             ;; A red set under --repair loads and validates the same way,
             ;; names its findings, and exits 1 while any finding stands
             ;; (SPEC-WORK.md:2430-2440). The source is never rewritten.
             (setf (session-findings sess)
                   (length (cow-load-findings (kernel-state k)))))
           (if serve
               (start-session-server sess
                                     :socket-path (or socket-path path)
                                     :foreground foreground)
                (values sess line
                        (if (and repair (plusp (session-findings sess))) 1 0)))))))

(defun session-repair-gate (sess candidate-state &key node)
  "The repair gate (SPEC-WORK.md:2430-2440): under `session start --repair` a
candidate is admitted only when its whole-validation finding count is strictly
below the session's current count, refused otherwise at exit 1 with the repair
diff `<MUTATION> FAIL node=<id> findings=<n> was=<n>: no repair`. The candidate
is the resident O as it would be with the event applied; neither the session's
source nor the candidate is rewritten here (values admitted-p line exit-code)."
  (let* ((current (session-findings sess))
         (candidate (length (cow-load-findings candidate-state))))
    (if (< candidate current)
        (values t nil 0)
        (values nil
                (format nil "<MUTATION> FAIL node=~A findings=~D was=~D: no repair"
                        (or node "<id>") candidate current)
                1))))
