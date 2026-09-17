;;;; slice-13-verifier.lisp --- the `verify` verb and the verification cache.
;;;;
;;;; SPEC-WORK.md:1242-1338  evidence, resolvers and the cache
;;;; SPEC-WORK.md:2302       the verb's grammar
;;;; SPEC-WORK.md:5368-5371  the output grammar
;;;; SPEC-WORK.md:5490-5491  exactly one count line
;;;; SPEC-WORK.md:5650-5653  settle keeps every VERIFY ROW verdict
;;;;
;;;; Each case drives the pure model in src/verifier.lisp the way the paragraph's
;;;; verb would and asserts the outcome the paragraph promises.

(in-package #:nova-work/tests)

(defun verify-token (line key)
  "The value of `<key>=` on one VERIFY line, as a string, or nil."
  (let ((start (search (concatenate 'string key "=") line)))
    (when start
      (let* ((begin (+ start (length key) 1))
             (end (or (position #\Space line :start begin) (length line))))
        (subseq line begin end)))))

(defun verify-number (line key)
  (let ((token (verify-token line key)))
    (and token (parse-integer token))))

(defun verify-shows (line key value)
  (check-equal value (verify-number line key)
               (format nil "~A=~A on ~A" key value line)))

(defun holding-resolver (scheme command &key (fact :holds)
                                          (stamp "2026-09-13T18:00:00Z") calls)
  "A resolver whose configured body establishes FACT at STAMP and counts its
runs in the one-element list CALLS when given."
  (make-verification-resolver
   scheme command
   :function (lambda (pointer subject)
               (declare (ignore pointer subject))
               (when calls (incf (first calls)))
               (values fact stamp))))

;;; ------------------------------------------------------------------
;;; verify-keeps-cached-evidence-rows  SPEC-WORK.md:5650-5653
;;; ------------------------------------------------------------------

(deftest "verify-keeps-cached-evidence-rows" "docs/SPEC-WORK.md:5650-5653"
    "expected=id-and-evidence-survive;verify-row-verdicts-unchanged"
  ;; An item's evidence events and the raw facts they resolve to survive a
  ;; settle, and `verify` over that item prints the same VERIFY ROW verdicts it
  ;; printed while the item was open. The states either side of the settle are
  ;; not modelled here; the evidence set and the session's one cache are, and
  ;; neither is touched by the verb.
  (let* ((cache (make-verification-cache))
         (session (make-verification-session
                   :cache cache
                   :resolvers (list (holding-resolver "test" "resolver-cmd"))
                   :source-revision "sha-1"))
         (evidence (list (make-verify-evidence
                          "ev-1" :pointer "test:pkg/name@sha-1"
                          :criterion :test :subject "pkg/name"
                          :against "sha-1" :generation "gen-1" :node "t1"))))
    (multiple-value-bind (line rows exit) (verify session evidence :node "t1")
      (verify-shows line "unverified" 0)
      (check-equal 0 exit "a verified item exits 0")
      (check-equal 1 (length rows) "one VERIFY ROW per evidence event")
      (ok (search "VERIFY ROW ev-1" (first rows)) "the row names the event id: ~A" (first rows))
      (ok (search "verdict=verified" (first rows)) "the pointer qualifies: ~A" (first rows))
      ;; The same evidence and the same session after the item settles: every
      ;; VERIFY ROW verdict and every verdict count are what they were. (The
      ;; cache counters move, because the first run warmed the cache; the
      ;; verdicts never do.)
      (multiple-value-bind (line2 rows2 exit2) (verify session evidence :node "t1")
        (check-equal rows rows2 "the VERIFY ROW verdicts are unchanged by a settle")
        (check-equal exit exit2 "the verdict count is unchanged by a settle")
        (dolist (key '("pointers" "verified" "unverified" "stale"))
          (check-equal (verify-number line key) (verify-number line2 key)
                       (format nil "~A= is unchanged by a settle" key)))))))

;;; ------------------------------------------------------------------
;;; verify-cache-holds-raw-resolutions  SPEC-WORK.md:1294-1338
;;; ------------------------------------------------------------------

(deftest "verify-cache-holds-raw-resolutions" "docs/SPEC-WORK.md:1294-1338"
    "expected=raw-fact-not-verdict;one-fact-two-verdicts;criterion-change-no-fetch;offline-derives"
  (let* ((cache (make-verification-cache))
         (calls (list 0))
         (resolver (holding-resolver "test" "resolver-cmd" :calls calls))
         (session (make-verification-session
                   :cache cache :resolvers (list resolver) :source-revision "sha-1"))
         (verified-event (make-verify-evidence
                          "ev-1" :pointer "test:pkg/x@sha-1"
                          :criterion :test :subject "pkg/x" :against "sha-1"))
         ;; Two evidence events name one pointer for two criteria: one qualifies,
         ;; one does not. They must be two verdicts over one cached fact.
         (other-criterion (make-verify-evidence
                           "ev-2" :pointer "test:pkg/x@sha-1"
                           :criterion :merged :subject "pkg/x" :against "sha-1")))
    (multiple-value-bind (line rows exit) (verify session (list verified-event other-criterion))
      (declare (ignore exit))
      (check-equal 1 (first calls)
                   "the resolver ran once per cache key, not once per event")
      (check-equal 1 (verification-cache-size cache)
                   "the cache holds one raw fact for the two criteria")
      (check-equal 2 (length rows) "one VERIFY ROW per evidence event, not per pointer")
      (verify-shows line "pointers" 1)
      (verify-shows line "verified" 1)
      (verify-shows line "unverified" 1)
      (ok (search "VERIFY ROW ev-1 pointer=test:pkg/x@sha-1 verdict=verified" (first rows))
          "the test: pointer qualifies the :test criterion: ~A" (first rows))
      (ok (search "VERIFY ROW ev-2 pointer=test:pkg/x@sha-1 verdict=unverified" (second rows))
          "a test: pointer never qualifies a :merged criterion: ~A" (second rows)))
    ;; The cache holds the raw resolution, not a verdict: the same criterion
    ;; changed to one the pointer qualifies produces a verdict with no fetch.
    (let ((changed (make-verify-evidence
                    "ev-2" :pointer "test:pkg/x@sha-1"
                    :criterion :test :subject "pkg/x" :against "sha-1")))
      (multiple-value-bind (line rows exit) (verify session (list verified-event changed))
        (declare (ignore exit))
        (check-equal 1 (first calls) "a changed criterion fetched nothing")
        (verify-shows line "fetched" 0)
        (verify-shows line "cached" 2)
        (ok (search "ev-2 pointer=test:pkg/x@sha-1 verdict=verified" (second rows))
            "the changed criterion is now verified with no fetch: ~A" (second rows))))
    ;; `verify --offline` derives verdicts from the cached facts and fetches
    ;; nothing (SPEC-WORK.md:1332).
    (let ((offline (make-verification-session
                    :cache cache :resolvers (list resolver) :offline t)))
      (multiple-value-bind (line rows exit) (verify offline (list verified-event other-criterion))
        (declare (ignore exit))
        (check-equal 1 (first calls) "offline fetched nothing")
        (verify-shows line "fetched" 0)
        (ok (search "ev-1 pointer=test:pkg/x@sha-1 verdict=verified" (first rows))
            "offline derived the verdict from the cached fact: ~A" (first rows))
        (ok (search "ev-2 pointer=test:pkg/x@sha-1 verdict=unverified" (second rows))
            "offline still derives the other criterion locally: ~A" (second rows))))))

;;; ------------------------------------------------------------------
;;; verify-job-criterion-reads-the-revision  SPEC-WORK.md:1278-1286
;;; ------------------------------------------------------------------

(deftest "verify-job-criterion-reads-the-revision" "docs/SPEC-WORK.md:1278-1286"
    "expected=job-criterion-needs-the-named-run-at-the-criterion-revision"
  ;; No current source revision here: the two events differ only in the
  ;; revision they were written against, so the shared pointer is one fact and
  ;; the two criteria are two verdicts. A :job criterion is met only where the
  ;; pointer's own `@<sha>` is the revision the evidence event was written
  ;; against.
  (let* ((cache (make-verification-cache))
         (calls (list 0))
         (resolver (holding-resolver "run" "run-resolver" :calls calls))
         (session (make-verification-session
                   :cache cache :resolvers (list resolver)))
         (matching (make-verify-evidence
                    "ev-1" :pointer "run:acme/work#j1@sha-1"
                    :criterion :job :subject "j1" :against "sha-1"))
         (other (make-verify-evidence
                 "ev-2" :pointer "run:acme/work#j1@sha-1"
                 :criterion :job :subject "j1" :against "sha-9")))
    (multiple-value-bind (line rows exit) (verify session (list matching other))
      (declare (ignore exit))
      (check-equal 1 (first calls) "one resolver run for the one cache key")
      (check-equal 1 (verification-cache-size cache) "one raw run fact")
      (ok (search "ev-1 pointer=run:acme/work#j1@sha-1 verdict=verified" (first rows))
          "the job is verified at the revision it was sent with: ~A" (first rows))
      (ok (search "ev-2 pointer=run:acme/work#j1@sha-1 verdict=unverified" (second rows))
          "another revision is found-not-qualifying, never a second fact: ~A" (second rows))
      (verify-shows line "pointers" 1)
      (verify-shows line "verified" 1)
      (verify-shows line "unverified" 1))))

;;; ------------------------------------------------------------------
;;; verify-negatives-and-unreachable  SPEC-WORK.md:1260-1263
;;; ------------------------------------------------------------------

(deftest "verify-negatives-and-unreachable" "docs/SPEC-WORK.md:1260-1263"
    "expected=negative-is-a-fact-and-cached;unreachable-is-never-cached"
  (let* ((cache (make-verification-cache))
         (absent (holding-resolver "test" "absent-resolver" :fact :absent
                                   :stamp "2026-09-13T18:00:00Z"))
         (session (make-verification-session
                   :cache cache :resolvers (list absent) :source-revision "sha-1"))
         (negative (make-verify-evidence
                    "ev-1" :pointer "test:pkg/x@sha-1"
                    :criterion :test :subject "pkg/x" :against "sha-1"))
         ;; no resolver is configured for the pr: scheme, so this pointer is
         ;; unreachable and counts unverified.
         (unreachable (make-verify-evidence
                       "ev-2" :pointer "pr:acme/work#7@sha-1"
                       :criterion :merged :subject "7" :against "sha-1")))
    (multiple-value-bind (line rows exit) (verify session (list negative unreachable))
      (check-equal 1 exit "an unverified verdict is exit 1")
      (ok (search "VERIFY FAIL" line) "the count line is VERIFY FAIL: ~A" line)
      (verify-shows line "verified" 0)
      (verify-shows line "unverified" 2)
      (verify-shows line "stale" 0)
      (ok (search "VERIFY ROW ev-1 pointer=test:pkg/x@sha-1 verdict=unverified" (first rows))
          "a negative is a fact and counts unverified: ~A" (first rows))
      (ok (search "VERIFY ROW ev-2 pointer=pr:acme/work#7@sha-1 verdict=unverified" (second rows))
          "an unreachable pointer counts unverified: ~A" (second rows))
      (check-equal 1 (verification-cache-size cache)
                   "the negative was cached and the unreachable answer was not"))
    ;; `verify --offline` answers the cached negative and fetches nothing.
    (let ((offline (make-verification-session
                    :cache cache :resolvers (list absent) :offline t)))
      (multiple-value-bind (line rows exit) (verify offline (list negative))
        (declare (ignore exit))
        (verify-shows line "fetched" 0)
        (ok (search "verdict=unverified" (first rows))
            "a cached negative is answered offline: ~A" (first rows))))))

;;; ------------------------------------------------------------------
;;; verify-resolver-identity-is-the-command  SPEC-WORK.md:1294-1301
;;; ------------------------------------------------------------------

(deftest "verify-resolver-identity-is-the-command" "docs/SPEC-WORK.md:1294-1301"
    "expected=command-string-is-the-third-key-field;snapshot-names-resolvers"
  ;; The resolver's identity is the command string verbatim, so two sessions
  ;; given different command lines do not share one cache key.
  (let* ((cache (make-verification-cache))
         (calls (list 0))
         (first-resolver (holding-resolver "test" "cmd-a" :calls calls))
         (second-resolver (holding-resolver "test" "cmd-b" :calls calls))
         (evidence (list (make-verify-evidence
                          "ev-1" :pointer "test:pkg/x@sha-1"
                          :criterion :test :subject "pkg/x" :against "sha-1"))))
    (verify (make-verification-session
             :cache cache :resolvers (list first-resolver) :source-revision "sha-1")
            evidence)
    (check-equal 1 (first calls) "the first command answered")
    (multiple-value-bind (line rows exit) (verify (make-verification-session
                                                   :cache cache
                                                   :resolvers (list second-resolver)
                                                   :source-revision "sha-1")
                                                  evidence)
      (declare (ignore rows exit))
      (check-equal 2 (first calls)
                   "a different command string is a different cache key and fetches again")
      (verify-shows line "fetched" 1))
    ;; A snapshot reader accepts a fact only where its resolver identity is one
    ;; its header names (SPEC-WORK.md:1311-1316).
    (let ((reader (make-verification-cache :resolvers '("cmd-a"))))
      (verification-cache-store reader "test:pkg/x@sha-1" "pkg/x" "cmd-b" :holds
                                "2026-09-13T18:00:00Z")
      (ok (null (verification-cache-lookup reader "test:pkg/x@sha-1" "pkg/x" "cmd-b"))
          "a fact the snapshot's header does not name is refused"))))

;;; ------------------------------------------------------------------
;;; verify-offline-refuses-a-fetch-budget  SPEC-WORK.md:1289-1292
;;; ------------------------------------------------------------------

(deftest "verify-offline-refuses-a-fetch-budget" "docs/SPEC-WORK.md:1289-1292"
    "expected=max-fetch-and-fetch-timeout-refused-under-offline"
  (let* ((session (make-verification-session
                   :cache (make-verification-cache)
                   :offline t :max-fetch 4 :fetch-timeout 30))
         (evidence (list (make-verify-evidence
                          "ev-1" :pointer "test:pkg/x@sha-1"
                          :criterion :test :subject "pkg/x" :against "sha-1"))))
    (handler-case
        (progn (verify session evidence)
               (ok nil "--offline with a fetch budget was accepted"))
      (unsupported-input (c)
        (declare (ignore c))
        (ok t "both fetch flags are refused under --offline")))))

;;; ------------------------------------------------------------------
;;; verify-stale-evidence-needs-no-fetch  SPEC-WORK.md:1338
;;; ------------------------------------------------------------------

(deftest "verify-stale-evidence-needs-no-fetch" "docs/SPEC-WORK.md:1338"
    "expected=stale-is-local-and-never-fetched"
  (let* ((cache (make-verification-cache))
         (calls (list 0))
         (resolver (holding-resolver "test" "resolver-cmd" :calls calls))
         (session (make-verification-session
                   :cache cache :resolvers (list resolver) :source-revision "sha-2"))
         (stale (make-verify-evidence
                 "ev-1" :pointer "test:pkg/x@sha-1"
                 :criterion :test :subject "pkg/x" :against "sha-1")))
    (multiple-value-bind (line rows exit) (verify session (list stale))
      (check-equal 0 exit "a stale-only run exits 0")
      (check-equal 0 (first calls) "stale evidence is local and fetches nothing")
      (verify-shows line "stale" 1)
      (verify-shows line "unverified" 0)
      (verify-shows line "fetched" 0)
      (ok (search "ev-1 pointer=test:pkg/x@sha-1 verdict=stale" (first rows))
          "the stale verdict is printed: ~A" (first rows)))))
