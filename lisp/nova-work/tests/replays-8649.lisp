;;;; replays-8649.lisp --- the five acceptance replays of card 8649: the
;;;; roadmap proof, one-journal rotation, rule 2's unavailable partition, the
;;;; savepoint's cut and its write-failure retention. Each deftest names the
;;;; paragraph of docs/SPEC-WORK.md it comes from and the required outcome.

(in-package #:nova-work/tests)

;;; ------------------------------------------------------------------
;;; roadmap-proof                              SPEC-WORK.md:6244
;;; ------------------------------------------------------------------

(deftest "roadmap-proof" "docs/SPEC-WORK.md:6244"
    "expected=chat-and-file-renders-identical;optional-axes;partial-and-stale-evidence;shared-prerequisites;discovered-scope;closed-members;marker-edit-preserves-bytes-and-refuses-ambiguity"
  (let* ((rows (list (make-roadmap-row :id "f-1" :axis :feature :kind :required
                                       :state :open :evidence :partial :status :stale)
                     (make-roadmap-row :id "t-1" :axis :task :kind :optional
                                       :state :open :evidence :none :status :current)
                     (make-roadmap-row :id "c-1" :axis :task :kind :required
                                       :state :closed :evidence :full :status :current)))
         (rm (make-roadmap :id "rm-1" :axes '(:feature :task) :rows rows
                           :shared-prerequisites '("dep-shared")
                           :discovered '("disc-1")
                           :closed '("c-1"))))
    ;; chat and file renders identical.
    (check-string= (roadmap-render rm :chat) (roadmap-render rm :file)
                   "chat and file renders are identical")
    (let ((text (roadmap-render rm :chat)))
      (ok (search "axis=feature" text) "the first axis is rendered")
      (ok (search "axis=task" text) "the optional axis is rendered")
      (ok (search "f-1" text) "the required member is rendered")
      (ok (search "t-1" text) "the optional member is rendered")
      (ok (search "evidence=partial" text) "partial evidence is visible")
      (ok (search "status=stale" text) "stale evidence is visible")
      (ok (search "shared-prerequisite dep-shared" text) "a shared prerequisite is named")
      (ok (search "discovered disc-1" text) "newly discovered scope is named")
      (ok (search "closed c-1" text) "a closed member is named"))
    ;; a unique marker edits that byte range and preserves every unrelated byte.
    (multiple-value-bind (edited line) (roadmap-edit-marker "alpha\nMARK\nomega\n" "MARK" "done")
      (check-equal nil line "a unique marker edits cleanly")
      (check-string= "alpha\ndone\nomega\n" edited "only the marker bytes change"))
    ;; an ambiguous marker refuses and publishes nothing.
    (multiple-value-bind (edited line) (roadmap-edit-marker "M\nM\n" "M" "x")
      (check-equal nil edited "an ambiguous marker publishes nothing")
      (ok (search "ambiguous" line) "the ambiguity is named: ~A" line))
    ;; an absent marker refuses.
    (multiple-value-bind (edited line) (roadmap-edit-marker "abc\n" "M" "x")
      (check-equal nil edited "an absent marker publishes nothing")
      (ok (search "not found" line) "the absence is named: ~A" line))))

;;; ------------------------------------------------------------------
;;; rotation-keeps-one-journal               SPEC-WORK.md:5998
;;; ------------------------------------------------------------------

(deftest "rotation-keeps-one-journal" "docs/SPEC-WORK.md:5998"
    "expected=one-journal-id;copied-boundary-same-seq-and-hash;chain-continues;cut-reachable-without-old-scan;export-identical-from-either-file"
  (let* ((rec1 (make-journal-record :seq 1 :events '("e1")))
         (rec2 (make-journal-record :seq 2 :events '("e2")))
         (old (make-journal-segment :path "journal.1" :header '(:segment 1)
                                    :records (list rec1 rec2)))
         (chain (make-journal-chain :id "journal-abc" :segments (list old)))
         (cut1 (list :sequence 1 :sha256 (journal-record-hash rec1)))
         ;; the clip rotates after a savepoint whose cut is record 1: the cut and
         ;; the dispositions it needs must stay reachable without scanning the
         ;; old segment.
         (rotated (rotate-journal chain :retained-cuts (list cut1)
                                        :retained-dispositions '("r1")))
         (new (car (last (journal-chain-segments rotated))))
         (header (journal-segment-header new))
         (boundary (getf header :copied-boundary)))
    ;; the new segment's header names the same journal id.
    (check-equal "journal-abc" (getf header :journal-id)
                 "the new segment header names the same journal id")
    ;; the copied boundary record carries its sequence and hash.
    (check-equal 2 (getf boundary :seq) "the boundary record keeps its sequence")
    (check-string= (journal-record-hash rec2) (getf boundary :sha256)
                   "the boundary record keeps its hash")
    (check-string= "journal.1" (getf header :copied-from)
                   "the boundary names the segment it was copied from")
    ;; the savepoint's cut and every disposition it needs are handed forward as
    ;; a bounded locator, never a scan of the old segment.
    (check-equal (list cut1) (getf header :retained-cuts)
                 "the retained cut is handed forward with its sequence and hash")
    (check-equal '("r1") (getf header :retained-dispositions)
                 "the retained dispositions are handed forward")
    (check-equal nil (savepoint-cut-reachable-p chain 1)
                 "before rotation the cut is not reachable from the newest segment")
    (check-equal t (savepoint-cut-reachable-p rotated 1)
                 "after rotation the savepoint cut is reachable from the header alone")
    (check-equal t (savepoint-cut-reachable-p rotated 2)
                 "the copied boundary record is reachable too")
    ;; a rotation that would lose a retained cut refuses and publishes nothing.
    (handler-case
        (progn (rotate-journal chain :retained-cuts
                              (list (list :sequence 1 :sha256 "not-the-hash")))
               (fail "a rotation losing a retained cut published a replacement"))
      (unsupported-input (c)
        (ok (search "lose retained cut" (format nil "~A" c))
            "the lost cut is named: ~A" c)))
    ;; the chain continues past the copied boundary.
    (let* ((rotated2 (journal-append-record rotated (make-journal-record :events '("e3"))))
           (new2 (car (last (journal-chain-segments rotated2)))))
      (check-equal 3 (journal-record-seq (car (last (journal-segment-records new2))))
                   "the chain continues past the boundary")
      (check-equal 2 (journal-record-seq (first (journal-segment-records new2)))
                   "the copied boundary record is the first record of the new segment"))
    ;; export --journal writes the same bundle from either file, and an unknown
    ;; file refuses rather than being guessed.
    (check-string= (export-journal-bundle rotated :from "journal.1")
                   (export-journal-bundle rotated :from (journal-segment-path new))
                   "the same bundle is written from either file")
    (handler-case
        (progn (export-journal-bundle rotated :from "journal.9")
               (fail "an unknown file wrote a bundle"))
      (unsupported-input () t))))

;;; ------------------------------------------------------------------
;;; rule-2-unavailable-is-not-green          SPEC-WORK.md:5024/5633
;;; ------------------------------------------------------------------

(deftest "rule-2-unavailable-is-not-green" "docs/SPEC-WORK.md:5033"
    "expected=unavailable-partition-is-a-finding-at-exit-1;distinct-from-dangling;never-green-over-unread-history"
  (let ((ref '(:id "n-9" :partition "2026-09-01" :resolved-p nil)))
    ;; a partition that cannot be read is `unavailable`, a finding at exit 1.
    (multiple-value-bind (verdict line) (rule-2-check ref :partition-readable-p nil)
      (check-equal :unavailable verdict "an unread partition is unavailable")
      (ok (search "rule 2: unavailable partition=2026-09-01" line)
          "the finding names the partition: ~A" line))
    ;; it is distinct from `dangling`.
    (multiple-value-bind (verdict line) (rule-2-check ref :partition-readable-p t)
      (check-equal :dangling verdict "a readable partition with no node is dangling")
      (ok (search "dangling" line) "the dangling finding is named: ~A" line))
    (check-equal nil (equal :unavailable :dangling)
                 "incomplete and invalid are different answers"))
  ;; a resolved reference is green; a green rule prints WORK OK, an unavailable
  ;; rule prints WORK FAIL at exit 1 and never green over unread history.
  (let ((good (rule-2-check '(:id "n-1" :partition "2026-09-01" :resolved-p t)
                            :partition-readable-p nil))
        (bad (rule-2-check '(:id "n-9" :partition "2026-09-01" :resolved-p nil)
                           :partition-readable-p nil)))
    (check-equal :green good "a resolved reference is green")
    (multiple-value-bind (code line) (validate-report (list good))
      (check-equal 0 code "an all-green validation exits 0")
      (ok (search "WORK OK" line) "green prints WORK OK: ~A" line))
    (multiple-value-bind (code line) (validate-report (list bad))
      (check-equal 1 code "an unavailable finding exits 1")
      (ok (search "WORK FAIL" line) "a finding prints WORK FAIL: ~A" line)
      (ok (not (search "WORK OK" line)) "green is never printed over unread history"))))

;;; ------------------------------------------------------------------
;;; savepoint-cut-never-splits-an-envelope   SPEC-WORK.md:5989
;;; ------------------------------------------------------------------

(deftest "savepoint-cut-never-splits-an-envelope" "docs/SPEC-WORK.md:5989"
    "expected=two-events-once-in-the-image;retry-byte-identical;cut-inside-an-envelope-refused;no-image-published;manifest-names-schema-journal-revision-cut-boundary-content"
  (let* ((rec0 (list :seq 1 :request "r0" :reply '("OK r0") :payload-sha256 "p0" :events '(1)))
         (rec1 (list :seq 2 :request "r1" :reply '("OK r1") :payload-sha256 "p1" :events '(2 3)))
         (records (list rec0 rec1))
         (journal (make-journal-chain
                   :id "journal-abc"
                   :segments (list (make-journal-segment
                                    :path "journal.1"
                                    :header (list :copied-boundary
                                                  (list :seq 1
                                                        :sha256 (journal-record-hash
                                                                 (make-journal-record :seq 1 :events '(1)))))
                                    :records '()))))
         (store (make-savepoint-store)))
    ;; a savepoint after the two-event request: both events once in the image.
    (multiple-value-bind (s line) (savepoint-write store "sp-1" 2 3 records :journal journal)
      (check-equal nil line "a cut at a record boundary publishes")
      (let* ((sp (savepoint-store-verified s))
             (events (getf (savepoint-image-events 3 records) :events))
             (replies (savepoint-retained-replies records)))
        (check-equal '(1 2 3) events "both events are represented once")
        (check-equal 3 (length events) "no event is duplicated")
        ;; the retained disposition of the two-event request answers its retry
        ;; byte-identically, and a disposition before the cut is retained too.
        (check-string= "OK r1"
                       (first (getf (find "r1" replies :key (lambda (d) (getf d :request))
                                          :test #'equal)
                                    :reply))
                       "a retry is answered the byte-identical original line")
        (check-equal "r0" (getf (first replies) :request)
                     "a disposition whose events lie before the cut is retained")
        (check-equal "p0" (getf (first replies) :payload-sha256)
                     "the retained disposition carries its payload digest")
        (check-equal 1 (getf (first replies) :sequence)
                     "the retained disposition carries its record sequence")
        (check-string= (journal-record-hash (make-journal-record :seq 1 :events '(1)))
                       (getf (first replies) :record-sha256)
                       "the retained disposition carries its record hash")
        ;; the manifest names the schema, the journal, the local revision, the
        ;; replay cut, the boundary and the two content references, in order,
        ;; and holds no digest of itself.
        (check-string= "work-savepoint-v1" (savepoint-schema sp) "the schema is named")
        (check-string= "journal-abc" (savepoint-journal-id sp) "the journal id is named")
        (check-equal 2 (savepoint-local-revision sp) "the image's local revision is named")
        (check-equal (list :sequence 2
                           :sha256 (journal-record-hash (make-journal-record :seq 2 :events '(2 3))))
                     (savepoint-replay-cut sp)
                     "the replay cut is a sequence and a record hash")
        (check-equal (list :sequence 1
                           :sha256 (journal-record-hash (make-journal-record :seq 1 :events '(1))))
                     (savepoint-boundary sp)
                     "the newest boundary record is named the same way")
        (check-equal '(:schema :journal :local-revision :replay-cut :boundary :state :local-replies)
                     (loop for (k nil) on (savepoint-manifest sp) by #'cddr collect k)
                     "the manifest's fields are in the spec's order and none is its own digest")
        ;; the image and the retained replies are hash-checked content
        ;; references inside the savepoint's own root.
        (check-string= (sha256-hex (canonical-string (list :events '(1 2 3))))
                       (getf (savepoint-image sp) :sha256)
                       "the image reference hashes the image bytes")
        (check-string= (sha256-hex (canonical-string replies))
                       (getf (savepoint-local-replies sp) :sha256)
                       "the local-replies reference hashes the retained replies")
        ;; `manifest=<sha>` is the manifest's complete canonical bytes hashed
        ;; outside it.
        (check-string= (sha256-hex (canonical-string (savepoint-manifest sp)))
                       (savepoint-manifest-sha sp)
                       "manifest=<sha> is the canonical manifest bytes hashed outside")
        (ok (search (format nil "manifest=~A" (savepoint-manifest-sha sp)) (savepoint-list s))
            "savepoint list prints manifest=<sha>")))
    ;; an initial image has no cut: the manifest spells it (:absent).
    (multiple-value-bind (s line) (savepoint-write store "sp-0" 0 0 '())
      (check-equal nil line "an initial image writes")
      (check-equal '(:absent) (savepoint-replay-cut (savepoint-store-verified s))
                   "a cut an initial image has none of is (:absent)"))
    ;; a cut between the two events is refused and no image is published.
    (multiple-value-bind (s line) (savepoint-write store "sp-2" 2 2 records :journal journal)
      (ok (search "cut inside an envelope" line) "the refusal is named: ~A" line)
      (check-equal nil (savepoint-store-published s) "no image is published"))))

;;; ------------------------------------------------------------------
;;; savepoint-write-failure-keeps-the-previous SPEC-WORK.md:6014
;;; ------------------------------------------------------------------

(deftest "savepoint-write-failure-keeps-the-previous" "docs/SPEC-WORK.md:6014"
    "expected=previous-verified-restores;attempt-verdict-failed;published-kept;manifest-identity-preserved"
  (let* ((records (list (list :seq 1 :request "r1" :reply '("OK r1")
                              :payload-sha256 "p1" :events '(1))))
         (journal (make-journal-chain
                   :id "journal-abc"
                   :segments (list (make-journal-segment :path "journal.1"
                                                         :header '() :records '()))))
         (base (make-savepoint-store)))
    ;; a clean write publishes a new verified savepoint and keeps it as the last
    ;; known-good while a replacement is written.
    (let* ((clean (savepoint-write base "sp-new" 1 1 records :journal journal))
           (clean-sp (savepoint-store-verified clean)))
      (check-equal "sp-new" (savepoint-id clean-sp) "the new savepoint is verified")
      (check-equal (list clean-sp) (savepoint-store-published clean)
                   "the new savepoint is the published last known-good")
      ;; the image write, the manifest publication and the sync each failed in
      ;; turn: the previous verified savepoint restores, its image stays
      ;; published and the attempt is listed verdict=failed.
      (dolist (stage '(:image :manifest :sync))
        (multiple-value-bind (s line)
            (savepoint-write clean "sp-repl" 2 1 records :journal journal :fail-stage stage)
          (ok (search "SAVEPOINT FAIL" line) "a failed write reports: ~A" line)
          (check-equal "sp-new" (savepoint-id (savepoint-store-verified s))
                       "the previous verified savepoint restores after a failed write")
          (check-equal (list clean-sp) (savepoint-store-published s)
                       "the last known-good image stays published through the failure")
          (check-string= (savepoint-manifest-sha clean-sp)
                         (savepoint-manifest-sha (savepoint-store-verified s))
                         "the previous verified identity is preserved")
          (ok (search "verdict=failed" (savepoint-list s))
              "savepoint list prints the attempt verdict=failed")
          (ok (search (format nil "stage=~(~A~)" stage) (savepoint-list s))
              "savepoint list names the failed stage"))))))

;;; ------------------------------------------------------------------
;;; E10-F04-03                                  SPEC-WORK.md:7228-7229
;;; Stage (3): an authorised real repository for read-only capture,
;;; reconciled against the captured records. Stage (4): an import into a
;;; disposable destination with the originals untouched, exported, loaded
;;; in a fresh engine, independently compared, repeated and resumed, with
;;; every gap inspected. The pilot ran for real against the private
;;; sandbox mas-bandwidth/nova-pilot (coordinator ruling 2026-09-23
;;; 1:35 PM ET) via tests/pilots/E10-F04-03-run.sh: the import is
;;; nova-work's own (a state built from the capture, written by
;;; `write-state-export` into a temp dir), and a fresh SBCL process loaded
;;; that export through `state-load` + `read-loaded-snapshot` and hashed
;;; every loaded record itself. The export it loaded is retained at
;;; tests/pilots/E10-F04-03-export/; this test verifies the receipt and
;;; loads that retained export again through the same path. (Renamed from
;;; #2390's duplicate TestE10F04RunTheAuthorizedReadOnly, which lives in
;;; replays-8643.)
;;; ------------------------------------------------------------------

(defparameter *e10-f04-03-receipt*
  "tests/pilots/E10-F04-03-nova-pilot.sexp")

(defparameter *e10-f04-03-repository* "mas-bandwidth/nova-pilot")

(defun %read-pilot-receipt ()
  (let ((path (asdf:system-relative-pathname :nova-work/tests *e10-f04-03-receipt*)))
    (when (probe-file path)
      (with-open-file (in path :direction :input :external-format :utf-8)
        (let ((*read-eval* nil)) (read in))))))

(defun %pilot-inventory (records)
  (mapcar (lambda (r) (inventory-record (getf r :id) (getf r :kind)
                                        :original (getf r :original)
                                        :mapping (getf r :mapping)))
          records))

(defun %pilot-sha-p (x) (and (stringp x) (= 64 (length x))))

(defun %pilot-receipt-verdict (receipt)
  "Verify a pilot receipt: authorised repository, GET-only, originals
untouched, a nova-work export in a disposable directory loaded by a fresh
engine whose re-export and inventory match, repeated and resumed
identically, no gap. Returns (values okp line)."
  (flet ((no (why) (return-from %pilot-receipt-verdict
                     (values nil (format nil "PILOT FAIL: ~A" why)))))
    (unless receipt (no "no receipt recorded"))
    (let* ((dest (getf receipt :destination))
           (imp (getf dest :import))
           (msha (getf imp :member-sha256))
           (resume (getf dest :resume))
           (ld (getf dest :load))
           (calls (getf receipt :calls)))
      (unless (equal *e10-f04-03-repository* (getf receipt :repository))
        (no (format nil "unauthorised repository ~S" (getf receipt :repository))))
      (unless calls (no "no reads recorded"))
      (let ((bad (find-if-not (lambda (c) (equal "GET" (first c))) calls)))
        (when bad (no (format nil "mutation call ~A ~A" (first bad) (second bad)))))
      (unless (eql 0 (getf receipt :mutations)) (no "mutation count is not zero"))
      (unless (and (%pilot-sha-p (getf receipt :source-before))
                   (string= (getf receipt :source-before) (getf receipt :source-after)))
        (no "source changed across the pilot"))
      (unless (getf receipt :refs) (no "no refs captured"))
      (unless (eq :temp-dir (getf dest :kind)) (no "destination is not disposable"))
      (unless (and (equal "nova-work write-state-export" (getf imp :engine))
                   (%pilot-sha-p (getf imp :manifest-sha256)) (%pilot-sha-p msha))
        (no "the import is not a nova-work export"))
      (unless (equal msha (getf dest :repeat-member-sha256))
        (no "repeat or resume differs from the first import"))
      (unless (and (equal msha (getf resume :member-sha256))
                   (plusp (or (getf resume :kept) 0))
                   (%pilot-sha-p (getf resume :partial-member-sha256))
                   (not (equal msha (getf resume :partial-member-sha256))))
        (no "repeat or resume differs from the first import"))
      (let ((line (getf ld :line)))
        (unless (and (stringp line) (eql 0 (search "LOAD OK" line))
                     (search (format nil "manifest=~A" (getf imp :manifest-sha256)) line))
          (no (format nil "the fresh engine did not load the export: ~S" line))))
      (unless (equal msha (getf ld :reexport-sha256))
        (no "the fresh engine's re-export differs from the import"))
      (when (getf dest :gaps) (no (format nil "gaps ~S" (getf dest :gaps))))
      (multiple-value-bind (okp line)
          (reconcile-inventory (%pilot-inventory (getf receipt :captured))
                               (%pilot-inventory (getf dest :records)))
        (unless okp (no line))
        (unless (equal line (getf receipt :disposition))
          (no (format nil "recorded disposition ~S is not ~S"
                      (getf receipt :disposition) line)))
        (values t line)))))

(defun %pilot-loaded-records (state)
  "The records a loaded nova-work state holds under the pilot repository's
work-set, each hashed here from the loaded bytes: receipt-shaped plists."
  (let ((repo *e10-f04-03-repository*))
    (loop for id in (nova-work::wstate-order state)
          for n = (gethash id (nova-work::wstate-nodes state))
          when (equal repo (nova-work::wnode-parent n))
            collect (list :id (subseq id (1+ (length repo)))
                          :kind (intern (string-upcase (nova-work::wnode-category n)) :keyword)
                          :original (sha256-hex (nova-work::wnode-title n))
                          :mapping (first (nova-work::wnode-links n))))))

(deftest "TestE10F04PilotReceiptOnNovaPilot" "docs/SPEC-WORK.md:7228-7229"
    "expected=real-pilot-receipt-recorded;get-only;originals-untouched;nova-work-export-in-temp-dir;fresh-engine-load-compares-equal;repeat-and-resume-identical;no-gaps;tampering-refused"
  (let* ((receipt (%read-pilot-receipt))
         (captured (getf receipt :captured))
         (dest (getf receipt :destination))
         (imp (getf dest :import)))
    (ok receipt "the E10-F04-03 pilot receipt is recorded at ~A" *e10-f04-03-receipt*)
    (multiple-value-bind (okp line) (%pilot-receipt-verdict receipt)
      (ok okp "the recorded pilot verifies: ~A" line)
      (ok (search "INVENTORY OK" line) "the reconciliation disposition is published: ~A" line))
    (ok (find :issues captured :key (lambda (r) (getf r :kind)))
        "the pilot captured real issues")
    ;; The retained destination is loaded again here through nova-work's
    ;; isolated read-only load: the manifest is the one the pilot's imp
    ;; wrote, the rebuilt model re-exports the msha bytes, and every record
    ;; it holds -- hashed from the loaded bytes -- reconciles with the capture.
    (let* ((from (namestring (asdf:system-relative-pathname
                              :nova-work/tests (concatenate 'string (getf dest :retained) "/"))))
           (into (namestring (merge-pathnames "snap/" (test-temp-dir "e10-f04-03")))))
      (ok (equal "tests/pilots/E10-F04-03-export" (getf dest :retained))
          "the pilot's export is retained: ~S" (getf dest :retained))
      (multiple-value-bind (snap line) (state-load :from from :into into
                                                   :max-bytes 1000000 :max-depth 1000 :max-nodes 1000000)
        (ok snap "the retained export loads: ~A" line)
        (when snap
          (check-string= (getf imp :manifest-sha256) (snapshot-manifest-hash snap)
                         "the retained export is the one the pilot's import wrote")
          (let* ((state (snapshot-state (read-loaded-snapshot into :max-bytes 1000000 :max-depth 1000 :max-nodes 1000000)))
                 (loaded (%pilot-loaded-records state)))
            (check-string= (getf imp :member-sha256)
                           (sha256-hex (canonical-string (state-canonical-form state)))
                           "the loaded destination re-exports the imported bytes")
            (multiple-value-bind (okp line)
                (reconcile-inventory (%pilot-inventory captured) (%pilot-inventory loaded))
              (ok okp "the loaded destination reconciles with the capture: ~A" line)
              (check-string= (getf receipt :disposition) line
                             "the load here gives the recorded disposition"))
            (flet ((by-id (rs) (sort (copy-list (%pilot-inventory rs)) #'string<
                                     :key (lambda (r) (getf r :id)))))
              (check-string= (sha256-hex (canonical-string (by-id captured)))
                             (sha256-hex (canonical-string (by-id loaded)))
                             "the loaded destination compares equal to the capture"))))))
    ;; tampering with the receipt is refused by name.
    (flet ((tampered (key value)
             (let ((r (copy-tree receipt))) (setf (getf r key) value) r))
           (dest-with (key value)
             (let ((d (copy-tree dest))) (setf (getf d key) value) d)))
      (dolist (case (list (list (tampered :calls (cons '("POST" "repos/x/issues")
                                                       (getf receipt :calls)))
                                "mutation call POST")
                          (list (tampered :source-after (make-string 64 :initial-element #\0))
                                "source changed")
                          (list (tampered :repository "mas-bandwidth/nova-tools")
                                "unauthorised repository")
                          (list (tampered :destination
                                          (dest-with :records (rest (getf dest :records))))
                                "count mismatch")
                          (list (tampered :destination
                                          (dest-with :resume (list :member-sha256 "0")))
                                "repeat or resume differs")
                          (list (tampered :destination
                                          (dest-with :load (list :line "LOAD FAIL: changed digest"
                                                                 :reexport-sha256 (getf imp :member-sha256))))
                                "did not load the export")
                          (list (tampered :destination
                                          (dest-with :load (list :line (getf (getf dest :load) :line)
                                                                 :reexport-sha256 (make-string 64 :initial-element #\0))))
                                "re-export differs")
                          (list (tampered :destination
                                          (dest-with :import (list :engine "cp" :manifest-sha256 "x"
                                                                   :member-sha256 "y")))
                                "not a nova-work export")
                          (list nil "no receipt recorded")))
        (multiple-value-bind (okp line) (%pilot-receipt-verdict (first case))
          (ok (not okp) "a tampered receipt is refused: ~A" (second case))
          (ok (search (second case) line) "the refusal names ~A: ~A" (second case) line))))))
