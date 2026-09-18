;;;; slice-09-state-export-replays.lisp --- the five state-export acceptance
;;;; replays of docs/SPEC-WORK.md:5879-5905 (the "state load" fold of :3273-3297).
;;;; Loaded by ../acceptance.lisp; an amendment edits one slice file.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; state-export-refuses-a-gap (docs/SPEC-WORK.md:5879)
;;; ------------------------------------------------------------------

(deftest "state-export-refuses-a-gap" "docs/SPEC-WORK.md:5879"
    "expected=each-gap-refused-with-no-valid-load;proof-gap-named-never-filled"
  (let* ((k (fresh))
         (base (export-manifest (kernel-state k) :id "exp-1")))
    ;; A whole, closed manifest loads.
    (multiple-value-bind (snap line) (load-state base :max-bytes 1000000)
      (ok snap "the good manifest refused: ~A" line))
    ;; A missing mandatory member is refused.
    (let ((m (copy-list base)))
      (setf (getf m :members) '())
      (multiple-value-bind (snap line) (load-state m :max-bytes 1000000)
        (ok (null snap) "a missing mandatory member loaded: ~A" line)
        (ok (search "missing mandatory member" line) "the refusal names the member: ~A" line)))
    ;; A changed digest is refused.
    (let ((m (copy-list base)))
      (setf (getf m :digest) "0000000000000000000000000000000000000000000000000000000000000000")
      (multiple-value-bind (snap line) (load-state m :max-bytes 1000000)
        (ok (null snap) "a changed digest loaded: ~A" line)
        (ok (search "changed digest" line) "the refusal names the digest: ~A" line)))
    ;; A dangling internal reference is refused.
    (let* ((dangling (canonical-string
                      (list :seed '((:id "a" :type :task :parent "missing" :state :doing))
                            :history '())))
           (m (list :version 1 :id "exp-d" :revision 0
                    :digest (sha256-hex dangling)
                    :members (list (cons "snapshot" dangling))
                    :observations-gone nil)))
      (multiple-value-bind (snap line) (load-state m :max-bytes 1000000)
        (ok (null snap) "a dangling reference loaded: ~A" line)
        (ok (search "dangling internal reference" line) "the refusal names the reference: ~A" line)))
    ;; A path escape, a symlink member and an output overrun are refused.
    (let ((m (copy-list base)))
      (setf (getf m :members) (cons '("../etc/passwd" . "x") (getf m :members)))
      (multiple-value-bind (snap line) (load-state m :max-bytes 1000000)
        (ok (null snap) "a path escape loaded: ~A" line)
        (ok (search "path escape" line) "the refusal names the escape: ~A" line)))
    (let ((m (copy-list base)))
      (setf (getf m :symlinks) '("snapshot"))
      (multiple-value-bind (snap line) (load-state m :max-bytes 1000000)
        (ok (null snap) "a symlink member loaded: ~A" line)
        (ok (search "symlink" line) "the refusal names the symlink: ~A" line)))
    (multiple-value-bind (snap line) (load-state base :max-bytes 1)
      (ok (null snap) "an output overrun loaded: ~A" line)
      (ok (search "output overrun" line) "the refusal names the overrun: ~A" line))
    ;; A corrupt S-expression is refused, never read as a valid load.
    (let* ((bad "((")
           (m (list :version 1 :id "exp-x" :revision 0
                    :digest (sha256-hex bad)
                    :members (list (cons "snapshot" bad))
                    :observations-gone nil)))
      (multiple-value-bind (snap line) (load-state m :max-bytes 1000000)
        (ok (null snap) "a corrupt S-expression loaded: ~A" line)
        (ok (search "corrupt S-expression" line) "the refusal names the corruption: ~A" line)))
    ;; A historical export whose resolver observations are gone is refused with
    ;; a named proof gap, and current observations are never substituted.
    (let ((m (copy-list base)))
      (setf (getf m :observations-gone) t)
      (multiple-value-bind (snap line) (load-state m :max-bytes 1000000)
        (ok (null snap) "a gone-observation export loaded: ~A" line)
        (ok (search "proof gap" line) "the refusal names the proof gap: ~A" line)))))

;;; ------------------------------------------------------------------
;;; state-export-pin-survives-clip (docs/SPEC-WORK.md:5889)
;;; ------------------------------------------------------------------

(deftest "state-export-pin-survives-clip" "docs/SPEC-WORK.md:5889"
    "expected=capture-R-then-clip-and-retention-at-R+1-then-exactly-R-or-a-named-gap"
  (let ((k (fresh)))
    (ok (submit k (close-request :request "req-1")) "close refused")
    (let* ((r-bytes (canonical-string (state-canonical-form (kernel-state k))))
           (r (state-revision (kernel-state k)))
           (op (export-state (kernel-state k) :id "op-1")))
      (check-equal r (state-export-revision op) "the export captured the wrong revision")
      ;; A later revision is accepted during the copy.
      (ok (submit k (reopen-request :request "req-2")) "reopen refused")
      (ok (> (state-revision (kernel-state k)) r) "the live revision did not advance")
      ;; The retention pass at R+1 cannot reclaim a pinned member.
      (multiple-value-bind (clipped reclaimed gap)
          (retention-pass (kernel-state k)
                          :reclaim '("req-1")
                          :pinned (state-export-members op)
                          :needed (state-export-members op))
        (declare (ignore clipped))
        (ok (null gap) "a pinned member was reclaimed: ~A" gap)
        (ok (null reclaimed) "the retention pass reclaimed a pinned member: ~S" reclaimed))
      ;; Exactly R completes, and no current (R+1) bytes are substituted.
      (export-complete op)
      (check-equal r (state-export-revision op) "the export completed at the wrong revision")
      (check-string= r-bytes (state-export-bytes op)
                     "the current bytes were substituted for the pinned capture"))
    ;; The other arm: an unpinned member the export needs is a named gap.
    (let ((op (export-state (kernel-state k) :id "op-2")))
      (multiple-value-bind (clipped reclaimed gap)
          (retention-pass (kernel-state k)
                          :reclaim '("req-1")
                          :pinned nil
                          :needed (state-export-members op))
        (declare (ignore clipped reclaimed))
        (ok (and gap (search "recovery gap" gap)) "no recovery gap was named: ~A" gap)))))

;;; ------------------------------------------------------------------
;;; state-export-disconnect-and-cancel (docs/SPEC-WORK.md:5892)
;;; ------------------------------------------------------------------

(deftest "state-export-disconnect-and-cancel" "docs/SPEC-WORK.md:5892"
    "expected=lost-client-restart-and-cancel-keep-one-operation-and-one-output-identity"
  (let* ((k (fresh))
         (op (export-state (kernel-state k) :id "op-1" :destination "out")))
    ;; One operation, one output identity across a lost client and a restart:
    ;; the same id answers, and the destination is refused if it already exists.
    (check-string= "op-1" (state-export-id op) "the operation id changed")
    (multiple-value-bind (ok line) (publish-state-export op '("out"))
      (ok (null ok) "an existing destination was published over: ~A" line)
      (ok (search "exists" line) "the refusal names the destination: ~A" line))
    (multiple-value-bind (ok line) (publish-state-export op '())
      (ok ok "the publication refused: ~A" line)
      ;; A second publication keeps the one output identity and claims no
      ;; reversal of the published one.
      (let ((again (publish-state-export op '())))
        (declare (ignore again))
        (ok (null (publish-state-export op '())) "a published export was republished")))
    ;; A cancellation around the no-replace publication keeps one operation and
    ;; claims no reversal of a published one.
    (multiple-value-bind (ok line) (cancel-state-export op)
      (ok (null ok) "a published export was cancelled: ~A" line)
      (ok (search "published" line) "the refusal does not name the published output: ~A" line))
    ;; A staged manifest before the commit is never published.
    (let ((staged (export-state (kernel-state k) :id "op-2")))
      (check-equal :running (state-export-status staged) "a staged export is not running")
      (check-equal nil (state-export-published staged) "a staged manifest was already published"))))

;;; ------------------------------------------------------------------
;;; state-load-is-isolated (docs/SPEC-WORK.md:5896)
;;; ------------------------------------------------------------------

(defvar *state-load-test-counter* 0)

(defun test-state-load-dir (name)
  "A fresh scratch parent for one state-load replay beneath the suite's one
private root, so neither the three copies of this slice file (the loader list
names it once per fold) nor a concurrent suite ever collide."
  (test-private-dir
   (format nil "state-load-~A-~D-~D" name
           (get-universal-time) (incf *state-load-test-counter*))))

(deftest "state-load-is-isolated" "docs/SPEC-WORK.md:5896"
    "expected=no-ownership-dispatch-replay-merge-resolver-network-or-repo-write;re-export-equal"
  (let* ((base (test-state-load-dir "isolated"))
         (k (fresh)))
    (ok (submit k (close-request :request "req-1")) "close refused")
    (let* ((source (kernel-state k))
           (before-bytes (canonical-string (state-canonical-form source)))
           (before-rev (state-revision source))
           (before-hist (state-history source))
           (export-dir (concatenate 'string base "export/"))
           (snap-dir (concatenate 'string base "snap/"))
           (writes nil)
           (snap nil)
           (line nil))
      ;; A real export directory on disk is the load's `--from` input.
      (multiple-value-bind (manifest manifest-hash member)
          (write-state-export source export-dir :id "exp-1")
        (declare (ignore manifest))
        (ok (probe-file (merge-pathnames "MANIFEST.sexp" export-dir))
            "the export wrote no MANIFEST.sexp")
        (check-equal 64 (length manifest-hash) "the manifest hash is a SHA-256 hex")
        (ok (probe-file member) "the export wrote no state member"))
      ;; The load reads the directory and materialises one snapshot under
      ;; `--into`, with every session-level effect left at zero.
      (with-isolation
        (multiple-value-bind (loaded out)
            (state-load :from export-dir :into snap-dir
                        :max-bytes 1000000 :max-depth 10 :max-nodes 100)
          (setf snap loaded line out))
        (setf writes (isolation-writes)))
      (ok snap "the isolated load refused: ~A" line)
      (ok (search "LOAD OK" line) "the load line is not LOAD OK: ~A" line)
      (ok (search (format nil "rev=~D" before-rev) line)
          "the load line omits the captured revision: ~A" line)
      (ok (search "manifest=" line) "the load line omits the manifest hash: ~A" line)
      (ok (search (concatenate 'string (string-right-trim "/" snap-dir) "/snapshot.sexp") line)
          "the load line omits the snapshot path: ~A" line)
      (ok (search (concatenate 'string (string-right-trim "/" snap-dir) "/cache.sexp") line)
          "the load line omits the cache path: ~A" line)
      (dolist (key '(:owners :dispatches :replays :merges :resolvers :network :repo-writes))
        (check-equal 0 (isolation-count key)
                     (format nil "~A happened during an isolated load" key)))
      (check-equal (list snap-dir) writes
                   "the load wrote outside its declared exclusive path")
      ;; No ownership change: the live source is untouched.
      (check-equal before-rev (state-revision source) "the load changed the source revision")
      (check-equal before-hist (state-history source) "the load changed the source history")
      ;; One directory in the snapshot and cache schemas, and no daemon, socket,
      ;; writable journal or OWNER.
      (ok (probe-file (merge-pathnames "snapshot.sexp" snap-dir)) "no snapshot materialised")
      (ok (probe-file (merge-pathnames "cache.sexp" snap-dir)) "no cache materialised")
      (dolist (name '("OWNER" "journal" "owner"))
        (ok (null (probe-file (merge-pathnames name snap-dir)))
            "an isolated load created ~A" name))
      ;; A fresh reader rebuilds the model from the stored bytes, and the loaded
      ;; snapshot answers `query --snapshot`.
      (let ((fresh (read-loaded-snapshot snap-dir)))
        (check-equal (state-open-count source) (snapshot-query fresh)
                     "the snapshot does not answer query --snapshot")
        (check-string= before-bytes
                       (canonical-string (state-canonical-form (snapshot-state fresh)))
                       "the loaded snapshot re-exports differently"))
      ;; Refused as a `--session` by every mutation, replay, clip and handoff verb.
      (dolist (verb '(:state-to-done :state-to-doing :event-reopen :replay :clip :handoff))
        (check-equal nil (snapshot-accept-session-p snap verb)
                     (format nil "the snapshot was accepted as a --session for ~A" verb)))
      ;; No-replace: a second load into the same directory refuses.
      (multiple-value-bind (again out)
          (state-load :from export-dir :into snap-dir :max-bytes 1000000)
        (ok (null again) "an existing destination was loaded over: ~A" out)
        (ok (search "exists" out) "the refusal names the existing destination: ~A" out))
      ;; A changed member digest is a gap, refuses, and leaves no destination.
      (let* ((tampered (concatenate 'string base "tampered/"))
             (dest (concatenate 'string base "tampered-load/")))
        (write-state-export source tampered :id "exp-2")
        (with-open-file (out (merge-pathnames "state/snapshot.sexp" tampered)
                             :direction :output :if-exists :overwrite)
          (write-string "((:id \"tampered\" :type :task :parent () :state :doing)) " out))
        (multiple-value-bind (bad out) (state-load :from tampered :into dest :max-bytes 1000000)
          (ok (null bad) "a changed member digest loaded: ~A" out)
          (ok (search "changed digest" out) "the refusal names the digest: ~A" out)
          (ok (null (probe-file dest)) "an incomplete load left a destination")))
      ;; A bound breach refuses before anything is written.
      (let ((dest (concatenate 'string base "overrun-load/")))
        (multiple-value-bind (bad out) (state-load :from export-dir :into dest :max-bytes 1)
          (ok (null bad) "an output overrun loaded: ~A" out)
          (ok (search "overrun" out) "the refusal names the overrun: ~A" out)
          (ok (null (probe-file dest)) "an overrun left a destination"))))))

;;; ------------------------------------------------------------------
;;; fenced-export-can-finish (docs/SPEC-WORK.md:5902)
;;; ------------------------------------------------------------------

(deftest "fenced-export-can-finish" "docs/SPEC-WORK.md:5902"
    "expected=terminal-read-by-id;cancel-reconciles;canonical-write=fenced;one-owner"
  (let* ((k (fresh))
         (s (make-fenced-session :owner "rowan")))
    ;; An export can start in a fenced session, and its status and terminal line
    ;; are read by its id.
    (let ((op (fenced-export-start s (kernel-state k) :id "op-1")))
      (export-complete op)
      (multiple-value-bind (ok line code) (fenced-status s "op-1")
        (ok ok "the terminal status was refused: ~A" line)
        (check-equal 0 code "the terminal status exit code")
        (ok (search "op-1" line) "the terminal line does not name its id: ~A" line)))
    ;; Separately, an unfinished one is cancelled with publication reconciled.
    (fenced-export-start s (kernel-state k) :id "op-2")
    (multiple-value-bind (ok line) (fenced-cancel s "op-2")
      (ok ok "the cancel refused: ~A" line)
      (ok (search "cancel" line) "the terminal line does not name the cancel: ~A" line))
    ;; An unknown id and a non-export id are refused.
    (multiple-value-bind (ok line) (fenced-status s "no-such-op")
      (ok (null ok) "an unknown id answered: ~A" line))
    (multiple-value-bind (ok line) (fenced-status s "task-1")
      (ok (null ok) "a non-export id answered: ~A" line))
    ;; `operation list` and every canonical write are refused `fenced`.
    (multiple-value-bind (ok line) (fenced-operation-list s)
      (ok (null ok) "operation list was not refused: ~A" line)
      (ok (search "fenced" line) "the refusal does not name fenced: ~A" line))
    (dolist (verb '(:state-to-done :event-reopen :move :priority :undo))
      (multiple-value-bind (ok line) (fenced-write s verb)
        (ok (null ok) "the canonical write ~A was admitted: ~A" verb line)
        (ok (search "fenced" line) "the refusal does not name fenced: ~A" line)))
    ;; No second owner and no mutation authority is created.
    (multiple-value-bind (ok line) (fenced-claim s "someone-else")
      (ok (null ok) "a second owner claimed the session: ~A" line)
      (ok (search "owner" line) "the refusal does not name the owner: ~A" line))))
