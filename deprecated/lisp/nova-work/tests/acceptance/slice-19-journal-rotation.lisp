;;;; slice-19-journal-rotation.lisp --- a stable logical journal identity and a
;;;; rotation that validates its replacement first (nova-work E05 row 6).
;;;;
;;;; SPEC-WORK.md:7176-7182  **Rotating or pruning a journal keeps a reachable
;;;;                          verified cut and every tail record and disposition
;;;;                          each retained savepoint needs, or publishes a
;;;;                          validated replacement first**; the newest filename
;;;;                          and the largest revision are never that proof, and
;;;;                          a rotation may hand a bounded locator to the same
;;;;                          retained record rather than a scan.
;;;; SPEC-WORK.md:402-410    a clip may rotate the journal; a read begins at the
;;;;                          newest boundary record, so the journal need not
;;;;                          grow forever.
;;;; SPEC-WORK.md:471-482    **A rotation opens a new physical segment of the
;;;;                          same logical journal**: the header names the
;;;;                          journal id, the previous segment and the copied
;;;;                          boundary record.
;;;; SPEC-WORK.md:6718-6722  the named replay `rotation-keeps-one-journal`.
;;;; SPEC-WORK.md:7119-7121  retention may never remove the only recoverable
;;;;                          copy.
;;;;
;;;; Every temp file lives under TMPDIR, mode 0700, and is removed at the end.

