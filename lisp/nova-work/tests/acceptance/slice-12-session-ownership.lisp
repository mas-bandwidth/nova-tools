;;;; slice-12-session-ownership.lisp --- the resume predicate, the ownership
;;;; record and the single command thread (nova-tools #749).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; ownership record (SPEC-WORK.md:202-214)
;;; ------------------------------------------------------------------

(deftest "ownership-record-round-trips" "docs/SPEC-WORK.md:202-214"
  "expected=format-then-parse-preserves-owner-generation-token-stamp-until-bench-successor"
  (let* ((rec (make-ownership-record
               :owner "emma"
               :generation 7
               :token "tok-abc"
               :stamp "2026-09-14T12:00:00Z"
               :until "2026-09-14T12:01:00Z"
               :bench "spacegame@64769:123456"
               :successor "stella"))
         (parsed (parse-ownership-record (format-ownership-record rec))))
    (check-string= (owner-owner rec) (owner-owner parsed) "owner round-trips")
    (check-equal (owner-generation rec) (owner-generation parsed) "generation round-trips")
    (check-string= (owner-token rec) (owner-token parsed) "token round-trips")
    (check-string= (owner-stamp rec) (owner-stamp parsed) "stamp round-trips")
    (check-string= (owner-until rec) (owner-until parsed) "until round-trips")
    (check-string= (owner-bench rec) (owner-bench parsed) "bench round-trips")
    (check-string= (owner-successor rec) (owner-successor parsed) "successor round-trips")))

(deftest "handoff-successor-takes-next-generation" "docs/SPEC-WORK.md:202-214"
  "expected=successor-takes-next-generation-immediately-without-waiting-for-expiry"
  (let ((current (make-ownership-record
                  :owner "emma"
                  :generation 3
                  :token "tok-emma"
                  :stamp "2026-09-14T11:59:00Z"
                  :until "2026-09-14T12:30:00Z"
                  :successor "stella")))
    (multiple-value-bind (action record line exit-code)
        (evaluate-ownership-claim current "stella"
                                  :now "2026-09-14T12:01:00Z"
                                  :every "30s"
                                  :skew "5s"
                                  :token "tok-stella-new"
                                  :my-bench "bench-stella")
      (declare (ignore line exit-code))
      (check-equal :take action "successor handoff is a take")
      (check-string= "stella" (owner-owner record) "owner is stella")
      (check-equal 4 (owner-generation record) "generation bumped to 4")
      (check-string= "tok-stella-new" (owner-token record) "new token")
      (check-string= "bench-stella" (owner-bench record) "the successor's bench is recorded")
      (check-string= "2026-09-14T12:02:00Z" (owner-until record) "until is now + 2*every"))
    ;; The real `session handoff` verb fences, clips and publishes the released
    ;; record with its successor and the same generation (SPEC-WORK.md:814-822).
    (let* ((seed '((:id "acme/work" :type :work-set :state :unknown)))
           (sess (session-start :owner "emma" :state-seed seed :base "abc123"
                                :every "30s" :skew "5s")))
      (multiple-value-bind (okp line published)
          (session-handoff sess :to "stella" :commit "deadbeef" :pushed 9
                           :now "2026-09-14T12:05:00Z")
        (ok okp "the handoff is admitted")
        (ok (search "HANDOFF OK" line) "the handoff prints HANDOFF OK: ~A" line)
        (ok (search "to=stella" line) "the line names the successor: ~A" line)
        (ok (search "generation=1" line) "the handoff keeps the generation: ~A" line)
        (ok (search "commit=deadbeef" line) "the line carries the commit: ~A" line)
        (ok (search "pushed=9" line) "the line carries the pushed revision: ~A" line)
        (check-equal :fenced (session-state sess) "the handoff fences the session")
        (check-string= "2026-09-14T12:05:00Z" (owner-until published)
                       "the owner record is released at the handoff stamp")
        (check-string= "stella" (owner-successor published)
                       "the released record names the successor")
        (check-equal 1 (owner-generation published) "the generation is kept")
        ;; The successor takes the next generation at once, needing neither
        ;; `until` nor `--skew` (SPEC-WORK.md:820-822).
        (multiple-value-bind (action successor-record successor-line code)
            (evaluate-ownership-claim published "stella"
                                      :now "2026-09-14T12:05:10Z"
                                      :every "30s" :skew "5s"
                                      :token "tok-stella" :my-bench "bench-stella")
          (declare (ignore successor-line))
          (check-equal :take action "the successor takes without waiting")
          (check-equal 0 code "the successor's take is green")
          (check-equal 2 (owner-generation successor-record)
                       "the successor takes the next generation"))))))

;;; ------------------------------------------------------------------
;;; W1: the resume predicate (bench identity + journal lock) and the single
;;; writer. SPEC-WORK.md:162-204, 2603-2616 (#749).
;;; ------------------------------------------------------------------

(defun journal-frame-seqs (path)
  "Read the frame sequence numbers of the journal at PATH, in order."
  (let ((seqs '()))
    (with-open-file (in path :direction :input :element-type 'character :external-format :utf-8)
      (read-header in path nil)
      (loop for n from 1
            for frame = (read-record-frame in path n)
            while frame
            do (push (getf (rest frame) :seq) seqs)))
    (nreverse seqs)))

(deftest "resume-refuses-a-copied-journal" "docs/SPEC-WORK.md:193-204"
    "expected=same-owner-and-token-but-different-bench-refused;no-lock-held-refused;reason-printed"
  (let ((current (make-ownership-record
                  :owner "emma"
                  :generation 3
                  :token "tok-emma-secret"
                  :stamp "2026-09-14T11:59:00Z"
                  :until "2026-09-14T12:01:00Z"
                  :bench "bench-a")))
    (multiple-value-bind (action record line exit-code)
        (evaluate-ownership-claim current "emma"
                                  :now "2026-09-14T12:00:30Z"
                                  :every "30s"
                                  :skew "5s"
                                  :journal-token "tok-emma-secret"
                                  :my-bench "bench-b"
                                  :lock-held t)
      (declare (ignore record))
      (check-equal :fenced action "a copied journal must not resume")
      (check-equal 1 exit-code "exit code is 1")
      (ok (search "SESSION FAIL" line) "line is a failure: ~A" line)
      (ok (search "bench" line) "the reason names the bench: ~A" line))
    (multiple-value-bind (action record line exit-code)
        (evaluate-ownership-claim current "emma"
                                  :now "2026-09-14T12:00:30Z"
                                  :every "30s"
                                  :skew "5s"
                                  :journal-token "tok-emma-secret"
                                  :my-bench "bench-a"
                                  :lock-held nil)
      (declare (ignore record))
      (check-equal :fenced action "no lock held must not resume")
      (check-equal 1 exit-code "exit code is 1")
      (ok (search "SESSION FAIL" line) "line is a failure: ~A" line)
      (ok (search "lock" line) "the reason names the lock: ~A" line))))

(deftest "resume-needs-the-journal-lock" "docs/SPEC-WORK.md:162-183,193-204"
    "expected=second-holder-cannot-take-the-lock;open-refuses-held;journal-not-held-fences-resume"
  (let* ((path (test-journal-path "resume-lock"))
         (foreign-lock (take-journal-lock path)))
    (ok foreign-lock "took the outer lock")
    (unwind-protect
         (progn
           (ok (null (take-journal-lock path))
               "a second flock on the held lock must fail")
           (let ((held nil))
             (handler-case (open-file-journal path :take-lock t)
               (journal-held () (setf held t)))
             (ok held "open-file-journal did not refuse the held lock"))
           (let ((current (make-ownership-record
                           :owner "emma" :generation 3 :token "tok-emma"
                           :until "2026-09-14T12:01:00Z" :bench "bench-a")))
             (multiple-value-bind (action record line exit-code)
                 (evaluate-ownership-claim current "emma"
                                           :journal-token "tok-emma"
                                           :my-bench "bench-a"
                                           :lock-held nil)
               (declare (ignore record))
               (check-equal :fenced action "no journal lock fences the resume")
               (check-equal 1 exit-code "exit code is 1")
               (ok (search "lock" line) "the reason names the lock: ~A" line))))
      (release-journal-lock foreign-lock)
      (ignore-errors (delete-file path))
      (ignore-errors (delete-file (concatenate 'string path ".lock"))))))

(deftest "submits-serialize-on-one-thread" "docs/SPEC-WORK.md:2603-2616"
    "expected=two-concurrent-submits-land-in-one-total-order-with-consecutive-sequence-numbers"
  (let* ((path (test-journal-path "serialize"))
         (seed (loop for i below 20 collect (list :id (format nil "t~D" i) :type :task :state :todo)))
         (j (open-file-journal path :initial-state-hash (root-digest (make-seed-state seed))))
         (k (make-kernel :state (make-seed-state seed) :journal j)))
    (unwind-protect
         (progn
           (let ((threads (loop for i below 20
                                collect (let ((i i))
                                          (sb-thread:make-thread
                                           (lambda ()
                                             (submit k (list :verb :state-to-doing
                                                             :node (format nil "t~D" i)
                                                             :by "emma"
                                                             :reason "r"
                                                             :request (format nil "req-~D" i)
                                                             :stamp "2026-09-14T12:00:00Z"
                                                             :clock :tool
                                                             :generation-owner "emma")))
                                           :name (format nil "submit-~D" i))))))
             (mapc #'sb-thread:join-thread threads))
           (check-equal 20 (journal-seq j) "twenty commands issue twenty sequence numbers")
           (check-equal 20 (length (journal-order j)) "every command recorded exactly once")
           (check-equal (loop for n from 1 to 20 collect n)
                        (journal-frame-seqs path)
                        "the frames carry consecutive sequence numbers 1..20"))
      (close-file-journal j)
      (ignore-errors (delete-file path))
      (ignore-errors (delete-file (concatenate 'string path ".lock"))))))

;;; ------------------------------------------------------------------
;;; session-server-daemon-and-session-start (SPEC-WORK.md:268-310,
;;; :2256-2267). The row the spec does not name a replay for gets its own
;;; deftest, named by the paragraph.
;;; ------------------------------------------------------------------

(defun daemon-serve-status (socket-path)
  "Connect to SOCKET-PATH, ask `status`, and answer the reply line."
  (let ((conn (make-instance 'sb-bsd-sockets:local-socket :type :stream)))
    (unwind-protect
         (progn
           (sb-bsd-sockets:socket-connect conn socket-path)
           (let ((stream (sb-bsd-sockets:socket-make-stream
                          conn :input t :output t
                          :element-type 'character :external-format :utf-8)))
             (write-line "status" stream)
             (finish-output stream)
             (read-line stream nil :eof)))
      (ignore-errors (sb-bsd-sockets:socket-close conn)))))

(deftest "session-server-daemon-and-session-start" "docs/SPEC-WORK.md:268-310,2256-2267"
    "expected=launcher-returns-session-ok-while-daemon-stays-up;status-served-over-the-local-socket;foreground-is-the-process;stop-shuts-the-listener"
  (let* ((base (short-socket-base (format nil "nwd-~D" (random 1000000))))
         (dir (concatenate 'string base "/s"))
         (sock (concatenate 'string dir "/w"))
         (fdir (concatenate 'string base "/f"))
         (fsock (concatenate 'string fdir "/w"))
         (seed '((:id "acme/work" :type :work-set :state :unknown))))
    (unless (probe-file base) (sb-posix:mkdir base #o700))
    (unwind-protect
         (progn
           ;; The launcher starts the session process, gets its SESSION OK line
           ;; and returns while the daemon it started stays up and serves.
           (multiple-value-bind (server line code)
               (session-start :owner "emma" :state-seed seed :serve t
                              :socket-path sock :foreground nil)
             (ok server "the launcher returns the running session daemon")
             (check-equal 0 code "a green load exits 0")
             (ok (and (stringp line) (search "SESSION OK" line))
                 "the launcher prints SESSION OK: ~S" line)
             (ok (session-server-running-p server)
                 "the process the launcher started stays up")
             (ok (listener-open-p (session-server-listener server))
                 "the session is listening at the endpoint it created")
             ;; A thin client sends `status` to the session listening at that
             ;; path and prints its one-line answer; the daemon serves reads.
             (let ((reply (daemon-serve-status sock)))
               (ok (and (stringp reply) (search "SESSION OK" reply))
                   "session status is answered over the local socket: ~S" reply)
               (ok (search "max-bytes=" reply) "the identity line carries the bounds")
               (ok (search "every=" reply) "the identity line carries the clip cadence"))
             (ok (session-server-stop server) "stop is explicit")
             (ok (not (session-server-running-p server)) "the daemon is stopped")
             (ok (not (listener-open-p (session-server-listener server)))
                 "the listener is closed"))
           ;; --foreground: the caller is the session process, prints the same
           ;; line on its own stdout and then serves.
           (multiple-value-bind (server line code)
               (session-start :owner "emma" :state-seed seed :serve t
                              :socket-path fsock :foreground t)
             (check-equal 0 code "the foreground session exits 0 when stopped")
             (check-equal t (session-server-foreground server)
                          "the session process is the caller under --foreground")
             (ok (and (stringp line) (search "SESSION OK" line))
                 "the session process prints its own SESSION OK line: ~S" line)
             (ok (listener-open-p (session-server-listener server))
                 "the foreground session serves at its endpoint")
             (session-server-stop server)))
      (dolist (s (list sock fsock))
        (ignore-errors (sb-posix:unlink s)))
      (dolist (d (list dir fdir))
        (ignore-errors (sb-posix:rmdir d)))
      (ignore-errors (sb-posix:rmdir base)))))

;;; ------------------------------------------------------------------
;;; session-status (SPEC-WORK.md:304-308, :2256-2267). The row has no named
;;; replay of its own, so it gets a deftest named by the paragraph.
;;; ------------------------------------------------------------------

(deftest "session-status" "docs/SPEC-WORK.md:304-310,2256-2267"
    "expected=SESSION-OK-carries-every-bound-and-build;fenced-session-still-answers-exit-0"
  (let* ((seed '((:id "acme/work"     :type :work-set :state :unknown)
                 (:id "acme/work/f1"  :type :feature  :parent "acme/work" :state :unknown)))
         (sess (session-start :owner "emma" :state-seed seed :base "abc123"
                              :every "30s" :skew "5s")))
    (let ((line (session-status-line sess)))
      (ok (search "SESSION OK" line) "status is a SESSION OK: ~A" line)
      ;; Every field the spec names is read rather than remembered
      ;; (SPEC-WORK.md:304-308, output grammar :5346).
      (dolist (field '("session=" "owner=emma" "generation=1" "state=live" "until="
                       "file=" "base=abc123" "journal=" "events=" "pending=" "pushed="
                       "nodes=" "edges=" "parses=" "replays=" "every=30s" "skew=5s"
                       "clip-every=" "clip-after=" "retain=" "index-cache="
                       "page-bytes=" "page-records=" "closed-window=" "max-bytes="
                       "max-depth=" "max-nodes=" "boundary=" "findings=" "emitted="))
        (ok (search field line) "the status line carries ~A: ~A" field line))
      ;; The running binary says which build it is (SPEC-WORK.md:302-303).
      (ok (search "build=" line) "the status line names the build: ~A" line)
      (ok (> (length (session-build-identity)) 0) "the build identity is named")
      ;; The counters read the resident state, not a remembered number.
      (ok (search "nodes=2" line) "nodes counts the resident structure: ~A" line)
      (ok (search "edges=1" line) "edges counts the containment path: ~A" line))
    ;; A fenced session is inspected, not suffered: status still exits 0
    ;; (SPEC-WORK.md:235-237).
    (setf (session-state sess) :fenced)
    (multiple-value-bind (admitted reason code)
        (session-check-admission sess :status)
      (declare (ignore reason))
      (ok admitted "status is admitted on a fenced session")
      (check-equal 0 code "status exits 0 on a fenced session")
      (ok (search "state=fenced" (session-status-line sess))
          "status answers about the fence"))))

;;; ------------------------------------------------------------------
;;; session-stop (SPEC-WORK.md:824-826, :2256-2267). The stop half of the
;;; stop-and-handoff row; handoff itself is the extended
;;; handoff-successor-takes-next-generation replay above.
;;; ------------------------------------------------------------------

(deftest "session-stop" "docs/SPEC-WORK.md:824-827,2256-2267"
    "expected=clip-then-owner-released-until-stop-stamp;no-clip-omits-the-clip;no-successor"
  (let* ((seed '((:id "acme/work" :type :work-set :state :unknown)))
         (sess (session-start :owner "emma" :state-seed seed :base "abc123"
                              :every "30s" :skew "5s")))
    (multiple-value-bind (okp lines record)
        (session-stop-lifecycle sess :now "2026-09-14T12:05:00Z")
      (ok okp "the stop is admitted")
      (check-equal 2 (length lines) "stop prints its CLIP OK then its SESSION OK")
      (ok (search "CLIP OK" (first lines)) "stop waits for its own clip: ~A" (first lines))
      (ok (search "SESSION OK" (second lines))
          "stop prints the SESSION OK second: ~A" (second lines))
      ;; The owner record is released with `until` at the stop's stamp and no
      ;; successor, so a taker waits --skew (SPEC-WORK.md:825-826).
      (check-string= "2026-09-14T12:05:00Z" (owner-until record)
                     "the owner is released until the stop's stamp")
      (ok (null (owner-successor record)) "a stop names no successor")
      (check-equal 1 (owner-generation record) "a stop keeps the generation"))
    ;; --no-clip prints no CLIP OK and still releases the owner.
    (let ((sess-2 (session-start :owner "emma" :state-seed seed :base "abc123"))
          )
      (multiple-value-bind (okp lines record)
          (session-stop-lifecycle sess-2 :no-clip t :now "2026-09-14T12:06:00Z")
        (ok okp "a no-clip stop is admitted")
        (check-equal 1 (length lines) "a no-clip stop prints only the SESSION OK")
        (ok (search "SESSION OK" (first lines)) "the no-clip stop still reports")
        (check-string= "2026-09-14T12:06:00Z" (owner-until record)
                       "a no-clip stop still releases the owner")))))