(in-package #:nova-work/tests)

(defun s19-temp-dir ()
  "A fresh 0700 directory under this run's own root. It was
`nova-work-test-rotation/<universal-time>-<counter>/` in the shared temporary
directory, which two suites on one host name alike (nova-tools#1699)."
  (test-temp-dir "rotation"))

(defun s19-delete-tree (dir)
  (ignore-errors (uiop:delete-directory-tree dir :validate t
                                                 :if-does-not-exist :ignore)))

(defun s19-init-digest ()
  (root-digest (make-seed-state *seed*)))

(defun s19-write-text (path text)
  (with-open-file (out path :direction :output :element-type 'character
                            :external-format :utf-8 :if-exists :supersede
                            :if-does-not-exist :create)
    (write-string text out))
  path)

(defun s19-write-manifest-text (root id text)
  (s19-write-text (savepoint-manifest-path root id) text))

(defun s19-plist-remove (plist key)
  (loop for (k v) on plist by #'cddr
        unless (eq k key)
          collect k and collect v))

(defun s19-plist-set (plist key value)
  (append (s19-plist-remove plist key) (list key value)))

(defun s19-header-set (form key value)
  "Set KEY in a header form whose first element is the :journal-header tag."
  (cons (first form) (s19-plist-set (rest form) key value)))

(defun s19-rewrite-header (path transform)
  "Rewrite PATH's first line as (FUNCALL TRANSFORM header-form), leaving every
other byte of PATH unchanged."
  (let* ((text (savepoint-file-text path))
         (newline (position #\Newline text))
         (rest (subseq text (1+ newline)))
         (form (read-restricted (subseq text 0 newline))))
    (with-open-file (out path :direction :output :element-type 'character
                              :external-format :utf-8 :if-exists :supersede)
      (write-string (canonical-string (funcall transform form)) out)
      (write-char #\Newline out)
      (write-string rest out))
    path))

(defun s19-strip-identity (path)
  "Rewrite PATH's header without any :journal-identity, making it a legacy
journal. Where the field is already absent this is a byte-for-byte no-op."
  (s19-rewrite-header
   path
   (lambda (form)
     (cons (first form) (s19-plist-remove (rest form) :journal-identity)))))

(defun s19-corrupt-frame (path)
  "Flip one hex digit of the first frame's stored checksum, keeping the file
valid UTF-8 and the frame structurally parseable: a corrupt record, not a tear."
  (let* ((text (savepoint-file-text path))
         (frame-pos (search "(:frame " text))
         (marker ":checksum \"")
         (cs-pos (search marker text :start2 frame-pos))
         (i (+ cs-pos (length marker))))
    (setf (char text i) (if (char= (char text i) #\a) #\b #\a))
    (s19-write-text path text)))

(defun s19-rename (from to)
  #+sbcl (sb-posix:rename (namestring from) (namestring to))
  #-sbcl (error "no rename outside SBCL"))

(defun s19-remove-journal-files (path)
  (dolist (p (list path
                   (format nil "~A.2" path)
                   (format nil "~A.2.candidate" path)
                   (format nil "~A.hidden" path)))
    (ignore-errors (delete-file p))))

;;; ------------------------------------------------------------------
;;; old-bytes-replay-byte-for-byte-after-the-identity-seam
;;; SPEC-WORK.md:7176-7182
;;; ------------------------------------------------------------------

(deftest "old-bytes-replay-byte-for-byte-after-the-identity-seam"
    "docs/SPEC-WORK.md:7176-7182"
    "expected=a-legacy-journal-opens-replays-and-reserialises-to-the-same-sha256"
  (let* ((fx (savepoint-fixture :id "sp-ob" :mutations 2))
         (journal (getf fx :journal))
         (journal-path (getf fx :journal-path))
         (init (s19-init-digest)))
    (unwind-protect
         (progn
           (close-file-journal journal)
           ;; the OLD header shape: no :journal-identity field
           (s19-strip-identity journal-path)
           (let ((before (file-sha256-hex journal-path)))
             (let ((reopened (open-file-journal journal-path :initial-state-hash init)))
               (unwind-protect
                    (let ((k (make-kernel :state (make-seed-state *seed*)
                                          :journal reopened)))
                      (multiple-value-bind (rk ev rec) (replay-journal reopened k)
                        (declare (ignore rk ev))
                        (check-equal 2 rec "the legacy journal replayed two records")))
                 (close-file-journal reopened)))
             (check-string= before (file-sha256-hex journal-path)
                            "the legacy journal's bytes changed")))
      (ignore-errors (close-file-journal journal))
      (s19-remove-journal-files journal-path)
      (s19-delete-tree (getf fx :root)))))

;;; ------------------------------------------------------------------
;;; an-old-manifest-verifies-by-its-header-hash-alone
;;; SPEC-WORK.md:7176-7182
;;; ------------------------------------------------------------------

(deftest "an-old-manifest-verifies-by-its-header-hash-alone"
    "docs/SPEC-WORK.md:7176-7182"
    "expected=a-manifest-without-journal-logical-verifies-header-only;the-new-shape-requires-and-verifies-both-fields"
  (let* ((fx (savepoint-fixture :id "sp-om" :mutations 2))
         (root (getf fx :root))
         (journal (getf fx :journal))
         (journal-path (getf fx :journal-path))
         (manifest-path (savepoint-manifest-path root "sp-om"))
         (original (savepoint-file-text manifest-path))
         (manifest (read-savepoint-manifest root "sp-om")))
    (unwind-protect
         (progn
           ;; 1. the explicit legacy shape: no :journal-logical verifies by its
           ;;    header hash alone, exactly as today.
           (s19-write-manifest-text root "sp-om"
                                    (canonical-string (s19-plist-remove manifest :journal-logical)))
           (multiple-value-bind (okp line code)
               (savepoint-verify-published root "sp-om" :journal-path journal-path)
             (declare (ignore code))
             (ok okp "a legacy-shape manifest did not verify header-only: ~A" line))
           (s19-write-manifest-text root "sp-om" original)
           ;; 2. the new shape, when the publish carries it, requires both.
           (let ((fresh (read-savepoint-manifest root "sp-om")))
             (when (getf fresh :journal-logical)
               (multiple-value-bind (okp line)
                   (savepoint-verify-published root "sp-om" :journal-path journal-path)
                 (ok okp "the new-shape manifest did not verify both fields: ~A" line))
               ;; a tampered logical identity is refused
               (s19-write-manifest-text
                root "sp-om"
                (canonical-string (s19-plist-set fresh :journal-logical
                                                 (sha256-hex "not-this-journal"))))
               (multiple-value-bind (okp line)
                   (savepoint-verify-published root "sp-om" :journal-path journal-path)
                 (check-equal nil okp "a tampered logical identity verified")
                 (ok (search "journal" line) "the refusal does not name the journal: ~A" line))
               (s19-write-manifest-text root "sp-om" original)
               ;; a tampered header hash is refused
               (s19-write-manifest-text
                root "sp-om"
                (canonical-string (s19-plist-set fresh :journal (sha256-hex "another header"))))
               (multiple-value-bind (okp line)
                   (savepoint-verify-published root "sp-om" :journal-path journal-path)
                 (check-equal nil okp "a tampered header hash verified")
                 (ok (search "journal" line) "the refusal does not name the journal: ~A" line))
               (s19-write-manifest-text root "sp-om" original))))
      (s19-write-manifest-text root "sp-om" original)
      (close-file-journal journal)
      (s19-remove-journal-files journal-path)
      (s19-delete-tree root))))

;;; ------------------------------------------------------------------
;;; rotation-then-restore-succeeds-across-the-new-segment
;;; SPEC-WORK.md:7176-7182
;;; ------------------------------------------------------------------

(deftest "rotation-then-restore-succeeds-across-the-new-segment"
    "docs/SPEC-WORK.md:7176-7182"
    "expected=a-savepoint-cut-restores-across-the-rotated-segment;the-cut-stays-sequence-plus-record-hash"
  (let* ((fx (savepoint-fixture :id "sp-rot" :mutations 2))
         (root (getf fx :root))
         (journal (getf fx :journal))
         (journal-path (getf fx :journal-path))
         (manifest (read-savepoint-manifest root "sp-rot"))
         (cut (getf manifest :replay-cut)))
    (unwind-protect
         (progn
           (close-file-journal journal)
           (let ((old-sha (file-sha256-hex journal-path)))
             (multiple-value-bind (rotated header)
                 (rotate-file-journal journal-path :root root
                                                  :retained-ids (list "sp-rot"))
               (declare (ignore header))
               (ok (probe-file rotated) "no rotated segment was published")
               (check-string= old-sha (file-sha256-hex journal-path)
                              "rotation changed the old segment")
               (multiple-value-bind (okp line code restored)
                   (savepoint-restore-published root "sp-rot" :journal-path rotated)
                 (ok okp "the restore across the rotated segment refused: ~A" line)
                 (check-equal 0 code "the restore exits 0")
                 (ok (restored-savepoint-p restored) "the restore exposed no state"))
               (multiple-value-bind (okp line code report)
                   (savepoint-verify-published root "sp-rot" :journal-path rotated)
                 (declare (ignore code))
                 (ok okp "verify across the rotated segment refused: ~A" line)
                 (check-equal (getf cut :sequence)
                              (getf (savepoint-report-cut report) :sequence)
                              "the cut's sequence moved")
                 (check-equal (getf cut :sha256)
                              (getf (savepoint-report-cut report) :sha256)
                              "the cut's record hash moved")))))
      (close-file-journal journal)
      (s19-remove-journal-files journal-path)
      (s19-delete-tree root))))

;;; ------------------------------------------------------------------
;;; restore-refuses-a-live-journal-of-another-logical-identity
;;; SPEC-WORK.md:7176-7182
;;; ------------------------------------------------------------------

(deftest "restore-refuses-a-live-journal-of-another-logical-identity"
    "docs/SPEC-WORK.md:7176-7182"
    "expected=verify-and-restore-both-refuse-a-different-logical-identity"
  (let* ((fx (savepoint-fixture :id "sp-id" :mutations 2))
         (root (getf fx :root))
         (journal (getf fx :journal))
         (journal-path (getf fx :journal-path)))
    (unwind-protect
         (progn
           (close-file-journal journal)
           (multiple-value-bind (rotated header)
               (rotate-file-journal journal-path :root root :retained-ids (list "sp-id"))
             (declare (ignore header))
             ;; same header shape, a DIFFERENT logical identity
             (s19-rewrite-header
              rotated
              (lambda (form)
                (s19-header-set form :journal-identity
                                (sha256-hex "another logical journal"))))
             (multiple-value-bind (okp line code)
                 (savepoint-verify-published root "sp-id" :journal-path rotated)
               (check-equal nil okp "a live journal of another logical identity verified")
               (check-equal 1 code "the refusal exits 1")
               (ok (search "identity" line) "the refusal does not name the identity: ~A" line))
             (multiple-value-bind (okp line code restored)
                 (savepoint-restore-published root "sp-id" :journal-path rotated)
               (declare (ignore line code))
               (check-equal nil okp "a live journal of another logical identity restored")
               (ok (not (restored-savepoint-p restored)) "a refused restore exposed a state"))))
      (close-file-journal journal)
      (s19-remove-journal-files journal-path)
      (s19-delete-tree root))))

;;; ------------------------------------------------------------------
;;; restore-refuses-a-live-journal-whose-chain-to-the-cut-is-broken
;;; SPEC-WORK.md:7176-7182
;;; ------------------------------------------------------------------

(deftest "restore-refuses-a-live-journal-whose-chain-to-the-cut-is-broken"
    "docs/SPEC-WORK.md:7176-7182"
    "expected=same-logical-identity-but-the-chain-does-not-reach-the-saved-cut"
  (let* ((seed *seed*)
         (init (s19-init-digest))
         (journal-path (test-journal-path "journal-rotate-chain"))
         (root (s19-temp-dir))
         (journal (open-file-journal journal-path :initial-state-hash init))
         (k (make-kernel :state (make-seed-state seed) :journal journal)))
    (unwind-protect
         (progn
           ;; a savepoint whose cut is the FIRST record
           (submit k (close-request :node "acme/work/f1/t1" :request "ch-1"))
           (multiple-value-bind (okp line)
               (savepoint-create k :root root :id "sp-chain" :as "rowan")
             (unless okp (fail "the fixture's savepoint was refused: ~A" line)))
           ;; a second record, then a rotation that hands forward only the
           ;; SECOND cut: the first cut's chain is left behind, while the same
           ;; logical identity and the same root header still hold.
           (submit k (close-request :node "acme/work/f1/t2" :request "ch-2"))
           (close-file-journal journal)
           (multiple-value-bind (rotated header)
               (rotate-file-journal journal-path :root root :retained-ids '())
             (declare (ignore header))
             (multiple-value-bind (okp line code)
                 (savepoint-verify-published root "sp-chain" :journal-path rotated)
               (check-equal nil okp "a broken chain verified")
               (check-equal 1 code "the refusal exits 1")
               (ok (search "chain" line) "the refusal does not name the chain: ~A" line))
             (multiple-value-bind (okp line code restored)
                 (savepoint-restore-published root "sp-chain" :journal-path rotated)
               (declare (ignore line code))
               (check-equal nil okp "a broken chain restored")
               (ok (not (restored-savepoint-p restored))
                   "a refused restore exposed a state"))))
      (ignore-errors (close-file-journal journal))
      (s19-remove-journal-files journal-path)
      (s19-delete-tree root))))

;;; ------------------------------------------------------------------
;;; a-legacy-journal-without-the-identity-field-selects-the-legacy-shape
;;; SPEC-WORK.md:7176-7182
;;; ------------------------------------------------------------------

(deftest "a-legacy-journal-without-the-identity-field-selects-the-legacy-shape"
    "docs/SPEC-WORK.md:7176-7182"
    "expected=absence-selects-the-named-legacy-branch-and-is-not-wildcard-acceptance"
  (let* ((fx (savepoint-fixture :id "sp-lg" :mutations 2))
         (root (getf fx :root))
         (journal (getf fx :journal))
         (journal-path (getf fx :journal-path))
         (manifest (read-savepoint-manifest root "sp-lg")))
    (unwind-protect
         (progn
           (close-file-journal journal)
           ;; make the live journal legacy
           (s19-strip-identity journal-path)
           ;; absence selects the named legacy branch, and it is not wildcard
           ;; acceptance: the live journal really has no logical identity.
           (check-equal nil (journal-logical-identity journal-path)
                        "a stripped journal still reports a logical identity")
           ;; a NEW-shape manifest against that legacy journal still refuses.
           (s19-write-manifest-text
            root "sp-lg"
            (canonical-string
             (s19-plist-set manifest :journal-logical (sha256-hex "a new-shape manifest"))))
           (multiple-value-bind (okp line)
               (savepoint-verify-published root "sp-lg" :journal-path journal-path)
             (check-equal nil okp "a new-shape manifest verified against a legacy journal")
             (ok (search "journal" line) "the refusal does not name the journal: ~A" line)))
      (s19-remove-journal-files journal-path)
      (s19-delete-tree root))))

;;; ------------------------------------------------------------------
;;; rotation-refuses-a-torn-tail-and-leaves-the-old-file-byte-identical
;;; SPEC-WORK.md:7176-7182
;;; ------------------------------------------------------------------

(deftest "rotation-refuses-a-torn-tail-and-leaves-the-old-file-byte-identical"
    "docs/SPEC-WORK.md:7176-7182"
    "expected=refused-before-any-write;old-sha256-unchanged;no-candidate-left"
  (let* ((fx (savepoint-fixture :id "sp-torn" :mutations 2))
         (root (getf fx :root))
         (journal (getf fx :journal))
         (journal-path (getf fx :journal-path)))
    (unwind-protect
         (progn
           (close-file-journal journal)
           (with-open-file (out journal-path :direction :output :if-exists :append
                                           :element-type 'character
                                           :external-format :utf-8)
             (write-string "(:frame :seq 3 :len 3 :checksum \"nope\" :record (:reso" out)
             (write-char #\Newline out))
           (let ((before (file-sha256-hex journal-path))
                 (refused nil))
             (handler-case
                 (rotate-file-journal journal-path :root root :retained-ids (list "sp-torn"))
               (unsupported-input (c) (setf refused (format nil "~A" c))))
             (ok refused "a torn tail rotated")
             (ok (search "torn" refused)
                 "the refusal does not name the torn tail: ~A" refused)
             (check-string= before (file-sha256-hex journal-path)
                            "the old file changed")
             (ok (null (probe-file (format nil "~A.2.candidate" journal-path)))
                 "a candidate was left on disk")
             (ok (null (probe-file (format nil "~A.2" journal-path)))
                 "a rotated segment was published")))
      (close-file-journal journal)
      (s19-remove-journal-files journal-path)
      (s19-delete-tree root))))

;;; ------------------------------------------------------------------
;;; rotation-refuses-a-corrupt-frame-and-leaves-the-old-file-byte-identical
;;; SPEC-WORK.md:7176-7182
;;; ------------------------------------------------------------------

(deftest "rotation-refuses-a-corrupt-frame-and-leaves-the-old-file-byte-identical"
    "docs/SPEC-WORK.md:7176-7182"
    "expected=a-corrupt-record-is-told-apart-from-a-torn-tail;old-sha256-unchanged"
  (let* ((fx (savepoint-fixture :id "sp-corrupt" :mutations 2))
         (root (getf fx :root))
         (journal (getf fx :journal))
         (journal-path (getf fx :journal-path)))
    (unwind-protect
         (progn
           (close-file-journal journal)
           (s19-corrupt-frame journal-path)
           (let ((before (file-sha256-hex journal-path))
                 (refused nil))
             (handler-case
                 (rotate-file-journal journal-path :root root :retained-ids (list "sp-corrupt"))
               (unsupported-input (c) (setf refused (format nil "~A" c))))
             (ok refused "a corrupt frame rotated")
             (ok (search "corrupt" refused)
                 "the refusal does not name the corrupt record: ~A" refused)
             (ok (not (search "torn" refused))
                 "a corrupt record was rounded to a torn tail: ~A" refused)
             (check-string= before (file-sha256-hex journal-path)
                            "the old file changed")
             (ok (null (probe-file (format nil "~A.2.candidate" journal-path)))
                 "a candidate was left on disk")))
      (close-file-journal journal)
      (s19-remove-journal-files journal-path)
      (s19-delete-tree root))))

;;; ------------------------------------------------------------------
;;; a-candidate-segment-that-fails-re-verification-publishes-nothing
;;; SPEC-WORK.md:7176-7182
;;; ------------------------------------------------------------------

(deftest "a-candidate-segment-that-fails-re-verification-publishes-nothing"
    "docs/SPEC-WORK.md:7176-7182"
    "expected=the-candidate-is-deleted;the-old-file-is-untouched;the-answer-is-a-refusal"
  (let* ((fx (savepoint-fixture :id "sp-cand" :mutations 2))
         (root (getf fx :root))
         (journal (getf fx :journal))
         (journal-path (getf fx :journal-path))
         (image-path (merge-pathnames "state" (savepoint-directory root "sp-cand"))))
    (unwind-protect
         (progn
           (close-file-journal journal)
           ;; the retained savepoint can no longer verify, so the candidate
           ;; cannot be published.
           (flip-one-ascii-byte image-path)
           (let ((before (file-sha256-hex journal-path))
                 (refused nil))
             (handler-case
                 (rotate-file-journal journal-path :root root :retained-ids (list "sp-cand"))
               (unsupported-input (c) (setf refused (format nil "~A" c))))
             (ok refused "a candidate that cannot re-verify was published")
             (ok (search "sp-cand" refused)
                 "the refusal does not name the retained savepoint: ~A" refused)
             (check-string= before (file-sha256-hex journal-path)
                            "the old file changed")
             (ok (null (probe-file (format nil "~A.2.candidate" journal-path)))
                 "the failed candidate was left on disk")
             (ok (null (probe-file (format nil "~A.2" journal-path)))
                 "a rotated segment was published")))
      (close-file-journal journal)
      (s19-remove-journal-files journal-path)
      (s19-delete-tree root))))

;;; ------------------------------------------------------------------
;;; the-rotation-locator-answers-the-retained-cut-without-scanning-the-old-segment
;;; SPEC-WORK.md:7176-7182
;;; ------------------------------------------------------------------

(deftest "the-rotation-locator-answers-the-retained-cut-without-scanning-the-old-segment"
    "docs/SPEC-WORK.md:7176-7182"
    "expected=the-locator-answers-from-the-segment-header-alone-with-the-old-segment-unreadable"
  (let* ((fx (savepoint-fixture :id "sp-loc" :mutations 2))
         (root (getf fx :root))
         (journal (getf fx :journal))
         (journal-path (getf fx :journal-path))
         (manifest (read-savepoint-manifest root "sp-loc"))
         (cut (getf manifest :replay-cut)))
    (unwind-protect
         (progn
           (close-file-journal journal)
           (multiple-value-bind (rotated header)
               (rotate-file-journal journal-path :root root :retained-ids (list "sp-loc"))
             ;; the old segment is unreadable for the duration
             (s19-rename journal-path (format nil "~A.hidden" journal-path))
             (let ((locator (journal-rotation-locator header cut)))
               (ok locator "the locator did not answer the retained cut")
               (check-equal (getf cut :sequence) (getf locator :sequence)
                            "the locator's sequence is not the cut's")
               (check-equal (getf cut :sha256) (getf locator :sha256)
                            "the locator's hash is not the cut's"))
             (ok (probe-file rotated) "the rotated segment vanished")))
      (ignore-errors (s19-rename (format nil "~A.hidden" journal-path) journal-path))
      (close-file-journal journal)
      (s19-remove-journal-files journal-path)
      (s19-delete-tree root))))

;;; ------------------------------------------------------------------
;;; the-journal-identity-is-32-bytes-from-the-os-csprng
;;; docs/SPEC-WORK.md:451 -- a journal id is 256 random bits as 64 lowercase hex
;;; characters, an identity and never an ownership token.
;;; ------------------------------------------------------------------

(defun s19-byte-source (bytes)
  "A byte source that answers BYTES for any requested count."
  (lambda (n)
    (declare (ignore n))
    bytes))

(deftest "the-journal-identity-is-32-bytes-from-the-os-csprng"
    "docs/SPEC-WORK.md:451"
    "expected=the-id-is-the-known-32-bytes-lowercase-hex-and-two-real-mints-differ"
  (let* ((bytes (make-array 32 :element-type '(unsigned-byte 8)
                               :initial-contents (loop for i from 0 below 32 collect i)))
         (expected "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
         (known (let ((nova-work::*journal-identity-byte-source*
                       (s19-byte-source bytes)))
                  (mint-journal-identity))))
    ;; The 32 bytes are hex-encoded DIRECTLY -- not hashed into a well-formed
    ;; digest -- so the identity is exactly the bytes' lowercase hex.
    (check-equal 64 (length known) "the identity is not 64 characters")
    (check-string= expected known
                   "the identity is not the known 32 bytes' lowercase hex")
    (check-string= (string-downcase known) known
                   "the identity is not lowercase hex")
    ;; With the seam unbound the real OS CSPRNG answers: two successive mints
    ;; differ and each is 64 lowercase hex characters.
    (let ((a (mint-journal-identity))
          (b (mint-journal-identity)))
      (check-equal 64 (length a) "a real mint is not 64 characters")
      (check-equal 64 (length b) "a real mint is not 64 characters")
      (check-string= (string-downcase a) a "a real mint is not lowercase hex")
      (ok (string/= a b) "two successive real mints were equal: ~A" a))))

;;; ------------------------------------------------------------------
;;; a-short-csprng-read-refuses-and-mints-nothing
;;; docs/SPEC-WORK.md:451 -- a source that falls short must refuse, never pad,
;;; never hash, and never fall back; no journal or segment file is created.
;;; ------------------------------------------------------------------

(deftest "a-short-csprng-read-refuses-and-mints-nothing"
    "docs/SPEC-WORK.md:451"
    "expected=a-short-or-erroring-source-refuses-and-no-journal-or-segment-file-appears"
  (let ((empty (s19-temp-dir)))
    (unwind-protect
         (progn
           ;; a 31-byte answer is not 256 bits: refuse, naming the source.
           (let ((refused nil))
             (handler-case
                 (let ((nova-work::*journal-identity-byte-source*
                         (s19-byte-source
                          (make-array 31 :element-type '(unsigned-byte 8)
                                         :initial-element 7))))
                   (mint-journal-identity))
               (unsupported-input (c) (setf refused (format nil "~A" c))))
             (ok refused "a 31-byte byte source minted an identity")
             (ok (search "byte source" refused)
                 "the refusal does not name the source: ~A" refused))
           ;; a source that errors refuses too, naming the source.
           (let ((refused nil))
             (handler-case
                 (let ((nova-work::*journal-identity-byte-source*
                         (lambda (n)
                           (declare (ignore n))
                           (error 'unsupported-input
                                  :what "the journal identity byte source could not be read"))))
                   (mint-journal-identity))
               (unsupported-input (c) (setf refused (format nil "~A" c))))
             (ok refused "a signalling byte source minted an identity")
             (ok (search "byte source" refused)
                 "the refusal does not name the source: ~A" refused))
           ;; a rotation that must mint for a legacy journal refuses at the
           ;; rotation call site and publishes no new segment.
           (let* ((rotdir (s19-temp-dir))
                  (journal-path (format nil "~Alegacy.journal" (namestring rotdir)))
                  (init (s19-init-digest))
                  (journal (open-file-journal journal-path :initial-state-hash init)))
             (unwind-protect
                  (progn
                    (close-file-journal journal)
                    (s19-strip-identity journal-path)
                    (let ((refused nil))
                      (handler-case
                          (let ((nova-work::*journal-identity-byte-source*
                                  (s19-byte-source
                                   (make-array 31 :element-type '(unsigned-byte 8)
                                                  :initial-element 9))))
                            (rotate-file-journal journal-path :root rotdir :retained-ids '()))
                        (unsupported-input (c) (setf refused (format nil "~A" c))))
                      (ok refused "a rotation minted from a short byte source")
                      (ok (null (probe-file (format nil "~A.2.candidate" journal-path)))
                          "a candidate segment was left on disk")
                      (ok (null (probe-file (format nil "~A.2" journal-path)))
                          "a segment was published")))
               (ignore-errors (close-file-journal journal))
               (s19-remove-journal-files journal-path)
               (s19-delete-tree rotdir)))
           ;; nothing was written to the empty directory.
           (ok (null (directory (merge-pathnames "*.*" empty)))
               "a journal or segment file was created: ~S"
               (directory (merge-pathnames "*.*" empty))))
      (s19-delete-tree empty))))
